// Command werkz is an agentic coding assistant for the terminal.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/TonmoyTalukder/werkz/internal/agent"
	"github.com/TonmoyTalukder/werkz/internal/catalog"
	"github.com/TonmoyTalukder/werkz/internal/config"
	"github.com/TonmoyTalukder/werkz/internal/llm/claudecode"
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
	// Claude Code starts werkz this way to ask permission questions (see internal/llm/claudecode).
	if len(os.Args) == 3 && os.Args[1] == claudecode.BridgeArg {
		if err := claudecode.Bridge(os.Args[2], os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		return
	}
	showVersion := flag.Bool("version", false, "print version")
	login := flag.Bool("login", false, "open the API key screen on start")
	modelFlag := flag.String("model", "", "model id for this session, e.g. claude-sonnet-5-5 (not saved)")
	providerFlag := flag.String("provider", "", "provider of --model: anthropic, openai, xai, gemini, openrouter, groq, ollama, chatgpt or claudecode (default: guessed)")
	wsFlag := flag.String("w", "", "open a saved workspace by name (default: no workspace)")
	updateFlag := flag.Bool("update", false, "check for a new release, install it and exit")
	var cont, resume bool
	flag.BoolVar(&cont, "continue", false, "continue the latest conversation in this folder")
	flag.BoolVar(&cont, "c", false, "short for --continue")
	flag.BoolVar(&resume, "resume", false, "choose a saved conversation to continue")
	flag.BoolVar(&resume, "r", false, "short for --resume")
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
	opts := tui.Options{Version: version, Login: *login, Workspace: *wsFlag, Dirs: flag.Args(), Continue: cont, Resume: resume && !cont,
		NoMouse: os.Getenv("WERKZ_NO_MOUSE") == "1"}
	if err := run(*providerFlag, *modelFlag, opts); err != nil {
		fmt.Fprintln(os.Stderr, "werkz:", err)
		os.Exit(1)
	}
}

func run(provider, model string, opts tui.Options) error {
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
	m := tui.New(cfg, eng, opts)
	popts := []tea.ProgramOption{tea.WithAltScreen()}
	if !opts.NoMouse {
		popts = append(popts, tea.WithMouseCellMotion())
	}
	p = tea.NewProgram(m, popts...)
	defer tools.KillAll()    // don't leave commands (and dev servers) started by the agent running after exit
	defer eng.CloseBrowser() // nor werkz's browser
	// Closing the terminal sends SIGHUP: quit normally so the cleanup above runs (dev servers run in
	// their own process groups and wouldn't get the signal). If the program can't quit in time,
	// clean up and exit anyway.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		if _, ok := <-hup; !ok {
			return
		}
		p.Quit()
		time.Sleep(3 * time.Second)
		eng.CloseBrowser()
		tools.KillAll()
		os.Exit(129)
	}()
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
	if how := u.Method(latest); how.Kind != "self" {
		fmt.Printf("werkz %s is available (you have %s). Update with:\n  %s\n", latest, version, how.Command)
		if how.Note != "" {
			fmt.Println("(" + how.Note + ")")
		}
		return nil
	}
	fmt.Printf("Installing werkz %s (you have %s)…\n", latest, version)
	if err := u.Install(ctx, latest); err != nil {
		if errors.Is(err, update.ErrNotWritable) {
			return fmt.Errorf("%w; update with: %s", err, update.InstallScript)
		}
		return err
	}
	fmt.Printf("✓ werkz %s installed\n", latest)
	return nil
}
