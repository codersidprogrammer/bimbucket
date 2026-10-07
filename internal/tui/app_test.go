package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/codersidprogrammer/bimbucket/internal/config"
	"github.com/codersidprogrammer/bimbucket/internal/migrate"
)

func TestRepoStatusClassification(t *testing.T) {
	cases := []struct {
		rec  *migrate.Record
		want string
	}{
		{nil, "not migrated"},
		{&migrate.Record{Status: migrate.StatusPending}, "not migrated"},
		{&migrate.Record{Status: migrate.StatusDone}, "migrated"},
		{&migrate.Record{Status: migrate.StatusFailed}, "failed"},
		{&migrate.Record{Status: migrate.StatusRollbackFailed}, "failed"},
		{&migrate.Record{Status: migrate.StatusRolledBack}, "rolled back"},
		{&migrate.Record{Status: migrate.StatusSkipped}, "skipped"},
	}
	for _, tc := range cases {
		if got := repoStatus(tc.rec); got != tc.want {
			t.Errorf("repoStatus(%v) = %q, want %q", tc.rec, got, tc.want)
		}
	}
}

func TestProjectsViewCountsAndTabBar(t *testing.T) {
	st := migrate.LoadState(filepath.Join(t.TempDir(), "state.json"))
	st.Update(&migrate.Record{Project: "A", Slug: "a", TargetSlug: "a", Status: migrate.StatusDone})

	m := &Model{cfg: &config.Config{}, state: st, width: 120, height: 40}
	m.plan = &migrate.Plan{Jobs: []migrate.RepoJob{
		{Project: "A", Slug: "a", TargetSlug: "a"},
		{Project: "A", Slug: "b", TargetSlug: "b"},
	}}
	m.rebuildProjects()

	out := m.viewProjects()
	if !strings.Contains(out, "migrated: 1") {
		t.Errorf("missing migrated count:\n%s", out)
	}
	if !strings.Contains(out, "not migrated: 1") {
		t.Errorf("missing not-migrated count:\n%s", out)
	}

	bar := m.tabBar()
	for _, name := range viewNames {
		if !strings.Contains(bar, name) {
			t.Errorf("tab bar missing %q: %s", name, bar)
		}
	}
}

func TestAuthHint(t *testing.T) {
	if got := authHint("GET /user: HTTP 401: no Bitbucket scopes"); !strings.Contains(got, "read:user:bitbucket") {
		t.Errorf("expected scope hint, got %q", got)
	}
	if got := authHint("dial tcp: connection refused"); got != "" {
		t.Errorf("unexpected hint for non-auth error: %q", got)
	}
}

func TestProjectsFilterNarrowsRows(t *testing.T) {
	st := migrate.LoadState(filepath.Join(t.TempDir(), "state.json"))
	m := &Model{
		cfg:        &config.Config{},
		state:      st,
		projFilter: textinput.New(),
		width:      120,
		height:     40,
	}
	m.plan = &migrate.Plan{Jobs: []migrate.RepoJob{
		{Project: "XOPS", Slug: "svc-a", TargetSlug: "svc-a"},
		{Project: "XOPS", Slug: "web", TargetSlug: "web"},
		{Project: "ENG", Slug: "api", TargetSlug: "api"},
	}}

	m.rebuildProjects()
	if m.projShown != 3 {
		t.Fatalf("unfiltered shown = %d, want 3", m.projShown)
	}

	m.projFilter.SetValue("xo") // matches the XOPS project key only
	m.rebuildProjects()
	if m.projShown != 2 {
		t.Errorf("filter %q shown = %d, want 2", "xo", m.projShown)
	}

	m.projFilter.SetValue("api") // matches repo + target slug
	m.rebuildProjects()
	if m.projShown != 1 {
		t.Errorf("filter %q shown = %d, want 1", "api", m.projShown)
	}

	m.projFilter.SetValue("missing")
	m.rebuildProjects()
	if m.projShown != 0 {
		t.Errorf("filter %q shown = %d, want 0", "missing", m.projShown)
	}
	if out := m.viewProjects(); !strings.Contains(out, "no projects match") {
		t.Errorf("expected empty-state message:\n%s", out)
	}
}

