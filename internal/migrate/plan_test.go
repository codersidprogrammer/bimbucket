package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/codersidprogrammer/bimbucket/internal/config"
	"github.com/codersidprogrammer/bimbucket/internal/source"
)

func TestNormalizeSlug(t *testing.T) {
	cases := map[string]string{
		"microservice-soe-v2": "microservice-soe-v2",
		"MicroService SOE v2": "microservice-soe-v2",
		"foo/bar":             "foo-bar",
		"a..b":                "a..b",
		"-leading":            "leading",
		"trailing-":           "trailing",
		"weird***name":        "weird-name",
		"":                    "repo",
	}
	for in, want := range cases {
		if got := NormalizeSlug(in); got != want {
			t.Errorf("NormalizeSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeProjectKey(t *testing.T) {
	cases := map[string]string{
		"xops":       "XOPS",
		"~johnsmith": "JOHNSMITH",
		"my.proj":    "MYPROJ",
	}
	for in, want := range cases {
		if got := normalizeProjectKey(in); got != want {
			t.Errorf("normalizeProjectKey(%q) = %q, want %q", in, got, want)
		}
	}
}

type fakeLister struct {
	repos map[string][]source.Repository
}

func (f fakeLister) ListRepos(_ context.Context, key string) ([]source.Repository, error) {
	return f.repos[key], nil
}

func baseCfg() *config.Config {
	return &config.Config{
		Source:   config.Source{BaseURL: "https://bb.example.com"},
		Projects: []config.Project{{Key: "XOPS"}, {Key: "ABC"}},
	}
}

func TestBuildPlanExpandsAndFilters(t *testing.T) {
	mk := func(project, slug string) source.Repository {
		var r source.Repository
		r.Slug = slug
		r.Project.Key = project
		return r
	}
	svcA := mk("XOPS", "svc-a")
	svcA.DefaultBranch = "refs/heads/main"
	archived := mk("XOPS", "archived")
	archived.Archived = true

	lister := fakeLister{repos: map[string][]source.Repository{
		"XOPS": {svcA, archived},
		"ABC":  {mk("ABC", "svc-b")},
	}}
	cfg := baseCfg()
	cfg.Projects[1].Repos = []string{"svc-b"}

	plan, err := BuildPlan(context.Background(), cfg, lister)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Jobs) != 2 {
		t.Fatalf("got %d jobs, want 2", len(plan.Jobs))
	}
	bySlug := map[string]RepoJob{}
	for _, j := range plan.Jobs {
		bySlug[j.Slug] = j
	}
	if got := bySlug["svc-a"]; got.DefaultBranch != "main" {
		t.Errorf("default branch = %q", got.DefaultBranch)
	}
	if got := bySlug["svc-a"].CloneURL; got != "https://bb.example.com/scm/xops/svc-a.git" {
		t.Errorf("clone url = %q", got)
	}
	if _, ok := bySlug["archived"]; ok {
		t.Error("archived repo should be excluded by default")
	}
}

func TestBuildPlanFailsOnSlugCollision(t *testing.T) {
	lister := fakeLister{repos: map[string][]source.Repository{
		"XOPS": {{Slug: "api"}},
		"ABC":  {{Slug: "API"}},
	}}
	_, err := BuildPlan(context.Background(), baseCfg(), lister)
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("expected collision error, got %v", err)
	}
}
