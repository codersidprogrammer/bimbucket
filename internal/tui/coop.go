package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/codersidprogrammer/bimbucket/internal/config"
	"github.com/codersidprogrammer/bimbucket/internal/coop"
)

// coopMode is the state of the Co-op tab's host/join form.
type coopMode int

const (
	coopIdle coopMode = iota
	coopHost
	coopJoin
)

func (m *Model) newCoopInput(placeholder, value string) textinput.Model {
	in := textinput.New()
	in.Placeholder = placeholder
	in.SetValue(value)
	in.CharLimit = 256
	w := m.width - 24
	if w < 20 {
		w = 20
	}
	in.Width = w
	return in
}

func (m *Model) startCoopHost() {
	m.coopMode = coopHost
	m.coopField = 0
	m.coopMsg = ""
	m.coopErr = ""
	m.coopForm[0] = m.newCoopInput("your name", m.coop.Device())
	m.coopForm[1] = m.newCoopInput("room name (e.g. team-a)", "")
	m.coopForm[2] = m.newCoopInput("passphrase", "")
	m.coopForm[2].EchoMode = textinput.EchoPassword
	_ = m.focusCoop(0)
}

func (m *Model) startCoopJoin(link string) {
	m.coopMode = coopJoin
	m.coopField = 0
	m.coopMsg = ""
	m.coopErr = ""
	m.coopForm[0] = m.newCoopInput("your name", m.coop.Device())
	m.coopForm[1] = m.newCoopInput("bimbucket://coop/<room>", link)
	m.coopForm[2] = m.newCoopInput("passphrase", "")
	m.coopForm[2].EchoMode = textinput.EchoPassword
	// Always start on the name field so a joiner picks an identity first.
	_ = m.focusCoop(0)
}

func (m *Model) stopCoopForms() {
	for i := range m.coopForm {
		m.coopForm[i].Blur()
	}
}

func (m *Model) focusCoop(i int) tea.Cmd {
	m.coopField = i
	for j := range m.coopForm {
		m.coopForm[j].Blur()
	}
	return m.coopForm[i].Focus()
}

// coopSeed turns the local config remaps into the room's initial overrides.
func (m *Model) coopSeed() []coop.OverrideUpdate {
	now := time.Now().UTC()
	var out []coop.OverrideUpdate
	for _, p := range m.cfg.Projects {
		for _, ov := range p.Overrides {
			out = append(out, coop.OverrideUpdate{
				Project:     p.Key,
				Repo:        ov.Repo,
				Destination: ov.Destination,
				TargetSlug:  ov.TargetSlug,
				Updated:     now,
			})
		}
	}
	return out
}

// submitCoop validates the form, then performs Host/Join off the UI thread.
// Validation failures are reported in place and return no command.
func (m *Model) submitCoop() tea.Cmd {
	name := strings.TrimSpace(m.coopForm[0].Value())
	target := strings.TrimSpace(m.coopForm[1].Value())
	pass := m.coopForm[2].Value()
	mode := m.coopMode
	seed := m.coopSeed()
	mgr := m.coop

	if name == "" {
		m.coopMsg = "your name is required"
		return nil
	}
	if mode == coopHost && target == "" {
		m.coopMsg = "room name is required"
		return nil
	}
	if mode == coopJoin && target == "" {
		m.coopMsg = "invite link is required"
		return nil
	}
	if strings.TrimSpace(pass) == "" {
		m.coopMsg = "passphrase is required"
		return nil
	}
	m.coopMsg = "connecting..."

	return func() tea.Msg {
		if mgr == nil {
			return coopJoinMsg{err: errors.New("co-op is not configured")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if mode == coopHost {
			invite, err := mgr.Host(ctx, target, name, pass, seed)
			return coopJoinMsg{invite: invite, err: err}
		}
		return coopJoinMsg{err: mgr.Join(ctx, target, name, pass)}
	}
}

func (m *Model) handleCoopKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	const fields = 3
	switch msg.String() {
	case "esc":
		m.coopMode = coopIdle
		m.stopCoopForms()
		m.coopMsg = ""
		return m, nil
	case "tab", "down":
		return m, m.focusCoop((m.coopField + 1) % fields)
	case "shift+tab", "up":
		return m, m.focusCoop((m.coopField + fields - 1) % fields)
	case "enter":
		return m, m.submitCoop()
	}
	var cmd tea.Cmd
	m.coopForm[m.coopField], cmd = m.coopForm[m.coopField].Update(msg)
	return m, cmd
}

// waitForCoop turns the manager's coalescing notification channel into a
// bubbletea command, the same pattern as waitForEvent. It replaces Program.Send
// so notifications can never block the update loop.
func waitForCoop(ch <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-ch
		return coopUpdateMsg{}
	}
}

func (m *Model) handleCoopViewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.coop == nil {
		return m, nil
	}
	if m.coop.Connected() {
		if msg.String() == "l" {
			m.coop.Close()
			m.chatFocus = false
			m.coopMsg = ""
			m.coopErr = ""
			m.resize()
		}
		return m, nil
	}
	switch msg.String() {
	case "h":
		if !m.coop.Configured() {
			m.coopMsg = "hosting needs FIREBASE_DB_URL"
			return m, nil
		}
		m.startCoopHost()
	case "j":
		m.startCoopJoin("")
	}
	return m, nil
}

