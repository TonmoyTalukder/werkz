package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/agent"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------- render helpers

func renderUser(text string, w int) string {
	lines := strings.Split(wrapTo(text, max(w-4, 10)), "\n")
	for i, l := range lines {
		pre := "❯ "
		if i > 0 {
			pre = "  "
		}
		lines[i] = userLine(pre+l, max(min(w-2, lipgloss.Width(pre+l)+2), 4))
	}
	return "\n" + strings.Join(lines, "\n")
}

func (m *model) renderAssistant(md string, w int) string {
	out := wrapTo(md, w-2)
	if r := m.mdFor(w); r != nil {
		if r, err := r.Render(md); err == nil {
			out = strings.Trim(r, "\n")
		}
	}
	lines := strings.Split(out, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = sViolet.Render(bullet) + strings.TrimPrefix(lines[i], " ")
		}
	}
	return strings.Join(lines, "\n")
}

func renderThinking(t string, d time.Duration, w int) string {
	head := sItalic.Render(fmt.Sprintf("✻ Thought for %ds", int(d.Seconds())))
	body := lipgloss.NewStyle().Width(max(w-6, 20)).Render(t)
	return head + "\n" + indent(tail(body, 6), "  ", "  ")
}

func renderThinkingLive(t string, w int) string {
	body := lipgloss.NewStyle().Width(max(w-6, 20)).Render(t)
	return sItalic.Render(indent(body, "✻ ", "  "))
}

func renderToolResult(r agent.ToolResultMsg, call string, w int) string {
	dot := sGreen.Render(bullet)
	sum := sDim.Render(r.Summary)
	if r.IsError {
		dot, sum = sRed.Render(bullet), sRed.Render(r.Summary)
	}
	s := dot + " " + ansi.Truncate(sBold.Render(call), max(w-2, 10), "…") + "\n" + sDim.Render("  ⎿  ") + sum
	if len(r.Detail) > 0 {
		s += "\n" + indent(renderDetail(r.Detail, w-6), "     ", "     ")
	}
	return s
}

func renderDetail(lines []string, w int) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		// cut, but keep indentation: it is part of the code being shown
		if r := []rune(strings.TrimRight(l, " ")); len(r) > max(w, 20) {
			l = string(r[:max(w, 20)]) + "…"
		}
		switch {
		case strings.HasPrefix(l, "+ "):
			out[i] = sDiffAdd.Render(l)
		case strings.HasPrefix(l, "- "):
			out[i] = sDiffDel.Render(l)
		case strings.HasPrefix(l, "…"):
			out[i] = sFaint.Render(l)
		default:
			out[i] = sDim.Render(l)
		}
	}
	return strings.Join(out, "\n")
}

func renderNotice(level, text string) string {
	switch level {
	case "error":
		return sRed.Render("✗ " + text)
	case "warn":
		return sYellow.Render("⚠ " + text)
	}
	return sSky.Render("✦ ") + sText.Render(text)
}

func box(s, color string, w int) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(color)).
		Padding(0, 1).MaxWidth(max(w, 20)).Render(s) + "\n"
}

func padRight(s string, n int) string {
	if d := n - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func tail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func indent(s, first, rest string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = first + lines[i]
		} else {
			lines[i] = rest + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}
