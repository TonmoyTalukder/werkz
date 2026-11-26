package agent

import (
	"time"

	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm"
)

// ---- messages sent from the engine goroutine to the UI ----

type StreamStartMsg struct{ Provider, Model string }
type TextDeltaMsg string
type ThinkingDeltaMsg string
type ToolPrepMsg string
type ToolCallMsg struct{ ID, Name, Summary string }
type ToolResultMsg struct {
	ID, Name, Summary string
	Detail            []string // extra lines, e.g. diff
	Image             string   // a picture to show (a screenshot's path)
	IsError           bool
}
type UsageMsg struct {
	Sub             bool // from a Task sub-agent (doesn't change the main context size)
	Provider, Model string
	Usage           llm.Usage
	Cost            float64
	Unpriced        bool // the model's price is unknown, so Cost is a lower bound (shown as "+?")
	ContextUsed     int
	ContextMax      int
}

// keepAliveMsg reports a cache keep-alive ping (cost of a cache read, no output).
type KeepAliveMsg struct {
	Usage llm.Usage
	Cost  float64
	Until time.Time // cache stays warm at least until then
}

// PlanLimitsMsg carries a subscription's usage limits (Claude Code, ChatGPT plan).
type PlanLimitsMsg llm.PlanLimits

type NoticeMsg struct {
	Level string // info | warn | error
	Text  string
}

// providerFailMsg reports a failed attempt; fallbackMsg announces an approved switch.
type ProviderFailMsg struct{ Provider, Model, Reason string }
type FallbackMsg struct{ From, To, Reason string }

// fallbackAskMsg asks the user which model to switch to after a failure (Ask mode).
// Candidates are the user's untried fallback models, in order; the user may also pick any
// other connected model not in Exclude (already tried, or provider unusable).
type FallbackAskMsg struct {
	From, FromModel string
	Reason          string
	Candidates      []config.FallbackModel
	Exclude         []config.FallbackModel
	Reply           chan FallbackChoice
}

// fallbackChoice is the user's answer: a model (for this message or the whole session) or stop.
type FallbackChoice struct {
	Provider, Model string
	Session         bool
	Stop            bool
}
type TurnDoneMsg struct{ Err error }

type PermDecision int

const (
	PermYes PermDecision = iota
	PermAlways
	PermNo
)

type PermReqMsg struct {
	Tool, Summary string
	Detail        []string
	// Always is the label of the "don't ask again" option ("" = not offered, e.g. for
	// compound or risky shell commands and anything outside the working folders).
	Always string
	Reply  chan PermDecision
}

type CompactDoneMsg struct {
	Before, After int64
	Auto          bool
}
