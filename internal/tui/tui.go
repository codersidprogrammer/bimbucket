package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	goodStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	badStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	activeTab     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("7")).Background(lipgloss.Color("62")).Padding(0, 1)
	inactiveTab   = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Padding(0, 1)
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	statusOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	statusFail    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	statusPending = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)
