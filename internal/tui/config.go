package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"

	"github.com/codersidprogrammer/bimbucket/internal/config"
)

func (m *Model) rebuildConfig() {
	cols := []table.Column{
		{Title: "Section", Width: 10},
		{Title: "Key", Width: 22},
		{Title: "Value", Width: m.fillWidth(10 + 22)},
	}
	var rows []table.Row
	add := func(section, key, value string) {
		rows = append(rows, table.Row{section, key, value})
	}

	add("source", "base_url", m.cfg.Source.BaseURL)
	add("source", "auth", sourceAuth(m.cfg.Source))
	add("target", "workspace", m.cfg.Target.Workspace)
	add("target", "email", m.cfg.Target.Email)
	add("target", "api_token", secret(m.cfg.Target.APIToken))

	for _, p := range m.cfg.Projects {
		repos := "all repositories"
		if len(p.Repos) > 0 {
			repos = strings.Join(p.Repos, ", ")
		}
		if p.IncludeArchived {
			repos += " (incl. archived)"
		}
		add("project", p.Key, repos)
	}

	add("options", "workers", fmt.Sprint(m.cfg.Options.Workers))
	add("options", "on_error", m.cfg.Options.OnError)
	add("options", "on_slug_collision", m.cfg.Options.OnSlugCollision)
	add("options", "rollback", fmt.Sprint(m.cfg.Options.Rollback))
	add("options", "create_cloud_projects", fmt.Sprint(m.cfg.Options.CreateCloudProjects))
	add("options", "temp_dir", dash(m.cfg.Options.TempDir))

	m.cfgTable = m.newTable(cols, rows)
}

func (m *Model) viewConfig() string {
	return m.cfgTable.View()
}

func sourceAuth(s config.Source) string {
	if s.Token != "" {
		return "access token (**)"
	}
	if s.User != "" && s.Password != "" {
		return "basic auth (user/password set)"
	}
	return "not configured"
}

func secret(v string) string {
	if v == "" {
		return "not set"
	}
	return "*** (set)"
}

func dash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}
