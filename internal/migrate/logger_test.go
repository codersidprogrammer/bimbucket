package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeRun(t *testing.T, dir, runID string, events []Event) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, runFilePrefix+runID+runFileSuffix))
	if err != nil {
		t.Fatalf("create run file: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			t.Fatalf("encode event: %v", err)
		}
	}
}

func TestListRunsSummarisesNewestFirst(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	writeRun(t, dir, "aaa", []Event{
		{TS: base, Project: "P", Repo: "r1", Stage: "clone", Status: "ok"},
		{TS: base.Add(time.Minute), Project: "P", Repo: "r1", Stage: "push", Status: "error", Error: "boom"},
	})
	writeRun(t, dir, "bbb", []Event{
		{TS: base.Add(2 * time.Hour), Project: "P", Repo: "r2", Stage: "clone", Status: "ok"},
	})
	// Non-run files must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "report-aaa.csv"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	runs, err := ListRuns(dir)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}
	if runs[0].RunID != "bbb" {
		t.Errorf("newest run = %q, want bbb", runs[0].RunID)
	}
	if runs[1].RunID != "aaa" || runs[1].Events != 2 || runs[1].Errors != 1 {
		t.Errorf("aaa summary = %+v", runs[1])
	}
	if !runs[1].When.Equal(base) {
		t.Errorf("aaa when = %v, want %v", runs[1].When, base)
	}
}

func TestListRunsMissingDirIsEmpty(t *testing.T) {
	runs, err := ListRuns(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("got %d runs, want 0", len(runs))
	}
}

func TestReadEventsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := []Event{
		{TS: time.Now().UTC(), Project: "P", Repo: "r", Stage: "clone", Status: "ok", DurationMS: 12},
		{TS: time.Now().UTC(), Project: "P", Repo: "r", Stage: "push", Status: "error", Error: "nope"},
	}
	writeRun(t, dir, "ccc", want)

	runs, err := ListRuns(dir)
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListRuns: %v, runs=%d", err, len(runs))
	}
	got, err := ReadEvents(runs[0].Path)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d", len(got), len(want))
	}
	if got[1].Stage != "push" || got[1].Error != "nope" || got[0].DurationMS != 12 {
		t.Errorf("round trip mismatch: %+v", got)
	}
}