func TestMigrateSelectionVisibleOnly(t *testing.T) {
	m := &Model{
		cfg:        &config.Config{},
		state:      migrate.LoadState(filepath.Join(t.TempDir(), "state.json")),
		projFilter: textinput.New(),
		spinner:    spinner.New(),
		width:      120,
		height:     40,
		mig:        migState{selected: map[string]bool{}, filter: textinput.New()},
	}
	m.plan = &migrate.Plan{Jobs: []migrate.RepoJob{
		{Project: "XOPS", Slug: "svc-a", TargetSlug: "svc-a"},
		{Project: "XOPS", Slug: "web", TargetSlug: "web"},
		{Project: "ENG", Slug: "api", TargetSlug: "api"},
	}}
	m.rebuildMigTable()
	if m.countSelected() != 0 {
		t.Fatalf("fresh selection = %d, want 0", m.countSelected())
	}

	m.setVisibleSelection(true)
	if m.countSelected() != 3 {
		t.Fatalf("select-all = %d, want 3", m.countSelected())
	}

	m.mig.filter.SetValue("xo") // only the two XOPS repos remain visible
	m.rebuildMigTable()
	if len(m.mig.jobs) != 2 {
		t.Fatalf("filtered jobs = %d, want 2", len(m.mig.jobs))
	}
	m.setVisibleSelection(false)

	if !m.mig.selected[jobKey(migrate.RepoJob{Project: "ENG", Slug: "api"})] {
		t.Errorf("filtered-out row must keep its selection")
	}
	if m.mig.selected[jobKey(migrate.RepoJob{Project: "XOPS", Slug: "web"})] {
		t.Errorf("clear-visible should have cleared XOPS/web")
	}
	if m.countSelected() != 1 {
		t.Errorf("selected = %d, want 1", m.countSelected())
	}
}

func TestAboutWordmarkAligned(t *testing.T) {
	lines := wordmarkLines()
	if len(lines) != 4 {
		t.Fatalf("wordmark has %d lines, want 4", len(lines))
	}
	want := lipgloss.Width(lines[0])
	for i, l := range lines {
		if got := lipgloss.Width(l); got != want {
			t.Errorf("line %d width = %d, want %d\n%s", i, got, want, l)
		}
	}

	m := &Model{}
	if out := m.viewAbout(); !strings.Contains(out, authorName) {
		t.Errorf("about view missing author name:\n%s", out)
	}
}

func TestDestLabelStatus(t *testing.T) {
	m := &Model{
		cfg:        &config.Config{Options: config.Options{CreateCloudProjects: true}},
		destExists: map[string]bool{"XOPS": true, "NEW": false},
	}
	if got := m.destLabel("XOPS"); got != "XOPS (ok)" {
		t.Errorf("existing dest = %q", got)
	}
	if got := m.destLabel("NEW"); got != "NEW (new)" {
		t.Errorf("creatable dest = %q", got)
	}

	m.destExists = nil
	if got := m.destLabel("XOPS"); got != "XOPS (?)" {
		t.Errorf("unchecked dest = %q", got)
	}

	m.cfg.Options.CreateCloudProjects = false
	m.destExists = map[string]bool{"NEW": false}
	if got := m.destLabel("NEW"); got != "NEW (missing)" {
		t.Errorf("missing dest without create = %q", got)
	}
}

func testInput(value string) textinput.Model {
	in := textinput.New()
	in.SetValue(value)
	return in
}

