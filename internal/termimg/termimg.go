// Package termimg shows PNG screenshots in the terminal: capability detection
// (environment and active probing) plus encoders for the Kitty graphics
// protocol (with Unicode placeholders), the iTerm2 inline image protocol and
// Sixel.
package termimg

import (
	"encoding/base64"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Protocol is an image protocol a terminal understands.
type Protocol int

// Supported protocols.
const (
	None Protocol = iota
	Kitty
	ITerm2
	Sixel
)

func (p Protocol) String() string {
	switch p {
	case Kitty:
		return "kitty"
	case ITerm2:
		return "iterm2"
	case Sixel:
		return "sixel"
	}
	return "none"
}

// Caps describes the terminal.
type Caps struct {
	Proto        Protocol
	Placeholders bool // Kitty Unicode placeholders (U=1) work
	CellW, CellH int  // cell size in pixels (0 if unknown)
	InTmux       bool
	TrueColor    bool // 24-bit colour works (the text-mode preview uses it)
	// Hint says how to get pictures when the terminal could show them but something is in the
	// way (tmux without passthrough); "" otherwise.
	Hint string
}

const (
	defCellW = 8
	defCellH = 16
	esc      = "\x1b"
	st       = "\x1b\\"
)

// FromEnv guesses the terminal's capabilities from the environment only.
func FromEnv(getenv func(string) string) Caps {
	c := Caps{InTmux: getenv("TMUX") != "", TrueColor: trueColor(getenv)}
	switch strings.ToLower(strings.TrimSpace(getenv("WERKZ_IMAGES"))) {
	case "kitty":
		c.Proto, c.Placeholders = Kitty, true
		return c
	case "iterm2":
		c.Proto = ITerm2
		return c
	case "sixel":
		c.Proto = Sixel
		return c
	case "off", "none", "blocks", "text":
		return c
	}
	switch {
	case c.InTmux:
	case getenv("KITTY_WINDOW_ID") != "" || getenv("TERM") == "xterm-kitty",
		getenv("TERM_PROGRAM") == "ghostty" || getenv("GHOSTTY_RESOURCES_DIR") != "":
		c.Proto, c.Placeholders = Kitty, true
	case getenv("TERM_PROGRAM") == "WezTerm",
		getenv("TERM_PROGRAM") == "iTerm.app" || getenv("LC_TERMINAL") == "iTerm2":
		c.Proto = ITerm2
	}
	return c
}

func (c Caps) cell() (int, int) {
	w, h := c.CellW, c.CellH
	if w <= 0 || h <= 0 {
		return defCellW, defCellH
	}
	return w, h
}

// Fit returns the size in cells to show a w×h px image within maxCols×maxRows,
// keeping the aspect ratio. The image is never enlarged beyond its native
// pixel size. The result is at least 1×1 (and 0×0 for invalid input).
func (c Caps) Fit(w, h, maxCols, maxRows int) (cols, rows int) {
	if w <= 0 || h <= 0 || maxCols <= 0 || maxRows <= 0 {
		return 0, 0
	}
	cw, ch := c.cell()
	if c.Proto == Sixel { // xterm draws at most SixelMaxPx a side, and Sixel costs more as it grows
		maxCols, maxRows = max(1, min(maxCols, SixelMaxPx/cw)), max(1, min(maxRows, SixelMaxPx/ch))
	}
	// Natural size in cells, then the scale that fits both limits (never >1).
	nc, nr := float64(w)/float64(cw), float64(h)/float64(ch)
	s := 1.0
	if f := float64(maxCols) / nc; f < s {
		s = f
	}
	if f := float64(maxRows) / nr; f < s {
		s = f
	}
	cols = max(1, min(maxCols, int(nc*s+0.5)))
	rows = max(1, min(maxRows, int(nr*s+0.5)))
	return cols, rows
}

// SixelMaxPx is the largest Sixel picture side werkz makes: xterm's default limit
// (maxGraphicSize 1000x1000); larger pictures are cut off there.
const SixelMaxPx = 1000

// ITerm2Image returns an OSC 1337 inline image sequence.
func ITerm2Image(png []byte, cols, rows int) string {
	return "\x1b]1337;File=inline=1;size=" + strconv.Itoa(len(png)) +
		";width=" + strconv.Itoa(cols) + ";height=" + strconv.Itoa(rows) +
		";preserveAspectRatio=1:" + base64.StdEncoding.EncodeToString(png) + "\a"
}

// FileLink returns an OSC 8 hyperlink to file://<host><abs path> showing text.
func FileLink(path, text string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	host, _ := os.Hostname()
	u := url.URL{Scheme: "file", Host: host, Path: filepath.ToSlash(abs)}
	return "\x1b]8;;" + u.String() + st + text + "\x1b]8;;" + st
}

// TmuxWrap wraps seq for tmux passthrough (ESC doubled).
func TmuxWrap(seq string) string {
	return "\x1bPtmux;" + strings.ReplaceAll(seq, esc, esc+esc) + st
}

// trueColor reports whether the terminal takes 24-bit colours.
func trueColor(getenv func(string) string) bool {
	switch strings.ToLower(getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return true
	}
	return getenv("KITTY_WINDOW_ID") != "" || getenv("GHOSTTY_RESOURCES_DIR") != "" || getenv("VTE_VERSION") != "" ||
		getenv("WEZTERM_PANE") != "" || getenv("LC_TERMINAL") == "iTerm2" ||
		getenv("TERM_PROGRAM") == "iTerm.app" || getenv("TERM_PROGRAM") == "WezTerm"
}

// outerKitty reports a kitty or Ghostty terminal around tmux (tmux keeps the variables these
// terminals set in the environment it started with).
func outerKitty(getenv func(string) string) bool {
	return getenv("KITTY_WINDOW_ID") != "" || getenv("GHOSTTY_RESOURCES_DIR") != "" || getenv("TERM") == "xterm-kitty"
}

// InTmuxCaps decides what werkz shows inside tmux. passthrough is tmux's allow-passthrough
// option ("on", "all" or "off"). Kitty's images survive tmux through passthrough, because they are
// drawn by ordinary text cells there; pictures placed at a position (iTerm2, Sixel) don't land
// where tmux draws, so those terminals get the text-mode preview.
func InTmuxCaps(c Caps, getenv func(string) string, passthrough string) Caps {
	if !c.InTmux || strings.TrimSpace(getenv("WERKZ_IMAGES")) != "" {
		return c
	}
	passthrough = strings.TrimSpace(passthrough)
	if !outerKitty(getenv) {
		return c
	}
	if passthrough == "on" || passthrough == "all" {
		c.Proto, c.Placeholders = Kitty, true
		return c
	}
	c.Hint = "tmux hides pictures: run  tmux set -g allow-passthrough on  and restart werkz to see the page as an image"
	return c
}
