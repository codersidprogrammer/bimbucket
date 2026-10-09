package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/codersidprogrammer/bimbucket/internal/config"
	"github.com/codersidprogrammer/bimbucket/internal/coop"
	"github.com/codersidprogrammer/bimbucket/internal/git"
	"github.com/codersidprogrammer/bimbucket/internal/migrate"
	"github.com/codersidprogrammer/bimbucket/internal/source"
	"github.com/codersidprogrammer/bimbucket/internal/target"
	"github.com/codersidprogrammer/bimbucket/internal/tui"
)

// version is embedded at build time via -ldflags "-X main.version=...".
var version = "1.1.0-beta"

func main() {
	var (
		configPath  = flag.String("config", "configs/projects.yaml", "path to YAML config")
		envPath     = flag.String("env", "", "path to a .env file (default: ./.env if present)")
		dryRun      = flag.Bool("dry-run", false, "plan and report without writing to Cloud")
		workers     = flag.Int("workers", 0, "override worker count")
		tempDir     = flag.String("temp-dir", "", "base directory for ephemeral clones")
		logDir      = flag.String("log-dir", "logs", "directory for run logs and reports")
		statePath   = flag.String("state", "migration-state.json", "path to the resume/idempotency state file")
		headless    = flag.Bool("no-tui", false, "run without the TUI (non-interactive)")
		coopJoin    = flag.String("coop-join", "", "join a co-op room by invite link on startup")
		coopName    = flag.String("coop-name", "", "display name for this device in co-op mode")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("bimbucket %s\n", version)
		return
	}
	tui.Version = version

	cfg, err := config.LoadWithEnv(*configPath, *envPath)
	if err != nil {
		fatal(err)
	}
	if *workers > 0 {
		cfg.Options.Workers = *workers
	}
	if *tempDir != "" {
		cfg.Options.TempDir = *tempDir
	}

	ctx := context.Background()
	src := source.NewClient(cfg.Source.BaseURL, cfg.Source.User, cfg.Source.Token, cfg.Source.Password)
	tgt := target.NewClient(cfg.Target.Workspace, cfg.Target.Email, cfg.Target.APIToken)
	gr := &git.Runner{TempBase: cfg.Options.TempDir}
	if err := gr.Verify(ctx); err != nil {
		fatal(err)
	}

	if *headless {
		plan, err := migrate.BuildPlan(ctx, cfg, src)
		if err != nil {
			fatal(err)
		}
		if len(plan.Jobs) == 0 {
			fatal(fmt.Errorf("no repositories matched the configured projects"))
		}
		if err := tui.RunHeadless(ctx, cfg, plan, tgt, gr, *statePath, *logDir, *dryRun); err != nil {
			fatal(err)
		}
		return
	}

	// Co-op mode is optional. The manager is always available so a joiner can
	// use an invite link that embeds the database URL; FIREBASE_DB_URL is only
	// required to host (or to join a link without an embedded URL).
	coopMgr := coop.New(os.Getenv("FIREBASE_DB_URL"), os.Getenv("FIREBASE_API_KEY"),
		coopDeviceName(*coopName), migrate.LoadState(*statePath))

	// The TUI builds the plan lazily so it can still open and report a source
	// connection failure via the Connection view.
	if err := tui.Run(tui.Options{
		Config:     cfg,
		ConfigPath: *configPath,
		Source:     src,
		Target:     tgt,
		Git:        gr,
		StatePath:  *statePath,
		LogDir:     *logDir,
		DryRun:     *dryRun,
		Coop:       coopMgr,
		JoinLink:   *coopJoin,
	}); err != nil {
		fatal(err)
	}
}

// coopDeviceName resolves the display name for this device: an explicit flag,
// else the hostname, else a short random fallback.
func coopDeviceName(explicit string) string {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "device"
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
