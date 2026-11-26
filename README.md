<p align="center">
  <img src="packaging/icons/werkz-256.png" width="120" alt="werkz">
</p>

<h1 align="center">werkz</h1>

<p align="center">
  <a href="https://github.com/TonmoyTalukder/werkz/releases/latest"><img src="https://img.shields.io/github/v/release/TonmoyTalukder/werkz" alt="Latest release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue.svg" alt="License: Apache 2.0"></a>
</p>

<p align="center">
  <a href="https://tonmoytalukder.github.io/werkz/"><b>Website</b></a> ·
  <a href="https://tonmoytalukder.github.io/werkz/docs/">Documentation</a> ·
  <a href="https://tonmoytalukder.github.io/werkz/docs/releases.html">Release notes</a> ·
  <a href="CONTRIBUTING.md">Contributing</a>
</p>

<p align="center">
  <b>An agentic coding tool for your terminal.</b><br>
  Use an API key (Claude, GPT, Grok), your ChatGPT or Claude subscription when you want the best models, or a free tier (Gemini, OpenRouter, Groq), or a model on your own machine (local Ollama).
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
- **Or use your subscription:** sign in with ChatGPT to use your ChatGPT plan, or run your installed Claude
  Code on your Claude subscription, alongside your API keys. `/usage` shows your plan's 5-hour and weekly
  limits. See [Using a subscription](docs/providers-and-models.md#using-a-subscription-instead-of-an-api-key).
- **You stay in control:** edits and commands ask first by default, anything outside your project asks, and
  credential files are never opened. Need speed in a sandbox? Bypass mode asks for nothing, after you confirm
  it, and is never saved.
- **No surprise spending:** fallback to another model is off unless you choose it, and paid models are always
  labelled.
- **Built to save tokens:** prompt caching with keep-alive, automatic compaction, trimmed tool output and a
  context window you can cap yourself (`/context window 350k`); follow-up requests read 97–99% of their input
  from the cache.
- **Several repos at once** with optional workspaces, and a full-screen terminal UI that adapts to any size.
- **Reads the web:** looks up docs and error messages with `WebFetch` and `WebSearch` (your model's own
  search, or Brave, Tavily or SearXNG); pages on your local network ask first.
- **Checks its own web UI:** starts your dev server, opens the page in your installed Chrome (a separate
  profile), clicks through it, reads console and network errors, and compares screenshots before and after,
  showing the page right in your terminal where it can (kitty, Ghostty, iTerm2, WezTerm, Sixel).
- **Made for daily use:** type `@` to attach files (with `@repo/path` across repos), drag to select and copy
  any text, and pick up any earlier conversation with `/resume` or `werkz -c`.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/TonmoyTalukder/werkz/main/install.sh | sh
```

This installs the latest [release](https://github.com/TonmoyTalukder/werkz/releases) for Linux (x86-64, ARM64)
or macOS (Apple Silicon, Intel), including a clickable app icon. werkz then keeps itself up to date
automatically. Every install method is on the [werkz website](https://tonmoytalukder.github.io/werkz/).

With [Homebrew](https://brew.sh) (macOS or Linux):

```bash
brew install --cask ZenixBit/tap/werkz
```

On Debian/Ubuntu and Fedora/RHEL you can also install with `sudo apt install werkz` or `sudo dnf install werkz`
after adding the werkz repository ([two commands](docs/installation.md#apt-debian-ubuntu)), and each release has
`.apk` and Arch Linux packages too. Homebrew and package installs update through their package manager, and
werkz shows the exact command when a new version is out. Prefer to download the files yourself, or use
`go install`? See [Installation](docs/installation.md).

## Quick start

```bash
cd ~/code/my-app
werkz
```

On first run, paste any API keys you have, or press **Esc** to start with a local
[Ollama](https://ollama.com/download) model. To use a subscription instead, type `/login chatgpt` for your
ChatGPT plan, or pick a "(Claude Code)" model in `/model` for your Claude subscription. Then describe what you
want done. werkz shows each command and edit before running it; choose with
**↑/↓** and **Enter**. Type `/help` for all commands, or see [Getting started](docs/getting-started.md).

## Documentation

| | |
|---|---|
| [Installation](docs/installation.md) | Installer, Homebrew, apt, dnf, downloads, updating, uninstalling |
| [Getting started](docs/getting-started.md) | First run and your first task |
| [Usage](docs/usage.md) | Commands, keyboard shortcuts, command-line flags, tools |
| [Providers and models](docs/providers-and-models.md) | API keys, ChatGPT and Claude subscriptions, free and local models, prices, effort |
| [Safety and privacy](docs/safety.md) | Permission modes (including Bypass) and the rules that always apply |
| [Fallback](docs/fallback.md) | What happens when a model fails |
| [Workspaces](docs/workspaces.md) | Working across several repositories |
| [Saving tokens and money](docs/saving-tokens.md) | Caching, compaction, context window, cost and usage limits |
| [Configuration](docs/configuration.md) | Config file, environment variables |
| [Troubleshooting](docs/troubleshooting.md) | Common problems and fixes |
| [Development](docs/development.md) | Building, testing, releasing, project layout |
| [Contributing](CONTRIBUTING.md) | Report a bug, set up, and send a change |

## License

werkz is licensed under the [Apache License, Version 2.0](LICENSE); see [NOTICE](NOTICE). Model providers'
own terms apply to your use of their APIs.
