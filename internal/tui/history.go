package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/codersidprogrammer/bimbucket/internal/migrate"
)

func (m *Model) handleHistoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.histDetail {
		switch msg.String() {
		case "esc":
			m.histDetail = false
			return m, nil
		case "r":
			return m, m.loadEventsCmd(m.histRun)
		}
		var cmd tea.Cmd
		m.eventsTable, cmd = m.eventsTable.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "r":
		return m, m.loadRunsCmd()
	case "enter":
		idx := m.histTable.Cursor()
		if idx >= 0 && idx < len(m.runs) {
			m.histRun = m.runs[idx]
			m.histDetail = true
			return m, m.loadEventsCmd(m.histRun)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.histTable, cmd = m.histTable.Update(msg)
	return m, cmd
}

func (m *Model) rebuildHistory() {
	cols := []table.Column{
		{Title: "Run ID", Width: 24},
		{Title: "When", Width: 22},
		{Title: "Events", Width: 8},
		{Title: "Errors", Width: 8},
	}
	rows := make([]table.Row, 0, len(m.runs))
	for _, r := range m.runs {
		rows = append(rows, table.Row{r.RunID, r.When.Local().Format("2006-01-02 15:04:05"), fmt.Sprint(r.Events), fmt.Sprint(r.Errors)})
	}
	m.histTable = m.newTable(cols, rows)
}

func (m *Model) rebuildEvents(events []migrate.Event) {
	m.histEvents = events
	m.rebuildEventsTable()
}

func (m *Model) rebuildEventsTable() {
	cols := []table.Column{
		{Title: "Time", Width: 10},
		{Title: "Repository", Width: 30},
		{Title: "Stage", Width: 14},
		{Title: "Status", Width: 8},
		{Title: "ms", Width: 8},
		{Title: "Error", Width: m.fillWidth(10 + 30 + 14 + 8 + 8)},
	}
	rows := make([]table.Row, 0, len(m.histEvents))
	for _, ev := range m.histEvents {
		ts := ""
		if !ev.TS.IsZero() {
			ts = ev.TS.Local().Format("15:04:05")
		}
		rows = append(rows, table.Row{ts, ev.Project + "/" + ev.Repo, ev.Stage, ev.Status, fmt.Sprint(ev.DurationMS), ev.Error})
	}
	m.eventsTable = m.newTable(cols, rows)
}

func (m *Model) viewHistory() string {
	if m.histDetail {
		var b strings.Builder
		b.WriteString(mutedStyle.Render("  run "+m.histRun.RunID) + "\n")
		b.WriteString(m.eventsTable.View())
		return b.String()
	}

	if m.runsErr != nil {
		return "  " + badStyle.Render("history error: "+m.runsErr.Error()) + "\n\n  press r to retry"
	}
	if !m.runsLoaded {
		return fmt.Sprintf("  %s loading run history...", m.spinner.View())
	}
	if len(m.runs) == 0 {
		return "  no runs found in " + m.logDir
	}

	var b strings.Builder
	b.WriteString(m.histTable.View())
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(fmt.Sprintf("  %d run(s) in %s", len(m.runs), m.logDir)))
	return b.String()
}
