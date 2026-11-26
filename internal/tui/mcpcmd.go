package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/mcp"
	"github.com/TonmoyTalukder/werkz/internal/mcpauth"
	"github.com/TonmoyTalukder/werkz/internal/textutil"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// MCP servers: yours (~/.config/werkz/mcp.json) start with werkz; a project's (.werkz/mcp.json)
// start only once you approve them, and again only while their command or address is unchanged.
//
//	/mcp                 servers, their state and tools
//	/mcp tools [server]  the tools they offer
//	/mcp approve <name>  approve and start a project's server
//	/mcp connect <name>  (re)start a server · /mcp disconnect <name> stops it
//	/mcp login <name>    sign in to a server that asks for it · /mcp logout <name>
//	/mcp reload          read the config files again

type mcpStartedMsg struct {
	name string
	err  error
}

type mcpLoginMsg struct {
	name string
	err  error
}

func (m *model) mcpEnabled() bool { return m.mcp != nil }

func (m *model) mcpProjectDir() string {
	if m.eng != nil {
		return m.eng.Dirs()[0]
	}
	return m.startDir
}

func approvalKey(dir, name string) string { return dir + "|" + name }

func (m *model) mcpApproved(c mcp.ServerConfig) bool {
	if c.Scope != "project" {
		return true
	}
	return m.cfg.MCPApproved[approvalKey(m.mcpProjectDir(), c.Name)] == c.Hash()
}

// loadMCP reads the config files and starts the servers that may start. Called at start-up and
// by /mcp reload.
func (m *model) loadMCP(announce bool) tea.Cmd {
	if !m.mcpEnabled() {
		return nil
	}
	cfgs, errs := mcp.LoadConfig(mcp.UserConfigPath(), m.mcpProjectDir())
	m.mcpCfgs = cfgs
	var cmds []tea.Cmd
	for _, err := range errs {
		cmds = append(cmds, m.printS(renderNotice("warn", "MCP config: "+err.Error())))
	}
	running := map[string]bool{}
	for _, s := range m.mcp.Status() {
		running[s.Name] = s.State == mcp.State("connected") || s.State == mcp.State("connecting")
	}
	var pending []string
	for _, c := range cfgs {
		if !m.mcpApproved(c) {
			pending = append(pending, c.Name)
			continue
		}
		if !running[c.Name] {
			cmds = append(cmds, m.startMCP(c))
		}
	}
	if len(pending) > 0 {
		cmds = append(cmds, m.printS(renderNotice("warn", fmt.Sprintf("This project has %d MCP server%s that need your approval before "+
			"they start: %s. /mcp shows what they run; /mcp approve <name> starts one.", len(pending), plural(len(pending)), strings.Join(pending, ", ")))))
	} else if announce && len(cfgs) == 0 {
		cmds = append(cmds, m.printS(renderNotice("info", "No MCP servers configured. Add them to "+tildify(mcp.UserConfigPath())+
			" or the project's .werkz/mcp.json (the same format Claude Code and Claude Desktop use).")))
	}
	return tea.Batch(cmds...)
}

func (m *model) startMCP(c mcp.ServerConfig) tea.Cmd {
	mgr := m.mcp
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		return mcpStartedMsg{name: c.Name, err: mgr.Start(ctx, c)}
	}
}

func (m *model) handleMCPStarted(msg mcpStartedMsg) tea.Cmd {
	if msg.err == nil {
		return nil // quiet when it works: /mcp shows it
	}
	if errors.Is(msg.err, mcpauth.ErrNeedsAuth) {
		return m.printS(renderNotice("warn", "MCP server "+msg.name+" needs you to sign in: /mcp login "+msg.name))
	}
	return m.printS(renderNotice("warn", "MCP server "+msg.name+" didn't start: "+msg.err.Error()+" (/mcp shows the details)"))
}

