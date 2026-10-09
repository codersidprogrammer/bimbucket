package coop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/codersidprogrammer/bimbucket/internal/migrate"
)

// leaseTTL is how long a run lease is considered fresh without a heartbeat.
const leaseTTL = 30 * time.Second

// Manager owns a connection to one co-op room: shared state, config remaps,
// chat and the run lease. All network work happens on its own goroutines; it
// never mutates the caller's *config.Config (remaps are drained by the UI).
type Manager struct {
	client *Client
	state  *migrate.State
	device string
	dbURL  string
	apiKey string

	mu       sync.Mutex
	room     *Room
	status   Status
	chat     []ChatMessage
	pending  []OverrideUpdate
	applied  map[string]int64
	notifyCh chan struct{}
	pubCh    chan pubItem
	ctx      context.Context
	cancel   context.CancelFunc
	hbCancel context.CancelFunc
}

type pubItem struct {
	path string
	val  any
}

// New builds a manager. state is the shared ledger it mirrors; dbURL is the
// default Firebase Realtime Database base URL (an invite link may override it).
// device is the default display name, overridable by the Host/Join form.
func New(dbURL, apiKey, device string, state *migrate.State) *Manager {
	return &Manager{
		state:    state,
		device:   device,
		dbURL:    strings.TrimSpace(dbURL),
		apiKey:   strings.TrimSpace(apiKey),
		applied:  map[string]int64{},
		notifyCh: make(chan struct{}, 1),
	}
}

// Configured reports whether a database URL is available (from the environment
// or a previously used invite link).
func (m *Manager) Configured() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dbURL != ""
}

// Device returns the display name currently configured for this device.
func (m *Manager) Device() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.device
}

func (m *Manager) setDevice(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	m.mu.Lock()
	m.device = name
	m.mu.Unlock()
}

// Notifications returns a channel that receives a token whenever the shared
// view changes. It is buffered and coalescing, so signal never blocks and it is
// safe to call from any goroutine (including the UI update loop).
func (m *Manager) Notifications() <-chan struct{} {
	if m == nil {
		return nil
	}
	return m.notifyCh
}

// signal nudges the notification channel without ever blocking the caller.
func (m *Manager) signal() {
	if m == nil {
		return
	}
	select {
	case m.notifyCh <- struct{}{}:
	default:
	}
}

// Connected reports whether a room is joined.
func (m *Manager) Connected() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status.Connected
}

// Host creates a room and connects to it. roomName labels the room, device is
// this participant's display name, and seed carries the local config remaps so
// the host's configuration becomes the room's starting point. The returned
// invite link embeds the database URL so invitees need no environment setup.
func (m *Manager) Host(ctx context.Context, roomName, device, pass string, seed []OverrideUpdate) (string, error) {
	m.mu.Lock()
	db := m.dbURL
	apiKey := m.apiKey
	m.mu.Unlock()
	if db == "" {
		return "", errors.New("coop: FIREBASE_DB_URL is not set (required to host a room)")
	}
	m.setDevice(device)
	client := NewClient(db, apiKey)
	m.mu.Lock()
	m.client = client
	m.mu.Unlock()

	r, err := Host(ctx, client, roomName, pass)
	if err != nil {
		return "", err
	}
	if err := m.connect(r); err != nil {
		return "", err
	}
	// Seed the room with the host's local remaps now that publishing is live.
	for _, u := range seed {
		if u.Updated.IsZero() {
			u.Updated = time.Now().UTC()
		}
		m.publishOverride(u)
	}
	return InviteLink(r.ID, db), nil
}

// Join connects to an existing room. device is this participant's display name.
// The database URL comes from the invite link when present, otherwise from
// FIREBASE_DB_URL.
func (m *Manager) Join(ctx context.Context, invite, device, pass string) error {
	_, db, err := ParseInvite(invite)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if db == "" {
		db = m.dbURL
	}
	apiKey := m.apiKey
	if db != "" {
		m.dbURL = db
		m.client = NewClient(db, apiKey)
	}
	client := m.client
	m.mu.Unlock()
	if db == "" || client == nil {
		return errors.New("coop: the invite has no database URL and FIREBASE_DB_URL is not set")
	}
	m.setDevice(device)
	r, err := Join(ctx, client, invite, pass)
	if err != nil {
		return err
	}
	return m.connect(r)
}

