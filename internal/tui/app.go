package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/codersidprogrammer/bimbucket/internal/config"
	"github.com/codersidprogrammer/bimbucket/internal/migrate"
	"github.com/codersidprogrammer/bimbucket/internal/source"
	"github.com/codersidprogrammer/bimbucket/internal/target"
)

// view identifies one menu tab.
type view int

const (
	viewProjects view = iota
	viewConnection
	viewStatus
	viewConfig
	viewHistory
	viewMigrate
	viewAbout
)

var viewNames = []string{"Projects", "Connection", "Status", "Config", "History", "Migrate", "About"}

const viewCount = int(viewAbout) + 1

// Options are the dependencies the menu TUI needs. The plan is built lazily so
// the UI can still open (and report failures) when the source is unreachable.
type Options struct {
	Config    *config.Config
	Source    *source.Client
	Target    *target.Client
	Git       migrate.GitOps
	StatePath string
	LogDir    string
	DryRun    bool
}

type (
	// planMsg carries the result of a lazy plan build.
	planMsg struct {
		plan *migrate.Plan
		err  error
	}
	// pingResult is one endpoint's connectivity check.
	pingResult struct {
		name    string
		detail  string
		latency time.Duration
		err     error
	}
	pingMsg struct{ results []pingResult }

	runsMsg struct {
		runs []migrate.RunSummary
		err  error
	}
	eventsMsg struct {
		run    migrate.RunSummary
		events []migrate.Event
		err    error
	}

	eventMsg migrate.Event
	doneMsg  struct{ err error }
)

// migState holds the interactive migration flow, which is one menu tab.
type migState struct {
	phase     migPhase
	err       error
	table     table.Model
	jobs      []migrate.RepoJob
	filter    textinput.Model
	filtering bool
	selected  map[string]bool
	inited    bool

	events   chan migrate.Event
	doneCh   chan error
	log      *migrate.Logger
	cancel   context.CancelFunc
	progress []migrate.Event
	runErr   error
}

type migPhase int

const (
	migBrowse migPhase = iota
	migConfirm
	migRunning
	migDone
)

// pingState holds the most recent connectivity results.
type pingState struct {
	results []pingResult
	at      time.Time
}

// Model is the root menu TUI.
type Model struct {
	cfg *config.Config
	src *source.Client
	tgt *target.Client
	gr  migrate.GitOps

	state     *migrate.State
	statePath string
	logDir    string
	dryRun    bool

	plan        *migrate.Plan
	planErr     error
	loadingPlan bool

	active view
	width  int
	height int

	spinner spinner.Model

	ping *pingState

	projTable     table.Model
	projFilter    textinput.Model
	projFiltering bool
	projShown     int

	connTable   table.Model
	statusTable table.Model
	cfgTable    table.Model
	histTable   table.Model

	runs        []migrate.RunSummary
	runsErr     error
	runsLoaded  bool
	histDetail  bool
	histRun     migrate.RunSummary
	histEvents  []migrate.Event
	eventsTable table.Model

	mig migState
}

