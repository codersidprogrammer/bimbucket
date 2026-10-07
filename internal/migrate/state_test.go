package migrate

import (
	"path/filepath"
	"testing"
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
