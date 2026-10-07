package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/codersidprogrammer/bimbucket/internal/config"
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

	flex := m.fillWidth(4 + 16 + 18 + 12)
	repoW := flex / 2
	targetW := flex - repoW
	cols := []table.Column{
		{Title: "Sel", Width: 4},
		{Title: "Project", Width: 16},
		{Title: "Repo", Width: repoW},
		{Title: "Dest", Width: 18},
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
		dest := j.CloudProject
		if dest == "" {
			dest = "-"
		}
		if _, ok := m.remaps[jobKey(j)]; ok {
			dest += " *"
		}
		rows = append(rows, table.Row{mark, j.Project, j.Slug, dest, j.TargetSlug, j.DefaultBranch})
	}
	return rows
}

func migMatches(query string, j migrate.RepoJob) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(j.Project), query) ||
		strings.Contains(strings.ToLower(j.Slug), query) ||
		strings.Contains(strings.ToLower(j.TargetSlug), query) ||
		strings.Contains(strings.ToLower(j.CloudProject), query)
}

// jobByKey returns the plan job with the given project/slug key.
func (m *Model) jobByKey(k string) *migrate.RepoJob {
	if m.plan == nil {
		return nil
	}
	for i := range m.plan.Jobs {
		if jobKey(m.plan.Jobs[i]) == k {
			return &m.plan.Jobs[i]
		}
	}
	return nil
}

// applyRemaps recomputes every job's destination from the config + source slug,
// then layers the session remap overlay on top. It is idempotent, so it can be
// re-run after a plan reload without compounding.
func (m *Model) applyRemaps() {
	if m.plan == nil {
		return
	}
	for i := range m.plan.Jobs {
		j := &m.plan.Jobs[i]
		p, ok := m.cfg.ProjectByKey(j.Project)
		if !ok {
			continue
		}
		cloudProject, targetSlug := migrate.ResolveJob(p, j.Slug)
		if ov, ok := m.remaps[jobKey(*j)]; ok {
			if ov.Destination != "" {
				cloudProject = ov.Destination
			}
			if ov.TargetSlug != "" {
				targetSlug = migrate.NormalizeSlug(ov.TargetSlug)
			}
		}
		j.CloudProject, j.TargetSlug = cloudProject, targetSlug
	}
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

	if m.mig.editing {
		return m.handleRemapKey(msg)
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
		case "e":
			m.openRemap()
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

// --- remap editor -----------------------------------------------------------

func (m *Model) openRemap() {
	i := m.mig.table.Cursor()
	if i < 0 || i >= len(m.mig.jobs) {
		return
	}
	j := m.mig.jobs[i]
	if m.remaps == nil {
		m.remaps = map[string]remapValue{}
	}
	m.mig.editing = true
	m.mig.editKey = jobKey(j)
	m.mig.editErr = ""
	m.mig.editDest = m.newRemapInput("Cloud project key", j.CloudProject)
	m.mig.editSlug = m.newRemapInput("target slug (empty = same)", j.TargetSlug)
	_ = m.focusRemap(0)
}

func (m *Model) newRemapInput(placeholder, value string) textinput.Model {
	in := textinput.New()
	in.Placeholder = placeholder
	in.SetValue(value)
	in.CharLimit = 128
	w := m.width - 22
	if w < 20 {
		w = 20
	}
	in.Width = w
	return in
}

func (m *Model) focusRemap(field int) tea.Cmd {
	m.mig.editField = field
	if field == 0 {
		m.mig.editSlug.Blur()
		return m.mig.editDest.Focus()
	}
	m.mig.editDest.Blur()
	return m.mig.editSlug.Focus()
}

func (m *Model) closeRemap() {
	if !m.mig.editing {
		return
	}
	m.mig.editing = false
	m.mig.editErr = ""
	m.mig.editKey = ""
	m.mig.editDest.Blur()
	m.mig.editSlug.Blur()
}

func (m *Model) handleRemapKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.closeRemap()
		return m, nil
	case "tab", "down":
		return m, m.focusRemap(1)
	case "shift+tab", "up":
		return m, m.focusRemap(0)
	case "enter":
		return m.commitRemap()
	}
	var cmd tea.Cmd
	if m.mig.editField == 0 {
		m.mig.editDest, cmd = m.mig.editDest.Update(msg)
	} else {
		m.mig.editSlug, cmd = m.mig.editSlug.Update(msg)
	}
	return m, cmd
}

// commitRemap validates and applies the editor. It reverts and reports an error
// if the remap would collide with another target slug.
func (m *Model) commitRemap() (tea.Model, tea.Cmd) {
	j := m.jobByKey(m.mig.editKey)
	if j == nil {
		m.closeRemap()
		return m, nil
	}
	dest, err := config.NormalizeDestination(m.mig.editDest.Value())
	if err != nil {
		m.mig.editErr = err.Error()
		return m, nil
	}
	slug := strings.TrimSpace(m.mig.editSlug.Value())
	if slug != "" {
		slug = migrate.NormalizeSlug(slug)
	}

	p, ok := m.cfg.ProjectByKey(j.Project)
	if !ok {
		m.mig.editErr = "project " + j.Project + " is not in the config"
		return m, nil
	}
	defCloud, defSlug := migrate.ResolveJob(p, j.Slug)
	noop := (dest == "" || dest == defCloud) && (slug == "" || slug == defSlug)

	prev, had := m.remaps[m.mig.editKey]
	if noop {
		delete(m.remaps, m.mig.editKey)
	} else {
		m.remaps[m.mig.editKey] = remapValue{Destination: dest, TargetSlug: slug}
	}
	m.applyRemaps()
	if cerr := migrate.CheckCollisions(m.plan.Jobs); cerr != nil {
		if had {
			m.remaps[m.mig.editKey] = prev
		} else {
			delete(m.remaps, m.mig.editKey)
		}
		m.applyRemaps()
		m.mig.editErr = cerr.Error()
		return m, nil
	}

	m.closeRemap()
	m.rebuildMigTable()
	m.rebuildProjects()
	m.rebuildStatus()
	m.destExists = nil
	return m, m.checkDestinationsCmd()
}

func (m *Model) remapPanel() string {
	label := m.mig.editKey
	if j := m.jobByKey(m.mig.editKey); j != nil {
		label = j.Project + "/" + j.Slug
	}
	var b strings.Builder
	b.WriteString("\n  " + titleStyle.Render("Remap "+label) + "\n\n")
	b.WriteString("  Cloud project  " + m.mig.editDest.View() + "\n")
	b.WriteString("  Target slug    " + m.mig.editSlug.View() + "\n\n")
	b.WriteString(helpStyle.Render("  tab next field   enter apply   esc cancel") + "\n")
	if m.mig.editErr != "" {
		b.WriteString("  " + badStyle.Render("error: "+m.mig.editErr) + "\n")
	}
	return b.String()
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
		if m.mig.editing {
			b.WriteString(m.remapPanel())
			return b.String()
		}
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
