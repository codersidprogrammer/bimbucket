package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"

	"github.com/codersidprogrammer/bimbucket/internal/migrate"
)

func (m *Model) rebuildStatus() {
	cols := []table.Column{
		{Title: "Metric", Width: 18},
		{Title: "Value", Width: m.fillWidth(18)},
	}
	rows := []table.Row{
		{"Source", m.endpointStatus(0)},
		{"Target", m.endpointStatus(1)},
		{"Plan", m.planStatus()},
		{"Migrated", fmt.Sprint(m.countByState(isMigrated))},
		{"Not migrated", fmt.Sprint(m.countByState(isPending))},
		{"Failed", fmt.Sprint(m.countByState(isFailed))},
		{"Run", m.runStatus()},
	}
	m.statusTable = m.newTable(cols, rows)
}

func (m *Model) endpointStatus(idx int) string {
	if m.ping == nil || idx >= len(m.ping.results) {
		return "not checked"
	}
	r := m.ping.results[idx]
	if r.err != nil {
		return "error: " + r.err.Error()
	}
	return fmt.Sprintf("ok (%s)", r.latency.Round(time.Millisecond))
}

func (m *Model) planStatus() string {
	switch {
	case m.planErr != nil:
		return "error: " + m.planErr.Error()
	case m.plan == nil:
		return "loading..."
	default:
		return fmt.Sprintf("%d repositories", len(m.plan.Jobs))
	}
}

func (m *Model) runStatus() string {
	switch m.mig.phase {
	case migRunning:
		return fmt.Sprintf("running (%d events)", len(m.mig.progress))
	case migDone:
		if m.mig.runErr != nil {
			return "finished: " + m.mig.runErr.Error()
		}
		return "finished: no fatal errors"
	default:
		return "idle"
	}
}

func (m *Model) countByState(pred func(*migrate.Record) bool) int {
	if m.plan == nil {
		return 0
	}
	n := 0
	for _, j := range m.plan.Jobs {
		if pred(m.state.Get(j.Project, j.Slug)) {
			n++
		}
	}
	return n
}

func (m *Model) viewStatus() string {
	return m.statusTable.View()
}
