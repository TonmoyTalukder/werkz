# Providers and models

| Provider | Cost | Get a key | Environment variable |
|---|---|---|---|
| **Claude** (Anthropic) | paid | [platform.claude.com](https://platform.claude.com/settings/keys) | `ANTHROPIC_API_KEY` |
| **OpenAI** (GPT / Codex models) | paid | [platform.openai.com](https://platform.openai.com/api-keys) | `OPENAI_API_KEY` |
| **xAI Grok** | paid | [console.x.ai](https://console.x.ai) | `XAI_API_KEY` |
| **Google Gemini** | free tier* | [aistudio.google.com](https://aistudio.google.com/apikey) | `GEMINI_API_KEY` |
| **OpenRouter** | `:free` models; paid models optional | [openrouter.ai/keys](https://openrouter.ai/keys) | `OPENROUTER_API_KEY` |
| **Groq** | free tier* | [console.groq.com](https://console.groq.com/keys) | `GROQ_API_KEY` |
| **Ollama** | free, local, offline | [ollama.com](https://ollama.com/download) (no key) | `OLLAMA_HOST` |
| **ChatGPT plan** | your ChatGPT subscription | `/login chatgpt` (browser sign-in, no key) | |
| **Claude Code** | your Claude subscription | install [Claude Code](https://code.claude.com) and sign in to it | |

\* Gemini and Groq are free only while the key's project has no billing, so werkz labels their models
"free tier". OpenRouter's `:free` models are labelled "free", and Ollama "local".

Add or change keys at any time with `/login` (or `werkz --login`). Keys are checked when you save them.

## Using a subscription instead of an API key

You can use the API and your subscription side by side: pick either from `/model`.

### ChatGPT plan (Sign in with ChatGPT)

```text
/login chatgpt
```

werkz opens your browser to sign in to ChatGPT ("Continue with ChatGPT"). After you allow werkz to use your
plan, models marked **plan** appear in `/model`, and requests to them count toward your ChatGPT plan's usage
limits instead of being billed to an API key. On Plus that is a five-hour limit shared with your other apps;
see or cap werkz's share at [chatgpt.com/settings/usage](https://chatgpt.com/settings/usage). The status bar
says **Using ChatGPT plan** while one of these models is selected.

- This uses OpenAI's official [Sign in with ChatGPT](https://developers.openai.com/siwc/token-sharing-open-source)
  for open-source apps. werkz never reads the Codex CLI's login.
- werkz's tools, permission modes and safety rules work exactly as with an API key.
- The sign-in is stored in `~/.config/werkz/chatgpt.json` (only your user can read it) and refreshed
  automatically. `/logout chatgpt` revokes it.
- When the plan's limit is reached, werkz says so and links to the usage page; with [fallback](fallback.md)
  on, another model can take over.

### Claude Code (your Claude subscription)

Anthropic doesn't allow other apps to sign in with a Claude account, so werkz doesn't. Instead it can run
**your own installed Claude Code** in its headless mode (`claude -p`): Claude Code uses its own sign-in and
your subscription, and werkz shows its work.

1. Install [Claude Code](https://code.claude.com) and run `claude` once to sign in.
2. In werkz, pick a model marked **(Claude Code)** in `/model` (`/login claudecode` checks the setup).

How it differs from the other providers:

- **Claude Code does the work with its own tools**, and werkz shows each step. When Claude Code needs
  permission, werkz asks you with its usual dialog and rules (plan mode, folders, credential locations).
  Actions Claude Code's own settings already allow don't ask.
- Claude Code keeps the conversation and compacts it itself; werkz's `/compact` and caching settings don't
  apply. Switching to Claude Code mid-conversation passes the conversation so far along.
- werkz removes `ANTHROPIC_API_KEY` from Claude Code's environment, so it uses your subscription rather than
  billing that key.
- This mode relies on Claude Code's documented headless mode; Anthropic's terms don't say whether running it
  from another app is allowed, so check them if that matters to you.

## Claude models

| Model | Best for | Price per 1M tokens (in / out) |
|---|---|---|
| Fable 5.2 | the longest, hardest tasks | $8 / $40 |
| Opus 5.5 | complex projects, agents, coding (default) | $4 / $20 |
| Sonnet 5.5 | everyday tasks, cost-efficient | $2 / $10 |
| Haiku 5.5 | fastest, lowest cost | $0.10 / $0.50 ($0.50 / $2.50 for prompts over 100K tokens) |

For the other providers, `/model` lists the models your key can use.

## OpenRouter: free and paid models

OpenRouter gives one key access to hundreds of models. By default werkz shows only its free (`:free`)
models. Run `/openrouter paid` to also list the paid ones (Claude, GPT, Gemini, Llama and many more) with their
real per-token prices; they're billed to your OpenRouter credit, which the status bar shows.
`/openrouter free` hides them again. Paid models are labelled as paid everywhere werkz offers them, including
the fallback dialogs.

Free OpenRouter models are withdrawn from time to time. werkz checks the live list at start-up and, if your
free model is no longer offered, switches to an available free one and tells you.

## Effort and fast mode

- **`/model`** switches between every connected provider's models, showing context size and price.
- **`/effort`** sets how much the model thinks before it answers:
  - On Claude the levels are `low`, `medium`, `high`, `xhigh` and `max`. Higher is smarter, slower and
    costs more; `xhigh` suits hard coding work. The defaults are `medium` on Opus 5.5 and Haiku 5.5, and
    `high` on Sonnet 5.5 and Fable 5.2.
  - On other providers the levels are `auto`, `low`, `medium` and `high`.
- **Fast mode** (Opus 5.5 and Fable 5.2) streams output up to about 2.5× faster, at double the price.

Changing the model, effort or fast mode in the middle of a conversation invalidates the prompt cache, so
werkz warns you when you make the change, with what re-sending the conversation will cost. See
[Saving tokens and money](saving-tokens.md).
