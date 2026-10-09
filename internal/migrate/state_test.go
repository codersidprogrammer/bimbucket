package migrate

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStateSnapshotSortedAndDetached(t *testing.T) {
	s := LoadState(filepath.Join(t.TempDir(), "state.json"))
	s.Update(&Record{Project: "B", Slug: "b", Status: StatusDone})
	s.Update(&Record{Project: "A", Slug: "a", Status: StatusFailed})

	snap := s.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(snap))
	}
	if snap[0].Project != "A" || snap[1].Project != "B" {
		t.Fatalf("snapshot not sorted: %s, %s", snap[0].Project, snap[1].Project)
	}

	snap[0].Status = StatusSkipped
	if got := s.Get("A", "a").Status; got != StatusFailed {
		t.Errorf("snapshot mutated ledger: got %s", got)
	}
}

func TestStateMergeLastWriteWins(t *testing.T) {
	s := LoadState(filepath.Join(t.TempDir(), "state.json"))
	now := time.Now().UTC()
	s.Merge(&Record{Project: "A", Slug: "a", Status: StatusCloning, Updated: now})

	// An older remote record must not overwrite the newer local one.
	if changed := s.Merge(&Record{Project: "A", Slug: "a", Status: StatusPending, Updated: now.Add(-time.Minute)}); changed {
		t.Error("older record should not be merged")
	}
	if got := s.Get("A", "a").Status; got != StatusCloning {
		t.Errorf("status = %s, want cloning", got)
	}

	// A newer remote record wins.
	if changed := s.Merge(&Record{Project: "A", Slug: "a", Status: StatusDone, Updated: now.Add(time.Minute)}); !changed {
		t.Error("newer record should be merged")
	}
	if got := s.Get("A", "a").Status; got != StatusDone {
		t.Errorf("status = %s, want done", got)
	}
}

func TestStateMergeDoesNotFireHook(t *testing.T) {
	s := LoadState(filepath.Join(t.TempDir(), "state.json"))
	var hookCalls int
	s.SetOnUpdate(func(*Record) { hookCalls++ })

	s.Merge(&Record{Project: "A", Slug: "a", Status: StatusDone, Updated: time.Now().UTC()})
	if hookCalls != 0 {
		t.Errorf("Merge fired the update hook %d times, want 0", hookCalls)
	}

	s.Update(&Record{Project: "B", Slug: "b", Status: StatusDone})
	if hookCalls != 1 {
		t.Errorf("Update fired the update hook %d times, want 1", hookCalls)
	}
}
