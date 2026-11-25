package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/agent"
	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm/openai"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

type uiState int

const (
	stateChat uiState = iota
	stateLogin
	stateModels
	stateEffort
	statePerm
	stateWorkspaces
	stateWsForm
	stateFallback
	stateFallbackMode
	stateFallbackChain
)

type tickMsg time.Time
type orCreditsMsg struct {
	Amount float64
	OK     bool
}
type modelsFetchedMsg struct {
	Provider string
	Err      error
}

var spinnerFrames = []string{"✶", "✸", "✹", "✺", "✹", "✷"}
var verbs = []string{"Refracting", "werkzatizing", "Splitting light", "Pondering", "Conjuring", "Spectrumizing",
	"Synthesizing", "Brewing", "Untangling", "Reasoning", "Composing", "Calibrating"}

var modes = []string{config.ModeDefault, config.ModeAcceptEdits, config.ModePlan, config.ModeAuto}

type pendingTool struct{ Name, Summary string }

type model struct {
	cfg *config.Config
	eng *agent.Engine
	w   int
	h   int

	state uiState
	input textarea.Model

	turn  turnState    // the request in flight, if any
	stats sessionStats // usage, cost and cache counters for this session

	// overlays
	sugSel    int
	picker    picker
	effortIdx int
	perm      *agent.PermReqMsg
	permSel   int
	dialogAt  time.Time // when a permission/fallback dialog opened (typeahead guard)
	fb        *agent.FallbackAskMsg
	fbSel     int
	fbBrowse  bool
	fbList    fbBrowseList
	fbModeSel int
	fbc       fbChain
	login     loginModel

	lastCtrlC time.Time
	flash     string

	// transcript: entries re-render at the current width, so resizing reflows everything
	vp      viewport.Model
	entries []*entry
	follow  bool
	mdCache map[int]*glamour.TermRenderer

	// optional workspace (nil = default: just the launch folder)
	ws       *config.Workspace
	wsp      wsPicker
	wsf      wsForm
	startDir string
	onStart  func() tea.Cmd // e.g. open a workspace given on the command line
	version  string
}

// turnState is the request currently running (if any) and what it has streamed so far.
type turnState struct {
	active     bool
	cancel     context.CancelFunc
	started    time.Time
	frame      int
	verb       string
	live       strings.Builder
	thinking   strings.Builder
	thinkStart time.Time
	curProv    string
	curModel   string
	approxOut  int
	prepTool   string
	pending    *pendingTool
	queue      []string
}

// sessionStats are the session's usage, cost and prompt-cache counters.
type sessionStats struct {
	cost        float64
	unpriced    bool // some usage had no known price, so cost is a lower bound
	tokIn       int64
	tokOut      int64
	ctxUsed     int
	ctxMax      int
	cacheRead   int64   // tokens served from the prompt cache
	cacheWrite  int64   // tokens written to the cache
	uncached    int64   // input tokens at full price
	cacheSaved  float64 // $ saved by cache reads vs full input price
	kaPings     int
	kaCost      float64
	warmUntil   time.Time
	idleWarned  string // message text already warned about by the idle rule
	orCredits   float64
	orCreditsOK bool
}

// saveConfig persists settings; a failure is shown instead of silently ignored.
func (m *model) saveConfig() {
	if m.cfg.LoadErr() != nil {
		return // explained once at start-up; the file is left untouched
	}
	if err := m.cfg.Save(); err != nil {
		m.printS(renderNotice("error", "Couldn't save settings: "+err.Error()))
	}
}

func (m *model) runOnStart() tea.Cmd {
	if m.onStart == nil {
		return nil
	}
	f := m.onStart
	m.onStart = nil
	return f()
}

func newModel(cfg *config.Config) *model {
	ta := textarea.New()
	ta.Placeholder = `Try "explain this repo" · / for commands`
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.SetPromptFunc(2, func(i int) string {
		if i == 0 {
			return "❯ "
		}
		return "  "
	})
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Prompt = sPink.Bold(true)
	ta.FocusedStyle.Placeholder = sDim
	ta.BlurredStyle = ta.FocusedStyle
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j", "alt+enter"))
	ta.Focus()

	m := &model{cfg: cfg, input: ta, w: 100, h: 30, follow: true, vp: viewport.New(100, 20)}
	m.startDir, _ = os.Getwd()
	m.vp.KeyMap = viewport.KeyMap{} // scrolling is handled explicitly (keys are needed for typing)
	if !cfg.Onboarded || (!cfg.AnyConfigured() && cfg.Provider == "") {
		m.state = stateLogin
		m.login = newLogin(cfg, true)
	}
	m.ensureProvider()
	return m
}

