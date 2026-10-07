package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner shells out to the system git. It never places credentials in
// arguments; the token is supplied through a temporary GIT_ASKPASS helper.
type Runner struct {
	// TempBase is the directory under which per-repo workspaces are created.
	// Empty means the OS temp directory.
	TempBase string
	Logf     func(format string, args ...any)
}

func (r *Runner) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

// NewWorkspace creates a fresh temporary directory for a single repository.
// The caller must remove it when done.
func (r *Runner) NewWorkspace(pattern string) (string, error) {
	return os.MkdirTemp(r.TempBase, pattern)
}

// Verify checks that git is installed and new enough for the operations used.
func (r *Runner) Verify(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "git", "--version").Output()
	if err != nil {
		return fmt.Errorf("git is required: %w", err)
	}
	r.logf("using %s", strings.TrimSpace(string(out)))
	return nil
}

// LFSInstalled reports whether the git-lfs extension is available.
func (r *Runner) LFSInstalled(ctx context.Context) bool {
	return exec.CommandContext(ctx, "git", "lfs", "version").Run() == nil
}

// CloneMirror performs a bare mirror clone of cloneURL into dir.
func (r *Runner) CloneMirror(ctx context.Context, cloneURL, dir string, cred Credentials) error {
	return r.run(ctx, filepath.Dir(dir), cred, "clone", "--mirror", cloneURL, dir)
}

// FetchLFS downloads all LFS objects for the repository in dir.
func (r *Runner) FetchLFS(ctx context.Context, dir string, cred Credentials) error {
	return r.run(ctx, dir, cred, "lfs", "fetch", "--all")
}

// PushBranchesAndTags pushes all branches and tags. It deliberately avoids
// --mirror because Bitbucket Cloud rejects its hidden refs/pull-requests/* refs.
func (r *Runner) PushBranchesAndTags(ctx context.Context, dir, pushURL string, cred Credentials) error {
	return r.run(ctx, dir, cred, "push", pushURL,
		"refs/heads/*:refs/heads/*",
		"refs/tags/*:refs/tags/*",
	)
}

// PushLFS uploads all LFS objects to pushURL.
func (r *Runner) PushLFS(ctx context.Context, dir, pushURL string, cred Credentials) error {
	return r.run(ctx, dir, cred, "lfs", "push", "--all", pushURL)
}

type Credentials struct {
	Username string
	Password string
}

func (r *Runner) run(ctx context.Context, dir string, cred Credentials, args ...string) error {
	askpass, cleanup, err := r.writeAskpass()
	if err != nil {
		return err
	}
	defer cleanup()

	full := append([]string{"-c", "credential.helper=", "-c", "core.askPass=" + askpass}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS="+askpass,
		"BBMIG_USERNAME="+cred.Username,
		"BBMIG_PASSWORD="+cred.Password,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (r *Runner) writeAskpass() (string, func(), error) {
	f, err := os.CreateTemp("", "bbmig-askpass-*.sh")
	if err != nil {
		return "", nil, err
	}
	script := `#!/bin/sh
case "$1" in
  *sername*) printf '%s\n' "$BBMIG_USERNAME" ;;
  *)         printf '%s\n' "$BBMIG_PASSWORD" ;;
esac
`
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, err
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0o700); err != nil {
		os.Remove(f.Name())
		return "", nil, err
	}
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}
