# golangci-lint-action-go

A Go reimplementation of the [golangci-lint GitHub Action](https://github.com/golangci/golangci-lint-action).
Drop-in replacement that ships as a single self-contained binary — no Node.js runtime required.

## Quick start

```yaml
# .github/workflows/lint.yml
name: Lint

on:
  push:
    branches: [main]
  pull_request:

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - uses: StevenACoffman/golangci-lint-action-go@v1
```

## Why a Go reimplementation?

The upstream action is a Node.js composite action. This version replaces the JS runtime with a
compiled Go binary:

- **No Node.js** on the runner — one fewer tool in the dependency chain
- **Faster startup** — no module resolution or JIT warmup
- **Auditable** — the same `go build` / `go test` workflow as the rest of your project
- **Behaviorally identical** — same inputs, same cache key format, same exit codes

## Common patterns

### Pin to a specific version

```yaml
- uses: StevenACoffman/golangci-lint-action-go@v1
  with:
    version: v2.3.4
    args: --timeout=5m
```

Version can be `v2.3` (maps to the latest v2.3.x patch), `v2.3.4` (exact), `latest`, or empty
(auto-detected from `go.mod` or a version file — see [Version resolution](#version-resolution)).

### Lint only new issues on pull requests

```yaml
- uses: StevenACoffman/golangci-lint-action-go@v1
  with:
    only-new-issues: true
```

On `pull_request` and `push` events the action fetches the diff from the GitHub API and passes
`--new-from-patch` to golangci-lint so only issues touching changed lines are reported. On
`merge_group` events it uses `--new-from-rev` instead (no network call needed).

### Add inline PR annotations

```yaml
- uses: StevenACoffman/golangci-lint-action-go@v1
  with:
    problem-matchers: true
```

Registers a problem matcher that parses golangci-lint output and creates inline code annotations
directly on the pull request diff.

### Monorepo — lint every Go module independently

```yaml
- uses: StevenACoffman/golangci-lint-action-go@v1
  with:
    experimental: automatic-module-directories
```

Globs for `**/go.mod` (excluding `vendor/`, `node_modules/`, `.git/`, `dist/`) and runs
`golangci-lint` once per discovered module directory.

### Use golangci-lint already installed in the runner image

```yaml
- uses: StevenACoffman/golangci-lint-action-go@v1
  with:
    install-mode: none
```

Skips download and version resolution; uses whatever `golangci-lint` is already in `PATH`.

### Install only, then run with custom flags

```yaml
- uses: StevenACoffman/golangci-lint-action-go@v1
  with:
    install-only: true

- name: Lint
  run: golangci-lint run --fix --out-format=github-actions
```

### Build from source with `go install`

```yaml
- uses: StevenACoffman/golangci-lint-action-go@v1
  with:
    install-mode: goinstall
    version: latest
```

Requires CGO (`CGO_ENABLED=1`) on the runner; useful when you need a custom build tag or are
targeting a platform without a pre-built release binary.

## Inputs

| Input | Default | Description |
|-------|---------|-------------|
| `version` | `""` | golangci-lint version: `v2.3`, `v2.3.4`, `latest`, or empty for auto-detect |
| `version-file` | `""` | Path to `.golangci-lint-version` or `.tool-versions`; ignored when `version` is set |
| `install-mode` | `binary` | `binary` — download release archive; `goinstall` — build with `go install`; `none` — expect it in PATH |
| `install-only` | `false` | Install the binary, then exit without running the linter |
| `working-directory` | `""` | Directory to run the linter in; empty means the process working directory |
| `github-token` | `${{ github.token }}` | Token used to fetch PR/push diffs when `only-new-issues: true` |
| `verify` | `true` | Run `golangci-lint config verify` before linting |
| `only-new-issues` | `false` | Restrict lint output to issues introduced in the current diff |
| `args` | `""` | Extra flags passed verbatim to `golangci-lint run` |
| `skip-cache` | `false` | Disable both cache restore and save |
| `skip-save-cache` | `false` | Disable cache save only; restore still runs |
| `cache-invalidation-interval` | `7` | Rotate the cache key every N days; `0` or negative gives a unique key per run |
| `problem-matchers` | `false` | Register the built-in problem matcher for inline PR annotations |
| `debug` | `""` | Comma-separated: `cache` (print cache status), `clean` (purge cache before linting) |
| `experimental` | `""` | Comma-separated: `automatic-module-directories` (lint each `go.mod` in the repo) |

## Version resolution

The version to install is determined by the first non-empty source in this order:

1. **`version` input** — used verbatim; minor-only versions (e.g. `v2.3`) are mapped to the
   latest patch via the upstream [version mapping JSON](https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/assets/github-action-config-v2.json).
2. **`go.mod`** — scans for a `require github.com/golangci/golangci-lint/v2 vX.Y.Z` directive.
3. **`version-file`** — reads `.golangci-lint-version` (raw version string) or `.tool-versions`
   (looks for a `golangci-lint X.Y.Z` line).
4. **Latest** — fetches the current latest from the upstream version mapping.

Minimum supported version is **v2.1**. Only the v2 major line is supported.

## Caching

The action caches `~/.cache/golangci-lint` (Windows: `%USERPROFILE%\.cache\golangci-lint`)
between runs using the GitHub Actions cache API.

**Cache key format:**

```
golangci-lint.cache-{RUNNER_OS}-{working-directory}-{interval-bucket}-{go.mod-sha1}
```

The *interval bucket* advances every `cache-invalidation-interval` days (default: 7). This
means the cache is shared across all runs within the same week while still rotating regularly
to pick up updated lint rules. A second restore key — the same string without the SHA-1 suffix
— allows partial hits when only `go.mod` has changed.

Set `skip-cache: true` to disable caching entirely, or `skip-save-cache: true` to restore but
never write back (useful when you want to build the cache only from the default branch).

## Migrating from `golangci-lint-action`

All input names are identical. Replace:

```yaml
- uses: golangci/golangci-lint-action@v6
```

with:

```yaml
- uses: StevenACoffman/golangci-lint-action-go@v1
```

No other changes needed. If you previously passed `cache: false` (a flag unique to the upstream
action), use `skip-cache: true` here.

## Development

```bash
# Build
go build -o golangci-lint-action-go ./...

# Test
go test ./...

# Lint
golangci-lint run ./...
```

See `go.mod` for the minimum required Go version.

### Running the subcommands locally

```bash
# Phase 1 — restore cache, install, lint
INPUT_VERSION=v2.3.4 \
INPUT_ARGS="--timeout=5m" \
GITHUB_STATE=/tmp/gh-state \
GITHUB_PATH=/tmp/gh-path \
golangci-lint-action-go run

# Phase 2 — save cache (reads state written by phase 1)
STATE_CACHE_KEY=... \
GITHUB_STATE=/tmp/gh-state \
golangci-lint-action-go post-run

# Other
golangci-lint-action-go version
golangci-lint-action-go --help
```

### Architecture

All business logic lives in `internal/` as pure, testable functions. The `cmd/` layer is a thin
shell that wires them together in the order the spec requires — no logic of its own.

```
cmd/
  run/
    run.go       – subcommand registration and exec entry point
    cache.go     – cache restore (keys, GOLANGCI_LINT_CACHE, state)
    install.go   – binary install: binary / goinstall / none; plugin builder
    lint.go      – arg assembly, config verify, run, exit-code mapping
  postrun/
    postrun.go   – cache save (reads state written by run phase)

internal/
  gha/           – GitHub Actions protocol: workflow commands, env files, state
  lintver/       – version string parsing, go.mod scan, version-file, mapping fetch
  install/       – platform strings, asset URLs, archive helpers, ParseGoInstallBinPath
  actionscache/  – GitHub Actions Cache REST API client
  cache/         – cache key construction; RestoreCache / SaveCache orchestration
  patch/         – PR/push diff fetch; path-prefix rewriting for working-directory
  plugins/       – .custom-gcl.{yml,yaml,json} detection and custom-binary build
  lint/          – ParseUserArgs, OnlyNewIssuesArgs, PathModeArg, InterpretExitCode
```

## License

See [LICENSE](LICENSE).
