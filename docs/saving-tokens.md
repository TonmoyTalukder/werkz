# Saving tokens and money

Every request to a model re-sends the whole conversation, so werkz is designed to keep requests small and
cached. Its techniques come from Anthropic's prompt-caching guidance and from how Claude Code, Codex CLI,
OpenCode, Gemini CLI, Aider and others manage context.

| Technique | What it does |
|---|---|
| **Prompt caching** (Claude) | The system prompt and tools never change, so every request starts with the same prefix. That prefix and the growing conversation are cached; cache reads cost 0.05× the input price on Opus and Sonnet 5.5 and 0.025× on Fable 5.2. Things that change (folders, date, workspace) are added after the history, so they never break the cache. |
| **Cache keep-alive** | While you're idle, a tiny `max_tokens: 0` ping every ~4½ minutes (billed as a cache read) keeps the cache warm for up to 30 minutes. It stops when you switch model, effort or fast mode. |
| **Idle warning** | If the cache has expired and the conversation is 150k+ tokens, werkz suggests `/compact` before re-sending everything. |
| **Compaction** | `/compact` (or automatically at 300k tokens) replaces the conversation with a structured summary plus the git state. It waits while a test is failing so the error isn't summarized away, and your newest message is always kept word for word. |
| **Quiet test output** | Passing tests and progress lines from `go test`, vitest, jest, npm test, pytest and `cargo test` are hidden; failures are kept. Add `NO_TRIM=1` to a command to see everything. |
| **Big output to a file** | Command output over 30k characters is cut to its start and end, and the full, untrimmed output is saved to a file the model can search (kept 7 days in `~/.cache/werkz/outputs`). |
| **Capped reads and searches** | Read returns at most 2,000 lines and asks for a range on files over 256 KB. Grep returns file names by default. |
| **Cheap sub-agent** | Broad exploration runs in a separate context on your current provider (Claude Haiku 5.5 at low effort when you're on Claude) and returns only a short report. |
| **Project files once** | `AGENTS.md` / `CLAUDE.md` are sent once per session, not with every message. |
| **Loop guard** | Five identical tool calls in a row stop the turn. |

The status bar shows the **cache hit rate**. `/cache` and `/cost` show tokens read from and written to the
cache, money saved, and whether the cache is still warm. `/cache ttl 1h` switches to the 1-hour cache, and
`/cache keepalive off` turns the keep-alive off.

**Measured** on the live Claude API (Haiku 5.5, three messages): after the first message, each request read
**97–99%** of its input from the cache and cost about a tenth of the first one.

## Cost and balance

The status bar shows the session cost:

- **OpenRouter:** the cost OpenRouter reports for each request.
- **Claude:** estimated from token usage and list prices (including cache reads and writes).
- **OpenAI and xAI:** werkz doesn't know their prices, so usage there makes the figure a lower bound, shown
  as `$0.42+?`.

Anthropic doesn't expose your prepaid credit to an API key, so set your starting credit once with
`/balance 100` and werkz subtracts its estimate as you work; the Console's Billing page is the exact figure.
OpenRouter credit is read live from its API.
