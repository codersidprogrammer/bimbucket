package coop

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codersidprogrammer/bimbucket/internal/migrate"
)

func testState(t *testing.T) *migrate.State {
	t.Helper()
	return migrate.LoadState(filepath.Join(t.TempDir(), "state.json"))
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestManagerHostJoinAndStateSync(t *testing.T) {
	srv := newFakeDB().server()
	defer srv.Close()
	ctx := context.Background()

	s1 := testState(t)
	m1 := New(srv.URL, "", "alpha", s1)
	invite, err := m1.Host(ctx, "team", "alpha", "pw", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m1.Close()

	s2 := testState(t)
	m2 := New(srv.URL, "", "beta", s2)
	if err := m2.Join(ctx, invite, "beta", "pw"); err != nil {
		t.Fatalf("join: %v", err)
	}
	defer m2.Close()

	// Wrong passphrase is rejected.
	if err := New(srv.URL, "", "gamma", testState(t)).Join(ctx, invite, "gamma", "nope"); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("wrong passphrase err = %v, want ErrRoomNotFound", err)
	}

	// A local record on m1 propagates to m2.
	s1.Update(&migrate.Record{Project: "A", Slug: "a", TargetSlug: "a", Status: migrate.StatusDone})
	waitFor(t, "record sync", func() bool {
		_ = m2.refreshRemote(ctx)
		return s2.Get("A", "a") != nil
	})
	if rec := s2.Get("A", "a"); rec == nil || rec.Status != migrate.StatusDone {
		t.Errorf("merged record = %+v", rec)
	}
}

func TestManagerOverrideSyncAndDedupe(t *testing.T) {
	srv := newFakeDB().server()
	defer srv.Close()
	ctx := context.Background()

	m1 := New(srv.URL, "", "alpha", testState(t))
	invite, err := m1.Host(ctx, "team", "alpha", "pw", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m1.Close()

	m2 := New(srv.URL, "", "beta", testState(t))
	if err := m2.Join(ctx, invite, "beta", "pw"); err != nil {
		t.Fatal(err)
	}
	defer m2.Close()

	m1.PublishOverride(OverrideUpdate{
		Project: "XOPS", Repo: "svc", Destination: "MICROSERVICE", Updated: time.Now().UTC(),
	})

	var ups []OverrideUpdate
	waitFor(t, "override sync", func() bool {
		_ = m2.refreshRemote(ctx)
		ups = m2.DrainOverrides()
		return len(ups) > 0
	})
	if ups[0].Project != "XOPS" || ups[0].Destination != "MICROSERVICE" {
		t.Errorf("override update = %+v", ups[0])
	}

	// A drained update must not be re-delivered.
	_ = m2.refreshRemote(ctx)
	if again := m2.DrainOverrides(); len(again) != 0 {
		t.Errorf("override re-delivered: %+v", again)
	}
}

func TestManagerLeaseSerializesRuns(t *testing.T) {
	srv := newFakeDB().server()
	defer srv.Close()
	ctx := context.Background()

	m1 := New(srv.URL, "", "alpha", testState(t))
	invite, err := m1.Host(ctx, "team", "alpha", "pw", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m1.Close()

	m2 := New(srv.URL, "", "beta", testState(t))
	if err := m2.Join(ctx, invite, "beta", "pw"); err != nil {
		t.Fatal(err)
	}
	defer m2.Close()

	if err := m1.AcquireLease(ctx, "run1"); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := m2.AcquireLease(ctx, "run2"); err == nil {
		t.Error("second device should be blocked while lease is held")
	}

	m1.ReleaseLease()
	if err := m2.AcquireLease(ctx, "run2"); err != nil {
		t.Errorf("acquire after release: %v", err)
	}
	m2.ReleaseLease()
}

// TestSignalNeverBlocks guards the bubbletea deadlock: signal is called from the
// UI update goroutine, so it must never wait on a reader.
func TestSignalNeverBlocks(t *testing.T) {
	m := New("https://db.example", "", "dev", testState(t))
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			m.signal()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("signal blocked")
	}
	select {
	case <-m.Notifications():
	default:
		t.Fatal("expected a pending notification")
	}
}

func TestSendChatDoesNotBlockWhenBufferFull(t *testing.T) {
	srv := newFakeDB().server()
	defer srv.Close()
	ctx := context.Background()

	m := New(srv.URL, "", "alpha", testState(t))
	if _, err := m.Host(ctx, "team", "alpha", "pw", nil); err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// Simulate a stalled publisher by pre-filling the outbound buffer.
	m.mu.Lock()
	full := make(chan pubItem, 1)
	full <- pubItem{}
	m.pubCh = full
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		_ = m.SendChat("hello")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SendChat blocked on a full publisher buffer")
	}
}

func TestChosenDeviceNameFlows(t *testing.T) {
	srv := newFakeDB().server()
	defer srv.Close()
	ctx := context.Background()

	m := New(srv.URL, "", "default-name", testState(t))
	if _, err := m.Host(ctx, "team", "alice", "pw", nil); err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if got := m.Device(); got != "alice" {
		t.Errorf("Device() = %q, want alice", got)
	}
	if err := m.SendChat("hi"); err != nil {
		t.Fatal(err)
	}
	msgs := m.ChatMessages()
	if len(msgs) == 0 || msgs[len(msgs)-1].Author != "alice" {
		t.Errorf("chat author = %+v, want alice", msgs)
	}
	if err := m.AcquireLease(ctx, "run1"); err != nil {
		t.Fatal(err)
	}
	defer m.ReleaseLease()
	if owner, ok := m.LeaseOwner(); !ok || owner != "alice" {
		t.Errorf("lease owner = %q,%v, want alice", owner, ok)
	}
}

// TestJoinFromInviteEmbeddedDBURL proves an invitee needs no FIREBASE_DB_URL
// when the invite link carries the database URL.
func TestJoinFromInviteEmbeddedDBURL(t *testing.T) {
	srv := newFakeDB().server()
	defer srv.Close()
	ctx := context.Background()

	host := New(srv.URL, "", "alpha", testState(t))
	invite, err := host.Host(ctx, "team", "alpha", "pw", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if !strings.Contains(invite, "?db=") {
		t.Fatalf("invite must embed the database URL: %s", invite)
	}

	joiner := New("", "", "beta", testState(t))
	if joiner.Configured() {
		t.Fatal("joiner must start unconfigured")
	}
	if err := joiner.Join(ctx, invite, "beta", "pw"); err != nil {
		t.Fatalf("join via embedded db url: %v", err)
	}
	defer joiner.Close()
	if !joiner.Configured() || !joiner.Connected() {
		t.Errorf("after join: configured=%v connected=%v", joiner.Configured(), joiner.Connected())
	}
}
