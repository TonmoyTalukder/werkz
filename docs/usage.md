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
| `/mcp` | MCP servers and their tools: `/mcp tools`, `/mcp approve`, `/mcp connect`, `/mcp disconnect`, `/mcp login`, `/mcp reload` (see [MCP servers](#mcp-servers)) |
| `/browser` | werkz's browser: `/browser view` to see the page in the terminal, `/browser connect` to use your own Chrome, `/browser show` to open its window, `/browser hide`, `/browser close` |
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
WSL) or, on a Linux desktop without one, with its own small clipboard helper (it keeps your copy pasteable
after werkz quits, until you copy something else); inside tmux into tmux's buffer, and over SSH with the OSC 52
terminal sequence. If your terminal doesn't
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

  Search is allowed in plan mode too: it only reads. `/search provider` uses only the model's own search; `/search auto` (the default) prefers it and falls back
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
- Dev servers keep running in the background until stopped, and werkz stops them when you quit or close the
  terminal. At most four run at once.
- Links that open a new tab or window open in werkz's tab instead (if the site is allowed). Two werkz windows
  can each use a browser at the same time. If Chrome crashes or you close its window, werkz starts it again
  when needed.

### Using your own Chrome

By default werkz uses its own browser profile, signed in to nothing. To let it work on sites where you are
signed in (a staging admin, a dashboard), connect it to your own Chrome for the session:

1. Start Chrome with a debugging port. `/browser connect` shows the exact command for your system, for example:

   ```bash
   google-chrome --remote-debugging-port=9222 --user-data-dir="$HOME/.config/werkz/chrome-connect"
   ```

   Chrome only allows this with a profile of its own (not your default one), so this is a separate profile: sign
   in there once to the sites werkz should use, and it remembers them.
2. In werkz, run `/browser connect` (or `/browser connect 9333` for another port) and confirm.

While connected, a red **connected to your Chrome** line shows at the bottom. werkz opens one tab of its own and
uses only that tab and the popups it opens: it never reads or touches your other tabs, and it never closes
Chrome. Sites other than this machine ask first even in bypass; sending forms, uploads and downloads always ask;
password and payment fields are never typed into. Downloads it's allowed go where your Chrome saves them.
`/browser disconnect` (or quitting werkz) closes werkz's tab. One limit: Chrome's download setting covers
your whole profile, so werkz turns downloads off only while it is acting and for about 1.5 seconds after (your
own tabs can't download during that time; watching with `/browser view` doesn't count as acting); a page werkz opened that starts a download on its own later can save it to your
Downloads folder, as any page you open can. Connecting is never saved: each session starts
with werkz's own browser.

### Seeing the page in your terminal

werkz shows its browser inside the terminal when the terminal can draw pictures:

| Terminal | What you see |
|---|---|
| kitty, Ghostty | Screenshots right in the conversation, sized to the window; they scroll and resize with the text. `/browser view` shows the live page full screen |
| iTerm2, WezTerm, terminals with Sixel (foot, Konsole, xterm with Sixel, recent GNOME Terminal builds) | `/browser view` shows the live page full screen; screenshots in the conversation are links |
| Others, and inside tmux | Screenshots are links to the files (Ctrl+click or ⌘+click in most terminals) |

`/browser view` refreshes every 1.5 seconds while werkz works; **r** refreshes now and **Esc** goes back. werkz
asks the terminal what it supports when it starts; set `WERKZ_IMAGES=kitty`, `iterm2`, `sixel` or `off` to choose
yourself (inside tmux, that also needs `set -g allow-passthrough on`).

What asks first:

| Action | Asks? |
|---|---|
| Opening a page on this machine (`localhost`, `127.0.0.1`) | No |
| Opening any other site | Yes (not in bypass), with "don't ask again" for that domain. The page can't move itself, open a popup or load a frame from a site you haven't approved |
| Clicking, typing, keys, dragging, setting form values | No, except as below. Blocked in plan mode (scrolling, hovering, reading and screenshots aren't) |
| Running JavaScript (`Eval`) | On this machine, no; elsewhere it asks (in your own Chrome, every time). On a page with a password or payment field it always asks, since a script can read what a page shows. Blocked in plan mode |
| Sending a form (one that posts data: a submit button, a click at its position, Enter or space in a form, or the page's own script) | Always, in every mode. werkz checks the element actually under the pointer or focus, and any post it wasn't approved for is stopped before it leaves the browser. Search-style forms (GET) open like links, under the site rules |
| Uploading a file (only files in your working folders) | Always |
| Downloading a file | Always; it's saved in `~/Downloads` |
| Typing into a password or payment field (by typing, keys or form values) | Never done |
| Starting a dev server | Like any command (`Bash` rules) |

## MCP servers

werkz can use tools from [MCP](https://modelcontextprotocol.io) servers: databases, issue trackers, design
tools, internal APIs. Add servers in the format Claude Code and Claude Desktop use, either in
`~/.config/werkz/mcp.json` (yours, in every project) or in `.werkz/mcp.json` in a project (shared with the
team):

```json
{
  "mcpServers": {
    "github": { "type": "http", "url": "https://api.githubcopilot.com/mcp/" },
    "postgres": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-postgres", "${DATABASE_URL}"] },
    "files": { "command": "my-mcp-server", "env": { "API_KEY": "${MY_API_KEY}" } }
  }
}
```

A server with `command` runs on your machine (stdio); one with `url` is remote (HTTP). `${VAR}` and
`${VAR:-default}` take values from your environment, so keys stay out of the file.

- **Your servers start with werkz.** `/mcp` shows each one's state and number of tools, `/mcp tools` lists the
  tools, `/mcp disconnect` and `/mcp connect` stop and restart one, and `/mcp reload` re-reads the files.
- **A project's servers need your approval first.** werkz tells you when a project has servers and `/mcp` shows
  exactly what each one runs; `/mcp approve <name>` starts it. The approval is remembered for that project and
  asked again if the server's command or address changes.
- **Remote servers that need a sign-in** say so: `/mcp login <name>` opens your browser (OAuth), and werkz keeps
  the tokens in `~/.config/werkz/mcp-auth.json`, readable only by you. `/mcp logout <name>` forgets them.
- **Each tool asks the first time it runs**, with "don't ask again" for that tool this session; in auto and bypass
  mode they don't ask, and in plan mode only tools the server marks read-only run. What a tool returns reaches
  the model marked as untrusted. The model sees them as `mcp__<server>__<tool>`.

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
| `Act` | Mouse and keyboard: clicks (also right, double, triple, with modifiers, at an element or a point), typing into the focused field, hover, scroll, keys and shortcuts, drag and drop, wait, and a zoomed screenshot of a region |
| `Find` · `PageText` · `FormInput` | Finds elements by text or role; reads a page's main text; sets selects, checkboxes, radios, sliders and text |
| `Eval` · `Tabs` · `Resize` · `Batch` | Runs JavaScript to inspect a page; werkz's own tabs; mobile, tablet or desktop sizes; several steps in one call |

Because `Bash` runs your real shell, werkz works with whatever your project uses: `git`, `go`, `npm`,
`pytest`, `cargo`, `make`, `docker` and so on. If a folder has an **`AGENTS.md`** or **`CLAUDE.md`**, werkz
reads it once per session so the model follows your project's conventions. What each tool may do without
asking depends on the [permission mode](safety.md).
