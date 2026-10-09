package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// tabIndexAt maps a tab-bar column to its view. The tab bar renders
// " N Name" per tab with one space of padding and one space between tabs.
func (m *Model) tabIndexAt(x int) (view, bool) {
	if x <= 0 {
		return 0, false
	}
	pos := 1
	for i, name := range viewNames {
		w := len(fmt.Sprintf("%d %s", i+1, name)) + 2
		if x >= pos && x < pos+w {
			return view(i), true
		}
		pos += w + 1
	}
	return 0, false
}

// activeTable returns the table backing the active view, for wheel scrolling.
func (m *Model) activeTable() tablePointer {
	switch m.active {
	case viewProjects:
		return &m.projTable
	case viewConnection:
		return &m.connTable
	case viewStatus:
		return &m.statusTable
	case viewConfig:
		return &m.cfgTable
	case viewHistory:
		if m.histDetail {
			return &m.eventsTable
		}
		return &m.histTable
	case viewMigrate:
		return &m.mig.table
	}
	return nil
}

// tablePointer is the minimal scrolling surface of bubbles/table.
type tablePointer interface {
	MoveUp(int)
	MoveDown(int)
}

// handleMouse implements the interactive bits: click the tab bar to switch
// views, click the sidebar to chat, and wheel to scroll the active pane.
func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		up := msg.Button == tea.MouseButtonWheelUp
		if m.sideW > 0 && msg.X >= m.mainWidth() {
			if up {
				m.chat.LineUp(2)
			} else {
				m.chat.LineDown(2)
			}
			return m, nil
		}
		if t := m.activeTable(); t != nil {
			if up {
				t.MoveUp(1)
			} else {
				t.MoveDown(1)
			}
		}
		return m, nil
	}

	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	// Tab bar occupies the top row.
	if msg.Y == 0 {
		if v, ok := m.tabIndexAt(msg.X); ok {
			m.chatFocus = false
			m.chatInput.Blur()
			return m.switchView(v)
		}
		return m, nil
	}

	// Anywhere in the sidebar focuses the chat input.
	if m.sideW > 0 && msg.X >= m.mainWidth() {
		m.chatFocus = true
		return m, m.chatInput.Focus()
	}
	return m, nil
}