// ensureProvider picks a sensible provider/model if the configured one is unusable.
// With preferKeyed, a keyed provider (e.g. Claude) replaces a local Ollama default.
func (m *model) ensureProvider(preferKeyed ...bool) {
	ok := m.cfg.Provider != "" && m.cfg.Configured(m.cfg.Provider) && m.cfg.Model != ""
	if ok && !(len(preferKeyed) > 0 && preferKeyed[0] && m.cfg.Provider == "ollama" && m.cfg.AnyConfigured()) {
		return
	}
	for _, id := range m.cfg.FallbackOrder {
		p := config.ProviderByID(id)
		if p != nil && p.NeedsKey && m.cfg.Key(id) != "" {
			m.selectModel(id, m.cfg.ModelFor(id))
			return
		}
	}
	m.selectModel("ollama", m.cfg.ModelFor("ollama"))
}

func (m *model) selectModel(provider, id string) {
	m.cfg.Provider, m.cfg.Model = provider, id
	m.cfg.ProviderModels[provider] = id
	mi := catalog.Lookup(provider, id)
	m.cfg.Effort = mi.DefaultEffort
	if mi.FastIn == 0 {
		m.cfg.Fast = false
	}
	m.stats.ctxMax = mi.Context
}

func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink, m.fetchCredits(), tea.SetWindowTitle("werkz")}
	if m.cfg.Configured("openrouter") {
		cmds = append(cmds, fetchModels(m.cfg, "openrouter")) // free models come and go: check ours
	}
	if m.state == stateChat {
		m.push(m.banner)
		m.homeNotice()
		if err := m.cfg.LoadErr(); err != nil {
			m.printS(renderNotice("error", "Couldn't load your settings: "+err.Error()+
				". werkz is running with defaults and won't overwrite the file this session."))
		}
		cmds = append(cmds, m.runOnStart())
	} else {
		cmds = append(cmds, m.login.inputs[0].Focus())
	}
	return tea.Batch(cmds...)
}

func (m *model) fetchCredits() tea.Cmd {
	k := m.cfg.Key("openrouter")
	if k == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		v, ok := openai.OpenRouterCredits(ctx, k)
		return orCreditsMsg{v, ok}
	}
}

// fetchModels loads a provider's model list in the background. The settings it needs are
// copied here, on the UI goroutine, so the background command never reads the live config.
func fetchModels(cfg *config.Config, id string) tea.Cmd {
	o := &openai.Client{ID: id, BaseURL: cfg.BaseURL(id), Key: cfg.Key(id), IncludePaid: cfg.OpenRouterPaid}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ms, err := o.ListModels(ctx)
		if err == nil && len(ms) > 0 {
			catalog.SetRemote(id, ms)
		}
		return modelsFetchedMsg{Provider: id, Err: err}
	}
}

