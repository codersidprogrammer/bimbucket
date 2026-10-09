package coop

import "time"

// RoomMeta is the room descriptor written at meta/. Its Verify token lets a
// joiner reject a wrong passphrase (the derived path silently points elsewhere).
type RoomMeta struct {
	Name      string `json:"name"`
	Verify    string `json:"verify"`
	CreatedAt int64  `json:"created_at"`
}

// overrideRecord is one shared per-repository remap. Removed is a tombstone so
// clearing an override propagates to peers.
type overrideRecord struct {
	Destination string `json:"destination,omitempty"`
	TargetSlug  string `json:"target_slug,omitempty"`
	Updated     int64  `json:"updated"`
	Device      string `json:"device,omitempty"`
	Removed     bool   `json:"removed,omitempty"`
}

// OverrideUpdate is a config remap applied by the TUI in its own goroutine, so
// the coop manager never touches the shared *config.Config off-thread.
type OverrideUpdate struct {
	Project     string
	Repo        string
	Destination string
	TargetSlug  string
	Removed     bool
	Updated     time.Time
}

// ChatMessage is one chat line in the room.
type ChatMessage struct {
	ID     string `json:"id"`
	TS     int64  `json:"ts"`
	Author string `json:"author"`
	Text   string `json:"text"`
}

// Lease is the single-writer guard for destructive runs.
type Lease struct {
	Owner     string `json:"owner"`
	Device    string `json:"device"`
	RunID     string `json:"run_id"`
	StartedAt int64  `json:"started_at"`
	Heartbeat int64  `json:"heartbeat"`
	Active    bool   `json:"active"`
}

// Status is an immutable view of the manager for rendering.
type Status struct {
	Configured   bool
	Connected    bool
	RoomID       string
	Invite       string
	Name         string
	Device       string
	DBURL        string
	LastSync     time.Time
	Err          string
	Participants int
	StateRecords int
	Overrides    int
	Lease        *Lease
}
