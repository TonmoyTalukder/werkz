# Usage

## Slash commands

| Command | What it does |
|---|---|
| `/model` | Choose a model and provider |
| `/effort` | Set the thinking effort; `f` toggles fast mode in the same dialog |
| `/fast` | Turn fast mode on or off (Opus 5.5 and Fable 5.2) |
| `/login` | Add, change or verify API keys |
| `/login chatgpt` · `/logout chatgpt` | Sign in with ChatGPT to use your ChatGPT plan · sign out (see [Providers](providers-and-models.md#using-a-subscription-instead-of-an-api-key)) |
| `/login claudecode` | Check that Claude Code is installed, to use your Claude subscription through it |
| `/fallback` | Choose the fallback mode and models (`/fallback models`, `/fallback status`, `/fallback gemini,ollama` sets the order) |
| `/workspace` | Open, create or leave a workspace (`/workspace new`, `/workspace <name>`, `/workspace none`) |
| `/add-dir <path>` | Add a working folder; `/dirs` lists them |
| `/compact [focus]` | Summarize the conversation to cut tokens; `/compact auto on\|off\|300k` |
| `/cache` | Prompt-cache stats; `/cache ttl 5m\|1h`, `/cache keepalive on\|off\|<minutes>` |
| `/context` | How much of the context window is in use; `/context window 350k\|full` sets or removes your own window limit |
| `/cost` | Session tokens, cost and cache savings |
| `/balance 100` | Track your remaining Claude credit |
| `/update` | Check for a new werkz release and install it; `/update auto on\|off` |
| `/openrouter paid\|free` | Show OpenRouter's paid models too, or only the free ones (default) |
| `/mode` | Cycle the permission mode |
| `/clear` | Start a fresh conversation |
| `/help` · `/exit` | Help · quit |

## Keyboard shortcuts

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

In permission and fallback dialogs, use **↑/↓** and **Enter**; **Esc** means no. Letters and digits are
ignored there, so text you're typing can't answer a dialog by accident.

## Command line

```bash
werkz                              # start in the current folder
werkz ~/code/api ~/code/web        # work in several folders (the first is the main one)
werkz -w my-workspace              # open a saved workspace
werkz --model claude-sonnet-5-5    # use a model for this session only (not saved)
werkz --provider groq --model llama-3.3-70b-versatile   # name the provider when the id is ambiguous
werkz --login                      # open the API key screen
werkz --update                     # install the latest release and exit
werkz --version
```

## The tools werkz uses

werkz gives the model a small set of tools and runs them on your machine:

| Tool | What it does |
|---|---|
| `Read` | Reads a file or a range of lines (up to 2,000 lines per call) |
| `Edit` | Replaces exact text in a file it has read, shown as a diff |
| `Write` | Creates a file, or overwrites one it has read |
| `Bash` | Runs a shell command in your project: tests, builds, git, package managers |
| `Grep` / `Glob` / `LS` | Searches code and finds files |
| `Task` | Hands a research question to a read-only sub-agent and gets a short report back |

Because `Bash` runs your real shell, werkz works with whatever your project uses: `git`, `go`, `npm`,
`pytest`, `cargo`, `make`, `docker` and so on. If a folder has an **`AGENTS.md`** or **`CLAUDE.md`**, werkz
reads it once per session so the model follows your project's conventions. What each tool may do without
asking depends on the [permission mode](safety.md).
