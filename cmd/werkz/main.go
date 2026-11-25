// Command werkz is an agentic coding assistant for the terminal.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/agent"
	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/tools"
	"github.com/TonmoyTalukder/werkz/internal/tui"
	"github.com/TonmoyTalukder/werkz/internal/update"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// version is set at build time: go build -ldflags "-X main.version=1.2.3". Builds made with
// "go install …@vX.Y.Z" report the module version instead.
var version = "dev"

func init() {
	if version != "dev" {
		return
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = strings.TrimPrefix(bi.Main.Version, "v")
	}
}

func main() {
	showVersion := flag.Bool("version", false, "print version")
	login := flag.Bool("login", false, "open the API key screen on start")
	modelFlag := flag.String("model", "", "model id for this session, e.g. claude-sonnet-5-5 (not saved)")
	providerFlag := flag.String("provider", "", "provider of --model: anthropic, openai, xai, gemini, openrouter, groq or ollama (default: guessed)")
	wsFlag := flag.String("w", "", "open a saved workspace by name (default: no workspace)")
	updateFlag := flag.Bool("update", false, "check for a new release, install it and exit")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: werkz [flags] [folder ...]")
		fmt.Fprintln(os.Stderr, "  folders: work in these folders (first is the main one); default is the current folder")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("werkz", version)
		return
	}
	if *updateFlag {
		if err := selfUpdate(); err != nil {
			fmt.Fprintln(os.Stderr, "werkz:", err)
			os.Exit(1)
		}
		return
	}
	if *providerFlag != "" && config.ProviderByID(*providerFlag) == nil {
		fmt.Fprintln(os.Stderr, "werkz: unknown provider", *providerFlag)
		os.Exit(2)
	}
	if err := run(*login, *providerFlag, *modelFlag, *wsFlag, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "werkz:", err)
		os.Exit(1)
	}
}

func run(login bool, provider, model, workspace string, dirs []string) error {
	// Use dark-terminal styles unless WERKZ_THEME=light.
	lipgloss.SetHasDarkBackground(os.Getenv("WERKZ_THEME") != "light")

	cfg := config.Load()
	if model != "" {
		if provider == "" {
			provider = catalog.GuessProvider(model)
		}
		cfg.UseSessionModel(provider, model, catalog.Lookup(provider, model).DefaultEffort)
	}

	var p *tea.Program
	eng := agent.New(cfg, func(v any) { p.Send(v) })
	m := tui.New(cfg, eng, tui.Options{Version: version, Login: login, Workspace: workspace, Dirs: dirs})
	p = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	defer tools.KillAll() // don't leave commands started by the agent running after exit
	if _, err := p.Run(); err != nil {
		return err
	}
	return cfg.Save()
}

// selfUpdate implements "werkz --update".
func selfUpdate() error {
	if !update.Newer("999.0.0", version) {
		return fmt.Errorf("this is a development build (%s); it doesn't update itself", version)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	u := &update.Updater{}
	latest, err := u.Latest(ctx)
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}
	if !update.Newer(latest, version) {
		fmt.Printf("werkz %s is up to date\n", version)
		return nil
	}
	fmt.Printf("Installing werkz %s (you have %s)…\n", latest, version)
	if err := u.Install(ctx, latest); err != nil {
		var managed *update.ManagedError
		if errors.As(err, &managed) {
			return fmt.Errorf("werkz %s is available; %w", latest, err)
		}
		if errors.Is(err, update.ErrNotWritable) {
			return fmt.Errorf("%w; update with: curl -fsSL https://raw.githubusercontent.com/TonmoyTalukder/werkz/main/install.sh | sh", err)
		}
		return err
	}
	fmt.Printf("✓ werkz %s installed\n", latest)
	return nil
}
