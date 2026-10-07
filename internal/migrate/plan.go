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
			jobs = append(jobs, RepoJob{
				Project:       p.Key,
				Slug:          repo.Slug,
				TargetSlug:    NormalizeSlug(repo.Slug),
				CloudProject:  normalizeProjectKey(p.Key),
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

	if err := detectCollisions(jobs); err != nil {
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