func tick() tea.Cmd {
	return tea.Tick(110*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// ---------------------------------------------------------------- update

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.input.SetWidth(max(m.w-4, 10))
		return m, tea.ClearScreen

	case tickMsg:
		if !m.turn.active {
			return m, nil
		}
		m.turn.frame++
		return m, tick()

	case orCreditsMsg:
		m.stats.orCredits, m.stats.orCreditsOK = msg.Amount, msg.OK
		return m, nil

	case modelsFetchedMsg:
		if msg.Provider == "openrouter" {
			m.ensureOpenRouterModel()
		}
		if m.state == stateModels {
			m.picker.refresh(catalog.All(m.cfg))
		}
		if m.state == stateFallbackChain {
			m.fbc.refresh(m.cfg)
		}
		return m, nil

	case validateMsg:
		m.login.handleValidate(msg)
		return m, nil

	case agent.StreamStartMsg:
		m.turn.curProv, m.turn.curModel = msg.Provider, msg.Model
		return m, nil

	case agent.ThinkingDeltaMsg:
		if m.turn.thinking.Len() == 0 {
			m.turn.thinkStart = time.Now()
		}
		m.turn.thinking.WriteString(string(msg))
		m.turn.approxOut += len(msg) / 4
		return m, nil

	case agent.TextDeltaMsg:
		m.turn.live.WriteString(string(msg))
		m.turn.approxOut += max(len(msg)/4, 1)
		m.turn.prepTool = ""
		return m, nil

	case agent.ToolPrepMsg:
		m.turn.prepTool = string(msg)
		return m, nil

	case agent.ToolCallMsg:
		m.flushLive("")
		m.turn.pending = &pendingTool{Name: msg.Name, Summary: msg.Summary}
		m.turn.prepTool = ""
		return m, nil

	case agent.ToolResultMsg:
		call := msg.Name
		if m.turn.pending != nil {
			call = m.turn.pending.Name + sDim.Render("("+m.turn.pending.Summary+")")
		}
		m.turn.pending = nil
		return m, m.push(func(w int) string { return renderToolResult(msg, call, w) })

	case agent.PermReqMsg:
		mm := msg
		m.perm, m.state = &mm, statePerm
		opts, _ := m.permOptions()
		m.permSel = len(opts) - 1 // highlight starts on "No"
		m.dialogAt = time.Now()
		return m, nil

	case agent.UsageMsg:
		m.stats.cost += msg.Cost
		m.stats.unpriced = m.stats.unpriced || msg.Unpriced
		if msg.Provider == "anthropic" {
			m.cfg.Spent += msg.Cost // the UI owns the config; the engine only reports usage
		}
		m.stats.tokIn += msg.Usage.In + msg.Usage.CacheRead + msg.Usage.CacheWrite
		m.stats.tokOut += msg.Usage.Out
		if !msg.Sub {
			m.stats.ctxUsed, m.stats.ctxMax = msg.ContextUsed, msg.ContextMax
		}
		m.stats.cacheRead += msg.Usage.CacheRead
		m.stats.cacheWrite += msg.Usage.CacheWrite
		m.stats.uncached += msg.Usage.In
		m.stats.cacheSaved += catalog.Lookup(msg.Provider, msg.Model).CacheSaving(msg.Usage.CacheRead, m.cfg.Fast)
		if msg.Provider == "anthropic" && !msg.Sub {
			m.stats.warmUntil = time.Now().Add(5 * time.Minute)
			if m.cfg.TTL1h() {
				m.stats.warmUntil = time.Now().Add(time.Hour)
			}
		}
		return m, nil

	case agent.CompactDoneMsg:
		m.stats.ctxUsed = int(msg.After)
		how := "Conversation compacted"
		if msg.Auto {
			how = "Auto-compacted the conversation"
		}
		m.stats.warmUntil = time.Time{}
		return m, m.printS(sSky.Render("✻ ") + sText.Render(how) + sDim.Render(fmt.Sprintf(" · ~%s → ~%s tokens per request",
			catalog.HumanTokens(int(msg.Before)), catalog.HumanTokens(int(msg.After)))))

	case agent.KeepAliveMsg:
		m.stats.kaPings++
		m.stats.kaCost += msg.Cost
		m.stats.cost += msg.Cost
		m.cfg.Spent += msg.Cost
		m.stats.warmUntil = msg.Until
		return m, nil

	case agent.ProviderFailMsg:
		m.flushLive(" (interrupted)")
		line := sYellow.Render("↯ ") + providerBadge(msg.Provider) + sDim.Render(" · "+msg.Model+" failed: ") + sYellow.Render(msg.Reason)
		return m, m.printS(line)

	case agent.FallbackMsg:
		return m, m.printS(sDim.Render("↪ switching to ") + sText.Render(msg.To))

	case agent.FallbackAskMsg:
		mm := msg
		m.fb, m.fbSel, m.fbBrowse, m.state = &mm, len(mm.Candidates)+1, false, stateFallback // highlight starts on "Stop"
		m.dialogAt = time.Now()
		return m, nil

	case agent.NoticeMsg:
		return m, m.printS(renderNotice(msg.Level, msg.Text))

	case agent.TurnDoneMsg:
		m.flushLive("")
		var cmds []tea.Cmd
		m.turn.active, m.turn.pending, m.turn.prepTool = false, nil, ""
		if m.state == statePerm || m.state == stateFallback {
			m.state, m.perm, m.fb, m.fbBrowse = stateChat, nil, nil, false
		}
		if m.turn.cancel != nil {
			m.turn.cancel()
		}
		m.saveConfig()
		if msg.Err != nil {
			if errors.Is(msg.Err, context.Canceled) {
				cmds = append(cmds, m.printS(renderNotice("warn", "Interrupted · tell werkz what to do instead")))
			} else {
				cmds = append(cmds, m.printS(renderNotice("error", msg.Err.Error())))
			}
		} else if m.turn.curProv == "anthropic" || m.stats.cost > 0 {
			el := time.Since(m.turn.started).Round(time.Second)
			cmds = append(cmds, m.printS(sFaint.Render(fmt.Sprintf("  ✻ %s · %s · $%.4f session", el, m.curModelLabel(), m.stats.cost))))
		}
		if m.turn.curProv == "openrouter" && m.cfg.OpenRouterPaid {
			cmds = append(cmds, m.fetchCredits()) // keep the "OR $x" balance current
		}
		if len(m.turn.queue) > 0 {
			next := m.turn.queue[0]
			m.turn.queue = m.turn.queue[1:]
			cmds = append(cmds, m.submit(next))
		}
		return m, tea.Sequence(cmds...)

	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonWheelUp {
			m.vp.ScrollUp(3)
			m.follow = false
		} else if msg.Button == tea.MouseButtonWheelDown {
			m.vp.ScrollDown(3)
			m.follow = m.vp.AtBottom()
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "pgup", "ctrl+u":
			m.vp.HalfPageUp()
			m.follow = false
			return m, nil
		case "pgdown", "ctrl+d":
			m.vp.HalfPageDown()
			m.follow = m.vp.AtBottom()
			return m, nil
		case "end", "ctrl+end":
			if m.input.Value() == "" {
				m.follow = true
				return m, nil
			}
		case "home", "ctrl+home":
			if m.input.Value() == "" && m.state == stateChat {
				m.vp.GotoTop()
				m.follow = false
				return m, nil
			}
		}
		return m.handleKey(msg)
	}

	if m.state == stateLogin {
		return m, m.login.update(msg)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func mdStyle() string {
	if s := os.Getenv("WERKZ_THEME"); s == "light" {
		return "light"
	}
	return "dark"
}

func (m *model) curModelInfo() catalog.ModelInfo { return catalog.Lookup(m.cfg.Provider, m.cfg.Model) }

func (m *model) curModelLabel() string {
	if m.turn.curModel != "" {
		return catalog.Lookup(m.turn.curProv, m.turn.curModel).Label
	}
	return m.curModelInfo().Label
}

// homeNotice explains the stricter rules when werkz starts in the home folder (e.g. from the
// desktop icon): home is never treated as a project.
func (m *model) homeNotice() {
	if h, err := os.UserHomeDir(); err == nil && m.startDir == h {
		m.printS(renderNotice("info", "You're in your home folder, which werkz never treats as a project: "+
			"every file change asks first. Open a project with /workspace or /add-dir <folder>."))
	}
}

// ensureOpenRouterModel replaces an OpenRouter model that is no longer listed (free models are
// withdrawn regularly) with the first free one, so requests don't fail with "unavailable".
func (m *model) ensureOpenRouterModel() {
	list := catalog.Remote("openrouter")
	if len(list) == 0 {
		return
	}
	cur := m.cfg.ModelFor("openrouter")
	if !m.cfg.OpenRouterPaid && !catalog.IsFree("openrouter", cur) {
		return // a paid model the user picked: the free-only list can't tell if it's gone
	}
	var first string
	for _, mi := range list {
		if mi.ID == cur {
			return // still offered
		}
		if first == "" && mi.Free {
			first = mi.ID
		}
	}
	if first == "" {
		return
	}
	m.cfg.ProviderModels["openrouter"] = first
	if m.cfg.Provider == "openrouter" && m.cfg.Model == cur {
		m.cfg.Model = first
	}
	m.saveConfig()
	m.printS(renderNotice("info", "OpenRouter no longer offers "+cur+"; using the free model "+first+" instead (/model to choose another)."))
}
