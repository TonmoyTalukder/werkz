package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
)

// Brand palette: saffron → red → blue.
const (
	cSaffron = "#FF9933"
	cBrandRd = "#E8344E"
	cBlue    = "#3B6CF6"

	cPink   = cSaffron  // primary accent: prompt, cursor, selection
	cViolet = cBlue     // secondary accent: assistant dot, borders
	cSky    = "#6FA0FF" // light blue: model name, info
	cGreen  = "#4ADE80"
	cYellow = "#FACC15"
	cRed    = "#F87171"
	cOrange = "#FB923C"
	cDim    = "#6B7280"
	cFaint  = "#3F3F46"
	cText   = "#E5E7EB"
)

var brandStops = []string{cSaffron, cBrandRd, cBlue}

func fg(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }

var (
	sDim     = fg(cDim)
	sFaint   = fg(cFaint)
	sText    = fg(cText)
	sBold    = lipgloss.NewStyle().Bold(true)
	sGreen   = fg(cGreen)
	sRed     = fg(cRed)
	sYellow  = fg(cYellow)
	sViolet  = fg(cViolet)
	sSky     = fg(cSky)
	sPink    = fg(cPink)
	sOrange  = fg(cOrange)
	sItalic  = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color(cDim))
	sUserMsg = lipgloss.NewStyle().Background(lipgloss.Color("#27272A")).Foreground(lipgloss.Color(cText))
	sDiffAdd = lipgloss.NewStyle().Background(lipgloss.Color("#14532D")).Foreground(lipgloss.Color("#DCFCE7"))
	sDiffDel = lipgloss.NewStyle().Background(lipgloss.Color("#7F1D1D")).Foreground(lipgloss.Color("#FEE2E2"))
)

var providerColor = map[string]string{
	"anthropic": "#D97757", "openai": "#10A37F", "xai": "#E5E7EB", "gemini": "#4C8BF5",
	"openrouter": cViolet, "groq": "#F55036", "ollama": "#FFFFFF", "opencode": "#FAB283",
}

func providerBadge(id string) string {
	c := providerColor[id]
	if c == "" {
		c = cText
	}
	return fg(c).Bold(true).Render(id)
}

func blend(stops []string, t float64) string {
	if t <= 0 {
		return stops[0]
	}
	if t >= 1 {
		return stops[len(stops)-1]
	}
	seg := t * float64(len(stops)-1)
	i := int(seg)
	a, _ := colorful.Hex(stops[i])
	b, _ := colorful.Hex(stops[i+1])
	return a.BlendLuv(b, seg-float64(i)).Clamped().Hex()
}

// gradient colors each rune along the stops; shift (0..1) animates it.
func gradient(s string, shift float64) string {
	stops := brandStops
	rs := []rune(s)
	n := len(rs)
	var sb strings.Builder
	for i, r := range rs {
		if r == ' ' {
			sb.WriteRune(r)
			continue
		}
		t := float64(i)/float64(max(n-1, 1)) + shift
		t -= float64(int(t))
		// ping-pong so the animation loops smoothly
		if t > 0.5 {
			t = 1 - t
		}
		t *= 2
		sb.WriteString(fg(blend(stops, t)).Render(string(r)))
	}
	return sb.String()
}

// gradientBlock colors a multi-line block column-wise so the gradient runs left → right.
func gradientBlock(block string, stops ...string) string {
	lines := strings.Split(block, "\n")
	w := 0
	for _, l := range lines {
		w = max(w, len([]rune(l)))
	}
	out := make([]string, len(lines))
	for li, l := range lines {
		var sb strings.Builder
		for i, r := range []rune(l) {
			if r == ' ' {
				sb.WriteRune(r)
				continue
			}
			t := (float64(i) + float64(li)*1.5) / float64(w+len(lines)*2)
			sb.WriteString(fg(blend(stops, t)).Render(string(r)))
		}
		out[li] = sb.String()
	}
	return strings.Join(out, "\n")
}

const logo = `                       ██
██     ██  ▄███▄  ██▄██▄ ██  ▄█▀ ███████
██  ▄  ██ ██▄▄▄██ ███▀   ██▄█▀      ▄█▀
██ ███ ██ ██▀▀▀▀  ██     ██▀█▄    ▄█▀
 ███ ███   ▀████▀ ██     ██  ▀█▄ ███████`

const smallLogo = `◢◤ werkz`
