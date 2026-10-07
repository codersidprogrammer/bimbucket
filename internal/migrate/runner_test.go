package migrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/codersidprogrammer/bimbucket/internal/config"
	"github.com/codersidprogrammer/bimbucket/internal/git"
	"github.com/codersidprogrammer/bimbucket/internal/target"
)

type fakeTarget struct {
	exists       map[string]bool
	created      []string
	deleted      []string
	projects     []string
	mainBranches map[string]string
}

func (f *fakeTarget) Workspace() string          { return "ws" }
func (f *fakeTarget) RepoURL(slug string) string { return "https://cloud.example/ws/" + slug }
func (f *fakeTarget) RepoExists(_ context.Context, slug string) (bool, error) {
	return f.exists[slug], nil
}
func (f *fakeTarget) CreateProject(_ context.Context, key, _ string) error {
	f.projects = append(f.projects, key)
	return nil
}
func (f *fakeTarget) CreateRepo(_ context.Context, req target.CreateRepoRequest) error {
	f.created = append(f.created, req.Slug)
	return nil
}
func (f *fakeTarget) SetMainBranch(_ context.Context, slug, branch string) error {
	if f.mainBranches == nil {
		f.mainBranches = map[string]string{}
	}
	f.mainBranches[slug] = branch
	return nil
}
func (f *fakeTarget) DeleteRepo(_ context.Context, slug string) error {
	f.deleted = append(f.deleted, slug)
	return nil
}

type fakeGit struct {
	cloneErr error
	pushErr  error
	cloned   []string
	pushed   []string
}

func (f *fakeGit) NewWorkspace(string) (string, error) { return os.MkdirTemp("", "bbmig-test") }
func (f *fakeGit) LFSInstalled(context.Context) bool   { return false }
func (f *fakeGit) CloneMirror(_ context.Context, url, _ string, _ git.Credentials) error {
	if f.cloneErr != nil {
		return f.cloneErr
	}
	f.cloned = append(f.cloned, url)
	return nil
}
func (f *fakeGit) FetchLFS(context.Context, string, git.Credentials) error { return nil }
func (f *fakeGit) PushBranchesAndTags(_ context.Context, _, url string, _ git.Credentials) error {
	if f.pushErr != nil {
		return f.pushErr
	}
	f.pushed = append(f.pushed, url)
	return nil
}
func (f *fakeGit) PushLFS(context.Context, string, string, git.Credentials) error { return nil }

func runOne(t *testing.T, tgt TargetClient, gr GitOps) (*State, error) {
	t.Helper()
	cfg := &config.Config{Options: config.Options{
		Workers: 1, OnError: config.OnErrorContinue, Rollback: true, CreateCloudProjects: true,
	}}
	plan := &Plan{Jobs: []RepoJob{{
		Project: "XOPS", Slug: "svc", TargetSlug: "svc", CloudProject: "XOPS",
		CloneURL: "https://bb.example.com/scm/xops/svc.git", DefaultBranch: "main",
	}}}
	state := LoadState(filepath.Join(t.TempDir(), "state.json"))
	log, err := NewLogger(t.TempDir(), "test", nil)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	defer log.Close()

	runner := NewRunner(cfg, plan, tgt, gr, state, log, false)
	runErr := runner.Run(context.Background())
	return state, runErr
}

func TestRunSuccess(t *testing.T) {
	tgt := &fakeTarget{exists: map[string]bool{}}
	gr := &fakeGit{}

	state, err := runOne(t, tgt, gr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rec := state.Get("XOPS", "svc")
	if rec.Status != StatusDone {
		t.Errorf("status = %s, want done", rec.Status)
	}
	if len(tgt.created) != 1 || len(tgt.deleted) != 0 {
		t.Errorf("created=%v deleted=%v", tgt.created, tgt.deleted)
	}
	if tgt.mainBranches["svc"] != "main" {
		t.Errorf("main branch not set: %v", tgt.mainBranches)
	}
}

func TestRunPushFailureRollsBackCreatedRepo(t *testing.T) {
	tgt := &fakeTarget{exists: map[string]bool{}}
	gr := &fakeGit{pushErr: errors.New("pre-receive hook declined")}

	state, err := runOne(t, tgt, gr)
	if err == nil {
		t.Fatal("expected error")
	}
	rec := state.Get("XOPS", "svc")
	if rec.Status != StatusRolledBack {
		t.Errorf("status = %s, want rolled_back", rec.Status)
	}
	if len(tgt.deleted) != 1 {
		t.Errorf("expected rollback delete, got %v", tgt.deleted)
	}
}

func TestRunFailureDoesNotDeletePreexistingRepo(t *testing.T) {
	tgt := &fakeTarget{exists: map[string]bool{"svc": true}}
	gr := &fakeGit{pushErr: errors.New("boom")}

	state, err := runOne(t, tgt, gr)
	if err == nil {
		t.Fatal("expected error")
	}
	rec := state.Get("XOPS", "svc")
	if rec.Status != StatusFailed {
		t.Errorf("status = %s, want failed", rec.Status)
	}
	if len(tgt.deleted) != 0 {
		t.Errorf("must not delete pre-existing repo, got %v", tgt.deleted)
	}
	if len(tgt.created) != 0 {
		t.Errorf("must not create existing repo, got %v", tgt.created)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	cfg := &config.Config{Options: config.Options{Workers: 1, Rollback: true}}
	plan := &Plan{Jobs: []RepoJob{{Project: "XOPS", Slug: "svc", TargetSlug: "svc"}}}
	state := LoadState(filepath.Join(t.TempDir(), "state.json"))
	log, _ := NewLogger(t.TempDir(), "test", nil)
	defer log.Close()

	tgt := &fakeTarget{exists: map[string]bool{}}
	runner := NewRunner(cfg, plan, tgt, &fakeGit{}, state, log, true)
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(tgt.created) != 0 {
		t.Errorf("dry run created repos: %v", tgt.created)
	}
	if rec := state.Get("XOPS", "svc"); rec.Status != StatusSkipped {
		t.Errorf("status = %s, want skipped", rec.Status)
	}
}
