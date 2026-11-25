<p align="center">
  <img src="packaging/icons/werkz-256.png" width="120" alt="werkz">
</p>

<h1 align="center">werkz</h1>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue.svg" alt="License: Apache 2.0"></a>
</p>

<p align="center">
  <b>An agentic coding tool for your terminal.</b><br>
  Use a paid API key (Claude, GPT, Grok) when you want the best models, or free models
  (Gemini, OpenRouter, Groq, local Ollama) when you don't.
</p>

---

werkz reads your codebase, edits files, runs commands and works with the development tools you already
use (your shell, git, test runners, linters and build tools). Describe a task in plain language and werkz
plans it, makes the changes and checks its work, asking before it does anything risky.

It works with whichever models you have: a paid **Claude**, **OpenAI** or **xAI Grok** API key, free tiers
from **Google Gemini**, **OpenRouter** and **Groq**, or a fully offline **Ollama** model. You can switch
models at any time, and you can let werkz fall back to another model when one fails, with your permission.

```text
❯ the login form accepts empty passwords, fix it and add a test

⏺ Grep(password in src)
  ⎿  3 files
⏺ Read(src/auth/login.ts)
  ⎿  Read 84 lines
⏺ Edit(src/auth/login.ts)
  ⎿  +4 −1 lines
⏺ Bash(npm test -- login)
  ⎿  exit 0
⏺ Empty and whitespace-only passwords are now rejected before the API call, with a test covering both.
```

## Highlights

- **Agentic.** werkz reads, searches, edits and runs code in a loop until the task is done, and shows every
  step. A read-only sub-agent handles broad exploration.
- **Bring your own model.** It supports Claude (Fable 5.2, Opus 5.5, Sonnet 5.5, Haiku 5.5), OpenAI, Grok,
  Gemini, OpenRouter, Groq and Ollama. Model lists are fetched live.
- **Free models built in.** OpenRouter's `:free` models are free, Gemini and Groq have free tiers, and Ollama runs
  locally and offline.
- **Fallback only when you choose it.** It's off by default. You can set it to ask before switching (it shows
  whether the next model is free or paid), or to switch automatically.
- **You stay in control.** Edits and commands need approval unless you choose otherwise. There's a read-only
  plan mode, and writes outside your project folders always ask first.
- **Several repos at once.** Optional workspaces group folders, such as a backend and a frontend, with their
  own environment variables.
- **Built to save tokens.** It uses prompt caching with keep-alive, automatic compaction, trimmed test output,
  capped reads and searches, and a cheap sub-agent. In a live check, follow-up requests read 97–99% of their
  input from the cache.
- **A terminal UI that adapts.** werkz runs full-screen and reflows on resize, with live streaming,
  rendered markdown, diffs, a context meter, cost tracking and a saffron-to-blue theme.
- **Click to open.** There's a desktop icon for Linux and a `.app` for macOS, as well as the `werkz` command.

## Contents

