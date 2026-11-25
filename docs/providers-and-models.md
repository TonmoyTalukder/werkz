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

\* Gemini and Groq are free only while the key's project has no billing, so werkz labels their models
"free tier". OpenRouter's `:free` models are labelled "free", and Ollama "local".

Add or change keys at any time with `/login` (or `werkz --login`). Keys are checked when you save them.

> ChatGPT Plus, Claude Pro/Max and Gemini app subscriptions can't be used by third-party tools. werkz needs
> API keys.

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
