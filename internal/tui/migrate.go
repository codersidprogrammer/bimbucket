package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/codersidprogrammer/bimbucket/internal/migrate"
)

func jobKey(j migrate.RepoJob) string { return j.Project + "/" + j.Slug }

// rebuildMigTable rebuilds the selection table from the current plan and filter,
// preserving selection. The table renders a fixed header with a scrolling body.
func (m *Model) rebuildMigTable() {
	if m.plan == nil {
		return
	}
	query := strings.ToLower(strings.TrimSpace(m.mig.filter.Value()))
	jobs := make([]migrate.RepoJob, 0, len(m.plan.Jobs))
	for _, j := range m.plan.Jobs {
		if migMatches(query, j) {
			jobs = append(jobs, j)
		}
	}
	m.mig.jobs = jobs

	flex := m.fillWidth(4 + 16 + 12)
	repoW := flex / 2
	targetW := flex - repoW
	cols := []table.Column{
		{Title: "Sel", Width: 4},
		{Title: "Project", Width: 16},
		{Title: "Repo", Width: repoW},
		{Title: "Target slug", Width: targetW},
		{Title: "Default", Width: 12},
	}
	m.mig.table = m.newTable(cols, m.migRows())
	if h := m.contentHeight() - 2; h >= 3 { // reserve the filter bar and footer lines
		m.mig.table.SetHeight(h)
	}
	m.mig.inited = true
}

func (m *Model) migRows() []table.Row {
	rows := make([]table.Row, 0, len(m.mig.jobs))
	for _, j := range m.mig.jobs {
		mark := "[ ]"
		if m.mig.selected[jobKey(j)] {
			mark = "[x]"
		}
		rows = append(rows, table.Row{mark, j.Project, j.Slug, j.TargetSlug, j.DefaultBranch})
	}
	return rows
}

func migMatches(query string, j migrate.RepoJob) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(j.Project), query) ||
		strings.Contains(strings.ToLower(j.Slug), query) ||
		strings.Contains(strings.ToLower(j.TargetSlug), query)
}

// toggleMigCursor flips selection of the highlighted row. It updates rows in
// place so the cursor does not jump.
func (m *Model) toggleMigCursor() {
	i := m.mig.table.Cursor()
	if i < 0 || i >= len(m.mig.jobs) {
		return
	}
	k := jobKey(m.mig.jobs[i])
	m.mig.selected[k] = !m.mig.selected[k]
	m.mig.table.SetRows(m.migRows())
}

// setVisibleSelection applies v to every currently visible (filtered) row.
func (m *Model) setVisibleSelection(v bool) {
	for _, j := range m.mig.jobs {
		m.mig.selected[jobKey(j)] = v
	}
	m.mig.table.SetRows(m.migRows())
}

