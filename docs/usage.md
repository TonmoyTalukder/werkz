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
| `/search` | Web search for the model: `/search brave\|tavily <key>`, `/search searxng <url>`, `/search off` (see [Web access](#web-access)) |
| `/browser` | werkz's browser: `/browser show` to watch it, `/browser hide`, `/browser close` |
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

## Web access

werkz can read pages and search the web, so it can look up current documentation, an error message or a
library's latest version.

- **WebFetch** reads any public `http(s)` page as Markdown, with scripts, menus and page furniture removed.
  Long pages are read in parts. It needs no setup and doesn't ask: it only reads.
- **Pages on your local network ask first**: `localhost`, private addresses (`10.x`, `192.168.x`, …) and
  cloud metadata addresses. Choose "don't ask again" to allow one host (say, your dev server) for the session.
  The check is made when connecting, so a public address that redirects or resolves to a local one asks too.
- **WebSearch** uses the search built into your model's provider when it has one: Claude (on your Claude
  model; Anthropic charges $10 per 1,000 searches plus tokens), OpenAI, and Gemini (Google Search). Other
  models use a search service you set up with `/search`:

  ```text
  /search brave <key>       # Brave (free tier)
  /search tavily <key>      # Tavily (free tier)
  /search searxng <url>     # your SearXNG, no key
  /search                   # what's used now
  /search off               # no web search
  ```

  `/search provider` uses only the model's own search; `/search auto` (the default) prefers it and falls back
  to a service. Keys can also come from `BRAVE_API_KEY`, `TAVILY_API_KEY` and `SEARXNG_URL`.

Everything read from the web reaches the model marked as untrusted: it is told to use it as information and
never to follow instructions in it. In Claude Code mode, Claude Code's own web tools are used, with the same
rule for local pages.

## The browser

werkz checks web UI it changes the way you would: it starts the dev server (`DevServer`), opens the page,
clicks and types through it, reads the console and the network requests, and compares screenshots from before
and after a change. For example: "the signup button does nothing, fix it and check it in the browser".

- It uses **your installed Chrome, Chromium or Edge**, with its own separate profile (your bookmarks, logins
  and cookies are never used). werkz never downloads a browser; if none is installed, the browser tools aren't
  offered. Set `WERKZ_BROWSER` to use a particular one.
- It runs in the background. `/browser show` opens its window so you can watch (on a desktop), `/browser hide`
  goes back, `/browser close` closes it. `/browser` shows its state and the running dev servers.
- The model reads pages through their accessibility tree (the structure screen readers use), with ids it
  clicks and types by. Screenshots are saved under `~/.cache/werkz/screenshots/`; comparing two says how much
  of the page changed and where.
- Dev servers keep running in the background until stopped, and werkz stops them when you quit. At most four
  run at once.

What asks first:

| Action | Asks? |
|---|---|
| Opening a page on this machine (`localhost`, `127.0.0.1`) | No |
| Opening any other site | Yes, with "don't ask again" for that domain. The page can't move itself to a site you haven't approved |
| Clicking and typing | No, except as below. Blocked in plan mode |
| Submitting a form (submit button, or Enter in a form) | Always, in every mode |
| Uploading a file (only files in your working folders) | Always |
| Downloading a file | Always |
| Typing into a password or payment field | Never done |
| Starting a dev server | Like any command (`Bash` rules) |

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
| `WebFetch` | Reads a web page as Markdown: docs, issues, changelogs, or your local dev server (see [Web access](#web-access)) |
| `WebSearch` | Searches the web for current information |
| `DevServer` | Starts your project's dev server in the background, waits for its port, reads its output, stops it |
| `Browse` · `ReadPage` · `Click` · `Type` | Opens and uses pages in werkz's browser (see [The browser](#the-browser)) |
| `Screenshot` · `Console` · `Network` | Captures and compares screenshots; reads console errors and failed requests |

Because `Bash` runs your real shell, werkz works with whatever your project uses: `git`, `go`, `npm`,
`pytest`, `cargo`, `make`, `docker` and so on. If a folder has an **`AGENTS.md`** or **`CLAUDE.md`**, werkz
reads it once per session so the model follows your project's conventions. What each tool may do without
asking depends on the [permission mode](safety.md).
