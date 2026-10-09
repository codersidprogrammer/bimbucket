package coop

import (
	"context"
	"errors"
	"testing"
)

func TestClientGetPut(t *testing.T) {
	srv := newFakeDB().server()
	defer srv.Close()
	c := NewClient(srv.URL, "")
	ctx := context.Background()

	if err := c.Put(ctx, "rooms/x/meta", RoomMeta{Name: "hi", Verify: "v"}); err != nil {
		t.Fatal(err)
	}
	var got RoomMeta
	if err := c.Get(ctx, "rooms/x/meta", &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "hi" || got.Verify != "v" {
		t.Errorf("round-trip = %+v", got)
	}

	if err := c.Get(ctx, "rooms/nope/meta", &got); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing path err = %v, want ErrNotFound", err)
	}
}

func TestClientETagConditions(t *testing.T) {
	srv := newFakeDB().server()
	defer srv.Close()
	c := NewClient(srv.URL, "")
	ctx := context.Background()

	// A read of an absent location reports the null sentinel ETag.
	if etag, err := c.getETag(ctx, "rooms/x/lease", nil); !errors.Is(err, ErrNotFound) || etag != nullETag {
		t.Fatalf("absent getETag = %q, %v; want %q, ErrNotFound", etag, err, nullETag)
	}

	// Create-only write succeeds when absent, fails when present.
	if err := c.PutIfMatch(ctx, "rooms/x/lease", map[string]int{"n": 1}, nullETag); err != nil {
		t.Fatalf("create-only: %v", err)
	}
	if err := c.PutIfMatch(ctx, "rooms/x/lease", map[string]int{"n": 2}, nullETag); !errors.Is(err, ErrPrecondition) {
		t.Errorf("second create-only err = %v, want ErrPrecondition", err)
	}

	// Read the current ETag, then a stale write must fail.
	etag, err := c.getETag(ctx, "rooms/x/lease", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.PutIfMatch(ctx, "rooms/x/lease", map[string]int{"n": 3}, etag); err != nil {
		t.Fatalf("matching ETag write: %v", err)
	}
	if err := c.PutIfMatch(ctx, "rooms/x/lease", map[string]int{"n": 4}, etag); !errors.Is(err, ErrPrecondition) {
		t.Errorf("stale ETag err = %v, want ErrPrecondition", err)
	}
}

func TestClientStreamClosesOnCancel(t *testing.T) {
	srv := newFakeDB().server()
	defer srv.Close()
	c := NewClient(srv.URL, "")

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := c.Stream(ctx, "rooms/x")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	for range ch {
	}
}