func (m *Model) handleMigrateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.planErr != nil || m.plan == nil {
		if msg.String() == "r" {
			return m, m.loadPlan()
		}
		return m, nil
	}
	if !m.mig.inited {
		m.rebuildMigTable()
	}

	switch m.mig.phase {
	case migBrowse:
		switch msg.String() {
		case "/":
			m.mig.filtering = true
			return m, m.mig.filter.Focus()
		case " ":
			m.toggleMigCursor()
			return m, nil
		case "a":
			m.setVisibleSelection(true)
			return m, nil
		case "n":
			m.setVisibleSelection(false)
			return m, nil
		case "enter":
			if m.countSelected() > 0 {
				m.mig.phase = migConfirm
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.mig.table, cmd = m.mig.table.Update(msg)
		return m, cmd

	case migConfirm:
		switch msg.String() {
		case "d":
			m.dryRun = !m.dryRun
		case "enter":
			m.mig.phase = migRunning
			return m, m.startRun()
		case "esc":
			m.mig.phase = migBrowse
		}
		return m, nil

	case migDone:
		if msg.String() == "esc" {
			m.mig.phase = migBrowse
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) viewMigrate() string {
	if m.planErr != nil {
		return "  " + badStyle.Render("plan error: "+m.planErr.Error()) + "\n\n  press r to retry"
	}
	if m.plan == nil {
		return fmt.Sprintf("  %s loading repositories...", m.spinner.View())
	}

	switch m.mig.phase {
	case migBrowse:
		var b strings.Builder
		b.WriteString(m.mig.filter.View())
		b.WriteString("\n")
		if len(m.mig.jobs) == 0 {
			b.WriteString(mutedStyle.Render("  no repositories match " + strconv.Quote(m.mig.filter.Value())))
		} else {
			b.WriteString(m.mig.table.View())
		}
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  selected: %s / %d",
			goodStyle.Render(fmt.Sprint(m.countSelected())), len(m.plan.Jobs)))
		return b.String()

	case migConfirm:
		plan := "MIGRATE"
		if m.dryRun {
			plan = "DRY RUN"
		}
		return fmt.Sprintf(
			"\n  %s\n\n  Repositories: %d\n  Mode:         %s\n  Workers:      %d\n\n  enter run    d toggle dry-run    esc back\n",
			titleStyle.Render("Confirm migration"), m.countSelected(), plan, m.cfg.Options.Workers,
		)

	case migRunning:
		var b strings.Builder
		b.WriteString(fmt.Sprintf("\n  %s migrating %d repositories...\n\n", m.spinner.View(), m.countSelected()))
		start := len(m.mig.progress) - 12
		if start < 0 {
			start = 0
		}
		for _, ev := range m.mig.progress[start:] {
			line := fmt.Sprintf("  %-8s %s/%s %s", ev.Status, ev.Project, ev.Repo, ev.Stage)
			switch ev.Status {
			case "error":
				b.WriteString(badStyle.Render(line) + "\n")
			case "warn":
				b.WriteString(warnStyle.Render(line) + "\n")
			default:
				b.WriteString(line + "\n")
			}
		}
		return b.String()

	case migDone:
		return m.migDoneView()
	}
	return ""
}

func (m *Model) migDoneView() string {
	var b strings.Builder
	b.WriteString("\n  " + titleStyle.Render("Migration complete") + "\n\n")
	if m.mig.runErr != nil {
		b.WriteString("  " + badStyle.Render(m.mig.runErr.Error()) + "\n\n")
	} else {
		b.WriteString("  " + goodStyle.Render("no fatal errors") + "\n\n")
	}
	b.WriteString(fmt.Sprintf("  events: %d\n  logs:   %s\n\n  esc back   1 projects   q quit\n", len(m.mig.progress), m.logDir))
	return b.String()
}

func (m *Model) startRun() tea.Cmd {
	jobs := make([]migrate.RepoJob, 0, len(m.plan.Jobs))
	for _, j := range m.plan.Jobs {
		if m.mig.selected[jobKey(j)] {
			jobs = append(jobs, j)
		}
	}
	plan := &migrate.Plan{Jobs: jobs}

	m.mig.progress = nil
	m.mig.runErr = nil
	m.mig.events = make(chan migrate.Event, 256)
	m.mig.doneCh = make(chan error, 1)

	log, err := migrate.NewLogger(m.logDir, runID(), func(ev migrate.Event) {
		select {
		case m.mig.events <- ev:
		default:
		}
	})
	if err != nil {
		m.mig.runErr = err
		m.mig.phase = migDone
		return nil
	}
	m.mig.log = log

	runner := migrate.NewRunner(m.cfg, plan, m.tgt, m.gr, m.state, log, m.dryRun)

	ctx, cancel := context.WithCancel(context.Background())
	m.mig.cancel = cancel

	go func() {
		m.mig.doneCh <- runner.Run(ctx)
	}()

	return waitForEvent(m.mig.events, m.mig.doneCh)
}

func waitForEvent(events <-chan migrate.Event, done <-chan error) tea.Cmd {
	return func() tea.Msg {
		select {
		case ev := <-events:
			return eventMsg(ev)
		case err := <-done:
			return doneMsg{err: err}
		}
	}
}

func (m *Model) countSelected() int {
	if m.plan == nil {
		return 0
	}
	n := 0
	for _, j := range m.plan.Jobs {
		if m.mig.selected[jobKey(j)] {
			n++
		}
	}
	return n
}
