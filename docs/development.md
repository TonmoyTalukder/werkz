# Development

```bash
go build -o werkz ./cmd/werkz   # build (version shows as "dev")
go test -race ./...             # tests (fake servers only, no paid API calls)
golangci-lint run ./...         # lint
govulncheck ./...               # known vulnerabilities
./packaging/build.sh            # release assets in dist/ (needs Python 3 with Pillow for the icons)
```

`go.mod` pins the Go toolchain (`toolchain go1.26.11`); the `go` command downloads it automatically.

## Releasing

1. Update `VERSION` (for example `0.2.0`) and commit.
2. Run `./packaging/build.sh`. It writes to `dist/release/`:
   - `werkz-<version>-linux.tar.gz` (x86-64 and ARM64, installer, launcher, icons)
   - `werkz-<version>-macos.zip` (`werkz.app` for Apple Silicon and Intel)
   - bare binaries `werkz-<version>-{linux,darwin}-{amd64,arm64}`
   - `SHA256SUMS`
3. Tag and publish:

   ```bash
   git tag -a v0.2.0 -m "werkz v0.2.0" && git push origin v0.2.0
   gh release create v0.2.0 dist/release/* --title "werkz v0.2.0" --notes-file notes.md
   ```

The version is injected at build time with `-ldflags "-X main.version=…"`; `go install …@vX.Y.Z` builds report
the module version instead.

**Installed copies update themselves from releases**, so every release must include the bare binaries
(`werkz-<version>-<os>-<arch>`) and `SHA256SUMS` exactly as `build.sh` names them, and the tag must be
`v<version>` with a plain `MAJOR.MINOR.PATCH` version. The updater (`internal/update`) follows
`/releases/latest`, so a release marked as a pre-release or draft isn't offered to users.

## Project layout

```text
werkz/
├── cmd/werkz/          entry point: flags and wiring
├── internal/
│   ├── agent/          agent loop, tool permissions, provider fallback, compaction,
│   │                   Task sub-agent, prompt-cache keep-alive
│   ├── catalog/        models, context windows, effort levels, pricing
│   ├── config/         settings and provider registry (~/.config/werkz/config.json)
│   ├── llm/            provider-neutral messages, tool calls, usage, Provider interface
│   │   ├── claude/     Anthropic Messages API backend (streaming, thinking, caching)
│   │   └── openai/     OpenAI-compatible backend (OpenAI, Grok, Gemini, OpenRouter, Groq, Ollama)
│   ├── tools/          Read/Write/Edit/Bash/LS/Glob/Grep, output trimming, read/edit guards
│   ├── tui/            Bubble Tea interface: transcript, input, dialogs, status bar, theme
│   ├── update/         self-update from GitHub releases (checksum-verified, atomic)
│   ├── textutil/       small shared text helpers
│   └── testutil/       fake model servers for tests
├── packaging/          icon generator, Linux launcher/installer, macOS app, build script
├── docs/               documentation
├── install.sh          one-line installer (downloads a GitHub release)
└── VERSION
```

## Architecture notes

- The UI and the agent run in separate goroutines and communicate through messages. The engine owns the
  conversation; the UI passes it a snapshot of the settings for each turn and changes shared state
  (permission mode, working folders) only through the engine's synchronized methods.
- History is stored in a provider-neutral form (`internal/llm`), so a conversation can move between
  providers. Claude turns also keep the exact API message for faithful replay (thinking blocks, caching).
- Tests use fake Anthropic and OpenAI-compatible servers (`internal/testutil`); `agent.WithProviders` lets
  tests in any package inject them.
