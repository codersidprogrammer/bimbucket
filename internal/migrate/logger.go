package migrate

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Event is one structured migration event, written as JSONL and mirrored to the
// UI progress sink.
type Event struct {
	TS         time.Time `json:"ts"`
	RunID      string    `json:"run_id"`
	Project    string    `json:"project"`
	Repo       string    `json:"repo"`
	TargetSlug string    `json:"target_slug"`
	Stage      string    `json:"stage"`
	Status     string    `json:"status"`
	DurationMS int64     `json:"duration_ms"`
	Error      string    `json:"error,omitempty"`
}

// ProgressFunc receives events for live display (TUI or stdout).
type ProgressFunc func(ev Event)

type Logger struct {
	mu     sync.Mutex
	dir    string
	runID  string
	file   *os.File
	enc    *json.Encoder
	report *os.File
	csv    *csv.Writer
	events []Event
	sink   ProgressFunc
}

func NewLogger(dir, runID string, sink ProgressFunc) (*Logger, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	file, err := os.Create(filepath.Join(dir, "run-"+runID+".jsonl"))
	if err != nil {
		return nil, err
	}
	report, err := os.Create(filepath.Join(dir, "report-"+runID+".csv"))
	if err != nil {
		file.Close()
		return nil, err
	}
	l := &Logger{
		dir:    dir,
		runID:  runID,
		file:   file,
		enc:    json.NewEncoder(file),
		report: report,
		csv:    csv.NewWriter(report),
		sink:   sink,
	}
	_ = l.csv.Write([]string{"project", "repo", "target_slug", "status", "error"})
	l.csv.Flush()
	return l, nil
}

func (l *Logger) Event(ev Event) {
	l.mu.Lock()
	ev.RunID = l.runID
	if ev.TS.IsZero() {
		ev.TS = time.Now().UTC()
	}
	l.events = append(l.events, ev)
	_ = l.enc.Encode(ev)
	l.mu.Unlock()

	if l.sink != nil {
		l.sink(ev)
	}
}

// Summary writes the per-repository final status to the CSV report.
func (l *Logger) Summary(records []*Record) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range records {
		_ = l.csv.Write([]string{r.Project, r.Slug, r.TargetSlug, string(r.Status), r.Error})
	}
	l.csv.Flush()
}

func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
	}
	if l.report != nil {
		l.report.Close()
	}
}

func (l *Logger) Dir() string { return l.dir }

const (
	runFilePrefix = "run-"
	runFileSuffix = ".jsonl"
)

// RunSummary describes one persisted run, derived from its JSONL event log.
type RunSummary struct {
	RunID  string
	Path   string
	When   time.Time
	Events int
	Errors int
}

// ListRuns scans dir for run-<id>.jsonl files and summarises each, newest
// first. A missing directory yields no runs and no error.
func ListRuns(dir string) ([]RunSummary, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	runs := make([]RunSummary, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, runFilePrefix) || !strings.HasSuffix(name, runFileSuffix) {
			continue
		}
		path := filepath.Join(dir, name)
		events, err := ReadEvents(path)
		if err != nil {
			continue
		}
		rs := RunSummary{
			RunID:  strings.TrimSuffix(strings.TrimPrefix(name, runFilePrefix), runFileSuffix),
			Path:   path,
			Events: len(events),
		}
		for _, ev := range events {
			if ev.Status == "error" {
				rs.Errors++
			}
			if !ev.TS.IsZero() && (rs.When.IsZero() || ev.TS.Before(rs.When)) {
				rs.When = ev.TS
			}
		}
		if rs.When.IsZero() {
			if info, err := e.Info(); err == nil {
				rs.When = info.ModTime()
			}
		}
		runs = append(runs, rs)
	}

	sort.Slice(runs, func(i, j int) bool { return runs[i].When.After(runs[j].When) })
	return runs, nil
}

// ReadEvents decodes every JSONL event from path. A truncated final line is an
// error, reported alongside the events read so far.
func ReadEvents(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []Event
	dec := json.NewDecoder(f)
	for {
		var ev Event
		if err := dec.Decode(&ev); err != nil {
			if err == io.EOF {
				return events, nil
			}
			return events, err
		}
		events = append(events, ev)
	}
}
