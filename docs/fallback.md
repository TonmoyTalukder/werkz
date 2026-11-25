# Fallback

When a model fails (out of credits, rate-limited, overloaded, offline or erroring), werkz can try another
model. Fallback is **off by default**. Choose your default with `/fallback`:

| Mode | What happens when a model fails |
|---|---|
| **Off** (default) | The request stops and you decide what to do, for example `/model` |
| **Ask** | werkz shows the failure and asks **which model** to switch to: your fallback models (labelled free or paid), any other connected model, or stop. Use it for this message (**Enter**) or the whole session (**Ctrl+A**) |
| **Auto** | werkz switches to the next model in your list without asking (this may use paid models) |

## Choosing the fallback models

Choosing **Ask** or **Auto** in `/fallback` opens a chooser: press **Space** to add or remove a model; the
numbers show the order they're tried. `/fallback models` edits the list later, and `/fallback status` shows it.

Without a list, werkz uses one model per connected provider, free ones first: Gemini, OpenRouter, Groq,
Ollama, then Claude, OpenAI and Grok (`/fallback gemini,ollama` changes that order).

## Behaviour

- After switching, werkz stays on the new model for the rest of that message instead of retrying the failing
  one at every step. Your next message starts with your chosen model again.
- A provider that's out of credits or has a bad key is skipped for the rest of the session, until `/login`
  confirms a working key.
- With fallback **off**, werkz never sends your next message to another provider instead.
- The conversation carries over between providers, so the new model sees everything so far.