func remapModel(t *testing.T, cfg *config.Config, jobs ...migrate.RepoJob) *Model {
	t.Helper()
	path := filepath.Join(t.TempDir(), "projects.yaml")
	var b strings.Builder
	b.WriteString("source:\n  base_url: https://bb.example.com\nprojects:\n")
	for _, p := range cfg.Projects {
		b.WriteString("  - key: " + p.Key + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &Model{
		cfg:        cfg,
		configPath: path,
		state:      migrate.LoadState(filepath.Join(t.TempDir(), "state.json")),
		projFilter: textinput.New(),
		spinner:    spinner.New(),
		width:      120,
		height:     40,
		mig:        migState{selected: map[string]bool{}, filter: textinput.New()},
	}
	m.plan = &migrate.Plan{Jobs: jobs}
	m.rebuildMigTable()
	return m
}

func TestRemapEditorOpens(t *testing.T) {
	m := remapModel(t, &config.Config{Projects: []config.Project{{Key: "XOPS"}}},
		migrate.RepoJob{Project: "XOPS", Slug: "svc", TargetSlug: "svc", CloudProject: "XOPS"})

	_, _ = m.handleMigrateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if !m.mig.editing {
		t.Fatal("pressing e should open the remap editor")
	}
	if m.mig.editKey != "XOPS/svc" {
		t.Errorf("edit key = %q, want XOPS/svc", m.mig.editKey)
	}
}

func TestRemapCommitAppliesAndInherits(t *testing.T) {
	cfg := &config.Config{Projects: []config.Project{{Key: "XOPS", Destination: "XOPS"}}}
	m := remapModel(t, cfg,
		migrate.RepoJob{Project: "XOPS", Slug: "microservice-soev2", TargetSlug: "microservice-soev2", CloudProject: "XOPS"})

	m.mig.editKey = "XOPS/microservice-soev2"
	m.mig.editDest = testInput("MICROSERVICE")
	m.mig.editSlug = testInput("")
	_, _ = m.commitRemap()
	if got := m.plan.Jobs[0].CloudProject; got != "MICROSERVICE" {
		t.Fatalf("cloud project = %q, want MICROSERVICE", got)
	}
	if got := m.plan.Jobs[0].TargetSlug; got != "microservice-soev2" {
		t.Errorf("target slug = %q, want source slug", got)
	}
	if ov, ok := m.cfg.Projects[0].OverrideFor("microservice-soev2"); !ok || ov.Destination != "MICROSERVICE" {
		t.Errorf("in-memory override = %+v, ok=%v", ov, ok)
	}
	if data, _ := os.ReadFile(m.configPath); !strings.Contains(string(data), "destination: MICROSERVICE") {
		t.Errorf("persisted config missing override:\n%s", data)
	}

	// Rename the target slug; empty destination inherits the project default.
	m.mig.editKey = "XOPS/microservice-soev2"
	m.mig.editDest = testInput("")
	m.mig.editSlug = testInput("API-v2")
	_, _ = m.commitRemap()
	if got := m.plan.Jobs[0].TargetSlug; got != "api-v2" {
		t.Fatalf("target slug = %q, want api-v2", got)
	}
	if got := m.plan.Jobs[0].CloudProject; got != "XOPS" {
		t.Errorf("cloud project = %q, want XOPS (inherited)", got)
	}
	if data, _ := os.ReadFile(m.configPath); !strings.Contains(string(data), "target_slug: api-v2") {
		t.Errorf("persisted config missing target_slug:\n%s", data)
	}
}

func TestRemapCollisionBlocked(t *testing.T) {
	cfg := &config.Config{Projects: []config.Project{{Key: "XOPS"}, {Key: "ABC"}}}
	m := remapModel(t, cfg,
		migrate.RepoJob{Project: "XOPS", Slug: "a", TargetSlug: "a", CloudProject: "XOPS"},
		migrate.RepoJob{Project: "ABC", Slug: "b", TargetSlug: "b", CloudProject: "ABC"})

	m.mig.editKey = "ABC/b"
	m.mig.editDest = testInput("")
	m.mig.editSlug = testInput("a")
	_, _ = m.commitRemap()

	if m.mig.editErr == "" {
		t.Fatal("expected collision error")
	}
	if got := m.plan.Jobs[1]; got.TargetSlug != "b" || got.CloudProject != "ABC" {
		t.Errorf("plan must be unchanged on collision, got %+v", got)
	}
	if _, ok := m.cfg.Projects[1].OverrideFor("b"); ok {
		t.Error("colliding remap must not be applied in memory")
	}
	if data, _ := os.ReadFile(m.configPath); strings.Contains(string(data), "repo: b") {
		t.Errorf("colliding remap must not be persisted:\n%s", data)
	}
}

func TestEveryViewRendersWithoutPanic(t *testing.T) {
	st := migrate.LoadState(filepath.Join(t.TempDir(), "state.json"))
	st.Update(&migrate.Record{Project: "A", Slug: "a", TargetSlug: "a", Status: migrate.StatusDone})

	sp := spinner.New()
	m := &Model{
		cfg:       &config.Config{Options: config.Options{Workers: 3}},
		state:     st,
		logDir:    t.TempDir(),
		statePath: filepath.Join(t.TempDir(), "state.json"),
		spinner:   sp,
		mig:       migState{selected: map[string]bool{}},
	}
	m.plan = &migrate.Plan{Jobs: []migrate.RepoJob{{Project: "A", Slug: "a", TargetSlug: "a"}}}
	m.rebuildConfig()
	m.width, m.height = 120, 40
	m.resize()

	for v := view(0); int(v) < viewCount; v++ {
		m.active = v
		if got := m.View(); got == "" {
			t.Errorf("view %d rendered empty", v)
		}
	}
}
