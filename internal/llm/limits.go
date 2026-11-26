package llm

import (
	"strings"
	"time"
)

// PlanWindow is one usage limit of a subscription, e.g. Claude's 5-hour or weekly window.
type PlanWindow struct {
	Name     string    // provider's name for it, e.g. "five_hour", "seven_day", "seven_day_fable"
	Used     float64   // fraction of the window used, 0..1 (negative when unknown)
	ResetsAt time.Time // zero when unknown
	Status   string    // allowed, allowed_warning, rejected, …
}

// Label is the window's name for people: "5-hour", "weekly", "weekly Fable".
func (w PlanWindow) Label() string {
	n := strings.ToLower(w.Name)
	var base, rest string
	switch {
	case strings.HasPrefix(n, "five_hour"):
		base, rest = "5-hour", strings.TrimPrefix(n, "five_hour")
	case strings.HasPrefix(n, "seven_day"):
		base, rest = "weekly", strings.TrimPrefix(n, "seven_day")
	case n == "primary":
		return "short-term"
	case n == "secondary":
		return "weekly"
	default:
		return strings.ReplaceAll(n, "_", " ")
	}
	if rest = strings.Trim(rest, "_"); rest != "" {
		base += " " + strings.ToUpper(rest[:1]) + strings.ReplaceAll(rest[1:], "_", " ")
	}
	return base
}

// Limited reports whether the window is used up.
func (w PlanWindow) Limited() bool { return w.Status == "rejected" || w.Used >= 1 }

// PlanLimits is what a subscription backend reported about its usage limits.
type PlanLimits struct {
	Provider string
	Windows  []PlanWindow
	At       time.Time
	Source   string // how werkz learned it, shown next to the numbers
}
