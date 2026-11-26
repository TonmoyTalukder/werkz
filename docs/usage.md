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
| `/usage` | Your subscription's limits: Claude's 5-hour, weekly and model weekly windows (through Claude Code), and the ChatGPT plan's limits when OpenAI reports them |
| `/balance 100` | Track your remaining Claude credit |
| `/update` | Check for a new werkz release and install it; `/update auto on\|off` |
| `/openrouter paid\|free` | Show OpenRouter's paid models too, or only the free ones (default) |
| `/mode` | Cycle the permission mode; `/mode default\|accept\|plan\|auto\|bypass` picks one (bypass asks to confirm) |
| `/resume [search]` | Find a saved conversation and continue it (see [Conversations](#saved-conversations)); `/resume off\|on` stops or restarts saving |
| `/copy` | Copy the latest response as markdown, or one of its code blocks exactly; `/copy 2` the one before, `/copy prompt` your last request, `/copy all` the whole conversation |
| `/export [file]` | Save the conversation as a markdown file |
| `/docs` | Open this documentation in your browser |
| `/clear` | Start a new conversation (the previous one stays in `/resume`) |
| `/help` · `/exit` | Help · quit |

## Keyboard shortcuts

| Key | Action |
|---|---|
| **Enter** | Send. Messages queue while werkz is working |
| **Ctrl+J** / **Alt+Enter** | New line |
| **Esc** | Interrupt the model, or close a dialog |
| **Shift+Tab** | Cycle the permission mode |
| **Tab** | Complete a slash command |
| **@** | Mention a file or folder (see [Mentioning files](#mentioning-files)) |
| **PgUp / PgDn**, **Ctrl+U / Ctrl+D**, mouse wheel | Scroll the conversation; **Home / End** jump to the top or bottom |
| **Drag** with the mouse | Select text in the conversation; releasing the button copies it (**Ctrl+C** or **y** copies again, **Esc** clears) |
| **Alt+↑** / **Ctrl+O** | Select a whole request or response with **↑/↓**, **Enter** copies it |
| **Ctrl+S** | Turn werkz's mouse handling off (and on again), so your terminal's own selection works |
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
werkz -c                           # continue the latest conversation in this folder (--continue)
werkz -r                           # choose a saved conversation to continue (--resume)
werkz --update                     # install the latest release and exit
werkz --version
```

## Mentioning files

Type **@** to pick a file or folder; its contents go with your message, so the model doesn't need to read it
first. The list follows what you type:

- `@login` finds files by name, anywhere in your folders; `@src/au/log` matches folder by folder
  (`src/auth/login.ts`).
- `@` alone lists the top level; **Tab** on a folder opens it (`@src/`), **Enter** chooses the highlighted
  file or folder, **Esc** closes the list. Enter never sends the message at the same time.
- In a workspace with several folders, paths start with the folder's name: `@api/main.go`,
  `@web/src/app.ts`. Two folders with the same name are told apart as `parent-name`.
- Add lines with `@main.go:10-40`, `@main.go:10` or `@main.go#L10-L40`; quote paths with spaces:
  `@"My Docs/notes.md"`.

Mentions are shown as code-style chips in the input box and in the conversation, with a line underneath
listing what was attached. Files are attached up to 2,000 lines (100 KB); longer files are cut, and the
model can read the rest. A folder sends a two-level listing, and a binary file just its path. The usual
rules apply: credential files are never attached, files outside your working folders aren't attached,
and an `@` inside a word (`me@example.com`) is never a mention. The list uses git's view of the
repository (so ignored files don't show up), or a capped scan of a folder that isn't a repository.

## Copying text

**Drag over the text you want** in the conversation: it's highlighted, and releasing the mouse button copies
it. To copy exactly what was written (markdown, or a code block without the frame around it), use **Alt+↑**
to pick a message, or `/copy`. A response with code blocks lets you choose one, or press **w** to save it to a
file. werkz copies with your system's clipboard tool (`wl-copy`, `xclip`, `xsel`, `pbcopy`, PowerShell on
WSL), inside tmux into tmux's buffer, and over SSH with the OSC 52 terminal sequence. If your terminal doesn't
support that, press **Ctrl+S** and select with the terminal itself.

## Saved conversations

Every conversation is saved as you go, so you can come back to it. `/resume` lists the ones from this
folder (Tab shows all folders), newest first; type to search what was asked and answered, **Enter**
continues one, **Ctrl+X** twice deletes one. Continuing a conversation brings back its folders, its model
and the whole transcript, and the model picks up where it left off. Files it read before are read again
before they're changed. From the command line, `werkz -c` continues the latest conversation in the current
folder and `werkz -r` opens the list.

werkz keeps the latest 500 conversations in `~/.config/werkz/sessions/`, readable only by you. They hold
what you and the model wrote, including file contents it read. `/resume off` stops saving (saved ones stay
until you delete them).

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
