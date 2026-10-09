package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"

	"github.com/codersidprogrammer/bimbucket/internal/migrate"
)

// newTable builds a focused, full-size table consistent across views.
func (m *Model) newTable(cols []table.Column, rows []table.Row) table.Model {
	width := m.mainWidth() - 2
	if width < 20 {
		width = 20
	}
	return table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(m.contentHeight()),
		table.WithWidth(width),
		table.WithStyles(table.DefaultStyles()),
	)
}

// fillWidth returns the width left for a flexible column after the fixed ones
// and a small margin, with a sane minimum.
func (m *Model) fillWidth(fixed int) int {
	w := m.mainWidth() - 2 - fixed
	if w < 20 {
		w = 20
	}
	return w
}

// statusFilter narrows the Projects view by migration state. It is independent
// of the free-text filter, so both apply together.
type statusFilter int

const (
	projStatusAll statusFilter = iota
	projStatusMigrated
	projStatusPending
	projStatusFailed
	statusFilterCount
)

func (s statusFilter) label() string {
	switch s {
	case projStatusMigrated:
		return "migrated"
	case projStatusPending:
		return "not migrated"
	case projStatusFailed:
		return "failed"
	default:
		return "all"
	}
}

func (s statusFilter) matches(rec *migrate.Record) bool {
	switch s {
	case projStatusMigrated:
		return isMigrated(rec)
	case projStatusPending:
		return isPending(rec)
	case projStatusFailed:
		return isFailed(rec)
	default:
		return true
	}
}

// cycleStatusFilter advances to the next status filter and rebuilds the table.
func (m *Model) cycleStatusFilter() {
	m.projStatus = (m.projStatus + 1) % statusFilterCount
	m.rebuildProjects()
}

func (m *Model) rebuildProjects() {
	if m.plan == nil {
		return
	}
	flex := m.fillWidth(16 + 20 + 14 + 18)
	repoW := flex / 2
	targetW := flex - repoW

	cols := []table.Column{
		{Title: "Project", Width: 16},
		{Title: "Dest project", Width: 20},
		{Title: "Repo", Width: repoW},
		{Title: "Target slug", Width: targetW},
		{Title: "Status", Width: 14},
		{Title: "Updated", Width: 18},
	}
	query := m.projQuery()
	rows := make([]table.Row, 0, len(m.plan.Jobs))
	for _, j := range m.plan.Jobs {
		if !projectMatches(query, j) {
			continue
		}
		rec := m.state.Get(j.Project, j.Slug)
		if !m.projStatus.matches(rec) {
			continue
		}
		rows = append(rows, table.Row{j.Project, m.destLabel(j.CloudProject), j.Slug, j.TargetSlug, repoStatus(rec), updatedAt(rec)})
	}
	m.projShown = len(rows)
	m.projTable = m.newTable(cols, rows)
	if h := m.contentHeight() - 1; h >= 3 { // reserve a line for the search bar
		m.projTable.SetHeight(h)
	}
}

// destLabel renders a destination project key with its Cloud existence status.
func (m *Model) destLabel(dest string) string {
	if dest == "" {
		return "-"
	}
	if m.destExists == nil {
		return dest + " (?)"
	}
	exists, ok := m.destExists[dest]
	if !ok {
		return dest + " (?)"
	}
	if exists {
		return dest + " (ok)"
	}
	if m.cfg.Options.CreateCloudProjects {
		return dest + " (new)"
	}
	return dest + " (missing)"
}

func (m *Model) projQuery() string {
	return strings.ToLower(strings.TrimSpace(m.projFilter.Value()))
}

func projectMatches(query string, j migrate.RepoJob) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(j.Project), query) ||
		strings.Contains(strings.ToLower(j.Slug), query) ||
		strings.Contains(strings.ToLower(j.TargetSlug), query)
}

func (m *Model) viewProjects() string {
	if m.planErr != nil {
		return "  " + badStyle.Render("plan error: "+m.planErr.Error()) + "\n\n  press r to retry"
	}
	if m.plan == nil {
		return fmt.Sprintf("  %s loading projects...", m.spinner.View())
	}
	if len(m.plan.Jobs) == 0 {
		return "  no repositories matched the configured projects"
	}

	var migrated, failed int
	for _, j := range m.plan.Jobs {
		rec := m.state.Get(j.Project, j.Slug)
		switch {
		case isMigrated(rec):
			migrated++
		case isFailed(rec):
			failed++
		}
	}
	pending := len(m.plan.Jobs) - migrated - failed

	var b strings.Builder
	b.WriteString(m.projFilter.View())
	b.WriteString("\n")
	if m.projShown == 0 {
		b.WriteString(mutedStyle.Render("  no repositories match " + m.projFilterDesc()))
	} else {
		b.WriteString(m.projTable.View())
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  showing: %s / %d   migrated: %s   not migrated: %s   failed: %s   status: %s",
		goodStyle.Render(fmt.Sprint(m.projShown)),
		len(m.plan.Jobs),
		goodStyle.Render(fmt.Sprint(migrated)),
		warnStyle.Render(fmt.Sprint(pending)),
		badStyle.Render(fmt.Sprint(failed)),
		warnStyle.Render(m.projStatus.label()),
	))
	return b.String()
}

// projFilterDesc describes the active Projects filters for the empty state.
func (m *Model) projFilterDesc() string {
	var parts []string
	if q := strings.TrimSpace(m.projFilter.Value()); q != "" {
		parts = append(parts, strconv.Quote(q))
	}
	if m.projStatus != projStatusAll {
		parts = append(parts, "status "+m.projStatus.label())
	}
	if len(parts) == 0 {
		return "the current filter"
	}
	return strings.Join(parts, " + ")
}
