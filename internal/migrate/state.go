package migrate

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
	"time"
)

type Status string

const (
	StatusPending        Status = "pending"
	StatusCloning        Status = "cloning"
	StatusPushing        Status = "pushing"
	StatusDone           Status = "done"
	StatusFailed         Status = "failed"
	StatusRolledBack     Status = "rolled_back"
	StatusRollbackFailed Status = "rollback_failed"
	StatusSkipped        Status = "skipped"
)

type Record struct {
	Project      string    `json:"project"`
	Slug         string    `json:"slug"`
	TargetSlug   string    `json:"target_slug"`
	Status       Status    `json:"status"`
	Error        string    `json:"error,omitempty"`
	CreatedByRun bool      `json:"created_by_run"`
	Updated      time.Time `json:"updated"`
}

// State is a small ledger used for idempotent re-runs. It is not a mirror of
// repository data.
type State struct {
	mu       sync.Mutex
	path     string
	Records  map[string]*Record `json:"records"`
	onUpdate func(*Record)
}

func LoadState(path string) *State {
	s := &State{path: path, Records: map[string]*Record{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(raw, s)
	if s.Records == nil {
		s.Records = map[string]*Record{}
	}
	return s
}

func (s *State) Get(project, slug string) *Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Records[key(project, slug)]
}

// Snapshot returns a copy of every record, sorted by project then slug, so
// callers can render the ledger without holding the lock or racing writes.
func (s *State) Snapshot() []*Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Record, 0, len(s.Records))
	for _, r := range s.Records {
		cp := *r
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}

func (s *State) Update(r *Record) {
	s.mu.Lock()
	r.Updated = time.Now().UTC()
	s.Records[key(r.Project, r.Slug)] = r
	_ = s.writeLocked()
	hook := s.onUpdate
	cp := *r
	s.mu.Unlock()

	if hook != nil {
		hook(&cp)
	}
}

// SetOnUpdate installs a hook invoked after every local Update with a copy of
// the record. It exists so an external mirror (e.g. co-op sync) can observe
// changes without the State package knowing about the network. The hook runs
// outside the lock and must not call back into State synchronously.
func (s *State) SetOnUpdate(f func(*Record)) {
	s.mu.Lock()
	s.onUpdate = f
	s.mu.Unlock()
}

// Merge applies a record received from a peer if it is newer than the local one
// (last-write-wins by Updated). It persists the ledger but never fires the
// onUpdate hook, so mirroring a remote change cannot echo back to the peer.
func (s *State) Merge(r *Record) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(r.Project, r.Slug)
	if cur, ok := s.Records[k]; ok && !r.Updated.After(cur.Updated) {
		return false
	}
	cp := *r
	s.Records[k] = &cp
	_ = s.writeLocked()
	return true
}

func (s *State) writeLocked() error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func key(project, slug string) string { return project + "/" + slug }
