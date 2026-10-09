package coop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ErrNotFound is returned when a Firebase path holds no value (JSON null).
var ErrNotFound = errors.New("coop: path not found")

// ErrPrecondition is returned when an ETag conditional write is rejected
// because the value changed since it was read.
var ErrPrecondition = errors.New("coop: precondition failed")

// nullETag is the Firebase sentinel ETag for a null (absent) location. Sending
// it in an if-match header makes the write succeed only while the location is
// still empty, which is how a fresh lease is created atomically. Firebase does
// NOT treat "*" as create-only (that is the plain HTTP "resource exists" rule).
const nullETag = "null_etag"

// Client is a minimal Firebase Realtime Database REST client. It speaks the
// documented REST surface (`.json` paths, optional `auth` query, ETag
// conditional writes) and the text/event-stream change feed. It deliberately
// avoids the Firebase SDK so the tool keeps a small dependency surface.
type Client struct {
	base string
	key  string
	hc   *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		base: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		key:  strings.TrimSpace(apiKey),
		hc:   &http.Client{},
	}
}

func (c *Client) endpoint(path, rawQuery string) string {
	u := c.base + "/" + strings.Trim(path, "/") + ".json"
	q := rawQuery
	if c.key != "" {
		if q != "" {
			q += "&"
		}
		q += "auth=" + url.QueryEscape(c.key)
	}
	if q != "" {
		u += "?" + q
	}
	return u
}

// Get decodes the value at path into out. A null value yields ErrNotFound.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.getJSON(ctx, path, "", out)
}

// GetQ is Get with extra raw query parameters (e.g. orderBy/limitToLast).
func (c *Client) GetQ(ctx context.Context, path, rawQuery string, out any) error {
	return c.getJSON(ctx, path, rawQuery, out)
}

func (c *Client) getJSON(ctx context.Context, path, rawQuery string, out any) error {
	_, body, err := c.get(ctx, path, rawQuery, nil)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// get performs a GET and returns the response ETag plus the raw JSON body.
func (c *Client) get(ctx context.Context, path, rawQuery string, hdr map[string]string) (etag string, body []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(path, rawQuery), nil)
	if err != nil {
		return "", nil, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		return "", nil, httpError(resp, body)
	}
	if isNull(body) {
		return nullETag, nil, ErrNotFound
	}
	return resp.Header.Get("ETag"), body, nil
}

// getETag fetches the value and its ETag in one conditional request. The ETag
// feeds a later PutIfMatch to make lease acquisition atomic.
func (c *Client) getETag(ctx context.Context, path string, out any) (string, error) {
	etag, body, err := c.get(ctx, path, "", map[string]string{"X-Firebase-ETag": "true"})
	if err != nil {
		return etag, err
	}
	if out != nil {
		if uerr := json.Unmarshal(body, out); uerr != nil {
			return "", uerr
		}
	}
	return etag, nil
}

// Put writes v at path, replacing any existing value.
func (c *Client) Put(ctx context.Context, path string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.put(ctx, c.endpoint(path, ""), body, "")
}

// PutIfMatch writes v at path only if the current ETag still matches. Pass
// nullETag (as returned by a read of an absent location) to require that the
// path still be empty (create-only). A stale ETag yields ErrPrecondition.
func (c *Client) PutIfMatch(ctx context.Context, path string, v any, etag string) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.put(ctx, c.endpoint(path, ""), body, etag)
}

func (c *Client) put(ctx context.Context, url string, body []byte, etag string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if etag != "" {
		req.Header.Set("if-match", etag)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode == http.StatusPreconditionFailed {
		return ErrPrecondition
	}
	if resp.StatusCode/100 != 2 {
		return httpError(resp, respBody)
	}
	return nil
}

// StreamEvent is one server-sent change notification. Only the event name is
// meaningful to the manager: any put/patch triggers a debounced re-pull.
type StreamEvent struct {
	Event string
}

// Stream subscribes to change notifications under path using the REST
// text/event-stream feed. The channel closes when ctx is cancelled or the
// connection drops; callers should re-subscribe on close.
func (c *Client) Stream(ctx context.Context, path string) (<-chan StreamEvent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(path, ""), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		return nil, httpError(resp, body)
	}

	ch := make(chan StreamEvent, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		var event string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if event == "put" || event == "patch" {
					select {
					case ch <- StreamEvent{Event: event}:
					case <-ctx.Done():
						return
					}
				}
				event = ""
			case strings.HasPrefix(line, "event:"):
				event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			}
		}
	}()
	return ch, nil
}

func isNull(b []byte) bool {
	t := strings.TrimSpace(string(b))
	return t == "" || t == "null"
}

func httpError(resp *http.Response, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200]
	}
	if msg == "" {
		return fmt.Errorf("coop: HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("coop: HTTP %d: %s", resp.StatusCode, msg)
}