// Run starts the interactive menu TUI.
func Run(opts Options) error {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	filter := textinput.New()
	filter.Prompt = "/ "
	filter.Placeholder = "filter project / repo / target"
	filter.CharLimit = 128

	migFilter := textinput.New()
	migFilter.Prompt = "/ "
	migFilter.Placeholder = "filter project / repo / target"
	migFilter.CharLimit = 128

	m := &Model{
		cfg:        opts.Config,
		src:        opts.Source,
		tgt:        opts.Target,
		gr:         opts.Git,
		statePath:  opts.StatePath,
		logDir:     opts.LogDir,
		dryRun:     opts.DryRun,
		state:      migrate.LoadState(opts.StatePath),
		spinner:    sp,
		projFilter: filter,
		mig:        migState{selected: map[string]bool{}, filter: migFilter},
	}
	m.rebuildConfig()

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.pingCmd(), m.loadPlan())
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case planMsg:
		m.loadingPlan = false
		m.plan, m.planErr = msg.plan, msg.err
		if msg.plan != nil {
			m.rebuildMigTable()
			m.rebuildProjects()
		}
		m.rebuildStatus()
		return m, nil

	case pingMsg:
		m.ping = &pingState{results: msg.results, at: time.Now()}
		m.rebuildConnection()
		m.rebuildStatus()
		return m, nil

	case runsMsg:
		m.runsLoaded = true
		m.runsErr = msg.err
		m.runs = msg.runs
		m.histDetail = false
		m.rebuildHistory()
		return m, nil

	case eventsMsg:
		if msg.err != nil {
			m.runsErr = msg.err
			return m, nil
		}
		m.histRun = msg.run
		m.histDetail = true
		m.rebuildEvents(msg.events)
		return m, nil

	case eventMsg:
		m.mig.progress = append(m.mig.progress, migrate.Event(msg))
		m.rebuildStatus()
		return m, waitForEvent(m.mig.events, m.mig.doneCh)

	case doneMsg:
		m.mig.runErr = msg.err
		m.mig.phase = migDone
		if m.mig.log != nil {
			m.mig.log.Close()
		}
		m.rebuildProjects()
		m.rebuildStatus()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	// Route async messages (e.g. cursor blink) to a focused filter input.
	if m.active == viewProjects && m.projFiltering {
		var cmd tea.Cmd
		m.projFilter, cmd = m.projFilter.Update(msg)
		return m, cmd
	}
	if m.active == viewMigrate && m.mig.filtering {
		var cmd tea.Cmd
		m.mig.filter, cmd = m.mig.filter.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		if m.mig.phase == migRunning && m.mig.cancel != nil {
			m.mig.cancel()
		}
		return m, tea.Quit
	}

	// While the project filter is focused, it consumes all keys so that digits,
	// "q" and tab type into the query instead of triggering app shortcuts.
	if m.active == viewProjects && m.projFiltering {
		switch msg.String() {
		case "esc":
			m.projFiltering = false
			m.projFilter.Blur()
			m.projFilter.SetValue("")
			m.rebuildProjects()
			return m, nil
		case "enter":
			m.projFiltering = false
			m.projFilter.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.projFilter, cmd = m.projFilter.Update(msg)
		m.rebuildProjects()
		return m, cmd
	}

	// While the migrate filter is focused, it consumes all keys so navigation
	// shortcuts type into the query instead.
	if m.active == viewMigrate && m.mig.filtering {
		switch msg.String() {
		case "esc":
			m.mig.filtering = false
			m.mig.filter.Blur()
			m.mig.filter.SetValue("")
			m.rebuildMigTable()
			return m, nil
		case "enter":
			m.mig.filtering = false
			m.mig.filter.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.mig.filter, cmd = m.mig.filter.Update(msg)
		m.rebuildMigTable()
		return m, cmd
	}

	switch msg.String() {
	case "q":
		if m.mig.phase != migRunning {
			return m, tea.Quit
		}
	case "/":
		if m.active == viewProjects {
			m.projFiltering = true
			return m, m.projFilter.Focus()
		}
	case "tab":
		return m.switchView(view((int(m.active) + 1) % viewCount))
	case "shift+tab":
		return m.switchView(view((int(m.active) - 1 + viewCount) % viewCount))
	}

	if s := msg.String(); len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
		if idx := int(s[0] - '1'); idx < viewCount {
			return m.switchView(view(idx))
		}
	}

	return m.handleViewKey(msg)
}

func (m *Model) switchView(v view) (tea.Model, tea.Cmd) {
	m.active = v
	return m, m.onEnterView()
}

func (m *Model) onEnterView() tea.Cmd {
	switch m.active {
	case viewProjects:
		if m.plan == nil && !m.loadingPlan {
			return m.loadPlan()
		}
	case viewConnection:
		if m.ping == nil {
			return m.pingCmd()
		}
	case viewHistory:
		if !m.runsLoaded {
			return m.loadRunsCmd()
		}
	}
	return nil
}

func (m *Model) handleViewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.active {
	case viewProjects:
		if msg.String() == "r" {
			return m, m.refreshAll()
		}
		var cmd tea.Cmd
		m.projTable, cmd = m.projTable.Update(msg)
		return m, cmd

	case viewConnection:
		if msg.String() == "r" {
			return m, m.pingCmd()
		}
		var cmd tea.Cmd
		m.connTable, cmd = m.connTable.Update(msg)
		return m, cmd

	case viewStatus:
		var cmd tea.Cmd
		m.statusTable, cmd = m.statusTable.Update(msg)
		return m, cmd

	case viewConfig:
		var cmd tea.Cmd
		m.cfgTable, cmd = m.cfgTable.Update(msg)
		return m, cmd

	case viewHistory:
		return m.handleHistoryKey(msg)

	case viewMigrate:
		return m.handleMigrateKey(msg)
	}
	return m, nil
}

// --- data loading -----------------------------------------------------------

func (m *Model) loadPlan() tea.Cmd {
	m.loadingPlan = true
	return func() tea.Msg {
		plan, err := migrate.BuildPlan(context.Background(), m.cfg, m.src)
		return planMsg{plan: plan, err: err}
	}
}

func (m *Model) refreshAll() tea.Cmd {
	if m.mig.phase != migRunning {
		m.state = migrate.LoadState(m.statePath)
	}
	m.rebuildProjects()
	return m.loadPlan()
}

