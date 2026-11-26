# Product

<!-- impeccable:product-schema 1 -->

## Platform

web (the website at https://tonmoytalukder.github.io/werkz/). The product itself is a terminal app.

## Stack

Website: plain static HTML/CSS with minimal JavaScript, no build tools, served by GitHub Pages from the
`gh-pages` branch. Documentation pages are generated from the repository's `docs/*.md` by a small script so
they stay in sync with each release. The release workflow also writes the `apt/` and `rpm/` package
repositories to the same branch; the site must not disturb them.

## Users

Two audiences of equal weight:

- **Developers discovering werkz**, deciding whether to try it: they need to grasp what it is, what makes it
  different, and get it installed in under a minute.
- **Existing werkz users**, coming back for reference: install and update methods, commands, providers,
  safety rules, configuration, troubleshooting.

## Product Purpose

werkz is an open-source agentic coding tool for the terminal, written in Go. It reads a codebase, edits files,
runs commands and checks its work, asking before anything risky. It works with the model access the user
already has: a paid API key (Claude, OpenAI, Grok), a subscription (ChatGPT plan through Sign in with ChatGPT,
Claude subscription through the user's installed Claude Code), free models (Gemini and Groq free tiers,
OpenRouter `:free` models) or fully offline with Ollama.

## Positioning

One terminal agent across paid APIs, subscriptions, free tiers and local models, with no surprise spending:
fallback to another model is off unless chosen, paid models are labelled, and prompt caching, compaction and
trimmed tool output keep token use low. Free, open source (Apache 2.0), no server or telemetry.

## Operating Context

Used inside a terminal on Linux and macOS, in a project folder or a workspace of several repositories.
Installed with a one-line installer, Homebrew, apt or dnf repositories, package files (.deb, .rpm, .apk, Arch)
or `go install`. Installer copies update themselves; package-manager copies are told the exact update command.

## Capabilities and Constraints

- Providers: Claude (Fable 5.2, Opus 5.5, Sonnet 5.5, Haiku 5.5), OpenAI, xAI Grok, Gemini, OpenRouter (free
  and paid), Groq, Ollama, ChatGPT plan, Claude Code.
- Tools: Read, Write, Edit, Bash, LS, Glob, Grep, Task (sub-agent).
- Permission modes: default, accept edits, plan (read-only), auto. Writes outside the working folders always
  ask; credential locations are never opened.
- Token saving: prompt caching with keep-alive (follow-up requests read 97–99% of input from cache),
  automatic compaction, trimmed tool output, an adjustable context window (`/context window 350k`).
- Workspaces: optional named sets of folders with their own environment variables.
- Platforms: Linux (x86-64, ARM64) and macOS (Apple Silicon, Intel). The macOS app is untested on a real Mac.

## Brand Commitments

- Name is always lowercase: **werkz**.
- Brand gradient saffron → red → blue (the terminal UI and icons use it); existing icons in
  `packaging/icons/` (werkz-16 … werkz-1024.png, werkz.icns).
- Voice: plain, direct, factual; no hype words.
- License: Apache 2.0, "Tonmoy Talukder and contributors".

## Evidence on Hand

Real: the repository, releases, documentation in `docs/`, the icon set, an example session transcript in the
README. Absent and must not be fabricated: user counts, testimonials, benchmarks, company logos, ratings.

## Product Principles

1. The user stays in control: nothing risky without asking, nothing paid without saying so.
2. Use what you already have: any key, subscription, free tier or local model.
3. Spend fewer tokens by design, not by asking the user to micromanage.
4. Plain facts over claims: show real commands and real behaviour.