// connect wires up a verified room: it loads the current room contents, starts
// the publisher and change-feed goroutines, and flips the manager to connected.
// The manager's lifetime is independent of the short context used to join.
func (m *Manager) connect(r *Room) error {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.room = r
	m.ctx = ctx
	m.cancel = cancel
	m.pubCh = make(chan pubItem, 1024)
	m.status = Status{
		Configured: true,
		RoomID:     r.ID,
		Invite:     InviteLink(r.ID, m.dbURL),
		Name:       r.Meta.Name,
		Device:     m.device,
		DBURL:      m.dbURL,
	}
	m.mu.Unlock()

	if err := m.refreshRemote(ctx); err != nil {
		cancel()
		return err
	}

	m.state.SetOnUpdate(m.publishState)
	go m.publisher()
	go m.streamLoop()

	m.mu.Lock()
	m.status.Connected = true
	m.status.Err = ""
	m.mu.Unlock()
	m.signal()
	return nil
}

// Close disconnects and stops all background work.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	cancel := m.cancel
	hb := m.hbCancel
	m.status.Connected = false
	m.mu.Unlock()
	if hb != nil {
		hb()
	}
	if cancel != nil {
		cancel()
	}
	if m.state != nil {
		m.state.SetOnUpdate(nil)
	}
}

// --- state mirroring --------------------------------------------------------

func (m *Manager) publishState(rec *migrate.Record) {
	m.mu.Lock()
	r := m.room
	ctx := m.ctx
	m.mu.Unlock()
	if r == nil || ctx == nil {
		return
	}
	cp := *rec
	m.enqueue(pubItem{path: r.Path + "/state/" + encodeKey(rec.Project, rec.Slug), val: &cp})
}

// enqueue hands an item to the publisher. It never blocks the caller: if the
// buffer is full it delivers from a goroutine, so a slow or unreachable
// database can stall neither the migration workers nor the UI.
func (m *Manager) enqueue(it pubItem) {
	m.mu.Lock()
	ch := m.pubCh
	ctx := m.ctx
	m.mu.Unlock()
	if ch == nil || ctx == nil {
		return
	}
	select {
	case ch <- it:
		return
	default:
	}
	go func() {
		select {
		case ch <- it:
		case <-ctx.Done():
		}
	}()
}

func (m *Manager) publisher() {
	m.mu.Lock()
	ctx := m.ctx
	ch := m.pubCh
	m.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case it := <-ch:
			putCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := m.client.Put(putCtx, it.path, it.val)
			cancel()
			if err != nil && ctx.Err() == nil {
				m.setErr(err)
			}
		}
	}
}

