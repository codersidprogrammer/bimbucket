package migrate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/codersidprogrammer/bimbucket/internal/config"
	"github.com/codersidprogrammer/bimbucket/internal/source"
)

// RepoLister is the subset of the Bitbucket Server client the planner needs.
type RepoLister interface {
	ListRepos(ctx context.Context, projectKey string) ([]source.Repository, error)
}

// RepoJob describes one repository to migrate.
type RepoJob struct {
	Project       string
	Slug          string
	TargetSlug    string
	CloudProject  string
	CloneURL      string
	DefaultBranch string
	Description   string
}

// Plan is the ordered set of repositories to migrate.
type Plan struct {
	Jobs []RepoJob
}

// BuildPlan expands configured projects into repositories and fails fast if any
// Cloud target slug would collide across the selected set (Cloud repository
// slugs are unique per workspace, unlike Server's project+slug).
func BuildPlan(ctx context.Context, cfg *config.Config, lister RepoLister) (*Plan, error) {
	var jobs []RepoJob

	for _, p := range cfg.Projects {
		repos, err := lister.ListRepos(ctx, p.Key)
		if err != nil {
			return nil, fmt.Errorf("list repos for project %q: %w", p.Key, err)
		}

		want := make(map[string]bool, len(p.Repos))
		for _, slug := range p.Repos {
			want[slug] = true
		}

		for _, repo := range repos {
			if len(want) > 0 && !want[repo.Slug] {
				continue
			}
			if repo.Archived && !p.IncludeArchived {
				continue
			}
			cloudProject, targetSlug := ResolveJob(p, repo.Slug)
			jobs = append(jobs, RepoJob{
				Project:       p.Key,
				Slug:          repo.Slug,
				TargetSlug:    targetSlug,
				CloudProject:  cloudProject,
				CloneURL:      repo.CloneURL(cfg.Source.BaseURL),
				DefaultBranch: repo.DefaultBranchName(),
				Description:   repo.Name,
			})
		}
	}

	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].Project != jobs[j].Project {
			return jobs[i].Project < jobs[j].Project
		}
		return jobs[i].Slug < jobs[j].Slug
	})

	if err := ApplySlugPolicy(jobs, cfg); err != nil {
		return nil, err
	}
	return &Plan{Jobs: jobs}, nil
}

// NormalizeSlug maps a Server repository slug to a valid, deterministic Cloud
// slug: lowercase, only [a-z0-9._-], runs of invalid characters collapsed to a
// single dash, and no leading/trailing separators.
func NormalizeSlug(slug string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(slug) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-._")
	if out == "" {
		out = "repo"
	}
	return out
}

// projectDestination resolves the destination Cloud project key for a
// configured project: an explicit destination wins, otherwise the source key is
// normalized.
func projectDestination(p config.Project) string {
	if p.Destination != "" {
		return p.Destination
	}
	return normalizeProjectKey(p.Key)
}

// ResolveJob resolves the destination Cloud project and target slug for a source
// repository, honoring the project's per-repo overrides. An override with an
// empty destination (or target_slug) inherits the project destination (or the
// normalized source slug).
func ResolveJob(p config.Project, sourceSlug string) (cloudProject, targetSlug string) {
	cloudProject = projectDestination(p)
	targetSlug = NormalizeSlug(sourceSlug)
	if ov, ok := p.OverrideFor(sourceSlug); ok {
		if ov.Destination != "" {
			cloudProject = ov.Destination
		}
		if ov.TargetSlug != "" {
			targetSlug = NormalizeSlug(ov.TargetSlug)
		}
	}
	return cloudProject, targetSlug
}

// ApplySlugPolicy enforces workspace-wide target slug uniqueness across jobs.
// Cloud repository slugs are unique per workspace, so collisions are resolved
// per options.on_slug_collision (an empty policy means the default, "auto").
//
// A target slug set explicitly by an override is authoritative: a collision
// that involves any explicit slug is rejected under either policy (renaming a
// user's chosen name would be surprising). Under "auto" the remaining
// duplicate slugs -- all derived from source slugs -- are prefixed with their
// source project key; under "fail" any collision is reported as an error.
func ApplySlugPolicy(jobs []RepoJob, cfg *config.Config) error {
	explicit := explicitSlugs(jobs, cfg)
	if err := detectExplicitCollisions(jobs, explicit); err != nil {
		return err
	}

	policy := cfg.Options.OnSlugCollision
	if policy == "" {
		policy = config.OnSlugCollisionAuto
	}
	if policy == config.OnSlugCollisionAuto {
		autoRenameCollisions(jobs, explicit)
	}
	return detectCollisions(jobs)
}

