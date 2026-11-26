# Safety and privacy

## Permission modes

Press **Shift+Tab** (or type `/mode`) to cycle through the modes:

| Mode | Reading and searching | Edits | Shell commands |
|---|---|---|---|
| **default** | ✓ | asks | asks |
| **accept edits** | ✓ | ✓ | asks |
| **plan** | ✓ | blocked (read-only) | blocked |
| **auto** | ✓ | ✓ | ✓ |
| **bypass** | ✓ | ✓ | ✓, and nothing asks, even outside your working folders |

**Auto**, **accept edits** and **bypass** last only for the current session; werkz always starts in
**default** (or **plan**, if you left it in plan mode).

**Bypass** is for sandboxes and projects you fully trust. It works with every model, including Claude Code
mode. Turning it on (Shift+Tab after auto, or `/mode bypass`) always asks for confirmation first, with **No**
preselected; the status bar then shows a red *bypass permissions on*. It is never saved: closing werkz ends
it. Credential locations stay blocked even in bypass.

## Rules that apply in every mode

- Reading, listing, searching or writing **outside your working folders** asks first (except in bypass). Symlinks are followed,
  so a link inside the project can't point the model elsewhere.
- **Credential locations** (`~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.config/werkz`, `.netrc`, `.git-credentials`, …)
  are never opened by the file tools.
- Your **home folder is never treated as a project**: started in `~` (for example from the desktop icon),
  every file change asks first and searches need a path. Open a project with `/workspace` or `/add-dir`.
- Edits are refused if the model hasn't read the file, or if the file changed on disk since it was read.
- Dialogs ignore keys for a moment after they appear and start on **No**, so text you're typing can't answer
  them by accident.
- "Don't ask again" for a shell command covers only that command (for example `go test …`), and isn't offered
  for compound commands (`;`, `&&`, pipes, redirects) or risky ones (`rm`, `sudo`, `curl`, `ssh`, …).
- Timeouts and **Esc** stop a command together with everything it started, and werkz stops its running
  commands when you quit.
- **Web pages on your local network** (`localhost`, private and cloud-metadata addresses) are fetched only
  after you approve, checked again when connecting, so redirects and DNS tricks can't reach them. Public pages
  and searches don't ask; their content is marked as untrusted for the model.
- **werkz's browser** uses its own profile, never yours. Sites other than this machine ask first; form
  submits, uploads and downloads ask in every mode, including bypass; password and payment fields are never
  typed into. Connecting it to your own Chrome (`/browser connect`) is opt-in per session, shows a red banner,
  and uses only the tab werkz opens. See [The browser](usage.md#the-browser).
- Text from models, tools and files is cleaned of terminal escape sequences before it's displayed.
- Project instruction files (`AGENTS.md`, `CLAUDE.md`) are passed to the model as repository content, not as
  werkz's own instructions, so they can't override these rules.

In **Claude Code mode**, Claude Code runs its own tools. werkz shows them and answers every permission
question Claude Code asks with the rules above, but actions Claude Code's own settings already allow (such
as reading files in the project) don't reach werkz.

## Where your data goes

- **Your code goes only to the provider you're using.** werkz has no server, telemetry or analytics. Prompts
  and the files the model reads are sent to the model provider you selected (or stay on your machine with
  Ollama). The `Task` sub-agent uses the same provider.
- **Saved conversations stay on your machine**, in files only your user can read (see
  [Saved conversations](usage.md#saved-conversations)); `/resume off` stops saving them.
- **@mentions follow the same rules as the Read tool:** credential files are never attached and files
  outside your working folders aren't attached.
- **Keys stay on your machine**, in a config file only your user can read (see [Configuration](configuration.md)).
- **No surprise spending or sharing:** fallback is off unless you turn it on (see [Fallback](fallback.md)),
  Ask mode labels paid models, and model or effort changes that would re-send the conversation uncached warn
  you first.
- **Mind your provider's data policy.** Free tiers (Gemini, OpenRouter `:free`) may use prompts to improve
  their models. Use a paid key or Ollama for sensitive code.
