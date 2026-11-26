// Package mcp is werkz's Model Context Protocol client manager: it loads
// server definitions from config files, connects to them (stdio, streamable
// HTTP or SSE) and exposes their tools to the agent.
package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ServerConfig describes one MCP server.
type ServerConfig struct {
	Name    string
	Type    string // "stdio" | "http" | "sse"
	Command string
	Args    []string
	Env     map[string]string
	URL     string
	Headers map[string]string
	Scope   string // "user" | "project"
	Source  string // the file it came from
	// raw is the entry as written, before ${VAR} expansion: what an approval is tied to.
	raw rawServer
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

var varRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// expand replaces ${VAR} and ${VAR:-default} from the environment. An unset
// variable without a default expands to the empty string; with a default, an
// unset or empty variable yields the default.
func expand(s string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := varRe.FindStringSubmatch(m)
		v := os.Getenv(sub[1])
		if v == "" && strings.Contains(m, ":-") {
			return sub[2]
		}
		return v
	})
}

// UserConfigPath is <os.UserConfigDir()>/werkz/mcp.json ("" if unknown).
func UserConfigPath() string {
	d, err := os.UserConfigDir()
	if err != nil || d == "" {
		return ""
	}
	return filepath.Join(d, "werkz", "mcp.json")
}

type rawServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// LoadConfig reads the user config file and projectDir/.werkz/mcp.json (both
// optional). Project servers replace user servers of the same name. A bad
// file or a bad entry produces an error but never stops the others.
func LoadConfig(userPath, projectDir string) ([]ServerConfig, []error) {
	var errs []error
	byName := map[string]ServerConfig{}
	load := func(path, scope string) {
		if path == "" {
			return
		}
		servers, es := loadFile(path, scope)
		errs = append(errs, es...)
		for _, s := range servers {
			byName[s.Name] = s
		}
	}
	load(userPath, "user")
	if projectDir != "" {
		load(filepath.Join(projectDir, ".werkz", "mcp.json"), "project")
	}
	out := make([]ServerConfig, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, errs
}

func loadFile(path, scope string) ([]ServerConfig, []error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("%s: %w", path, err)}
	}
	var top struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, []error{fmt.Errorf("%s: invalid JSON: %w", path, err)}
	}
	names := make([]string, 0, len(top.MCPServers))
	for n := range top.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []ServerConfig
	var errs []error
	for _, name := range names {
		if !nameRe.MatchString(name) {
			errs = append(errs, fmt.Errorf("%s: invalid server name %q (use letters, digits, _ and -, max 40)", path, name))
			continue
		}
		var r rawServer
		if err := json.Unmarshal(top.MCPServers[name], &r); err != nil {
			errs = append(errs, fmt.Errorf("%s: server %q: %w", path, name, err))
			continue
		}
		c := ServerConfig{
			Name: name, Type: strings.ToLower(strings.TrimSpace(r.Type)),
			Command: expand(r.Command), URL: expand(r.URL),
			Scope: scope, Source: path, raw: r,
		}
		for _, a := range r.Args {
			c.Args = append(c.Args, expand(a))
		}
		if len(r.Env) > 0 {
			c.Env = map[string]string{}
			for k, v := range r.Env {
				c.Env[k] = expand(v)
			}
		}
		if len(r.Headers) > 0 {
			c.Headers = map[string]string{}
			for k, v := range r.Headers {
				c.Headers[k] = expand(v)
			}
		}
		if c.Type == "streamable-http" || c.Type == "streamable_http" {
			c.Type = "http"
		}
		if c.Type == "" {
			switch {
			case c.Command != "":
				c.Type = "stdio"
			case c.URL != "":
				c.Type = "http"
			}
		}
		if err := c.validate(); err != nil {
			errs = append(errs, fmt.Errorf("%s: server %q: %w", path, name, err))
			continue
		}
		out = append(out, c)
	}
	return out, errs
}

func (c ServerConfig) validate() error {
	switch c.Type {
	case "stdio":
		if c.Command == "" {
			return errors.New("stdio server needs a \"command\"")
		}
	case "http", "sse":
		if c.URL == "" {
			return fmt.Errorf("%s server needs a \"url\"", c.Type)
		}
	case "":
		return errors.New("needs a \"command\" (stdio) or a \"url\" (http)")
	default:
		return fmt.Errorf("unknown type %q (use stdio, http or sse)", c.Type)
	}
	return nil
}

// Hash is a stable hash of what runs or connects: type, command, args, env
// keys and values, url and header keys (header values, which hold secrets,
// are excluded). Name, scope and source are not part of it.
func (c ServerConfig) Hash() string {
	keys := func(m map[string]string, withValues bool) [][2]string {
		out := make([][2]string, 0, len(m))
		for k, v := range m {
			if !withValues {
				v = ""
			}
			out = append(out, [2]string{k, v})
		}
		sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
		return out
	}
	// The entry as written (before ${VAR} expansion) when it came from a file: every change to the
	// file, headers included, needs approval again, while a variable's value may change freely.
	// Built in code (no file), the expanded values stand in, header values left out.
	cmd, args, env, url, hdr := c.Command, c.Args, keys(c.Env, true), c.URL, keys(c.Headers, false)
	if c.Source != "" {
		cmd, args, env, url, hdr = c.raw.Command, c.raw.Args, keys(c.raw.Env, true), c.raw.URL, keys(c.raw.Headers, true)
	}
	b, _ := json.Marshal(struct {
		Type    string      `json:"t"`
		Command string      `json:"c"`
		Args    []string    `json:"a"`
		Env     [][2]string `json:"e"`
		URL     string      `json:"u"`
		Headers [][2]string `json:"h"`
	}{c.Type, cmd, args, env, url, hdr})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Describe is what the server runs or connects to, as written in its file (${VAR} templates, not
// their values): the command and arguments, its environment variables, or the URL and headers.
// Approvals show this, since a variable like NODE_OPTIONS or a header changes what runs or is sent.
func (c ServerConfig) Describe() string {
	r := c.raw
	if c.Source == "" { // built in code: no file to quote
		r = rawServer{Command: c.Command, Args: c.Args, URL: c.URL}
	}
	pairs := func(m map[string]string, sep string) []string {
		var out []string
		for k, v := range m {
			out = append(out, k+sep+v)
		}
		sort.Strings(out)
		return out
	}
	var parts []string
	if c.Type == "stdio" {
		parts = append(parts, strings.TrimSpace(r.Command+" "+strings.Join(r.Args, " ")))
		if env := pairs(r.Env, "="); len(env) > 0 {
			parts = append(parts, "with "+strings.Join(env, " "))
		}
	} else {
		parts = append(parts, r.URL)
		if h := pairs(r.Headers, ": "); len(h) > 0 {
			parts = append(parts, "headers "+strings.Join(h, ", "))
		}
	}
	return strings.Join(parts, " · ")
}
