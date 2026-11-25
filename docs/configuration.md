# Configuration

Settings live in **`~/.config/werkz/config.json`** (file mode `0600`, readable only by you). Everything in it
can be changed from inside werkz, so you rarely need to edit it by hand.

- Saves are atomic (written to a temporary file, then renamed).
- If the file ever can't be read (say, after a typo while editing it), werkz tells you, runs with defaults
  and doesn't overwrite it until you fix it.
- If two werkz windows are open, the cost they record is added together rather than overwritten.
- A model chosen with `--model` applies to that session only; picking one with `/model` is saved.

## Environment variables

| Variable | Purpose |
|---|---|
| `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `XAI_API_KEY`, `GEMINI_API_KEY`, `OPENROUTER_API_KEY`, `GROQ_API_KEY` | API keys; used when no key is saved for that provider |
| `OLLAMA_HOST` | Ollama address (default `http://localhost:11434`; `host:port` without a scheme also works) |
| `WERKZ_THEME=light` | Use light-background styles |
| `WERKZ_TERMINAL` | Terminal used by the Linux desktop icon (`kitty`, `alacritty`, `gnome-terminal`, …) |
| `NO_TRIM=1` | In a command, disables test-output trimming for that run |
| `WERKZ_NO_UPDATE=1` | Don't check for or install updates this session (see [Updating](installation.md#updating)) |

## Files werkz writes

| Path | What |
|---|---|
| `~/.config/werkz/config.json` | Settings, API keys, workspaces, fallback list |
| `~/.cache/werkz/outputs/` | Oversized command output, kept for 7 days |
| `~/.local/bin/werkz` | The command (installer) |
| `~/.local/share/applications/werkz.desktop`, `~/.local/share/icons/hicolor/…` | Linux app entry and icons |
