<p align="center">
  <img src="packaging/icons/werkz-256.png" width="120" alt="werkz">
</p>

<h1 align="center">werkz</h1>

<p align="center">
  <a href="https://github.com/TonmoyTalukder/werkz/releases/latest"><img src="https://img.shields.io/github/v/release/TonmoyTalukder/werkz" alt="Latest release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue.svg" alt="License: Apache 2.0"></a>
</p>

<p align="center">
  <b>An agentic coding tool for your terminal.</b><br>
  Use a paid API key (Claude, GPT, Grok) when you want the best models, or free models
  (Gemini, OpenRouter, Groq, local Ollama) when you don't.
</p>

---

werkz reads your codebase, edits files, runs commands and works with the tools you already use: your shell,
git, test runners, linters and build tools. Describe a task in plain language and werkz plans it, makes the
changes and checks its work, asking before it does anything risky.

```text
❯ the login form accepts empty passwords, fix it and add a test

⏺ Grep(password in src)
  ⎿  3 files
⏺ Edit(src/auth/login.ts)
  ⎿  +4 −1 lines
⏺ Bash(npm test -- login)
  ⎿  exit 0
⏺ Empty and whitespace-only passwords are now rejected before the API call, with a test covering both.
```

## Highlights

- **Bring your own model:** Claude (Fable 5.2, Opus 5.5, Sonnet 5.5, Haiku 5.5), OpenAI, Grok, Gemini,
  OpenRouter (free and paid models), Groq, or fully offline with Ollama.
- **You stay in control:** edits and commands ask first by default, anything outside your project always asks,
  and credential files are never opened.
- **No surprise spending:** fallback to another model is off unless you choose it, and paid models are always
  labelled.
- **Built to save tokens:** prompt caching with keep-alive, automatic compaction and trimmed tool output;
  follow-up requests read 97–99% of their input from the cache.
- **Several repos at once** with optional workspaces, and a full-screen terminal UI that adapts to any size.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/TonmoyTalukder/werkz/main/install.sh | sh
```

This installs the latest [release](https://github.com/TonmoyTalukder/werkz/releases) for Linux (x86-64, ARM64)
or macOS (Apple Silicon, Intel), including a clickable app icon. Prefer to download the files yourself, or use
`go install`? See [Installation](docs/installation.md).

## Quick start

```bash
cd ~/code/my-app
werkz
```

On first run, paste any API keys you have (or press **Esc** to use local [Ollama](https://ollama.com/download)),
then describe what you want done. werkz shows each command and edit before running it; choose with
**↑/↓** and **Enter**. Type `/help` for all commands, or see [Getting started](docs/getting-started.md).

## Documentation

| | |
|---|---|
| [Installation](docs/installation.md) | Installer, downloads, `go install`, updating, uninstalling |
| [Getting started](docs/getting-started.md) | First run and your first task |
| [Usage](docs/usage.md) | Commands, keyboard shortcuts, command-line flags, tools |
| [Providers and models](docs/providers-and-models.md) | Where to get keys, model prices, effort and fast mode |
| [Safety and privacy](docs/safety.md) | Permission modes and the rules that always apply |
| [Fallback](docs/fallback.md) | What happens when a model fails |
| [Workspaces](docs/workspaces.md) | Working across several repositories |
| [Saving tokens and money](docs/saving-tokens.md) | Caching, compaction, cost and balance tracking |
| [Configuration](docs/configuration.md) | Config file, environment variables |
| [Troubleshooting](docs/troubleshooting.md) | Common problems and fixes |
| [Development](docs/development.md) | Building, testing, releasing, project layout |

## License

werkz is licensed under the [Apache License, Version 2.0](LICENSE); see [NOTICE](NOTICE). Model providers'
own terms apply to your use of their APIs.