func (m *model) mcpConfig(name string) (mcp.ServerConfig, bool) {
	for _, c := range m.mcpCfgs {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	return mcp.ServerConfig{}, false
}

func (m *model) mcpCommand(args []string) tea.Cmd {
	p := m.printS
	if !m.mcpEnabled() {
		return p(renderNotice("warn", "MCP isn't available in this session."))
	}
	if len(args) == 0 || args[0] == "list" {
		return m.push(func(int) string { return m.mcpText() })
	}
	sub := strings.ToLower(args[0])
	if sub == "reload" {
		return m.loadMCP(true)
	}
	if sub == "tools" {
		server := ""
		if len(args) > 1 {
			server = args[1]
		}
		return m.push(func(int) string { return m.mcpToolsText(server) })
	}
	if len(args) < 2 {
		return p(renderNotice("warn", "Usage: /mcp · /mcp tools [server] · /mcp approve|connect|disconnect|login|logout <server> · /mcp reload"))
	}
	c, ok := m.mcpConfig(args[1])
	if !ok {
		return p(renderNotice("warn", "No MCP server named "+args[1]+" in your config (/mcp lists them; /mcp reload re-reads the files)."))
	}
	switch sub {
	case "approve":
		if c.Scope != "project" {
			return p(renderNotice("info", c.Name+" is one of your own servers: it doesn't need approval."))
		}
		// show everything it runs, in full, and confirm
		m.mcpPending, m.mcpSel, m.state, m.dialogAt = c.Name, 0, stateMCPApprove, time.Now()
		return nil
	}
	return m.mcpCommand2(sub, c)
}

// approveMCP records the approval of a project server and starts it.
func (m *model) approveMCP(c mcp.ServerConfig) tea.Cmd {
	p := m.printS
	{
		if m.cfg.MCPApproved == nil {
			m.cfg.MCPApproved = map[string]string{}
		}
		m.cfg.MCPApproved[approvalKey(m.mcpProjectDir(), c.Name)] = c.Hash()
		m.saveConfig()
		return tea.Batch(p(renderNotice("info", "Approved "+c.Name+" for this project. It starts now, and with werkz "+
			"from now on, until its entry in .werkz/mcp.json changes.")), m.startMCP(c))
	}
}

func (m *model) mcpCommand2(sub string, c mcp.ServerConfig) tea.Cmd {
	p := m.printS
	switch sub {
	case "connect", "start":
		if !m.mcpApproved(c) {
			return p(renderNotice("warn", c.Name+" is a project server you haven't approved: it runs "+mcpWhat(c)+". /mcp approve "+c.Name+" starts it."))
		}
		return tea.Batch(p(sDim.Render("  starting "+c.Name+"…")), m.startMCP(c))
	case "disconnect", "stop":
		if err := m.mcp.Stop(c.Name); err != nil {
			return p(renderNotice("warn", err.Error()))
		}
		return p(renderNotice("info", "Stopped MCP server "+c.Name+"."))
	case "login":
		if !m.mcpApproved(c) {
			return p(renderNotice("warn", c.Name+" is a project server you haven't approved: it connects to "+mcpWhat(c)+
				". /mcp approve "+c.Name+" first."))
		}
		if c.Type == "stdio" {
			return p(renderNotice("info", c.Name+" runs on this machine: there's nothing to sign in to."))
		}
		url := c.URL
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer cancel()
			err := mcpauth.Login(ctx, url, mcpauth.OpenStore(""), func(authURL string) error {
				return openBrowser(authURL)
			})
			return mcpLoginMsg{name: c.Name, err: err}
		}
	case "logout":
		if err := mcpauth.Logout(c.URL, mcpauth.OpenStore("")); err != nil {
			return p(renderNotice("warn", err.Error()))
		}
		_ = m.mcp.Stop(c.Name)
		return p(renderNotice("info", "Signed out of "+c.Name+" and stopped it."))
	}
	return p(renderNotice("warn", "Usage: /mcp · /mcp tools [server] · /mcp approve|connect|disconnect|login|logout <server> · /mcp reload"))
}

func (m *model) handleMCPLogin(msg mcpLoginMsg) tea.Cmd {
	if msg.err != nil {
		return m.printS(renderNotice("error", "Sign-in to "+msg.name+" failed: "+msg.err.Error()))
	}
	c, ok := m.mcpConfig(msg.name)
	if !ok || !m.mcpApproved(c) {
		return nil // never starts a server that isn't approved
	}
	return tea.Batch(m.printS(renderNotice("info", "Signed in to "+msg.name+"; connecting.")), m.startMCP(c))
}

// mcpWhat is what a server runs or where it connects, as written: command, arguments and
// environment, or URL and headers. Terminal escapes are removed, so the file can't hide any of it.
func mcpWhat(c mcp.ServerConfig) string { return "`" + textutil.Exact(c.Describe()) + "`" }

