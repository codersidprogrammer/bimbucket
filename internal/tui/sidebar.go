package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	sidebarWidth       = 34
	minWidthForSidebar = 110
)

// sidebarVisible reports whether the co-op chat sidebar should show.
func (m *Model) sidebarVisible() bool {
	return m.coop != nil && m.coop.Connected() && m.width >= minWidthForSidebar
}

// mainWidth is the width available to the main content once the sidebar is
// accounted for.
func (m *Model) mainWidth() int {
	if m.sideW > 0 {
		return m.width - m.sideW
	}
	return m.width
}

// layoutSidebar sizes the chat viewport and rebuilds its content.
func (m *Model) layoutSidebar() {
	if !m.sidebarVisible() {
		m.sideW = 0
		return
	}
	m.sideW = sidebarWidth
	h := m.height
	if h < 6 {
		h = 6
	}
	m.chat.Width = sidebarWidth - 3
	// Leave room for the head, the input, and one spare row for a status or
	// error line so the sidebar never becomes the tallest column.
	m.chat.Height = h - 5
	m.chatInput.Width = sidebarWidth - 5
	m.rebuildSidebar()
}

// rebuildSidebar recomputes the chat viewport content from the manager.
func (m *Model) rebuildSidebar() {
	if m.coop == nil {
		m.chat.SetContent("")
		return
	}
	w := m.chat.Width
	if w < 10 {
		w = 10
	}
	wrap := lipgloss.NewStyle().Width(w)
	var b strings.Builder
	for _, msg := range m.coop.ChatMessages() {
		who := msg.Author
		if who == "" {
			who = "?"
		}
		ts := time.Unix(msg.TS, 0).Local().Format("15:04")
		b.WriteString(goodStyle.Render(who) + " " + mutedStyle.Render(ts) + "\n")
		b.WriteString(wrap.Render(msg.Text) + "\n")
	}
	m.chat.SetContent(b.String())
	m.chat.GotoBottom()
}

// viewSidebar renders the chat panel with a left border.
func (m *Model) viewSidebar() string {
	var b strings.Builder
	head := " " + titleStyle.Render("Chat")
	if st := m.coop.Snapshot(); st.RoomID != "" {
		head += mutedStyle.Render(" · " + st.RoomID)
	}
	b.WriteString(head + "\n")
	b.WriteString(m.chat.View() + "\n")
	b.WriteString(m.chatInput.View())
	if m.coopMsg != "" {
		// Truncate to a single line so a long error cannot grow the sidebar
		// past its height budget and push the tab bar off-screen.
		msg := badStyle.Render(" ! " + m.coopMsg)
		b.WriteString("\n" + lipgloss.NewStyle().MaxWidth(sidebarWidth-5).MaxHeight(1).Render(msg))
	}
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(sidebarWidth - 3).
		Render(b.String())
}

// handleChatKey routes keys to the focused chat input. esc leaves the field and
// enter posts the message.
func (m *Model) handleChatKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.chatFocus = false
		m.chatInput.Blur()
		return m, nil
	case "enter":
		text := m.chatInput.Value()
		m.chatInput.SetValue("")
		m.coopMsg = ""
		m.coopErr = ""
		if m.coop != nil {
			if err := m.coop.SendChat(text); err != nil {
				m.coopMsg = err.Error()
				m.coopErr = err.Error()
			}
		}
		m.rebuildSidebar()
		return m, nil
	}
	var cmd tea.Cmd
	m.chatInput, cmd = m.chatInput.Update(msg)
	return m, cmd
}

// joinColumns places right alongside left, padding left to leftW visible cells.
// Widths are measured ANSI-aware so styled lines still align.
func joinColumns(left, right string, leftW int) string {
	ll := strings.Split(left, "\n")
	rl := strings.Split(right, "\n")
	n := len(ll)
	if len(rl) > n {
		n = len(rl)
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		var l, r string
		if i < len(ll) {
			l = ll[i]
		}
		if i < len(rl) {
			r = rl[i]
		}
		pad := leftW - lipgloss.Width(l)
		if pad < 0 {
			pad = 0
		}
		b.WriteString(l)
		b.WriteString(strings.Repeat(" ", pad))
		b.WriteString(r)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
