package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
)

func (m *Model) rebuildConnection() {
	cols := []table.Column{
		{Title: "Endpoint", Width: 45},
		{Title: "Status", Width: 8},
		{Title: "Latency", Width: 10},
		{Title: "Detail", Width: m.fillWidth(45 + 8 + 10)},
	}
	var rows []table.Row
	if m.ping != nil {
		for _, r := range m.ping.results {
			status := "ok"
			if r.err != nil {
				status = "error"
			}
			rows = append(rows, table.Row{r.name, status, r.latency.Round(time.Millisecond).String(), r.detail})
		}
	}
	m.connTable = m.newTable(cols, rows)
}

func (m *Model) viewConnection() string {
	if m.ping == nil {
		return fmt.Sprintf("  %s checking connections...", m.spinner.View())
	}
	var b strings.Builder
	b.WriteString(m.connTable.View())
	for _, r := range m.ping.results {
		if h := authHint(r.detail); h != "" {
			b.WriteString("\n")
			b.WriteString(warnStyle.Render("  ! " + h))
		}
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("  last checked " + m.ping.at.Format("15:04:05")))
	return b.String()
}

// authHint turns known Bitbucket Cloud auth failures into actionable guidance.
func authHint(detail string) string {
	if strings.Contains(detail, "no Bitbucket scopes") {
		return "the Cloud API token has no Bitbucket scopes; recreate it at " +
			"https://id.atlassian.com/manage-profile/security/api-tokens with app Bitbucket and scopes: " +
			"read:user:bitbucket, read:workspace:bitbucket, read:project:bitbucket, admin:project:bitbucket, " +
			"read:repository:bitbucket, write:repository:bitbucket, admin:repository:bitbucket, delete:repository:bitbucket"
	}
	return ""
}
