package source

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListProjectsPaginates(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		start := r.URL.Query().Get("start")
		w.Header().Set("Content-Type", "application/json")
		if start == "0" {
			fmt.Fprint(w, `{"size":2,"limit":1000,"isLastPage":false,"start":0,"nextPageStart":2,"values":[{"key":"XOPS"},{"key":"ABC"}]}`)
			return
		}
		fmt.Fprint(w, `{"size":1,"limit":1000,"isLastPage":true,"start":2,"values":[{"key":"DEF"}]}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", "tok", "")
	got, err := c.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d projects, want 3", len(got))
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("auth = %q, want Bearer tok", gotAuth)
	}
}

func TestListReposParsesDefaultBranchAndCloneURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"isLastPage":true,"values":[{"slug":"svc","name":"Svc","defaultBranch":"refs/heads/main","project":{"key":"XOPS"},"links":{"clone":[{"href":"https://bb.example.com/scm/xops/svc.git","name":"http"}]}}]}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "admin", "", "pw")
	repos, err := c.ListRepos(context.Background(), "XOPS")
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("got %d repos", len(repos))
	}
	if repos[0].DefaultBranchName() != "main" {
		t.Errorf("default branch = %q", repos[0].DefaultBranchName())
	}
	if repos[0].CloneURL("https://bb.example.com") != "https://bb.example.com/scm/xops/svc.git" {
		t.Errorf("clone url = %q", repos[0].CloneURL("https://bb.example.com"))
	}
}

func TestCloneURLFallback(t *testing.T) {
	r := Repository{Slug: "svc"}
	r.Project.Key = "XOPS"
	if got := r.CloneURL("https://bb.example.com/"); got != "https://bb.example.com/scm/xops/svc.git" {
		t.Errorf("clone url = %q", got)
	}
}

func TestPingUsesMinimalAuthenticatedRequest(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"isLastPage":true,"values":[]}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", "tok", "")
	if _, err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if gotPath != "/rest/api/1.0/projects" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("auth = %q", gotAuth)
	}
}

func TestPingReportsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", "tok", "")
	if _, err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected error for HTTP 401")
	}
}
