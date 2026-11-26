# Safety and privacy

## Permission modes

Press **Shift+Tab** (or type `/mode`) to cycle through the modes:

| Mode | Reading and searching | Edits | Shell commands |
|---|---|---|---|
| **default** | ✓ | asks | asks |
| **accept edits** | ✓ | ✓ | asks |
| **plan** | ✓ | blocked (read-only) | blocked |
| **auto** | ✓ | ✓ | ✓ |

**Auto** and **accept edits** last only for the current session; werkz always starts in **default**.

## Rules that apply in every mode

- Reading, listing, searching or writing **outside your working folders** asks first. Symlinks are followed,
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
- **Keys stay on your machine**, in a config file only your user can read (see [Configuration](configuration.md)).
- **No surprise spending or sharing:** fallback is off unless you turn it on (see [Fallback](fallback.md)),
  Ask mode labels paid models, and model or effort changes that would re-send the conversation uncached warn
  you first.
- **Mind your provider's data policy.** Free tiers (Gemini, OpenRouter `:free`) may use prompts to improve
  their models. Use a paid key or Ollama for sensitive code.
