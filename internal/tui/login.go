package tui

import (
	"context"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/agent"
	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm/openai"
	"github.com/TonmoyTalukder/werkz/internal/textutil"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type validateMsg struct {
	ID  string
	OK  bool
	Err string
}

type loginModel struct {
	cfg        *config.Config
	first      bool
	inputs     []textinput.Model
	focus      int
	status     map[string]string // "", checking, ok, fail
	errs       map[string]string
	validating int
	validated  bool
	saveErr    error // last failure writing the config file, shown on the screen
}

func newLogin(cfg *config.Config, first bool) loginModel {
	l := loginModel{cfg: cfg, first: first, status: map[string]string{}, errs: map[string]string{}}
	for _, p := range config.Providers {
		ti := textinput.New()
		ti.Placeholder = p.KeyHint
		ti.Prompt = ""
		ti.Width = 44
		ti.CharLimit = 400
		if p.NeedsKey {
			ti.EchoMode = textinput.EchoPassword
			ti.EchoCharacter = '•'
		}
		ti.SetValue(cfg.Keys[p.ID])
		ti.PlaceholderStyle = sFaint
		ti.TextStyle = sText
		l.inputs = append(l.inputs, ti)
		if cfg.Key(p.ID) != "" && p.NeedsKey {
			l.status[p.ID] = "saved"
		}
	}
	return l
}

func (l *loginModel) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	l.inputs[l.focus], cmd = l.inputs[l.focus].Update(msg)
	return cmd
}

func (l *loginModel) setFocus(i int) tea.Cmd {
	l.inputs[l.focus].Blur()
	l.focus = (i + len(l.inputs)) % len(l.inputs)
	return l.inputs[l.focus].Focus()
}

// key returns done=true when the user leaves the screen.
func (l *loginModel) key(k tea.KeyMsg) (bool, tea.Cmd) {
	switch k.String() {
	case "esc": // leaving without keys is fine: werkz can run on local Ollama
		return true, nil
	case "up", "shift+tab":
		return false, l.setFocus(l.focus - 1)
	case "down", "tab":
		return false, l.setFocus(l.focus + 1)
	case "ctrl+s":
		return false, l.save()
	case "enter":
		if l.validated && l.validating == 0 {
			return true, nil
		}
		if l.focus == len(l.inputs)-1 {
			return false, l.save()
		}
		return false, l.setFocus(l.focus + 1)
	}
	var cmd tea.Cmd
	l.inputs[l.focus], cmd = l.inputs[l.focus].Update(k)
	l.validated = false
	return false, cmd
}

func (l *loginModel) save() tea.Cmd {
	var cmds []tea.Cmd
	for i, p := range config.Providers {
		v := strings.TrimSpace(l.inputs[i].Value())
		if v == "" {
			delete(l.cfg.Keys, p.ID)
		} else {
			l.cfg.Keys[p.ID] = v
		}
		if l.cfg.Key(p.ID) == "" && p.NeedsKey {
			l.status[p.ID] = ""
			continue
		}
		l.status[p.ID] = "checking"
		l.validating++
		cmds = append(cmds, validateCmd(l.cfg, p.ID))
	}
	l.saveErr = l.cfg.Save()
	l.validated = true
	return tea.Batch(cmds...)
}

func (l *loginModel) handleValidate(v validateMsg) {
	l.validating--
	if v.OK {
		l.status[v.ID] = "ok"
	} else {
		l.status[v.ID] = "fail"
		l.errs[v.ID] = v.Err
	}
}

func validateCmd(cfg *config.Config, id string) tea.Cmd {
	// copy what the background check needs while still on the UI goroutine
	key := cfg.Key(id)
	o := &openai.Client{ID: id, BaseURL: cfg.BaseURL(id), Key: key, IncludePaid: cfg.OpenRouterPaid}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if id == "anthropic" {
			c := anthropic.NewClient(option.WithAPIKey(key), option.WithMaxRetries(0))
			_, err := c.Models.List(ctx, anthropic.ModelListParams{})
			if err != nil {
				r, _ := agent.Classify(id, err)
				return validateMsg{ID: id, Err: r}
			}
			return validateMsg{ID: id, OK: true}
		}
		ms, err := o.ListModels(ctx)
		if err != nil {
			r, _ := agent.Classify(id, err)
			return validateMsg{ID: id, Err: r}
		}
		catalog.SetRemote(id, ms)
		return validateMsg{ID: id, OK: true}
	}
}