- [Install](#install)
- [Quick start](#quick-start)
- [Providers and models](#providers-and-models)
- [How werkz works on your code](#how-werkz-works-on-your-code)
- [Commands and keys](#commands-and-keys)
- [Models, effort and fast mode](#models-effort-and-fast-mode)
- [Fallback](#fallback)
- [Workspaces](#workspaces)
- [Saving tokens and money](#saving-tokens-and-money)
- [Configuration](#configuration)
- [Privacy and safety](#privacy-and-safety)
- [Troubleshooting](#troubleshooting)
- [Development](#development)
- [License](#license)

## Install

### Linux

```bash
tar xzf werkz-0.1.0-linux.tar.gz
cd werkz-linux && ./install.sh
```

The installer puts the `werkz` command in `~/.local/bin` and adds **werkz** with its icon to your app grid.
It also places a clickable icon on your Desktop; if the icon shows as a plain file, right-click it and choose
**Allow Launching**. Clicking the icon opens werkz in a new terminal window: ptyxis, GNOME Terminal, Konsole,
kitty, Alacritty, WezTerm, foot, Tilix or xterm, whichever is installed. To use a specific one, set
`WERKZ_TERMINAL=kitty`. `./uninstall.sh` removes werkz and keeps your settings.

The package supports x86-64 and ARM64.

### macOS

Unzip `werkz-0.1.0-macos.zip` and drag **werkz.app** to Applications. Clicking it opens werkz in a dark
Terminal window. One app runs on both Apple Silicon and Intel.

The app isn't signed with an Apple Developer ID, so the first time you open it, right-click it and choose
**Open**, or run:

```bash
xattr -dr com.apple.quarantine /Applications/werkz.app
```

macOS also asks once for permission for werkz to control Terminal.

### From source

You need Go 1.26 or later.

```bash
cd werkz            # the project folder
go build -o werkz ./cmd/werkz
./werkz
```

`go install ./cmd/werkz` puts `werkz` in `~/go/bin`. To build the Linux and macOS packages, see
[Development](#development).

## Quick start

1. **Start werkz in a project folder:**
   ```bash
   cd ~/code/my-app
   werkz
   ```
2. **Connect at least one provider.** The first run opens the key screen. Paste any keys you have, press
   **Ctrl+S** to save and verify them, then **Enter** to start. No keys at all? Install
   [Ollama](https://ollama.com/download), run `ollama pull llama3.2` and press **Esc**.
3. **Ask for something:**
   ```text
   ❯ explain how authentication works in this repo
   ❯ add pagination to the /users endpoint and update the tests
   ❯ why does `go test ./...` fail? fix it
   ```
4. **Approve the risky steps.** werkz shows each command, and a preview of each edit as a diff, before running it. Choose
   with **↑/↓** and **Enter**: yes, yes and don't ask again this session, or no (also **Esc**: no, and tell werkz what to do
   instead).

Useful next steps: `/model` to switch models, `/effort` to set how hard the model thinks, `/workspace` to work
across several repos, and `/help` for everything else.

## Providers and models

| Provider | Cost | Get a key | Environment variable |
|---|---|---|---|
| **Claude** (Anthropic) | paid | [platform.claude.com](https://platform.claude.com/settings/keys) | `ANTHROPIC_API_KEY` |
| **OpenAI** (GPT / Codex models) | paid | [platform.openai.com](https://platform.openai.com/api-keys) | `OPENAI_API_KEY` |
| **xAI Grok** | paid | [console.x.ai](https://console.x.ai) | `XAI_API_KEY` |
| **Google Gemini** | free tier* | [aistudio.google.com](https://aistudio.google.com/apikey) | `GEMINI_API_KEY` |
| **OpenRouter** | `:free` models; paid models optional | [openrouter.ai/keys](https://openrouter.ai/keys) | `OPENROUTER_API_KEY` |
| **Groq** | free tier* | [console.groq.com](https://console.groq.com/keys) | `GROQ_API_KEY` |
| **Ollama** | free, local, offline | [ollama.com](https://ollama.com/download) (no key) | `OLLAMA_HOST` |

\* Gemini and Groq are free only while the key's project has no billing; werkz labels their models "free tier"
for that reason. OpenRouter's `:free` models are labelled "free", and Ollama "local".

Add or change keys at any time with `/login` (or `werkz --login`). Keys are checked when you save them.

**Claude models:**

| Model | Best for | Price per 1M tokens (in / out) |
|---|---|---|
| Fable 5.2 | the longest, hardest tasks | $8 / $40 |
| Opus 5.5 | complex projects, agents, coding (default) | $4 / $20 |
| Sonnet 5.5 | everyday tasks, cost-efficient | $2 / $10 |
| Haiku 5.5 | fastest, lowest cost | $0.10 / $0.50 ($0.50 / $2.50 for prompts over 100K tokens) |

For the other providers, `/model` lists the models your key can use.

**OpenRouter** gives one key access to hundreds of models. By default werkz shows only its free (`:free`)
models. Run `/openrouter paid` to also list the paid ones (Claude, GPT, Gemini, Llama and many more) with their
real per-token prices; they're billed to your OpenRouter credit, which the status bar shows. `/openrouter free`
hides them again. Paid models are labelled as paid everywhere werkz offers them, including the fallback
dialogs.

> ChatGPT Plus, Claude Pro/Max and Gemini app subscriptions can't be used by third-party tools. werkz needs
> API keys.

## How werkz works on your code

werkz gives the model a small set of tools and runs them on your machine:

| Tool | What it does |
|---|---|
| `Read` | Reads a file or a range of lines |
| `Edit` | Replaces exact text in a file you have read, shown as a diff |
| `Write` | Creates a file, or overwrites one that has been read |
| `Bash` | Runs a shell command in your project: tests, builds, git, package managers |
| `Grep` / `Glob` / `LS` | Searches code and finds files |
| `Task` | Hands a research question to a read-only sub-agent and gets a short report back |

Because `Bash` runs your real shell, werkz works with whatever your project uses: `git`, `go`, `npm`,
`pytest`, `cargo`, `make`, `docker` and so on. If a folder has an **`AGENTS.md`** or **`CLAUDE.md`**, werkz
reads it so the model follows your project's conventions.

### Permission modes

Press **Shift+Tab** (or type `/mode`) to cycle through the modes:

| Mode | Reading and searching | Edits | Shell commands |
|---|---|---|---|
| **default** | ✓ | asks | asks |
| **accept edits** | ✓ | ✓ | asks |
| **plan** | ✓ | blocked (read-only) | blocked |
| **auto** | ✓ | ✓ | ✓ |

Safety rules that apply in **every** mode:

- Reading, listing, searching or writing **outside your working folders** asks first (symlinks are followed,
  so a link inside the project can't point the model elsewhere).
- **Credential locations** (`~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.config/werkz`, `.netrc`, `.git-credentials`, …)
  are never opened by the file tools.
- Your **home folder is never treated as a project**: started in `~` (for example from the desktop icon),
  every file change asks first and searches need a path. Open a project with `/workspace` or `/add-dir`.
- Edits are refused if the model hasn't read the file, or if the file changed on disk since it was read.
- Dialogs ignore keys for a moment after they appear and start on **No**, so text you're typing can't answer
  them by accident.
- "Don't ask again" for a shell command covers only that command (for example `go test …`), and isn't offered
  for compound commands (`;`, `&&`, pipes, redirects) or risky ones (`rm`, `sudo`, `curl`, `ssh`, …).
- **Auto** and **accept edits** last only for the current session; werkz always starts in **default**.
- Timeouts and **Esc** stop a command together with everything it started, and werkz stops its running
  commands when you quit.

## Commands and keys

| Command | What it does |
|---|---|
| `/model` | Choose a model and provider |
| `/effort` | Set the thinking effort; `f` toggles fast mode in the same dialog |
| `/fast` | Turn fast mode on or off (Opus 5.5 and Fable 5.2) |
| `/login` | Add, change or verify API keys |
| `/fallback` | Choose the fallback mode (`/fallback status` shows the chain; `/fallback gemini,ollama` sets the order) |
| `/workspace` | Open, create or leave a workspace (`/workspace new`, `/workspace <name>`, `/workspace none`) |
| `/add-dir <path>` | Add a working folder; `/dirs` lists them |
| `/compact [focus]` | Summarize the conversation to cut tokens; `/compact auto on\|off\|300k` |
| `/cache` | Prompt-cache stats; `/cache ttl 5m\|1h`, `/cache keepalive on\|off\|<minutes>` |
| `/context` | How much of the context window is in use |
| `/cost` | Session tokens, cost and cache savings |
| `/balance 100` | Track your remaining Claude credit |
| `/openrouter paid\|free` | Show OpenRouter's paid models too, or only the free ones (default) |
| `/mode` | Cycle the permission mode |
| `/clear` | Start a fresh conversation |
| `/help` · `/exit` | Help · quit |

| Key | Action |
|---|---|
| **Enter** | Send. Messages queue while werkz is working |
| **Ctrl+J** / **Alt+Enter** | New line |
| **Esc** | Interrupt the model, or close a dialog |
| **Shift+Tab** | Cycle the permission mode |
| **Tab** | Complete a slash command |
| **PgUp / PgDn**, **Ctrl+U / Ctrl+D**, mouse wheel | Scroll the conversation; **Home / End** jump to the top or bottom |
| **Shift + drag** | Select text |
| **Ctrl+C** twice | Quit |

**Command line:**

```bash
werkz                              # start in the current folder
werkz ~/code/api ~/code/web        # work in several folders (the first is the main one)
werkz -w my-workspace              # open a saved workspace
werkz --model claude-sonnet-5-5    # use a model for this session only (not saved)
werkz --provider groq --model llama-3.3-70b-versatile   # name the provider when the id is ambiguous
werkz --login                      # open the API key screen
werkz --version
```

## Models, effort and fast mode

- **`/model`** switches between every connected provider's models, showing context size and price.
- **`/effort`** sets how much the model thinks before it answers:
  - On Claude the levels are `low`, `medium`, `high`, `xhigh` and `max`. Higher is smarter, slower and
    costs more; `xhigh` suits hard coding work. The defaults are `medium` on Opus 5.5 and Haiku 5.5, and
    `high` on Sonnet 5.5 and Fable 5.2.
  - On other providers the levels are `auto`, `low`, `medium` and `high`.
- **Fast mode** (Opus 5.5 and Fable 5.2) streams output up to about 2.5× faster, at double the price.

Changing the model, effort or fast mode in the middle of a conversation invalidates the prompt cache, so
werkz warns you when you make the change, with what re-sending the conversation will cost.

## Fallback

When a model fails (out of credits, rate-limited, overloaded, offline or erroring), werkz can try the next
provider. Fallback is **off by default**. Choose your default with `/fallback`:

| Mode | What happens when a model fails |
|---|---|
| **Off** (default) | The request stops and you decide what to do, for example `/model` |
| **Ask** | werkz shows the failure and asks **which model** to switch to: your fallback models (labelled free or paid), any other connected model, or stop. Use it for this message (**Enter**) or the whole session (**Ctrl+A**) |
| **Auto** | werkz switches to the next provider without asking (this may use paid models) |

Choosing **Ask** or **Auto** in `/fallback` lets you pick exactly which models to fall back to, in order
(`/fallback models` edits the list). Without a list, werkz uses one model per connected provider, free ones
first: Gemini, OpenRouter, Groq, Ollama, then Claude, OpenAI and Grok. The conversation continues on the
new model for the rest of that message. A provider that's out of credits or has a bad key is skipped for the
rest of the session, and with fallback **off** werkz never sends your next message somewhere else instead.

## Workspaces

By default there is **no workspace**: werkz works in the folder you start it from. When a task spans several
repos, open a workspace, which is a named set of folders plus optional environment variables:

- **`/workspace`** opens a picker: No workspace (default), your starred and recent workspaces, and
  **+ New workspace**. In the picker, **Ctrl+S** stars a workspace, **Ctrl+E** edits it and **Ctrl+X**
  (pressed twice) deletes it.
- **`/workspace new`** asks for a name, the folders (Tab completes paths; the first folder is the main one)
  and any environment variables, such as `APP_ENV=dev`.
- **`/add-dir <path>`** adds a folder on the fly. If a workspace is open, the folder is saved into it.

Inside a workspace, the model can read, search and edit in every folder, and `Grep` and `Glob` search all of
them. Shell commands run in the main folder with the workspace's variables. The status bar shows the
workspace, for example `▸ my-app (2)`.

## Saving tokens and money

Every request to a model re-sends the whole conversation, so werkz is designed to keep requests small and
cached. Its techniques come from Anthropic's prompt-caching guidance and from how Claude Code, Codex CLI,
OpenCode, Gemini CLI, Aider and others manage context.

| Technique | What it does |
|---|---|
| **Prompt caching** (Claude) | The system prompt and tools never change, so every request starts with the same prefix. That prefix and the growing conversation are cached; cache reads cost 0.05× the input price on Opus and Sonnet 5.5 and 0.025× on Fable 5.2. Things that change (folders, date, workspace) are added after the history, so they never break the cache. |
| **Cache keep-alive** | While you're idle, a tiny `max_tokens: 0` ping every ~4½ minutes (billed as a cache read) keeps the cache warm for up to 30 minutes. |
| **Idle warning** | If the cache has expired and the conversation is 150k+ tokens, werkz suggests `/compact` before re-sending everything. |
| **Compaction** | `/compact` (or automatically at 300k tokens) replaces the conversation with a structured summary plus the git state. It waits while a test is failing so the error isn't summarized away. |
| **Quiet test output** | Passing tests and progress lines from `go test`, vitest, jest, npm test, pytest and `cargo test` are hidden; failures are kept. |
| **Big output to a file** | Command output over 30k characters is cut to its start and end, and the full, untrimmed output is saved to a file the model can search (kept 7 days in `~/.cache/werkz/outputs`). |
| **Capped reads and searches** | Read returns at most 2,000 lines and asks for a range on files over 256 KB. Grep returns file names by default. |
| **Cheap sub-agent** | Broad exploration runs in a separate context on your current provider (Claude Haiku 5.5 at low effort when you're on Claude) and returns only a short report. |
| **Loop guard** | Five identical tool calls in a row stop the turn. |

The status bar shows the **cache hit rate**. `/cache` and `/cost` show tokens read from and written to the
cache, money saved, and whether the cache is still warm.

**Measured** on the live Claude API (Haiku 5.5, three messages): after the first message, each request read
**97–99%** of its input from the cache and cost about a tenth of the first one.

### Cost and balance

The status bar shows the session cost. For OpenRouter it's the cost OpenRouter reports for each request; for
Claude it's estimated from token usage and list prices. werkz doesn't know OpenAI's or xAI's prices, so usage
there makes the figure a lower bound, shown as `$0.42+?`. Anthropic doesn't expose your prepaid credit to an
API key, so set your starting credit once with `/balance 100` and werkz subtracts its estimate as you work; the
Console's Billing page is the exact figure. OpenRouter credit is read live from its API.

## Configuration

Settings live in **`~/.config/werkz/config.json`** (file mode `0600`). Everything in it can be changed from
inside werkz, so you rarely need to edit it by hand. Saves are atomic, and if the file ever can't be read
(say, after a typo while editing it), werkz tells you and runs with defaults without overwriting it.

| Environment variable | Purpose |
|---|---|
| `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `XAI_API_KEY`, `GEMINI_API_KEY`, `OPENROUTER_API_KEY`, `GROQ_API_KEY` | API keys; used when no key is saved for that provider |
| `OLLAMA_HOST` | Ollama address (default `http://localhost:11434`) |
| `WERKZ_THEME=light` | Use light-background styles |
| `WERKZ_TERMINAL` | Terminal used by the Linux desktop icon (`kitty`, `alacritty`, `gnome-terminal`, …) |
| `NO_TRIM=1` | In a command, disables test-output trimming for that run |

Other files werkz writes:

- Oversized command output, kept for 7 days: `~/.cache/werkz/outputs/`
- Desktop integration on Linux: `~/.local/bin/werkz`, `~/.local/share/applications/werkz.desktop` and
  icons in `~/.local/share/icons/hicolor`

## Privacy and safety

- **Your code goes only to the provider you're using.** werkz has no server, telemetry or analytics. Prompts
  and the files the model reads are sent to the model provider you selected (or stay on your machine with
  Ollama).
- **Keys stay on your machine**, in a config file only your user can read.
- **Approval before risky actions:** edits and commands ask by default; anything outside your working folders
  always asks; credential files are never opened; plan mode is read-only (see
  [Permission modes](#permission-modes)).
- **No surprise spending or sharing:** fallback is off unless you turn it on, the sub-agent stays on your
  provider, Ask mode labels paid models, and model or effort changes that would re-send the conversation
  uncached warn you first.
- **Mind your provider's data policy.** Free tiers (Gemini, OpenRouter `:free`) may use prompts to improve
  their models. Use a paid key or Ollama for sensitive code.

## Troubleshooting

| Problem | Fix |
|---|---|
| *"No provider available"* | Add a key with `/login`, or start Ollama (`ollama serve`) |
| *"rate limited"* on a free model | Free tiers allow only a few requests per minute. Wait a moment, pick another model with `/model`, or enable `/fallback` |
| *"model not found"* | That model isn't available to your key. Choose one from `/model` |
| *"out of API credits"* | Top up in your provider's console, or switch model. Claude credit is managed at platform.claude.com |
| A free model errors as soon as tools are used | Some small or local models don't support tool calls; werkz retries without tools, but choose a tool-capable model for coding work |
| werkz takes a few seconds to appear in some terminals | At startup the UI library asks the terminal for its background colour; terminals that never answer delay it by up to 5 seconds. Wait for the screen before typing |
| The Desktop icon opens as a text file (Linux) | Right-click it → **Allow Launching** |
| *"werkz.app is damaged / can't be opened"* (macOS) | It's the unsigned-app quarantine: right-click → **Open**, or run the `xattr` command in [Install](#install) |
| Text can't be selected | Hold **Shift** while dragging (the mouse is used for scrolling) |

## Development

```bash
go build -o werkz ./cmd/werkz   # build (version shows as "dev")
go test -race ./...             # tests (fake servers only, no paid API calls)
golangci-lint run ./...         # lint
./packaging/build.sh            # release packages in dist/ (needs Python 3 with Pillow for the icons)
```

The release version comes from the `VERSION` file and is injected at build time with
`-ldflags "-X main.version=…"`. `./packaging/build.sh` builds `werkz-<version>-linux.tar.gz` (x86-64 and ARM64,
with installer, launcher and icons) and `werkz-<version>-macos.zip` (`werkz.app` for Apple Silicon and Intel).

### Project layout

```text
werkz/
├── cmd/werkz/          entry point: flags and wiring
├── internal/
│   ├── agent/          agent loop, tool permissions, provider fallback, compaction,
│   │                   Task sub-agent, prompt-cache keep-alive
│   ├── catalog/        models, context windows, effort levels, pricing
│   ├── config/         settings and provider registry (~/.config/werkz/config.json)
│   ├── llm/            provider-neutral messages, tool calls, usage, Provider interface
│   │   ├── claude/     Anthropic Messages API backend (streaming, thinking, caching)
│   │   └── openai/     OpenAI-compatible backend (OpenAI, Grok, Gemini, OpenRouter, Groq, Ollama)
│   ├── tools/          Read/Write/Edit/Bash/LS/Glob/Grep, output trimming, read/edit guards
│   ├── tui/            Bubble Tea interface: transcript, input, dialogs, status bar, theme
│   ├── textutil/       small shared text helpers
│   └── testutil/       fake model servers for tests
├── packaging/          icon generator, Linux launcher/installer, macOS app, build script
└── VERSION
```

The UI and the agent run in separate goroutines and communicate through messages. The engine owns the
conversation; the UI passes it a snapshot of the settings for each turn and changes shared state (permission
mode, working folders) only through the engine's synchronized methods.

## License

werkz is licensed under the [Apache License, Version 2.0](LICENSE). See [NOTICE](NOTICE) for the copyright
notice.

Unless required by applicable law or agreed to in writing, werkz is distributed on an "AS IS" basis, without
warranties or conditions of any kind. Model providers' own terms apply to your use of their APIs.
