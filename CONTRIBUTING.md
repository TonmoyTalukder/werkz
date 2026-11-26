# Contributing to werkz

Thanks for helping. Bug reports, documentation fixes and code are all welcome. werkz is licensed under the
[Apache License 2.0](LICENSE); by contributing, you agree that your contribution is licensed the same way.

## Reporting a bug

[Open an issue](https://github.com/TonmoyTalukder/werkz/issues/new) with:

- what you did, what you expected, and what happened instead;
- `werkz --version`, your operating system, and how you installed werkz;
- the provider and model you were using (never paste API keys or tokens).

**Security problems** go to the maintainers privately instead: use
[Report a vulnerability](https://github.com/TonmoyTalukder/werkz/security/advisories/new) on GitHub.

## Setting up

You need Git and Go. `go.mod` pins the exact toolchain, and the `go` command downloads it for you.

```bash
git clone https://github.com/<you>/werkz.git
cd werkz
go build -o werkz ./cmd/werkz
./werkz
```

Fork the repository on GitHub first and clone your fork, so you can push a branch for your pull request.

## Making a change

1. Create a branch: `git switch -c fix-empty-password`.
2. Make the change, with a test when it changes behaviour.
3. Check it:

   ```bash
   go test -race ./...         # tests use fake model servers: no paid API calls
   golangci-lint run ./...     # lint
   gofmt -l .                  # formatting (no output means formatted)
   ```

4. If you changed what users see, update the matching page in [docs/](docs/) and, for big changes, the
   [README](README.md).

The [Development](docs/development.md) guide explains the project layout, how releases are made, and how to
preview the website.

## Commits and pull requests

- Write commit titles in the [Conventional Commits](https://www.conventionalcommits.org) style:
  `feat: …` for new features, `fix: …` for bug fixes, `docs: …` for documentation, `build: …` for packaging.
  `feat`, `fix` and `build` titles become the release notes, so write them for users.
- Keep one change per pull request, and describe what changed and how you tested it.
- Never commit API keys, tokens, `.env` files or personal settings.
- A maintainer reviews every pull request; small, focused changes are reviewed fastest.

## Documentation and the website

The documentation lives in [docs/](docs/) as Markdown and is also published on the website. The website's
source is in `site/`; `go run ./tools/sitegen -out _site` builds it locally. See
[Development](docs/development.md#website).
