package tui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/codersidprogrammer/bimbucket/internal/config"
	"github.com/codersidprogrammer/bimbucket/internal/migrate"
)

func runID() string { return time.Now().Format("20060102-150405") }

// RunHeadless performs the migration without the TUI, streaming events to
// stdout. Useful for CI and for verifying the engine independently of the UI.
func RunHeadless(ctx context.Context, cfg *config.Config, plan *migrate.Plan, tgt migrate.TargetClient, gr migrate.GitOps, statePath, logDir string, dryRun bool) error {
	log, err := migrate.NewLogger(logDir, runID(), func(ev migrate.Event) {
		if ev.Error != "" {
			fmt.Fprintf(os.Stdout, "[%s] %s/%s %s: %s\n", ev.Status, ev.Project, ev.Repo, ev.Stage, ev.Error)
			return
		}
		fmt.Fprintf(os.Stdout, "[%s] %s/%s %s\n", ev.Status, ev.Project, ev.Repo, ev.Stage)
	})
	if err != nil {
		return err
	}
	defer log.Close()

	state := migrate.LoadState(statePath)
	runner := migrate.NewRunner(cfg, plan, tgt, gr, state, log, dryRun)
	fmt.Printf("migrating %d repositories (workers=%d, dry-run=%v)\n", len(plan.Jobs), cfg.Options.Workers, dryRun)
	runErr := runner.Run(ctx)

	printSummary(plan, state)
	if runErr != nil {
		return runErr
	}
	return nil
}

func printSummary(plan *migrate.Plan, state *migrate.State) {
	var done, failed, rolled, skipped int
	for _, job := range plan.Jobs {
		rec := state.Get(job.Project, job.Slug)
		if rec == nil {
			continue
		}
		switch rec.Status {
		case migrate.StatusDone:
			done++
		case migrate.StatusFailed, migrate.StatusRollbackFailed:
			failed++
		case migrate.StatusRolledBack:
			rolled++
		case migrate.StatusSkipped:
			skipped++
		}
	}
	fmt.Printf("\nsummary: done=%d failed=%d rolled_back=%d skipped=%d total=%d\n", done, failed, rolled, skipped, len(plan.Jobs))
}
