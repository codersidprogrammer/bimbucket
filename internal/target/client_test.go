package target

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	c := NewClient("ws", "me@example.com", "token")
	c.baseURL = srv.URL
	return c, srv
}

func TestGetUserUsesBasicAuth(t *testing.T) {
	var user, pass string
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ = r.BasicAuth()
		if r.URL.Path != "/user" {
			t.Errorf("path = %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]string{"username": "me"})
	})
	defer srv.Close()

	u, err := c.GetUser(context.Background())
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if u.Username != "me" {
		t.Errorf("username = %q", u.Username)
	}
	if user != "me@example.com" || pass != "token" {
		t.Errorf("basic auth = %q/%q", user, pass)
	}
}

func TestRepoExistsNotFound(t *testing.T) {
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"Not found"}}`, http.StatusNotFound)
	})
	defer srv.Close()

	exists, err := c.RepoExists(context.Background(), "missing")
	if err != nil {
		t.Fatalf("RepoExists: %v", err)
	}
	if exists {
		t.Error("expected not found")
	}
}

func TestCreateRepoBody(t *testing.T) {
	var body map[string]any
	var method, path string
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	err := c.CreateRepo(context.Background(), CreateRepoRequest{
		Slug: "svc", Description: "Svc", ProjectKey: "XOPS", Private: true,
	})
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if method != http.MethodPost || path != "/repositories/ws/svc" {
		t.Errorf("%s %s", method, path)
	}
	if body["scm"] != "git" || body["is_private"] != true {
		t.Errorf("body = %v", body)
	}
	proj, _ := body["project"].(map[string]any)
	if proj["key"] != "XOPS" {
		t.Errorf("project = %v", proj)
	}
}

func TestSetMainBranch(t *testing.T) {
	var method string
	var body map[string]any
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
	})
	defer srv.Close()

	if err := c.SetMainBranch(context.Background(), "svc", "main"); err != nil {
		t.Fatalf("SetMainBranch: %v", err)
	}
	if method != http.MethodPut {
		t.Errorf("method = %s", method)
	}
	mb, _ := body["mainbranch"].(map[string]any)
	if mb["name"] != "main" {
		t.Errorf("mainbranch = %v", body)
	}
}

func TestCreateProjectConflictIsOK(t *testing.T) {
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "conflict", http.StatusConflict)
	})
	defer srv.Close()

	if err := c.CreateProject(context.Background(), "XOPS", "XOPS"); err != nil {
		t.Errorf("conflict should be ignored, got %v", err)
	}
}
