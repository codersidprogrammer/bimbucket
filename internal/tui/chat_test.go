package tui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/codersidprogrammer/bimbucket/internal/config"
	"github.com/codersidprogrammer/bimbucket/internal/coop"
	"github.com/codersidprogrammer/bimbucket/internal/migrate"
)

// fakeRTDB is a minimal Realtime Database: null for every read, 200 for every
// write, and a blocking event stream. Enough to connect a room in a test.
func fakeRTDB(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
			return
		}
		if r.Method == http.MethodGet {
			io.WriteString(w, "null")
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

// TestChatSendDoesNotHangUpdateLoop reproduces the reported hang: sending a chat
// message used to call Program.Send from inside Update, which deadlocked on the
// unbuffered program channel. The notification path is now a plain channel, so
// the handler must return immediately.
func TestChatSendDoesNotHangUpdateLoop(t *testing.T) {
	srv := fakeRTDB(t)
	defer srv.Close()

	m := &Model{
		cfg:       &config.Config{},
		state:     migrate.LoadState(filepath.Join(t.TempDir(), "state.json")),
		coop:      coop.New(srv.URL, "", "me", migrate.LoadState(filepath.Join(t.TempDir(), "state.json"))),
		chatInput: textinput.New(),
		mig:       migState{selected: map[string]bool{}, filter: textinput.New()},
	}
	if _, err := m.coop.Host(context.Background(), "team", "alice", "pw", nil); err != nil {
		t.Fatalf("host: %v", err)
	}
	defer m.coop.Close()

	m.chatFocus = true
	m.chatInput.SetValue("hello")

	done := make(chan struct{})
	go func() {
		_, _ = m.handleChatKey(tea.KeyMsg{Type: tea.KeyEnter})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("chat send hung the update loop")
	}
}

// TestWaitForCoopDelivers ensures the notification bridge surfaces a message
// exactly when the manager signals.
func TestWaitForCoopDelivers(t *testing.T) {
	ch := make(chan struct{}, 1)
	cmd := waitForCoop(ch)
	if cmd == nil {
		t.Fatal("waitForCoop returned nil")
	}
	ch <- struct{}{}
	if _, ok := cmd().(coopUpdateMsg); !ok {
		t.Fatal("waitForCoop did not yield a coopUpdateMsg")
	}
}

// TestFrameHeightFitsTerminal guards the reported bug where a busy chat (and a
// co-op error) grew the composed view past the terminal height; Bubble Tea then
// drops lines from the top, hiding the tab bar.
func TestFrameHeightFitsTerminal(t *testing.T) {
	srv := fakeRTDB(t)
	defer srv.Close()

	m := &Model{
		cfg:       &config.Config{Options: config.Options{Workers: 3}},
		state:     migrate.LoadState(filepath.Join(t.TempDir(), "state.json")),
		logDir:    t.TempDir(),
		statePath: filepath.Join(t.TempDir(), "state.json"),
		spinner:   spinner.New(),
		coop:      coop.New(srv.URL, "", "me", migrate.LoadState(filepath.Join(t.TempDir(), "state.json"))),
		chatInput: textinput.New(),
		mig:       migState{selected: map[string]bool{}, filter: textinput.New()},
	}
	if _, err := m.coop.Host(context.Background(), "team", "alice", "pw", nil); err != nil {
		t.Fatalf("host: %v", err)
	}
	defer m.coop.Close()

	m.plan = &migrate.Plan{}
	m.rebuildConfig()
	m.width, m.height = 120, 30
	m.resize()

	long := strings.Repeat("crowded chat line that should wrap ", 12)
	for i := 0; i < 80; i++ {
		_ = m.coop.SendChat(long)
	}
	// The reported trigger: a busy chat plus a co-op error banner and a sidebar
	// status line. Previously the frame grew to height+1 and Bubble Tea dropped
	// the top row (the tab bar).
	m.coopErr = "coop: another device started a run just now; retry"
	m.coopMsg = "another device started a run just now; retry"
	m.rebuildSidebar()

	out := m.View()
	lines := strings.Split(out, "\n")
	if len(lines) > m.height {
		t.Fatalf("frame has %d lines, terminal height %d: tab bar would be clipped", len(lines), m.height)
	}
	if !strings.Contains(lines[0], viewNames[0]) {
		t.Errorf("first line is not the tab bar: %q", lines[0])
	}
}
