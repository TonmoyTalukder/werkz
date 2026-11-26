# Development

```bash
go build -o werkz ./cmd/werkz   # build (version shows as "dev")
go test -race ./...             # tests (fake servers only, no paid API calls)
golangci-lint run ./...         # lint
govulncheck ./...               # known vulnerabilities
./packaging/build.sh            # installer tarball, werkz.app and bare binaries in dist/release/
goreleaser check                # validate .goreleaser.yaml
goreleaser release --snapshot --clean   # build every release asset into dist/ without publishing
```

The icons in `packaging/icons/` are committed; regenerate them with
`python3 packaging/make_icon.py packaging/icons` (needs Pillow).

`go.mod` pins the Go toolchain (`toolchain go1.26.11`); the `go` command downloads it automatically.

## Releasing

Releases are built by [GoReleaser](https://goreleaser.com) (`.goreleaser.yaml`) in GitHub Actions
(`.github/workflows/release.yml`):

1. Update `VERSION` (for example `0.2.0`), commit and push to `main`.
2. Tag and push the tag:

   ```bash
   git tag -a v0.2.0 -m "werkz v0.2.0" && git push origin v0.2.0
   ```

The workflow checks that `VERSION` matches the tag, runs the tests, and publishes the release with a changelog
grouped by commit type (`feat:`, `fix:`, `docs:`). Each release contains:

- bare binaries `werkz-<version>-{linux,darwin}-{amd64,arm64}` (used by the self-updater)
- `werkz-<version>-linux.tar.gz` (x86-64 and ARM64, installer, launcher, icons) and
  `werkz-<version>-macos.zip` (`werkz.app`), both built by `packaging/build.sh` as a GoReleaser hook
- per-platform `werkz_<version>_<os>_<arch>.tar.gz` archives (used by Homebrew)
- `.deb`, `.rpm`, `.apk` and Arch Linux packages
- `SHA256SUMS` covering all of the above

It then rebuilds the signed apt and dnf repositories on the `gh-pages` branch (served by GitHub Pages at
<https://tonmoytalukder.github.io/werkz/>) with `packaging/repos/build-repos.sh`, keeping only the latest
release; pre-releases are skipped. The RPMs and the repository metadata are signed with the werkz GPG key, which
the workflow reads from the repository secret `SIGNING_KEY` (the armored private key).

It also updates the Homebrew cask in [ZenixBit/homebrew-tap](https://github.com/ZenixBit/homebrew-tap).
That needs the repository secret `HOMEBREW_TAP_TOKEN`: a fine-grained GitHub token with **Contents: read and
write** access to the `homebrew-tap` repository only.

**macOS `werkz.app`:** GoReleaser has no format for an app bundle with a Terminal launcher, so `build.sh` still
builds it and GoReleaser attaches the zip. Homebrew users get the plain `werkz` command.

Without GitHub Actions, the same release can be published from a machine with
`GITHUB_TOKEN=… HOMEBREW_TAP_TOKEN=… goreleaser release --clean`.

The version is injected at build time with `-ldflags "-X main.version=…"`; `go install …@vX.Y.Z` builds report
the module version instead.

**Installed copies update themselves from releases**, so every release must include the bare binaries
(`werkz-<version>-<os>-<arch>`) and `SHA256SUMS` named exactly as they are now, and the tag must be
`v<version>` with a plain `MAJOR.MINOR.PATCH` version. The updater (`internal/update`) follows
`/releases/latest`, so a release marked as a pre-release or draft isn't offered to users. Copies installed
by Homebrew or a Linux package are never replaced (see `Updater.Managed`); they get a "update with your
package manager" notice instead.

## Website

The site at <https://tonmoytalukder.github.io/werkz/> is plain static HTML and CSS in `site/` (landing page,
styles, a small script, self-hosted fonts). `tools/sitegen` turns it into the published site: it renders
`docs/*.md` into the documentation pages and fills in the documentation list and the release notes, which come
from the version tags and their `feat:`, `fix:` and `build:` commits.

```bash
go run ./tools/sitegen -out _site                     # build into _site/
python3 -m http.server 8642 --directory _site          # preview at http://localhost:8642
```

`.github/workflows/pages.yml` publishes it to the `gh-pages` branch whenever `site/`, `docs/` or the generator
change on `main`, and after every release (for the new release notes). It never touches the `apt/` and `rpm/`
repositories or the signing key on that branch.

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
│   ├── tools/          Read/Write/Edit/Bash/LS/Glob/Grep, DevServer, output trimming, read/edit guards
│   ├── web/            WebFetch (HTML to Markdown, local-network guard) and WebSearch backends
│   ├── browser/        werkz's browser over Chrome DevTools: launch and connected mode, actions,
│   │                   form-submit guard, tabs, screenshots
│   ├── termimg/        pictures in the terminal: Kitty placeholders, iTerm2, Sixel, links
│   ├── mcp/            MCP client: config files, stdio/HTTP/SSE servers, tools
│   ├── mcpauth/        OAuth sign-in for remote MCP servers, token store
│   ├── mention/        @ mentions: parsing, resolving, the file index and search
│   ├── session/        saved conversations (/resume)
│   ├── clipboard/      copying: clipboard tools, werkz's own X11 helper, OSC 52
│   ├── tui/            Bubble Tea interface: transcript, input, dialogs, status bar, theme
│   ├── update/         self-update from GitHub releases (checksum-verified, atomic)
│   ├── textutil/       small shared text helpers
│   └── testutil/       fake model servers for tests
├── packaging/          icon generator, Linux launcher/installer, macOS app, build script
├── docs/               documentation (also rendered on the website)
├── site/               the website (static HTML/CSS)
├── tools/sitegen/      builds the website: docs pages and release notes
├── .goreleaser.yaml    release build (binaries, packages, Homebrew cask)
├── .github/workflows/  release workflow (runs GoReleaser on v* tags)
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
