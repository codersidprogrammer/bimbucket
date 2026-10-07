package migrate

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/codersidprogrammer/bimbucket/internal/config"
	"github.com/codersidprogrammer/bimbucket/internal/git"
	"github.com/codersidprogrammer/bimbucket/internal/target"
)

// TargetClient is the Cloud surface the runner needs.
type TargetClient interface {
	Workspace() string
	RepoURL(slug string) string
	RepoExists(ctx context.Context, slug string) (bool, error)
	CreateProject(ctx context.Context, key, name string) error
	CreateRepo(ctx context.Context, req target.CreateRepoRequest) error
	SetMainBranch(ctx context.Context, slug, branch string) error
	DeleteRepo(ctx context.Context, slug string) error
}

// GitOps is the git surface the runner needs. *git.Runner implements it.
type GitOps interface {
	NewWorkspace(pattern string) (string, error)
	LFSInstalled(ctx context.Context) bool
	CloneMirror(ctx context.Context, cloneURL, dir string, cred git.Credentials) error
	FetchLFS(ctx context.Context, dir string, cred git.Credentials) error
	PushBranchesAndTags(ctx context.Context, dir, pushURL string, cred git.Credentials) error
	PushLFS(ctx context.Context, dir, pushURL string, cred git.Credentials) error
}

type Runner struct {
	cfg    *config.Config
	plan   *Plan
	tgt    TargetClient
	git    GitOps
	state  *State
	log    *Logger
	dryRun bool

	projectsMu    sync.Mutex
	projectsReady map[string]bool
}

func NewRunner(cfg *config.Config, plan *Plan, tgt TargetClient, gr GitOps, state *State, log *Logger, dryRun bool) *Runner {
	return &Runner{
		cfg:           cfg,
		plan:          plan,
		tgt:           tgt,
		git:           gr,
		state:         state,
		log:           log,
		dryRun:        dryRun,
		projectsReady: map[string]bool{},
	}
}