func (l *loginModel) view(w, h int) string {
	var b strings.Builder
	title := "Welcome to werkz"
	if !l.first {
		title = "Providers & API keys"
	}
	nameW := 27
	if w < 80 {
		nameW = 20
	}
	fieldW := min(46, max(w-nameW-6-24, 14))
	wide := w >= nameW+6+fieldW+40 // room for hints next to the fields
	rows := len(config.Providers) * 2

	// logo only when there is room for it
	if h >= rows+18 && w >= lipgloss.Width(logo)+2 {
		b.WriteString("\n" + gradientBlock(logo, brandStops...) + "\n\n")
	} else if h >= rows+10 {
		b.WriteString(gradient(smallLogo, 0) + "\n\n")
	}
	b.WriteString(sBold.Render(gradient("✦ "+title, 0)) + "\n")
	if h >= rows+12 {
		b.WriteString(wrapTo(sDim.Render("Connect Claude plus any fallbacks you have. Free options: Gemini, OpenRouter :free models, Groq, or local Ollama. OpenRouter paid models: /openrouter paid."), w) + "\n")
		b.WriteString(wrapTo(sFaint.Render("Keys are stored in "+config.Path()+" (mode 0600). Environment variables also work."), w) + "\n")
	}
	b.WriteString("\n")

	for i, p := range config.Providers {
		label := p.Name
		if w < 80 {
			label = strings.SplitN(p.Name, " (", 2)[0]
		}
		cursor := "  "
		name := sText.Render(padRight(textutil.Truncate(label, nameW), nameW))
		if i == l.focus {
			cursor = sPink.Render("❯ ")
			name = sPink.Bold(true).Render(padRight(textutil.Truncate(label, nameW), nameW))
		}
		in := l.inputs[i]
		in.Width = fieldW - 2
		field := lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(lipgloss.Color(map[bool]string{true: cViolet, false: cFaint}[i == l.focus])).
			Width(fieldW).Render(in.View())
		var st string
		switch l.status[p.ID] {
		case "checking":
			st = sYellow.Render("⟳ checking…")
		case "ok":
			st = sGreen.Render("✓ connected")
		case "fail":
			st = sRed.Render("✗ " + textutil.Truncate(l.errs[p.ID], 40))
		case "saved":
			st = sGreen.Render("● saved")
		default:
			if wide {
				if p.ID == "ollama" {
					st = sDim.Render("no key needed · " + p.SignupURL)
				} else {
					st = sDim.Render("get one → " + p.SignupURL)
				}
			}
		}
		tag := "     "
		if p.Free {
			tag = sGreen.Render("free ")
		}
		row := lipgloss.JoinHorizontal(lipgloss.Top, cursor+name+tag, field, "  "+st)
		b.WriteString(row + "\n")
	}
	if !wide && l.status[config.Providers[l.focus].ID] == "" {
		b.WriteString(sDim.Render("get a key → "+config.Providers[l.focus].SignupURL) + "\n")
	}
	b.WriteString("\n")
	switch {
	case l.validating > 0:
		b.WriteString(sYellow.Render("Checking keys…"))
	case l.saveErr != nil:
		b.WriteString(sRed.Render("Couldn't save keys: " + l.saveErr.Error()))
	case l.validated:
		b.WriteString(sGreen.Render("Saved. ") + sText.Render("Press enter to start"))
	default:
		b.WriteString(sDim.Render("↑/↓ move · enter next · ctrl+s save & verify · esc skip"))
	}
	if h >= rows+14 {
		b.WriteString("\n" + wrapTo(sFaint.Render("Note: ChatGPT/Codex and Gemini app subscriptions can't be used here; use API keys from the links above."), w))
	}
	return b.String()
}
