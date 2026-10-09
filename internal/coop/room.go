package coop

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ErrRoomNotFound means the derived room path has no meta, i.e. the room does
// not exist or the passphrase is wrong.
var ErrRoomNotFound = errors.New("coop: room not found (wrong link or passphrase)")

const roomIDAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// Room identifies a shared session. The passphrase is never stored anywhere:
// only a hash of (room ID + passphrase) selects the unguessable database path.
type Room struct {
	ID   string
	Pass string
	Path string
	Meta RoomMeta
}

// DerivePath returns the database subtree for a room ID and passphrase. Both
// secrets are required to reach the same path; a wrong passphrase yields a
// different, empty subtree.
func DerivePath(roomID, pass string) string {
	sum := sha256.Sum256([]byte(roomID + "\x00" + pass))
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
	return "rooms/" + strings.ToLower(enc)[:32]
}

// verifyToken is stored in RoomMeta so a joiner can detect a wrong passphrase.
func verifyToken(roomID, pass string) string {
	sum := sha256.Sum256([]byte(roomID + ":" + pass))
	return hex.EncodeToString(sum[:])
}

// GenerateRoomID returns a fresh, URL-safe room identifier.
func GenerateRoomID() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should never fail; fall back to a time-derived ID.
		return fmt.Sprintf("%010d", time.Now().UnixNano()%1e10)
	}
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = roomIDAlphabet[int(v)%len(roomIDAlphabet)]
	}
	return string(out)
}

// InviteLink is the shareable string handed to a partner. It carries the room
// id and the database endpoint (which is not a secret) so the invitee does not
// need to configure FIREBASE_DB_URL. The passphrase is deliberately not part of
// it and must be shared out of band.
func InviteLink(roomID, dbURL string) string {
	link := "bimbucket://coop/" + roomID
	if strings.TrimSpace(dbURL) != "" {
		link += "?db=" + url.QueryEscape(strings.TrimSpace(dbURL))
	}
	return link
}

// ParseInvite extracts the room id and the optional embedded database URL from
// a link, or from a bare room id (in which case dbURL is empty).
func ParseInvite(s string) (roomID, dbURL string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", errors.New("coop: empty invite")
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		query := s[i+1:]
		s = s[:i]
		for _, kv := range strings.Split(query, "&") {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			switch k {
			case "db":
				dbURL, _ = url.QueryUnescape(v)
			case "room":
				s = v
			}
		}
	}
	for _, p := range []string{"bimbucket://coop/", "bimbucket:coop:", "coop://", "coop:"} {
		if strings.HasPrefix(s, p) {
			s = strings.TrimPrefix(s, p)
			break
		}
	}
	if i := strings.Index(s, "room="); i >= 0 {
		s = s[i+len("room="):]
	}
	s = strings.Trim(s, "/ ")
	if s == "" {
		return "", "", errors.New("coop: invite has no room id")
	}
	for _, r := range s {
		if !strings.ContainsRune(roomIDAlphabet, r) {
			return "", "", fmt.Errorf("coop: invalid room id %q", s)
		}
	}
	return s, strings.TrimSpace(dbURL), nil
}

// Host creates a new room: it derives the path, writes the meta record and
// returns the invitable room. A non-empty pre-existing meta is overwritten so
// re-hosting the same ID restarts the room.
func Host(ctx context.Context, c *Client, name, pass string) (*Room, error) {
	if strings.TrimSpace(pass) == "" {
		return nil, errors.New("coop: passphrase is required")
	}
	r := &Room{ID: GenerateRoomID(), Pass: pass}
	r.Path = DerivePath(r.ID, pass)
	r.Meta = RoomMeta{Name: name, Verify: verifyToken(r.ID, pass), CreatedAt: time.Now().UTC().Unix()}
	if err := c.Put(ctx, r.Path+"/meta", r.Meta); err != nil {
		return nil, err
	}
	return r, nil
}

// Join derives the room path from the invite and passphrase and verifies it
// exists, failing with ErrRoomNotFound otherwise.
func Join(ctx context.Context, c *Client, invite, pass string) (*Room, error) {
	id, _, err := ParseInvite(invite)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(pass) == "" {
		return nil, errors.New("coop: passphrase is required")
	}
	r := &Room{ID: id, Pass: pass, Path: DerivePath(id, pass)}
	if err := c.Get(ctx, r.Path+"/meta", &r.Meta); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrRoomNotFound
		}
		return nil, err
	}
	if r.Meta.Verify != verifyToken(id, pass) {
		return nil, ErrRoomNotFound
	}
	return r, nil
}

// encodeKey maps a "project/repo" identifier to a Firebase-safe key.
func encodeKey(project, repo string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(project + "/" + repo))
}

// decodeKey reverses encodeKey.
func decodeKey(k string) (project, repo string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(k)
	if err != nil {
		return "", "", err
	}
	project, repo, ok := strings.Cut(string(raw), "/")
	if !ok {
		return "", "", fmt.Errorf("coop: malformed key %q", k)
	}
	return project, repo, nil
}