func (m *Manager) streamLoop() {
	m.mu.Lock()
	ctx := m.ctx
	room := m.room
	m.mu.Unlock()
	for ctx.Err() == nil {
		ch, err := m.client.Stream(ctx, room.Path)
		if err != nil {
			m.setErr(err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for range ch {
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
			m.refreshRemote(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// refreshRemote re-pulls state, overrides, chat and the lease and merges them.
// Rooms are small, so a full pull per change burst is simpler and safer than
// reconstructing deltas from stream patches.
func (m *Manager) refreshRemote(ctx context.Context) error {
	m.mu.Lock()
	room := m.room
	m.mu.Unlock()
	if room == nil {
		return errors.New("coop: not connected")
	}
	pullCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if err := m.pullState(pullCtx, room); err != nil {
		m.setErr(err)
		return err
	}
	if err := m.pullOverrides(pullCtx, room); err != nil {
		m.setErr(err)
		return err
	}
	if err := m.pullChat(pullCtx, room); err != nil {
		m.setErr(err)
		return err
	}
	if err := m.pullLease(pullCtx, room); err != nil {
		m.setErr(err)
		return err
	}

	m.mu.Lock()
	m.status.LastSync = time.Now()
	m.status.Err = ""
	m.mu.Unlock()
	m.signal()
	return nil
}

func (m *Manager) pullState(ctx context.Context, room *Room) error {
	var raw map[string]migrate.Record
	if err := m.client.Get(ctx, room.Path+"/state", &raw); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	for _, rec := range raw {
		r := rec
		m.state.Merge(&r)
	}
	return nil
}

func (m *Manager) pullOverrides(ctx context.Context, room *Room) error {
	var raw map[string]overrideRecord
	if err := m.client.Get(ctx, room.Path+"/overrides", &raw); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	var updates []OverrideUpdate
	m.mu.Lock()
	for k, ov := range raw {
		if ov.Updated <= m.applied[k] {
			continue
		}
		project, repo, err := decodeKey(k)
		if err != nil {
			continue
		}
		m.applied[k] = ov.Updated
		updates = append(updates, OverrideUpdate{
			Project:     project,
			Repo:        repo,
			Destination: ov.Destination,
			TargetSlug:  ov.TargetSlug,
			Removed:     ov.Removed,
			Updated:     time.Unix(ov.Updated, 0).UTC(),
		})
	}
	m.pending = append(m.pending, updates...)
	m.status.Overrides = len(raw)
	m.mu.Unlock()
	return nil
}

func (m *Manager) pullChat(ctx context.Context, room *Room) error {
	var raw map[string]ChatMessage
	// orderBy=$key with limitToLast keeps only the newest page.
	if err := m.client.GetQ(ctx, room.Path+"/chat", "orderBy=%22%24key%22&limitToLast=200", &raw); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	msgs := make([]ChatMessage, 0, len(raw))
	for id, msg := range raw {
		msg.ID = id
		msgs = append(msgs, msg)
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })

	authors := map[string]bool{}
	for _, msg := range msgs {
		authors[msg.Author] = true
	}
	m.mu.Lock()
	m.chat = msgs
	m.status.Participants = len(authors)
	m.mu.Unlock()
	return nil
}

func (m *Manager) pullLease(ctx context.Context, room *Room) error {
	var lease Lease
	err := m.client.Get(ctx, room.Path+"/lease", &lease)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	m.mu.Lock()
	if errors.Is(err, ErrNotFound) {
		m.status.Lease = nil
	} else {
		l := lease
		m.status.Lease = &l
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) setErr(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	m.status.Err = err.Error()
	m.mu.Unlock()
	m.signal()
}

// --- overrides --------------------------------------------------------------

// DrainOverrides returns and clears the remote remaps waiting to be applied by
// the UI (which owns the in-memory config).
func (m *Manager) DrainOverrides() []OverrideUpdate {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending) == 0 {
		return nil
	}
	out := m.pending
	m.pending = nil
	return out
}

// PublishOverride records a local remap so peers receive it.
func (m *Manager) PublishOverride(u OverrideUpdate) {
	if m == nil {
		return
	}
	m.publishOverride(u)
}

func (m *Manager) publishOverride(u OverrideUpdate) {
	if u.Updated.IsZero() {
		u.Updated = time.Now().UTC()
	}
	m.mu.Lock()
	room := m.room
	device := m.device
	m.applied[encodeKey(u.Project, u.Repo)] = u.Updated.Unix()
	m.mu.Unlock()
	if room == nil {
		return
	}
	rec := overrideRecord{
		Destination: u.Destination,
		TargetSlug:  u.TargetSlug,
		Updated:     u.Updated.Unix(),
		Device:      device,
		Removed:     u.Removed,
	}
	m.enqueue(pubItem{path: room.Path + "/overrides/" + encodeKey(u.Project, u.Repo), val: rec})
}

// --- chat -------------------------------------------------------------------

// SendChat posts a message to the room.
func (m *Manager) SendChat(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	m.mu.Lock()
	room := m.room
	device := m.device
	m.mu.Unlock()
	if room == nil {
		return errors.New("coop: not connected")
	}
	now := time.Now().UTC()
	msg := ChatMessage{
		ID:     fmt.Sprintf("%013d-%s", now.UnixMilli(), randSuffix(4)),
		TS:     now.Unix(),
		Author: device,
		Text:   text,
	}
	// Optimistically append for a snappy UI; the refresh reconciles.
	m.mu.Lock()
	m.chat = append(m.chat, msg)
	m.mu.Unlock()
	m.signal()
	m.enqueue(pubItem{path: room.Path + "/chat/" + msg.ID, val: msg})
	return nil
}

// ChatMessages returns a copy of the current chat log.
func (m *Manager) ChatMessages() []ChatMessage {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ChatMessage, len(m.chat))
	copy(out, m.chat)
	return out
}

// --- lease ------------------------------------------------------------------

// AcquireLease takes the single-writer run lease, or returns an error naming
// the device that currently holds it. Concurrent attempts are resolved with an
// ETag conditional write.
func (m *Manager) AcquireLease(ctx context.Context, runID string) error {
	if m == nil || !m.Connected() {
		return nil
	}
	m.mu.Lock()
	room := m.room
	device := m.device
	m.mu.Unlock()
	if room == nil {
		return nil
	}
	leasePath := room.Path + "/lease"

	var lease Lease
	etag, err := m.client.getETag(ctx, leasePath, &lease)
	if errors.Is(err, ErrNotFound) {
		// Absent lease: getETag returns nullETag, so the write below is
		// create-only and exactly one device can win.
	} else if err != nil {
		return err
	} else if etag == "" {
		return errors.New("coop: run lease read returned no ETag")
	}
	if lease.Active && lease.Device != device && leaseFresh(lease) {
		return fmt.Errorf("coop: migration already running on %q", lease.Owner)
	}

	mine := Lease{
		Owner:     device,
		Device:    device,
		RunID:     runID,
		StartedAt: time.Now().UTC().Unix(),
		Heartbeat: time.Now().UTC().Unix(),
		Active:    true,
	}
	if err := m.client.PutIfMatch(ctx, leasePath, mine, etag); err != nil {
		if errors.Is(err, ErrPrecondition) {
			return errors.New("coop: another device started a run just now; retry")
		}
		return err
	}

	m.mu.Lock()
	m.status.Lease = &mine
	hbCtx, hbCancel := context.WithCancel(m.ctx)
	m.hbCancel = hbCancel
	m.mu.Unlock()
	m.signal()
	go m.heartbeat(hbCtx, leasePath, runID)
	return nil
}

func (m *Manager) heartbeat(ctx context.Context, leasePath, runID string) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.mu.Lock()
			device := m.device
			m.mu.Unlock()
			lease := Lease{
				Owner:     device,
				Device:    device,
				RunID:     runID,
				StartedAt: time.Now().UTC().Unix(),
				Heartbeat: time.Now().UTC().Unix(),
				Active:    true,
			}
			putCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := m.client.Put(putCtx, leasePath, lease)
			cancel()
			if err == nil {
				m.mu.Lock()
				m.status.Lease = &lease
				m.mu.Unlock()
			}
		}
	}
}

// ReleaseLease clears the lease, allowing another device to run.
func (m *Manager) ReleaseLease() {
	if m == nil {
		return
	}
	m.mu.Lock()
	room := m.room
	device := m.device
	hb := m.hbCancel
	m.hbCancel = nil
	m.mu.Unlock()
	if hb != nil {
		hb()
	}
	if room == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = m.client.Put(ctx, room.Path+"/lease", Lease{Active: false, Device: device,
		Heartbeat: time.Now().UTC().Unix()})
	m.mu.Lock()
	m.status.Lease = nil
	m.mu.Unlock()
	m.signal()
}

// LeaseOwner returns the device currently holding an active, fresh lease.
func (m *Manager) LeaseOwner() (string, bool) {
	if m == nil {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.status.Lease
	if l == nil || !l.Active || !leaseFresh(*l) {
		return "", false
	}
	return l.Owner, true
}

func leaseFresh(l Lease) bool {
	return time.Since(time.Unix(l.Heartbeat, 0)) < leaseTTL
}

// --- status -----------------------------------------------------------------

// Snapshot returns an immutable view for rendering.
func (m *Manager) Snapshot() Status {
	if m == nil {
		return Status{}
	}
	m.mu.Lock()
	s := m.status
	m.mu.Unlock()
	if m.state != nil {
		s.StateRecords = len(m.state.Snapshot())
	}
	return s
}

func randSuffix(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "0000"
	}
	return hex.EncodeToString(b)
}
