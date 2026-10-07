package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/codersidprogrammer/bimbucket/internal/config"
	"github.com/codersidprogrammer/bimbucket/internal/git"
	"github.com/codersidprogrammer/bimbucket/internal/migrate"
	"github.com/codersidprogrammer/bimbucket/internal/source"
	"github.com/codersidprogrammer/bimbucket/internal/target"
	"github.com/codersidprogrammer/bimbucket/internal/tui"
)

// version is embedded at build time via -ldflags "-X main.version=...".
var version = "1.0.0"

func main() {
	var (
		configPath  = flag.String("config", "configs/projects.yaml", "path to YAML config")
		dryRun      = flag.Bool("dry-run", false, "plan and report without writing to Cloud")
		workers     = flag.Int("workers", 0, "override worker count")
		tempDir     = flag.String("temp-dir", "", "base directory for ephemeral clones")
		logDir      = flag.String("log-dir", "logs", "directory for run logs and reports")
		statePath   = flag.String("state", "migration-state.json", "path to the resume/idempotency state file")
		headless    = flag.Bool("no-tui", false, "run without the TUI (non-interactive)")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("bimbucket %s\n", version)
		return
	}
	tui.Version = version

	cfg, err := config.Load(*configPath)
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

	// The TUI builds the plan lazily so it can still open and report a source
	// connection failure via the Connection view.
	if err := tui.Run(tui.Options{
		Config:    cfg,
		Source:    src,
		Target:    tgt,
		Git:       gr,
		StatePath: *statePath,
		LogDir:    *logDir,
		DryRun:    *dryRun,
	}); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