// Run migrates every job using a bounded worker pool.
func (r *Runner) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	workers := r.cfg.Options.Workers
	if workers > len(r.plan.Jobs) {
		workers = len(r.plan.Jobs)
	}
	if workers < 1 {
		workers = 1
	}

	jobs := make(chan RepoJob)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := r.migrateOne(ctx, job); err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					if r.cfg.Options.OnError == config.OnErrorStop {
						cancel()
						return
					}
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, job := range r.plan.Jobs {
			select {
			case jobs <- job:
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()

	records := make([]*Record, 0, len(r.plan.Jobs))
	for _, job := range r.plan.Jobs {
		if rec := r.state.Get(job.Project, job.Slug); rec != nil {
			records = append(records, rec)
		}
	}
	r.log.Summary(records)

	return firstErr
}

func (r *Runner) migrateOne(ctx context.Context, job RepoJob) error {
	rec := &Record{Project: job.Project, Slug: job.Slug, TargetSlug: job.TargetSlug, Status: StatusPending}
	r.state.Update(rec)

	if r.dryRun {
		r.event(job, "plan", "ok", 0, "")
		rec.Status = StatusSkipped
		r.state.Update(rec)
		return nil
	}

	// Ensure the Cloud project exists, then create the repository if missing.
	if r.cfg.Options.CreateCloudProjects && job.CloudProject != "" {
		start := time.Now()
		if err := r.ensureProject(ctx, job.CloudProject); err != nil {
			return r.fail(job, rec, "create-project", start, err)
		}
		r.event(job, "create-project", "ok", time.Since(start), "")
	}

	start := time.Now()
	exists, err := r.tgt.RepoExists(ctx, job.TargetSlug)
	if err != nil {
		return r.fail(job, rec, "check-repo", start, err)
	}
	if !exists {
		err := r.tgt.CreateRepo(ctx, target.CreateRepoRequest{
			Slug:        job.TargetSlug,
			Description: job.Description,
			ProjectKey:  job.CloudProject,
			Private:     true,
		})
		if err != nil {
			return r.fail(job, rec, "create-repo", start, err)
		}
		rec.CreatedByRun = true
		r.state.Update(rec)
	}
	r.event(job, "create-repo", "ok", time.Since(start), "")

	// Ephemeral clone -> push -> cleanup.
	dir, err := r.git.NewWorkspace("bbmig-" + sanitize(job.TargetSlug) + "-*")
	if err != nil {
		return r.fail(job, rec, "workspace", start, err)
	}
	defer os.RemoveAll(dir)

	cloneCred := git.Credentials{Username: cloneUser(r.cfg.Source.User), Password: clonePassword(r.cfg.Source)}
	pushCred := git.Credentials{Username: "x-bitbucket-api-token-auth", Password: r.cfg.Target.APIToken}

	rec.Status = StatusCloning
	r.state.Update(rec)

	start = time.Now()
	if err := r.git.CloneMirror(ctx, job.CloneURL, dir, cloneCred); err != nil {
		return r.fail(job, rec, "clone", start, err)
	}
	r.event(job, "clone", "ok", time.Since(start), "")

	if r.git.LFSInstalled(ctx) {
		start = time.Now()
		if err := r.git.FetchLFS(ctx, dir, cloneCred); err != nil {
			r.event(job, "lfs-fetch", "warn", time.Since(start), err.Error())
		} else {
			r.event(job, "lfs-fetch", "ok", time.Since(start), "")
		}
	}

	rec.Status = StatusPushing
	r.state.Update(rec)

	pushURL := r.tgt.RepoURL(job.TargetSlug)
	start = time.Now()
	if err := r.git.PushBranchesAndTags(ctx, dir, pushURL, pushCred); err != nil {
		return r.fail(job, rec, "push", start, err)
	}
	r.event(job, "push", "ok", time.Since(start), "")

	if r.git.LFSInstalled(ctx) {
		start = time.Now()
		if err := r.git.PushLFS(ctx, dir, pushURL, pushCred); err != nil {
			r.event(job, "lfs-push", "warn", time.Since(start), err.Error())
		} else {
			r.event(job, "lfs-push", "ok", time.Since(start), "")
		}
	}

	start = time.Now()
	if err := r.tgt.SetMainBranch(ctx, job.TargetSlug, job.DefaultBranch); err != nil {
		return r.fail(job, rec, "main-branch", start, err)
	}
	r.event(job, "main-branch", "ok", time.Since(start), "")

	rec.Status = StatusDone
	rec.Error = ""
	r.state.Update(rec)
	r.event(job, "complete", "ok", 0, "")
	return nil
}

func (r *Runner) fail(job RepoJob, rec *Record, stage string, start time.Time, cause error) error {
	r.event(job, stage, "error", time.Since(start), cause.Error())
	rec.Error = stage + ": " + cause.Error()

	if r.cfg.Options.Rollback && rec.CreatedByRun {
		// Use a fresh context: the run context may already be cancelled.
		rbCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := r.tgt.DeleteRepo(rbCtx, job.TargetSlug); err != nil {
			rec.Status = StatusRollbackFailed
			rec.Error += fmt.Sprintf("; rollback failed: %v", err)
			r.event(job, "rollback", "error", 0, err.Error())
		} else {
			rec.Status = StatusRolledBack
			r.event(job, "rollback", "ok", 0, "")
		}
	} else {
		rec.Status = StatusFailed
	}
	r.state.Update(rec)

	return fmt.Errorf("%s/%s %s: %w", job.Project, job.Slug, stage, cause)
}

func (r *Runner) ensureProject(ctx context.Context, key string) error {
	r.projectsMu.Lock()
	if r.projectsReady[key] {
		r.projectsMu.Unlock()
		return nil
	}
	r.projectsMu.Unlock()

	if err := r.tgt.CreateProject(ctx, key, key); err != nil {
		return err
	}

	r.projectsMu.Lock()
	r.projectsReady[key] = true
	r.projectsMu.Unlock()
	return nil
}

func (r *Runner) event(job RepoJob, stage, status string, d time.Duration, errMsg string) {
	r.log.Event(Event{
		Project:    job.Project,
		Repo:       job.Slug,
		TargetSlug: job.TargetSlug,
		Stage:      stage,
		Status:     status,
		DurationMS: d.Milliseconds(),
		Error:      errMsg,
	})
}

func cloneUser(user string) string {
	if user == "" {
		return "x-token-auth"
	}
	return user
}

func clonePassword(s config.Source) string {
	if s.Token != "" {
		return s.Token
	}
	return s.Password
}

func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}