// onCoopUpdate applies queued remote remaps (in the UI goroutine, so the shared
// config is never touched from a network goroutine) and re-renders.
func (m *Model) onCoopUpdate() {
	if m.coop == nil {
		return
	}
	updates := m.coop.DrainOverrides()
	if len(updates) > 0 {
		for _, u := range updates {
			m.applyRemoteOverride(u)
		}
		m.applyRemaps()
		m.destExists = nil
		m.rebuildMigTable()
		m.rebuildProjects()
		m.rebuildConfig()
	}
	m.resize()
	m.rebuildStatus()
	if m.coop.Snapshot().Err == "" {
		m.coopErr = ""
	}
}

func (m *Model) applyRemoteOverride(u coop.OverrideUpdate) {
	if _, ok := m.cfg.ProjectByKey(u.Project); !ok {
		return
	}
	dest, slug := u.Destination, u.TargetSlug
	if u.Removed {
		dest, slug = "", ""
	}
	m.cfg.SetOverride(u.Project, u.Repo, dest, slug)
	_ = config.SetRepoOverride(m.configPath, u.Project, u.Repo, dest, slug)
}

// publishRemap mirrors a local remap edit to the room.
func (m *Model) publishRemap(project, repo, dest, slug string) {
	if m.coop == nil || !m.coop.Connected() {
		return
	}
	m.coop.PublishOverride(coop.OverrideUpdate{
		Project:     project,
		Repo:        repo,
		Destination: dest,
		TargetSlug:  slug,
		Removed:     dest == "" && slug == "",
		Updated:     time.Now().UTC(),
	})
}

func (m *Model) viewCoop() string {
	if m.coop == nil {
		var b strings.Builder
		b.WriteString("\n  " + badStyle.Render("Co-op is unavailable") + "\n")
		return b.String()
	}
	st := m.coop.Snapshot()
	if st.Connected {
		return m.viewCoopStatus(st)
	}
	if m.coopMode != coopIdle {
		return m.viewCoopForm()
	}
	return m.viewCoopIntro()
}

func (m *Model) viewCoopIntro() string {
	var b strings.Builder
	b.WriteString("\n  " + titleStyle.Render("Co-op mode") + "\n\n")
	b.WriteString("  Share migration progress and config remaps across devices in real time.\n\n")
	b.WriteString("  " + goodStyle.Render("h") + " host a room   " + goodStyle.Render("j") + " join with an invite link\n\n")
	if !m.coop.Configured() {
		b.WriteString(warnStyle.Render("  Hosting needs FIREBASE_DB_URL. Joining works when the invitation\n  link includes the database URL.") + "\n\n")
	}
	b.WriteString(mutedStyle.Render("  Partners need both the invite link and the passphrase.") + "\n")
	if m.coopMsg != "" {
		b.WriteString("\n  " + badStyle.Render(m.coopMsg) + "\n")
	}
	return b.String()
}

func (m *Model) viewCoopForm() string {
	label := "Host a room"
	mid := "Room name      "
	if m.coopMode == coopJoin {
		label = "Join a room"
		mid = "Invite link    "
	}
	var b strings.Builder
	b.WriteString("\n  " + titleStyle.Render(label) + "\n\n")
	b.WriteString("  Your name      " + m.coopForm[0].View() + "\n")
	b.WriteString("  " + mid + m.coopForm[1].View() + "\n")
	b.WriteString("  Passphrase     " + m.coopForm[2].View() + "\n\n")
	b.WriteString(helpStyle.Render("  tab next field   enter connect   esc cancel") + "\n")
	if m.coopMsg != "" {
		b.WriteString("  " + warnStyle.Render(m.coopMsg) + "\n")
	}
	return b.String()
}

func (m *Model) viewCoopStatus(st coop.Status) string {
	var b strings.Builder
	b.WriteString("\n  " + titleStyle.Render("Co-op: connected") + "\n\n")
	b.WriteString(fmt.Sprintf("  Room        %s\n", st.RoomID))
	if st.Name != "" {
		b.WriteString(fmt.Sprintf("  Name        %s\n", st.Name))
	}
	b.WriteString(fmt.Sprintf("  Device      %s\n", st.Device))
	b.WriteString(fmt.Sprintf("  Database    %s\n", st.DBURL))
	b.WriteString(fmt.Sprintf("  Synced      %d record(s), %d override(s)\n", st.StateRecords, st.Overrides))
	b.WriteString(fmt.Sprintf("  Peers       %d\n", st.Participants))
	if !st.LastSync.IsZero() {
		b.WriteString(fmt.Sprintf("  Last sync   %s\n", st.LastSync.Local().Format("15:04:05")))
	}

	b.WriteString("\n  " + titleStyle.Render("Invite") + "\n")
	b.WriteString("  " + goodStyle.Render(st.Invite) + "\n")
	b.WriteString(mutedStyle.Render("  Share this link and the passphrase separately.") + "\n")

	b.WriteString("\n  " + titleStyle.Render("Run lease") + "\n")
	if st.Lease != nil && st.Lease.Active {
		b.WriteString(fmt.Sprintf("  held by %s (run %s)\n", st.Lease.Owner, st.Lease.RunID))
	} else {
		b.WriteString("  free\n")
	}

	if st.Err != "" {
		b.WriteString("\n  " + badStyle.Render("error: "+st.Err) + "\n")
	}
	if m.coopMsg != "" {
		b.WriteString("\n  " + badStyle.Render(m.coopMsg) + "\n")
	}
	b.WriteString("\n" + helpStyle.Render("  l leave room   c chat") + "\n")
	return b.String()
}