func (m *Model) pingCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		results := make([]pingResult, 2)
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			lat, err := m.src.Ping(ctx)
			detail := "authenticated"
			if err != nil {
				detail = err.Error()
			}
			results[0] = pingResult{name: "Source  " + m.cfg.Source.BaseURL, detail: detail, latency: lat, err: err}
		}()

		go func() {
			defer wg.Done()
			start := time.Now()
			u, err := m.tgt.GetUser(ctx)
			lat := time.Since(start)
			detail := "user: " + u.Username
			if err != nil {
				detail = err.Error()
			}
			results[1] = pingResult{name: "Target  bitbucket.org/" + m.tgt.Workspace(), detail: detail, latency: lat, err: err}
		}()

		wg.Wait()
		return pingMsg{results: results}
	}
}

func (m *Model) loadRunsCmd() tea.Cmd {
	return func() tea.Msg {
		runs, err := migrate.ListRuns(m.logDir)
		return runsMsg{runs: runs, err: err}
	}
}

func (m *Model) loadEventsCmd(run migrate.RunSummary) tea.Cmd {
	return func() tea.Msg {
		events, err := migrate.ReadEvents(run.Path)
		return eventsMsg{run: run, events: events, err: err}
	}
}

// --- layout -----------------------------------------------------------------

func (m *Model) contentHeight() int {
	h := m.height - 3 // tab bar + help line + separators
	if h < 3 {
		h = 3
	}
	return h
}

func (m *Model) resize() {
	if m.width == 0 {
		return
	}
	m.rebuildProjects()
	m.rebuildConnection()
	m.rebuildStatus()
	m.rebuildConfig()
	m.rebuildHistory()
	m.rebuildEventsTable()
	if w := m.width - 6; w > 10 {
		m.projFilter.Width = w
		m.mig.filter.Width = w
	}
	m.rebuildMigTable()
}

func (m *Model) View() string {
	if m.width == 0 {
		return "loading..."
	}

	var b strings.Builder
	b.WriteString(m.tabBar())
	b.WriteString("\n")

	switch m.active {
	case viewProjects:
		b.WriteString(m.viewProjects())
	case viewConnection:
		b.WriteString(m.viewConnection())
	case viewStatus:
		b.WriteString(m.viewStatus())
	case viewConfig:
		b.WriteString(m.viewConfig())
	case viewHistory:
		b.WriteString(m.viewHistory())
	case viewMigrate:
		b.WriteString(m.viewMigrate())
	case viewAbout:
		b.WriteString(m.viewAbout())
	}

	b.WriteString("\n")
	b.WriteString(m.helpLine())
	return b.String()
}

func (m *Model) tabBar() string {
	var parts []string
	for i, name := range viewNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if view(i) == m.active {
			parts = append(parts, activeTab.Render(label))
		} else {
			parts = append(parts, inactiveTab.Render(label))
		}
	}
	return " " + strings.Join(parts, " ")
}

func (m *Model) helpLine() string {
	switch m.active {
	case viewProjects:
		return helpStyle.Render("  ↑/↓ scroll   / filter   r refresh   tab/1-7 switch view   q quit")
	case viewConnection:
		return helpStyle.Render("  r re-check   tab/1-7 switch view   q quit")
	case viewHistory:
		if m.histDetail {
			return helpStyle.Render("  ↑/↓ scroll   esc back to runs   r reload   q quit")
		}
		return helpStyle.Render("  ↑/↓ scroll   enter open run   r reload   tab/1-7 switch view   q quit")
	case viewMigrate:
		return helpStyle.Render("  ↑/↓ scroll   space toggle   a all   n none   / filter   enter confirm   tab/1-7 switch view")
	case viewAbout:
		return helpStyle.Render("  tab/1-7 switch view   q quit")
	default:
		return helpStyle.Render("  ↑/↓ scroll   tab/1-7 switch view   q quit")
	}
}

// --- shared helpers ---------------------------------------------------------

func repoStatus(rec *migrate.Record) string {
	if rec == nil {
		return "not migrated"
	}
	switch rec.Status {
	case migrate.StatusDone:
		return "migrated"
	case migrate.StatusCloning:
		return "cloning"
	case migrate.StatusPushing:
		return "pushing"
	case migrate.StatusFailed, migrate.StatusRollbackFailed:
		return "failed"
	case migrate.StatusRolledBack:
		return "rolled back"
	case migrate.StatusSkipped:
		return "skipped"
	default:
		return "not migrated"
	}
}

func updatedAt(rec *migrate.Record) string {
	if rec == nil || rec.Updated.IsZero() {
		return "-"
	}
	return rec.Updated.Local().Format("2006-01-02 15:04")
}

func isMigrated(rec *migrate.Record) bool {
	return rec != nil && rec.Status == migrate.StatusDone
}

func isFailed(rec *migrate.Record) bool {
	return rec != nil && (rec.Status == migrate.StatusFailed || rec.Status == migrate.StatusRollbackFailed)
}

func isPending(rec *migrate.Record) bool {
	return !isMigrated(rec) && !isFailed(rec)
}