func (m *model) mcpApproveKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "up", "down", "tab", "shift+tab":
		m.mcpSel = 1 - m.mcpSel
	case "esc":
		m.state = stateChat
	case "enter":
		m.state = stateChat
		if c, ok := m.mcpConfig(m.mcpPending); ok && m.mcpSel == 1 {
			return m.approveMCP(c)
		}
	}
	return nil
}

func (m *model) mcpApproveView() string {
	c, _ := m.mcpConfig(m.mcpPending)
	w := max(m.w-8, 20)
	var b strings.Builder
	b.WriteString(sYellow.Bold(true).Render("Start the project's MCP server "+c.Name+"?") + "\n\n")
	what := "It runs this command on your machine, with your user's access:"
	if c.Type != "stdio" {
		what = "It connects to this server and sends it these headers:"
	}
	b.WriteString(wrapTo(sText.Render(what), w) + "\n")
	b.WriteString(wrapTo(sPink.Render(textutil.Exact(c.Describe())), w) + "\n")
	b.WriteString(sDim.Render("From "+tildify(c.Source)+". Approve only if you trust this repository.") + "\n\n")
	opts := []string{"No (esc)", "Yes, approve and start it for this project"}
	for i, o := range opts {
		if i == m.mcpSel {
			b.WriteString(sPink.Render("❯ ") + sPink.Bold(true).Render(o) + "\n")
		} else {
			b.WriteString(sDim.Render("  "+o) + "\n")
		}
	}
	b.WriteString(sDim.Render("↑/↓ choose · enter confirm"))
	return box(b.String(), cYellow, m.w)
}

func (m *model) mcpText() string {
	w := max(m.w-8, 30)
	var lines []string
	add := func(s string) { lines = append(lines, indent(wrapTo(s, w), "  ", "    ")) }
	status := map[string]mcp.Status{}
	for _, s := range m.mcp.Status() {
		status[s.Name] = s
	}
	if len(m.mcpCfgs) == 0 {
		add(sDim.Render("No MCP servers configured. Add them to " + tildify(mcp.UserConfigPath()) + " (yours) or .werkz/mcp.json (this project), " +
			"in the format Claude Code and Claude Desktop use; then /mcp reload."))
	}
	for _, c := range m.mcpCfgs {
		st, has := status[c.Name]
		state := sDim.Render("not started")
		switch {
		case !m.mcpApproved(c):
			state = sYellow.Render("needs your approval · /mcp approve " + c.Name)
		case has && st.State == mcp.State("connected"):
			state = sGreen.Render(fmt.Sprintf("✓ connected · %d tool%s", st.Tools, plural(st.Tools)))
		case has && st.State == mcp.State("connecting"):
			state = sDim.Render("connecting…")
		case has && st.State == mcp.State("needs-auth"):
			state = sYellow.Render("needs you to sign in · /mcp login " + c.Name)
		case has && st.State == mcp.State("failed"):
			state = sRed.Render("✗ " + st.Err)
		case has && st.State == mcp.State("stopped"):
			state = sDim.Render("stopped · /mcp connect " + c.Name)
		}
		add(sText.Render(padRight(c.Name, 16)) + sDim.Render(padRight(c.Scope, 9)) + state)
		add(sFaint.Render("  " + ansi.Truncate(mcpWhat(c), w-4, "…")))
	}
	lines = append(lines, "")
	add(sDim.Render("/mcp tools · /mcp approve|connect|disconnect|login|logout <name> · /mcp reload"))
	add(sDim.Render("Each MCP tool asks the first time it runs (not in auto or bypass mode); what it returns reaches the model as untrusted."))
	return box(gradient("MCP servers", 0)+"\n"+strings.Join(lines, "\n"), cSky, m.w)
}

func (m *model) mcpToolsText(server string) string {
	var lines []string
	w := max(m.w-8, 30)
	for _, t := range m.mcp.Tools() {
		if server != "" && !strings.EqualFold(t.Server, server) {
			continue
		}
		tag := ""
		if t.ReadOnly {
			tag = sDim.Render(" · read-only")
		}
		desc := strings.Join(strings.Fields(t.Description), " ")
		lines = append(lines, "  "+sSky.Render(t.Server)+sDim.Render(" · ")+sText.Render(t.Name)+tag)
		if desc != "" {
			lines = append(lines, sFaint.Render("    "+ansi.Truncate(desc, w-4, "…")))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, sDim.Render("  No tools: no MCP server is connected (/mcp)."))
	}
	return box(gradient("MCP tools", 0)+"\n"+strings.Join(lines, "\n"), cSky, m.w)
}