// explicitSlugs marks the jobs whose target slug came from an explicit
// target_slug override (rather than the normalized source slug).
func explicitSlugs(jobs []RepoJob, cfg *config.Config) map[int]bool {
	explicit := make(map[int]bool)
	for i, j := range jobs {
		if p, ok := cfg.ProjectByKey(j.Project); ok {
			if ov, ok := p.OverrideFor(j.Slug); ok && ov.TargetSlug != "" {
				explicit[i] = true
			}
		}
	}
	return explicit
}

// detectExplicitCollisions reports any target slug shared by two jobs where at
// least one of them is an explicit override.
func detectExplicitCollisions(jobs []RepoJob, explicit map[int]bool) error {
	sources := make(map[string][]string, len(jobs))
	explicitSlug := make(map[string]bool, len(jobs))
	for i, j := range jobs {
		sources[j.TargetSlug] = append(sources[j.TargetSlug], j.Project+"/"+j.Slug)
		if explicit[i] {
			explicitSlug[j.TargetSlug] = true
		}
	}
	var collisions []string
	for slug, srcs := range sources {
		if len(srcs) > 1 && explicitSlug[slug] {
			sort.Strings(srcs)
			collisions = append(collisions, fmt.Sprintf("%s <- %s", slug, strings.Join(srcs, ", ")))
		}
	}
	if len(collisions) == 0 {
		return nil
	}
	sort.Strings(collisions)
	return fmt.Errorf("explicit target_slug collision(s); Cloud repository slugs are unique per workspace:\n  %s", strings.Join(collisions, "\n  "))
}

// autoRenameCollisions prefixes every duplicated (non-explicit) target slug
// with its source project key. Because a source project key plus repo slug is
// unique, the result is unique too; the loop guards against a prefix itself
// introducing a new clash. Explicit slugs are never renamed.
func autoRenameCollisions(jobs []RepoJob, explicit map[int]bool) {
	for i := 0; i <= len(jobs); i++ {
		counts := make(map[string]int, len(jobs))
		for _, j := range jobs {
			counts[j.TargetSlug]++
		}
		renamed := false
		for k := range jobs {
			if counts[jobs[k].TargetSlug] > 1 && !explicit[k] {
				jobs[k].TargetSlug = slugToken(jobs[k].Project) + "-" + jobs[k].TargetSlug
				renamed = true
			}
		}
		if !renamed {
			return
		}
	}
}

// slugToken derives a Cloud-slug-safe token from a source project key.
func slugToken(project string) string {
	tok := strings.ToLower(normalizeProjectKey(project))
	if tok == "" {
		return "proj"
	}
	return tok
}

// normalizeProjectKey upper-cases a Server project key and strips a leading
// tilde used for personal projects, which Cloud project keys do not allow.
func normalizeProjectKey(key string) string {
	key = strings.TrimPrefix(key, "~")
	key = strings.ToUpper(key)
	var b strings.Builder
	for _, r := range key {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func detectCollisions(jobs []RepoJob) error {
	seen := make(map[string][]string, len(jobs))
	for _, j := range jobs {
		seen[j.TargetSlug] = append(seen[j.TargetSlug], j.Project+"/"+j.Slug)
	}

	var collisions []string
	for slug, sources := range seen {
		if len(sources) > 1 {
			sort.Strings(sources)
			collisions = append(collisions, fmt.Sprintf("%s <- %s", slug, strings.Join(sources, ", ")))
		}
	}
	if len(collisions) == 0 {
		return nil
	}
	sort.Strings(collisions)
	return fmt.Errorf("target slug collision(s) detected; Cloud repository slugs are unique per workspace:\n  %s", strings.Join(collisions, "\n  "))
}
