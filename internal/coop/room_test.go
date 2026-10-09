package coop

import (
	"context"
	"errors"
	"testing"
)

func TestDerivePathDependsOnBothSecrets(t *testing.T) {
	a := DerivePath("room123", "hunter2")
	if a != DerivePath("room123", "hunter2") {
		t.Error("path is not deterministic")
	}
	if a == DerivePath("room123", "wrong") {
		t.Error("path must differ for a different passphrase")
	}
	if a == DerivePath("other", "hunter2") {
		t.Error("path must differ for a different room id")
	}
	if len(a) != len("rooms/")+32 {
		t.Errorf("unexpected path shape %q", a)
	}
}

func TestParseInvite(t *testing.T) {
	cases := map[string]string{
		"bimbucket://coop/abc123": "abc123",
		"coop://abc123":           "abc123",
		"abc123":                  "abc123",
		"https://x/y#room=abc123": "abc123",
		"  abc123  ":              "abc123",
	}
	for in, want := range cases {
		got, db, err := ParseInvite(in)
		if err != nil || got != want || db != "" {
			t.Errorf("ParseInvite(%q) = %q,%q,%v; want %q", in, got, db, err, want)
		}
	}
	got, db, err := ParseInvite("bimbucket://coop/abc123?db=https%3A%2F%2Fdb.example")
	if err != nil || got != "abc123" || db != "https://db.example" {
		t.Errorf("db invite = %q,%q,%v", got, db, err)
	}
	if _, _, err := ParseInvite("bad id!"); err == nil {
		t.Error("invalid room id should fail")
	}
	if _, _, err := ParseInvite(""); err == nil {
		t.Error("empty invite should fail")
	}
}

func TestEncodeDecodeKey(t *testing.T) {
	k := encodeKey("XOPS", "microservice-soev2")
	if k == "" || containsSlash(k) {
		t.Fatalf("encoded key must be safe: %q", k)
	}
	p, r, err := decodeKey(k)
	if err != nil || p != "XOPS" || r != "microservice-soev2" {
		t.Errorf("decodeKey = %q/%q, %v", p, r, err)
	}
}

func containsSlash(s string) bool {
	for _, r := range s {
		if r == '/' {
			return true
		}
	}
	return false
}

func TestHostAndJoin(t *testing.T) {
	srv := newFakeDB().server()
	defer srv.Close()
	c := NewClient(srv.URL, "")
	ctx := context.Background()

	room, err := Host(ctx, c, "team", "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	invite := InviteLink(room.ID, "")

	joined, err := Join(ctx, c, invite, "s3cret")
	if err != nil {
		t.Fatalf("join with correct passphrase failed: %v", err)
	}
	if joined.Path != room.Path {
		t.Errorf("joined path %q != host path %q", joined.Path, room.Path)
	}

	if _, err := Join(ctx, c, invite, "wrong"); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("wrong passphrase err = %v, want ErrRoomNotFound", err)
	}
}
