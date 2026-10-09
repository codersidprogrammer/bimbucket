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

func TestBuildPlanAutoRenamesSlugCollision(t *testing.T) {
	lister := fakeLister{repos: map[string][]source.Repository{
		"XOPS": {{Slug: "api"}},
		"ABC":  {{Slug: "API"}},
	}}
	// Default policy (empty) is auto.
	plan, err := BuildPlan(context.Background(), baseCfg(), lister)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	byProject := map[string]string{}
	for _, j := range plan.Jobs {
		byProject[j.Project] = j.TargetSlug
	}
	if got := byProject["XOPS"]; got != "xops-api" {
		t.Errorf("XOPS/api target = %q, want xops-api", got)
	}
	if got := byProject["ABC"]; got != "abc-api" {
		t.Errorf("ABC/API target = %q, want abc-api", got)
	}
}

func TestBuildPlanFailsOnSlugCollisionWhenConfigured(t *testing.T) {
	lister := fakeLister{repos: map[string][]source.Repository{
		"XOPS": {{Slug: "api"}},
		"ABC":  {{Slug: "API"}},
	}}
	cfg := baseCfg()
	cfg.Options.OnSlugCollision = config.OnSlugCollisionFail
	_, err := BuildPlan(context.Background(), cfg, lister)
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("expected collision error, got %v", err)
	}
}

func TestBuildPlanRejectsExplicitOverrideCollisionUnderAuto(t *testing.T) {
	mk := func(slug string) source.Repository { return source.Repository{Slug: slug} }
	lister := fakeLister{repos: map[string][]source.Repository{
		"XOPS": {mk("a")},
		"ABC":  {mk("b")},
	}}
	cfg := &config.Config{
		Source: config.Source{BaseURL: "https://bb.example.com"},
		// Empty policy => auto, but explicit collisions are still rejected.
		Projects: []config.Project{
			{Key: "XOPS"},
			{Key: "ABC", Overrides: []config.RepoOverride{{Repo: "b", TargetSlug: "a"}}},
		},
	}
	if _, err := BuildPlan(context.Background(), cfg, lister); err == nil || !strings.Contains(err.Error(), "explicit") {
		t.Fatalf("expected explicit collision error, got %v", err)
	}
}

func TestBuildPlanAppliesDestinationMapping(t *testing.T) {
	mk := func(slug string) source.Repository {
		return source.Repository{Slug: slug}
	}
	lister := fakeLister{repos: map[string][]source.Repository{
		"XOPS": {mk("a")},
		"ABC":  {mk("b")},
		"DEV":  {mk("c")},
	}}
	cfg := &config.Config{
		Source: config.Source{BaseURL: "https://bb.example.com"},
		Projects: []config.Project{
			{Key: "XOPS", Destination: "PLATFORM"}, // explicit mapping
			{Key: "ABC"},                           // default = normalized source key
			{Key: "DEV", Destination: "PLATFORM"},  // two sources -> one destination
		},
	}

	plan, err := BuildPlan(context.Background(), cfg, lister)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	bySlug := map[string]RepoJob{}
	for _, j := range plan.Jobs {
		bySlug[j.Slug] = j
	}
	if got := bySlug["a"].CloudProject; got != "PLATFORM" {
		t.Errorf("a destination = %q, want PLATFORM", got)
	}
	if got := bySlug["b"].CloudProject; got != "ABC" {
		t.Errorf("b destination = %q, want ABC (default)", got)
	}
	if got := bySlug["c"].CloudProject; got != "PLATFORM" {
		t.Errorf("c destination = %q, want PLATFORM", got)
	}
}

func TestResolveJobAppliesOverrides(t *testing.T) {
	p := config.Project{
		Key:         "XOPS",
		Destination: "XOPS",
		Overrides: []config.RepoOverride{
			{Repo: "microservice-soev2", Destination: "MICROSERVICE"},
			{Repo: "legacy", TargetSlug: "API-v2"},
		},
	}
	cases := []struct {
		slug, wantProject, wantSlug string
	}{
		{"microservice-soev2", "MICROSERVICE", "microservice-soev2"},
		{"legacy", "XOPS", "api-v2"},
		{"other", "XOPS", "other"},
	}
	for _, tc := range cases {
		gotProject, gotSlug := ResolveJob(p, tc.slug)
		if gotProject != tc.wantProject || gotSlug != tc.wantSlug {
			t.Errorf("ResolveJob(%q) = (%q, %q), want (%q, %q)", tc.slug, gotProject, gotSlug, tc.wantProject, tc.wantSlug)
		}
	}
}

func TestBuildPlanAppliesRepoOverrides(t *testing.T) {
	mk := func(slug string) source.Repository { return source.Repository{Slug: slug} }
	lister := fakeLister{repos: map[string][]source.Repository{
		"XOPS": {mk("xopsapi"), mk("microservice-soev2")},
	}}
	cfg := &config.Config{
		Source: config.Source{BaseURL: "https://bb.example.com"},
		Projects: []config.Project{{
			Key: "XOPS", Destination: "XOPS",
			Overrides: []config.RepoOverride{{Repo: "microservice-soev2", Destination: "MICROSERVICE"}},
		}},
	}

	plan, err := BuildPlan(context.Background(), cfg, lister)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	bySlug := map[string]RepoJob{}
	for _, j := range plan.Jobs {
		bySlug[j.Slug] = j
	}
	if got := bySlug["xopsapi"].CloudProject; got != "XOPS" {
		t.Errorf("xopsapi destination = %q, want XOPS", got)
	}
	if got := bySlug["microservice-soev2"].CloudProject; got != "MICROSERVICE" {
		t.Errorf("microservice-soev2 destination = %q, want MICROSERVICE", got)
	}
}

func TestBuildPlanFailsOnOverrideTargetCollision(t *testing.T) {
	mk := func(slug string) source.Repository { return source.Repository{Slug: slug} }
	lister := fakeLister{repos: map[string][]source.Repository{
		"XOPS": {mk("a")},
		"ABC":  {mk("b")},
	}}
	cfg := &config.Config{
		Source:  config.Source{BaseURL: "https://bb.example.com"},
		Options: config.Options{OnSlugCollision: config.OnSlugCollisionFail},
		Projects: []config.Project{
			{Key: "XOPS"},
			{Key: "ABC", Overrides: []config.RepoOverride{{Repo: "b", TargetSlug: "a"}}},
		},
	}
	if _, err := BuildPlan(context.Background(), cfg, lister); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("expected override collision error, got %v", err)
	}
}
