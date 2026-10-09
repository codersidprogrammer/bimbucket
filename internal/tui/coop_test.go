package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/codersidprogrammer/bimbucket/internal/config"
	"github.com/codersidprogrammer/bimbucket/internal/coop"
	"github.com/codersidprogrammer/bimbucket/internal/migrate"
)

func TestJoinColumnsAligns(t *testing.T) {
	out := joinColumns("ab\ncd", "X\nY", 5)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	if lines[0] != "ab   X" || lines[1] != "cd   Y" {
		t.Errorf("joinColumns = %q", lines)
	}
	if lipgloss.Width(lines[0]) != lipgloss.Width(lines[1]) {
		t.Errorf("columns not aligned: %q vs %q", lines[0], lines[1])
	}
}

func TestTabIndexAt(t *testing.T) {
	m := &Model{}
	if v, ok := m.tabIndexAt(1); !ok || v != viewProjects {
		t.Errorf("x=1 -> %v,%v", v, ok)
	}
	if _, ok := m.tabIndexAt(0); ok {
		t.Error("x=0 is the leading margin, not a tab")
	}
	// Locate a couple of later tabs by scanning, then confirm they map back.
	want := map[view]bool{viewConnection: true, viewCoop: true, viewAbout: true}
	for x := 1; x < 200; x++ {
		if v, ok := m.tabIndexAt(x); ok {
			delete(want, v)
		}
	}
	if len(want) != 0 {
		t.Errorf("tabs not reachable by hit test: %v", want)
	}
}

func TestMouseTabClickSwitchesView(t *testing.T) {
	m := &Model{width: 200, height: 40}
	x := 0
	for xx := 1; xx < 200; xx++ {
		if v, ok := m.tabIndexAt(xx); ok && v == viewAbout {
			x = xx
			break
		}
	}
	if x == 0 {
		t.Fatal("could not locate About tab")
	}
	_, _ = m.handleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: x, Y: 0})
	if m.active != viewAbout {
		t.Errorf("active = %d, want About", m.active)
	}
}

func TestMainWidthWithSidebar(t *testing.T) {
	m := &Model{width: 120}
	if got := m.mainWidth(); got != 120 {
		t.Errorf("no sidebar mainWidth = %d, want 120", got)
	}
	m.sideW = sidebarWidth
	if got := m.mainWidth(); got != 120-sidebarWidth {
		t.Errorf("with sidebar mainWidth = %d, want %d", got, 120-sidebarWidth)
	}
}

func TestSidebarVisibility(t *testing.T) {
	m := &Model{width: 200}
	if m.sidebarVisible() {
		t.Error("no coop manager must hide the sidebar")
	}
	m.coop = coop.New("https://db.example", "", "dev", migrate.LoadState(t.TempDir()+"/s.json"))
	if m.sidebarVisible() {
		t.Error("unconnected manager must hide the sidebar")
	}
	m.width = 80
	if m.sidebarVisible() {
		t.Error("narrow terminal must hide the sidebar")
	}
}

func TestCoopViewStates(t *testing.T) {
	if out := (&Model{width: 120}).viewCoop(); !strings.Contains(out, "unavailable") {
		t.Errorf("nil manager coop view:\n%s", out)
	}

	m := &Model{
		width: 120,
		cfg:   &config.Config{},
		coop:  coop.New("https://db.example", "", "dev", migrate.LoadState(t.TempDir()+"/s.json")),
	}
	out := m.viewCoop()
	if !strings.Contains(out, "host a room") {
		t.Errorf("configured coop intro missing host hint:\n%s", out)
	}
	if !m.coop.Configured() {
		t.Error("manager should report configured with a db url")
	}
}
