# Implementation Plan: golangci-lint-action-go

Translate the TypeScript golangci-lint-action into Go, faithfully implementing the behavior
in `spec.md` while following all coding rules in `RULES.md` and satisfying the linter
configuration in `.golangci.yaml`.

---

## 1. Design Principles

The following RULES.md rules drive every structural decision:

- **Functional core / imperative shell**: all logic is in pure functions inside `internal/`;
  `cmd/run` and `cmd/postrun` are thin shells that call those functions in sequence.
- **No global state**: `gochecknoglobals` and `gochecknoinits` are enforced. Every dependency
  is passed as a parameter, never pulled from a package-level variable.
- **`getenv` injection**: the action reads all 14 inputs via `os.Getenv`. To make the two
  subcommands testable without `t.Setenv`, `root.Config` gains a `Getenv func(string) string`
  field. `main.go` passes `os.Getenv`; tests pass a custom function.
- **Manual DI**: no framework; `cmd/run/run.go` and `cmd/postrun/postrun.go` wire all
  internal packages by passing values through function calls.
- **No third-party test frameworks**: stdlib `testing` only; three assertion helpers per
  package; table-driven tests in `map[string]struct{...}` form.
- **ff/v4 for flags**: each subcommand uses `ff.NewFlagSet`; `ff.WithEnvVarPrefix` is used
  in `cmd/cmd.go`. Action inputs come from `gha.Input(getenv, name)`, not CLI flags.
- **Error wrapping**: every package uses `const op = "pkg.Func"` and wraps with
  `fmt.Errorf("%s: %w", op, err)`.
- **`os.Exit` only in `main.go`**: subcommands return `error` or `root.ExitError`.

---

## 2. Package Layout

```
golangci-lint-action-go/
├── main.go                      # exists — add Getenv to run() call
├── action.yml                   # NEW: GitHub Actions manifest
├── problem-matchers.json        # NEW: copy from reference TS action
├── go.mod                       # modify: add gopkg.in/yaml.v3
├── go.sum                       # generated
│
├── cmd/
│   ├── cmd.go                   # modify: register run + postrun; pass Getenv
│   ├── root/root.go             # modify: add Getenv field
│   ├── version/version.go       # unchanged
│   ├── run/run.go               # NEW: Phase 1 subcommand
│   └── postrun/postrun.go       # NEW: Phase 2 subcommand
│
└── internal/
    ├── gha/                     # GitHub Actions protocol
    │   ├── gha.go
    │   └── gha_test.go
    ├── lintver/                 # golangci-lint version parsing + resolution
    │   ├── lintver.go
    │   └── lintver_test.go
    ├── install/                 # binary installation (binary / goinstall / none)
    │   ├── install.go
    │   └── install_test.go
    ├── actionscache/            # GitHub Actions Cache REST API client
    │   ├── actionscache.go
    │   └── actionscache_test.go
    ├── cache/                   # cache key construction + orchestration
    │   ├── cache.go
    │   └── cache_test.go
    ├── patch/                   # PR/push diff fetching + transformation
    │   ├── patch.go
    │   └── patch_test.go
    ├── plugins/                 # .custom-gcl.yml plugin builder
    │   ├── plugins.go
    │   └── plugins_test.go
    └── lint/                    # lint arg processing + execution
        ├── lint.go
        └── lint_test.go
```

---

## 3. New Dependencies

Add to `go.mod`:

```
gopkg.in/yaml.v3   — parse .custom-gcl.{yml,yaml,json} plugin config (plugins.go)
```

All other requirements use the Go standard library:
- `net/http` — HTTP client for version mapping fetch, PR/push diff, cache API
- `archive/tar`, `archive/zip`, `compress/gzip` — binary archive extraction
- `crypto/sha1` — go.mod checksum for cache key
- `os/exec` — `go install` and `golangci-lint run`
- `regexp` — version string parsing, diff rewriting
- `encoding/json` — version mapping JSON, cache API JSON

No GitHub client library is needed: the two API calls (PR diff, push diff) are simple GET
requests with a custom `Accept` header; implement them directly with `net/http`.

---

## 4. Implementation Phases

Phases are ordered by dependency: each phase may import all earlier phases.
Implement each phase completely (production code + tests) before starting the next.

---

### Phase 1 — `internal/gha`

**Purpose:** Thin adapter over the GitHub Actions protocol. All interaction with the runner
environment goes through this package, making every caller testable with an injected
`getenv func(string) string` and an injected `io.Writer`.

**Key types and functions:**

```go
// Input reads INPUT_<NAME> (spaces→underscores, uppercased, dashes kept).
// Spec §2; mirrors core.getInput().
func Input(getenv func(string) string, name string) string

// BoolInput parses Input(getenv, name) as "true"/"false" (case-insensitive).
func BoolInput(getenv func(string) string, name string) (bool, error)

// Info writes msg to out followed by a newline (mirrors core.info → plain stdout).
func Info(out io.Writer, msg string)

// Warning writes "::warning::<esc(msg)>\n" to out.
func Warning(out io.Writer, msg string)

// Error writes "::error::<esc(msg)>\n" to out.
func Error(out io.Writer, msg string)

// LogWarning writes "[warning]<msg>\n" to out via Info.
// No space between "]" and msg — spec §13 logWarning format.
func LogWarning(out io.Writer, msg string)

// SaveState appends "KEY=VALUE\n" to the file at $GITHUB_STATE.
func SaveState(getenv func(string) string, key, value string) error

// GetState reads the STATE_<KEY> env var (mirrors core.getState).
func GetState(getenv func(string) string, key string) string

// AddPath appends path+"\n" to the file at $GITHUB_PATH.
func AddPath(getenv func(string) string, path string) error

// Group writes "::group::<name>\n", calls fn, writes "::endgroup::\n".
// Returns fn's error. If fn panics, endgroup is still written.
func Group(out io.Writer, name string, fn func() error) error

// RegisterProblemMatcher emits "##[add-matcher]{path}\n" via Info.
// path is the absolute path to the problem-matchers.json file.
// Spec §4.1.
func RegisterProblemMatcher(out io.Writer, path string)

// EventName returns GITHUB_EVENT_NAME from getenv.
func EventName(getenv func(string) string) string

// IsValidEvent returns true when GITHUB_REF is present and non-empty (spec §5.3).
func IsValidEvent(getenv func(string) string) bool

// Workspace returns GITHUB_WORKSPACE from getenv (empty string if absent).
func Workspace(getenv func(string) string) string

// RunnerOS returns RUNNER_OS from getenv.
func RunnerOS(getenv func(string) string) string
```

**Implementation notes:**
- All functions accept `getenv` and `io.Writer`; none touch `os.Getenv` or `os.Stdout` directly.
- `Input`: `strings.ToUpper(strings.ReplaceAll(name, " ", "_"))` then prefix `INPUT_`, dashes are kept.
- `Warning`/`Error`: workflow command escaping replaces `,`, `:`, `\r`, `\n`, `%` with `%XX`.
- `SaveState`/`AddPath`: open the file at `getenv("GITHUB_STATE")` / `getenv("GITHUB_PATH")`
  in append mode; write `KEY=VALUE\n` or `path\n`.
- `Group`: deferred `endgroup` ensures it runs even if fn returns an error.

**Tests:** Table-driven tests for `Input` (dashes kept, spaces→underscores), `BoolInput`
(true/false/TRUE/FALSE/invalid), `LogWarning` (exact `[warning]` prefix, no space),
`IsValidEvent` (present/empty/absent key).

---

### Phase 2 — `internal/lintver`

**Purpose:** Pure functions for parsing, validating, and resolving golangci-lint version
strings. No I/O; all network is abstracted behind an injected fetcher. Spec §3.

**Key types:**

```go
// Version holds the parsed components of a golangci-lint version string.
// Patch is nil when only major.minor was specified.
type Version struct {
    Major int
    Minor int
    Patch *int // nil if absent
}

// VersionInfo is the resolved version after mapping lookup.
type VersionInfo struct {
    TargetVersion string
    Error         string // non-empty means this version slot has an error
}

// InstallMode represents the requested installation strategy.
type InstallMode string

const (
    ModeBinary    InstallMode = "binary"
    ModeGoInstall InstallMode = "goinstall"
    ModeNone      InstallMode = "none"
)
```

**Key functions:**

```go
// ParseVersion parses a version string per spec §3.1.
// Returns nil for "" or "latest".
// Errors: "invalid version string '...' expected format v1.2 or v1.2.3"
//         "invalid version string '...', golangci-lint v<N> is not supported ..."
func ParseVersion(s string) (*Version, error)

// StringifyVersion converts a Version back to "v<major>.<minor>" or
// "v<major>.<minor>.<patch>". Returns "latest" for nil.
func StringifyVersion(v *Version) string

// IsLessVersion returns true if a < b, comparing major then minor only (spec §3.1).
func IsLessVersion(a, b *Version) bool

// RequestedVersion reads version source priority (spec §3.2):
// 1. version input (non-empty)
// 2. go.mod regex scan
// 3. version-file dispatch
// 4. fallthrough → nil ("latest")
// readFile is injected so callers control filesystem access.
func RequestedVersion(
    versionInput, versionFileInput, workingDir string,
    readFile func(path string) ([]byte, error),
    exists func(path string) bool,
    warnf func(format string, args ...any),
) (*Version, error)

// GetVersion resolves the final VersionInfo from the requested version.
// For goinstall: returns VersionInfo{TargetVersion: versionInput or "latest"} immediately.
// For binary with exact triplet: skips network.
// Otherwise: calls fetchMapping to get the version mapping JSON.
// Spec §3.3, §3.4.
func GetVersion(
    mode InstallMode,
    versionInput string,
    requested *Version,
    fetchMapping func() (map[string]VersionInfo, error),
    infof func(format string, args ...any),
) (VersionInfo, error)

// ParseVersionMapping parses the version mapping JSON body.
// Returns error if MinorVersionToConfig field is missing (spec §3.4).
func ParseVersionMapping(body []byte) (map[string]VersionInfo, error)
```

**Implementation notes:**
- `versionRe`: `regexp.MustCompile(`^v(\d+)\.(\d+)(?:\.(\d+))?$`)`
- `modVersionRe`: `regexp.MustCompile(`github\.com/golangci/golangci-lint/v2\s(v\S+)`)`
- tool-versions regex: `regexp.MustCompile(`(?m)^golangci-lint\s+([^\n#]+)`)`
- tool-versions result: `"v" + strings.TrimSpace(match[1])` then strip leading `v` via
  `regexp.MustCompile(`(?i)^v`).ReplaceAllString(…, "")`
- `.golangci-lint-version` result: `"v" + strings.TrimSpace(string(content))` then same v-strip.
- Both file types: prepend `"v"` exactly once (the v-strip before prepend handles leading-v inputs).
- Minimum version: `{Major:2, Minor:1, Patch: ptr(0)}`. `IsLessVersion` compares major
  then minor; patch field intentionally ignored per spec comment.
- `GetVersion` for goinstall: `versionInput` taken as-is from raw input (no Parse call).
- Exact triplet check: `v.Major == 2 && v.Minor != nil && v.Patch != nil`.

**Tests:** All 17 version test anchors from spec §14. Especially: bad-format error message
(exact), wrong-major error message (exact, with trailing period), below-minimum, isLessVersion
ignores patch, exact triplet skips fetcher, goInstall returns raw input, tool-versions comment
stripping, leading-v normalization.

---

### Phase 3 — `internal/install`

**Purpose:** Install the golangci-lint binary via one of three modes. Spec §4.

**Key functions:**

```go
// AssetURL builds the download URL for the given version and platform.
// platform/arch strings follow spec §4.4 mapping tables.
// ext is "zip" on windows, "tar.gz" otherwise.
func AssetURL(targetVersion, platform, arch string) string

// ExtractBinPath returns the binary path within an extracted archive root.
// dirName = last path segment of URL with archive extension stripped.
// result = filepath.Join(extractedRoot, dirName, "golangci-lint")
// Spec §4.4.
func ExtractBinPath(assetURL, extractedRoot string) string

// TarArgs returns the tar extraction flags for the current platform.
// ["xz", "--overwrite"] on non-darwin; ["xz"] on darwin. Spec §4.4.
func TarArgs(goos string) []string

// PlatformStrings maps runtime.GOOS and runtime.GOARCH to the golangci-lint
// release asset naming convention. Spec §4.4.
func PlatformStrings(goos, goarch string) (platform, arch, ext string)

// ParseGoInstallBinPath parses the stderr of "go install -n ..." to find the
// binary path from the first "touch " line. Spec §4.3.
func ParseGoInstallBinPath(stderr string) string

// InstallMode normalizes the install-mode input string (lowercase). Spec §2.
func NormalizeMode(raw string) InstallMode

// FindInPath returns the absolute path of golangci-lint if found in PATH.
// Returns ("", nil) if not found, error only for unexpected lookup failures.
// Spec §4.2.
func FindInPath(lookPath func(file string) (string, error)) (string, error)
```

**Shell functions (in `cmd/run/run.go`, not in install package):**

`DownloadAndExtract` (calls `http.Get`, writes to `os.CreateTemp`, runs tar/unzip) and
`GoInstallBinary` (calls `exec.Command`) live in the shell layer (`cmd/run`) because they
perform I/O. `install` contains only the pure mapping/parsing logic.

**Implementation notes:**
- `PlatformStrings`: GOOS `"windows"` → platform `"windows"`, ext `"zip"`. All others: GOOS
  unchanged, ext `"tar.gz"`. GOARCH: `"arm64"→"arm64"`, `"amd64"→"amd64"`, `"386"→"386"`,
  default `"amd64"`.
- `AssetURL`: strips leading `"v"` from targetVersion via `strings.TrimPrefix(targetVersion, "v")`.
- `ParseGoInstallBinPath`: split on `\r?\n`, trim, filter prefix `"touch "`, join, split on
  `" "` with limit 2, return element 1. Exactly matches the TypeScript chain in spec §4.3.
- Mode=none error: `errors.New("golangci-lint binary not found in the PATH")`.

**Tests:** URL construction for linux/amd64, windows/arm64, darwin/amd64; TarArgs darwin vs
linux; ExtractBinPath for both zip and tar.gz URLs; ParseGoInstallBinPath with multi-line
stderr, empty stderr, no touch line.

---

### Phase 4 — `internal/actionscache`

**Purpose:** Implement the GitHub Actions Cache REST API (the equivalent of `@actions/cache`).
This is the most complex internal package. Spec §5.3–5.4.

**GitHub Actions Cache API endpoints:**

All calls use bearer auth: `Authorization: Bearer <ACTIONS_RUNTIME_TOKEN>`.
Base URL: `ACTIONS_CACHE_URL` (trailing slash included by the runner).
API version header: `Accept: application/json;api-version=6.0-preview.1`.

```
GET  {base}_apis/artifactcache/cache?keys={url-encoded-keys}&version={version}
     → 200 {archiveLocation: "...", cacheKey: "..."} if found
     → 204 No Content if not found

POST {base}_apis/artifactcache/caches
     Body: {"key":"...", "version":"...", "compressionMethod":"gzip"}
     → 201 {cacheId: N}
     → 409 Conflict (ReserveCacheError)

PATCH {base}_apis/artifactcache/caches/{id}
     Content-Range: bytes 0-{size-1}/{size}
     Content-Type: application/octet-stream
     Body: archive bytes

POST {base}_apis/artifactcache/caches/{id}   (commit)
     Body: {"size": N}
```

The `version` query parameter in the lookup is the SHA-256 of the sorted joined paths being
cached, which is what `@actions/cache` computes internally. For this implementation, compute:
`sha256(strings.Join(sortedPaths, "\n"))` as the cache version.

**Key types and functions:**

```go
// Client holds the HTTP client and the runtime credentials.
// Construct with NewClient; inject in tests with a custom httpDo.
type Client struct {
    baseURL string
    token   string
    // http sender — injectable for tests
    do func(req *http.Request) (*http.Response, error)
}

func NewClient(getenv func(string) string) *Client

// RestoreCache looks up the cache and, if found, downloads and extracts the archive
// into each of paths. Returns the matched cache key, or "" if not found.
// Corresponds to @actions/cache restoreCache. Spec §5.3.
func (c *Client) RestoreCache(paths []string, primaryKey string, restoreKeys []string) (string, error)

// SaveCache archives paths, reserves a slot, uploads in chunks, commits.
// Spec §5.4.
func (c *Client) SaveCache(paths []string, key string) error

// ValidationError is returned by SaveCache when the key is invalid.
// Matches the @actions/cache ValidationError type check (error.name === "ValidationError").
type ValidationError struct{ Msg string }
func (e *ValidationError) Error() string

// ReserveCacheError is returned by SaveCache when the slot is already taken.
// Matches error.name === "ReserveCacheError".
type ReserveCacheError struct{ Msg string }
func (e *ReserveCacheError) Error() string

// CacheVersion computes the SHA-256 version string for a list of paths.
func CacheVersion(paths []string) string
```

**Implementation notes:**
- Archive format: the TS action uses `zstd` compression. For simplicity, use `gzip` (set
  `compressionMethod: "gzip"` in POST body) since the Go standard library supports it without
  external dependencies. Spec does not prescribe compression format.
- Chunk size for PATCH: use 32 MiB chunks (same as TS action default: `32 * 1024 * 1024`).
- Archive creation: use `archive/tar` + `compress/gzip`; walk each path in `paths` recursively.
- Archive extraction: stream from the signed URL (`archiveLocation`) through `compress/gzip`
  and `archive/tar`, writing files relative to `$GITHUB_WORKSPACE` or absolute paths.
- HTTP retries (max 5, exponential backoff): wrap the `do` func.

**Separate from error-name-based error detection** in the caller (spec §5.3–5.4):
- 409 response from POST → return `*ReserveCacheError`.
- 400 response → return `*ValidationError`.

**Tests:** Use an injected `do` function (an `http.RoundTripper` wrapped in a func) to mock
HTTP responses. Test: lookup miss (204), lookup hit with download, reserve-conflict (409),
reserve success + upload + commit. `CacheVersion` is pure and tested directly.

---

### Phase 5 — `internal/cache`

**Purpose:** Build cache keys, set `GOLANGCI_LINT_CACHE`, orchestrate restore/save using the
`actionscache.Client`. All pure key-building logic is separate from the I/O that reads go.mod
and calls the cache API. Spec §5.1–5.4.

**Key functions:**

```go
// LintCacheDir returns the golangci-lint cache directory path.
// On Windows (detected via goos): filepath.Join(userProfile, ".cache", "golangci-lint").
// Otherwise: filepath.Join(home, ".cache", "golangci-lint"). Spec §5.1.
func LintCacheDir(goos, home, userProfile string) string

// IntervalBucket computes the cache interval bucket string for a given time and N.
// N ≤ 0: returns strconv.FormatInt(now.UnixMilli(), 10)  ← milliseconds, spec §5.2.
// N > 0: returns strconv.Itoa(int(math.Floor(float64(now.UnixMilli()) / 1000 / float64(N*86400)))).
func IntervalBucket(now time.Time, n int) string

// GoModChecksum returns the SHA-1 hex of the file at goModPath.
// Returns "nogomod" if the file does not exist. Spec §5.2.
func GoModChecksum(goModPath string, readFile func(string) ([]byte, error)) (string, error)

// BuildCacheKeys returns [primaryKey, restoreKey] in that order.
// primaryKey = "golangci-lint.cache-{OS}-{wd}-{bucket}-{sha1}"
// restoreKey = "golangci-lint.cache-{OS}-{wd}-{bucket}-"  (trailing dash, no hash)
// Spec §5.2.
func BuildCacheKeys(runnerOS, workingDir string, bucket, checksum string) (primary, restore string)

// RestoreCache wraps actionscache.Client.RestoreCache.
// Sets os.Setenv("GOLANGCI_LINT_CACHE", cacheDir) BEFORE calling the client.
// Saves primary key in GITHUB_STATE["CACHE_KEY"].
// On hit: saves matched key in GITHUB_STATE["CACHE_RESULT"].
// Spec §5.3 — exact execution order.
func RestoreCache(
    ctx context.Context,
    client CacheClient,           // interface over actionscache.Client
    getenv func(string) string,
    setenv func(k, v string) error,
    saveState func(k, v string) error,
    cacheDir, primaryKey string,
    restoreKeys []string,
    out io.Writer,
) error

// SaveCache wraps actionscache.Client.SaveCache with the three-way error split.
// Spec §5.4.
func SaveCache(
    ctx context.Context,
    client CacheClient,
    getenv func(string) string,
    saveState func(k, v string) error,
    cacheDir, primaryKey, matchedKey string,
    out io.Writer,
) error

// CacheClient is the narrow interface consumed by cache.RestoreCache / SaveCache.
type CacheClient interface {
    RestoreCache(paths []string, primaryKey string, restoreKeys []string) (string, error)
    SaveCache(paths []string, key string) error
}
```

**Implementation notes:**
- `IntervalBucket` N ≤ 0 branch: `now.UnixMilli()` which is `now.UnixNano() / 1e6`. This is
  milliseconds, not seconds — spec §5.2 explicitly calls this out.
- `BuildCacheKeys`: the restore key is `golangci-lint.cache-{OS}-{wd}-{bucket}-` with a
  trailing dash. This matches the TypeScript code that pushes the partial key before appending
  the hash.
- `RestoreCache`: sets `GOLANGCI_LINT_CACHE` via `setenv` before calling `client.RestoreCache`.
  This ordering is mandated by spec §5.3 and test anchor #34.
- `SaveCache` three-way error split (spec §5.4, test anchors #38–#40):
  - `*actionscache.ValidationError` → re-return (fatal).
  - `*actionscache.ReserveCacheError` → `gha.Info(out, err.Error())` (plain info, no [warning]).
  - anything else → `gha.Info(out, "[warning] "+err.Error())` (with space after `]`).
- `isExactKeyMatch`: `strings.EqualFold` is not quite right; use `strings.Compare` with
  Unicode case-insensitive comparison equivalent. The TS uses
  `localeCompare(key, undefined, {sensitivity: "accent"})`. In Go, use
  `strings.ToLower(a) == strings.ToLower(b)` as a practical approximation.

**Tests:** `IntervalBucket` with N=0, N=-1, N=7; `BuildCacheKeys` verifying trailing dash on
restore key; `GoModChecksum` with present/absent file; `SaveCache` error routing (all three
branches) using a `CacheClient` stub.

---

### Phase 6 — `internal/patch`

**Purpose:** Fetch PR/push diff from GitHub API and transform paths for a working directory.
Spec §6.

**Key types and functions:**

```go
// HTTPDoer is the injectable HTTP function (injectable for tests).
type HTTPDoer func(req *http.Request) (*http.Response, error)

// FetchPatch fetches the appropriate diff based on event name (spec §6.1).
// Returns ("", nil) for merge_group and for unrecognized event names.
// workingDir may be empty; GITHUB_WORKSPACE from getenv.
// tempDir is injected so tests can control where files are written.
func FetchPatch(
    ctx context.Context,
    eventName, owner, repo string,
    payload map[string]any, // parsed github.context.payload
    token, workingDir, workspace string,
    do HTTPDoer,
    tempDir func() (string, error),
    warnf func(format string, args ...any),
    infof func(format string, args ...any),
) (string, error)

// AlterDiffPatch rewrites a unified diff to strip workingDir prefix.
// Returns patch unchanged when workingDir is empty.
// Spec §6.4.
func AlterDiffPatch(patch, workingDir, workspace string) string

// filterDiffSections implements the line-by-line walk with ignore flag.
// Exported for testing individual edge cases.
func FilterDiffSections(lines []string, wd string) []string

// EscapeRegexpMeta escapes all regexp special characters in s.
// Matches JS: /[.*+?^${}()|[\]\\]/g → "\\$&". Spec §6.4.
func EscapeRegexpMeta(s string) string
```

**Implementation notes:**
- `FetchPatch` for `pull_request`/`pull_request_target`: reads `payload["pull_request"]["number"]`;
  calls `GET /repos/{owner}/{repo}/pulls/{number}` with header `Accept: application/vnd.github.diff`.
  Writes transformed patch to `path.Join(tempDir, "pull.patch")`. Spec §6.2.
- `FetchPatch` for `push`: basehead = `payload["before"].(string) + "..." + payload["after"].(string)`.
  Calls `GET /repos/{owner}/{repo}/compare/{basehead}`. Writes to `push.patch`. Spec §6.3.
- Non-200 response: `warnf("failed to fetch ... patch: response status is %d", status)`, return `""`.
- Network error: `warnf("failed to fetch ... patch: %v", err)`, return `""`.
- Write error: `warnf("failed to save pull request patch: %v", err)`, return `""`.
  Note: write-error message says "pull request patch" even for push — replicate the source bug.
- HTTP retry: up to 5 attempts with exponential backoff for 5xx responses.
- `AlterDiffPatch`: compute `wd = filepath.Rel(workspace, workingDir)` (use `/` separator on
  all platforms since git diff paths always use forward slash). Build the two regexp patterns
  from spec §6.4. Split on `"\n"`, filter, join with `"\n"`.
- For `diff --git` lines, detect `" a/"+wd+"/"` (space + `a/` prefix, spec §6.4 literal).

**Tests:** `AlterDiffPatch` with various patches (lines in-wd, lines out-of-wd, mixed);
`EscapeRegexpMeta` round-trip; `FilterDiffSections` edge cases (no working-directory, wd at
root, wd with regexp-special characters); `FetchPatch` for merge_group (empty return); stub
HTTP for PR and push with success and non-200.

---

### Phase 7 — `internal/plugins`

**Purpose:** Install a custom golangci-lint binary from `.custom-gcl.{yml,yaml,json}`.
Spec §4.5.

**Key types and functions:**

```go
// PluginConfig holds parsed fields from .custom-gcl.{yml,yaml,json}.
type PluginConfig struct {
    Version     string `yaml:"version" json:"version"`
    Destination string `yaml:"destination" json:"destination"`
    Name        string `yaml:"name" json:"name"`
}

// FindConfigFile returns the first .custom-gcl config file found in rootDir
// (order: yml, yaml, json), or ("", nil) if none exists. Spec §4.5.
func FindConfigFile(rootDir string, stat func(string) (os.FileInfo, error)) (string, error)

// ParseConfig reads and parses the config file (YAML for all extensions).
func ParseConfig(path string, readFile func(string) ([]byte, error)) (*PluginConfig, error)

// ApplyDefaults fills in default values: destination "." and name "custom-gcl".
func ApplyDefaults(cfg *PluginConfig)

// Install runs golangci-lint custom in rootDir and returns the new binary path.
// destExists and mkdirAll are injectable for tests.
// versionInput is the raw version input from the user ("" if not set).
// Spec §4.5.
func Install(
    binPath, rootDir, configFile string,
    cfg *PluginConfig,
    versionInput string,
    destExists func(string) bool,
    mkdirAll func(path string, perm os.FileMode) error,
    runCmd func(name string, args []string, dir string) error,
    warnf func(format string, args ...any),
    infof func(format string, args ...any),
) (string, error)
```

**Implementation notes:**
- `rootDir` resolution: if `working-directory` input is non-empty, validate existence and call
  `filepath.Abs`; otherwise `os.Getwd()`. Validation failure throws
  `fmt.Errorf("working-directory (%s) was not a path", rootDir)`. Spec §4.5.
- Config file search order: `[".custom-gcl.yml", ".custom-gcl.yaml", ".custom-gcl.json"]`.
  Return the first that exists. Spec §4.5.
- Version mismatch warning: only when `versionInput != ""` AND `cfg.Version != versionInput`.
- Mkdir check: `destExists(cfg.Destination)` (checks `cfg.Destination` directly, NOT
  `filepath.Join(rootDir, cfg.Destination)`). Replicates the spec §4.5 quirk.
- Return path: `filepath.Join(rootDir, cfg.Destination, cfg.Name)`.
- On `runCmd` error: wrap as `fmt.Errorf("Failed to build custom golangci-lint binary: %w", err)`.

**Tests:** `FindConfigFile` with yml/yaml/json present and absent; `ApplyDefaults` for both
fields; `Install` happy path, version mismatch warning, mkdir creation, runCmd error.

---

### Phase 8 — `internal/lint`

**Purpose:** Pure logic for lint argument processing, auto-module detection, command string
assembly, and exit-code interpretation. Spec §7.

**Key types and functions:**

```go
// UserArgs holds the parsed user arguments from the `args` input.
type UserArgs struct {
    Raw      string            // original unparsed string, appended verbatim
    ArgMap   map[string]string // lowercased key → value
    ArgNames map[string]bool   // lowercased keys
}

// ParseUserArgs implements spec §7.1.
// Tokens not starting with "-" are discarded.
// Keys are lowercased; value is "" when no "=" present.
func ParseUserArgs(raw string) UserArgs

// BuildLintCommand assembles the full lint command string. Spec §7.5.
// Returns "<binPath> run <addedArgs...> <userArgs.Raw>", right-trimmed.
func BuildLintCommand(binPath string, addedArgs []string, userArgs UserArgs) string

// OnlyNewIssuesArgs returns the addedArgs for only-new-issues mode. Spec §7.2.
// Returns an error if userArgs contain any --new* flag.
// For merge_group: always returns 4 args unconditionally.
// For pr/push: returns 4 args only when patchPath is non-empty.
// For other events: returns nil.
func OnlyNewIssuesArgs(eventName, baseSHA, patchPath string, userArgs UserArgs) ([]string, error)

// PathModeArg returns ["--path-mode=abs"] when workingDir is non-empty and
// userArgs has neither "path-prefix" nor "path-mode". Spec §7.3.
func PathModeArg(workingDir string, userArgs UserArgs) []string

// InterpretExitCode translates a lint process exit code to an action outcome.
// Returns ("", nil) for 0; ("issues found", ErrExitCode) for 1;
// ("golangci-lint exit with code N", ErrExitCode) for other non-zero.
func InterpretExitCode(code int) (msg string, err error)

// ErrExitCode is a sentinel error returned by InterpretExitCode.
var ErrExitCode = errors.New("lint exit code non-zero")

// ModulesAutoDetection globs for go.mod files under rootDir, excluding vendor,
// node_modules, .git, dist. Returns deduplicated sorted absolute paths. Spec §7.6.
// glob is injectable for tests.
func ModulesAutoDetection(
    rootDir string,
    glob func(pattern string, dir string) ([]string, error),
) ([]string, error)
```

**Implementation notes:**
- `ParseUserArgs`: `.TrimSpace(raw).Split(spaces)` → filter `-` prefix → strip leading `--?` →
  split on first `=` → lowercase key, value `""` if no `=`.
- `OnlyNewIssuesArgs` for PR/push: ALL 4 args inside `if patchPath != ""`. When patch is empty,
  return nil (no args). Spec §7.2 test anchor #47.
- `OnlyNewIssuesArgs` for merge_group: push all 4 args unconditionally. Spec test anchor #48.
- Conflict check (spec §7.2): keys `new`, `new-from-rev`, `new-from-patch`, `new-from-merge-base`.
  Error: `errors.New("please, don't specify manually --new* args when requesting only new issues")`.
- `BuildLintCommand`: `strings.TrimRight(binPath + " run " + strings.Join(addedArgs, " ") + " " + raw, " ")`.
- `ModulesAutoDetection`: use `filepath.Glob` or `os.ReadDir` recursively; exclude directories
  named `vendor`, `node_modules`, `.git`, `dist`. Resolve each match to absolute path with
  `filepath.Abs`. Sort, then deduplicate via a map.

**Tests:** All of the lint execution test anchors (§14 #51–64). Specifically: args with mixed
flags, `--Config=x` recognized as lowercase `config`; no-patch produces no args for PR;
merge_group always gets args; conflict check; InterpretExitCode all three cases;
ModulesAutoDetection exclusion and deduplication.

---

### Phase 9 — `cmd/run/run.go`

**Purpose:** Phase 1 shell. Wires all internal packages in the order mandated by spec §11.
Thin: no business logic.

**Command registration:**

```go
// New creates and registers the "run" subcommand.
func New(parent *root.Config) *Config
```

`Config` embeds `*root.Config` and declares the following ff/v4 flags (CLI overrides for
testability; action inputs come from `gha.Input`):

No CLI flags are strictly required — all inputs come from `INPUT_*` env vars via `gha.Input`.
The ff/v4 `--help` and env-var prefix from `cmd/cmd.go` still apply.

**`exec` function — ordered exactly per spec §11:**

```go
func (cfg *Config) exec(ctx context.Context, _ []string) error {
    // 1. Read all inputs via gha.Input(cfg.Getenv, ...)
    // 2. Group("Restore cache"):
    //    a. Check skip conditions (skip-cache, isValidEvent)
    //    b. GOLANGCI_LINT_CACHE = cache.LintCacheDir(...)
    //       → set via os.Setenv BEFORE restoreCache call (spec §5.3)
    //    c. Build cache keys; save primary key to state
    //    d. client.RestoreCache(...)
    // 3. Group("Install"):
    //    a. If problem-matchers=true: gha.RegisterProblemMatcher(...)
    //    b. Normalize install mode
    //    c. If mode=none: FindInPath; if not found: return error
    //    d. lintver.GetVersion(mode, ..., fetchMappingHTTP)
    //    e. Download/extract or go-install binary
    //    f. plugins.Install(binPath, rootDir, ...)
    // 4. gha.AddPath(dirname(binPath))
    // 5. If debug non-empty: Group("Debug"): run cache clean/status
    // 6. If install-only: return nil
    // 7. workingDir = validated working directory (or "")
    // 8. If experimental contains "automatic-module-directories":
    //       dirs = lint.ModulesAutoDetection(workingDir)
    //       for each dir: Group("run golangci-lint in <rel>"): runLint(dir)
    //    else:
    //       Group("run golangci-lint"): runLint(workingDir)
    // On any error: return as-is (top-level run() in main.go handles it)
}
```

**`runLint` helper (inside cmd/run, unexported):**

```go
func runLint(
    ctx context.Context,
    binPath, rootDir string,
    inputs runInputs,
    out io.Writer,
) error {
    // 1. ParseUserArgs
    // 2. If only-new-issues: OnlyNewIssuesArgs → fetch patch first
    // 3. PathModeArg
    // 4. runVerify (if verify=true)
    // 5. BuildLintCommand
    // 6. exec.Command, stream stdout/stderr to out via gha.Info
    // 7. finally: gha.Info(out, "Ran golangci-lint in Xms")
    // 8. InterpretExitCode → root.ExitError(1) for issues found
}
```

**Error handling per spec §1:**
- Fatal errors propagate up; `cmd/cmd.go`'s dispatcher logs them.
- Lint exit code 1 returns `root.ExitError(1)` so `run()` in `main.go` calls `os.Exit(1)`.
- Lint output (`golangci-lint found no issues` / `golangci-lint exit with code N`) is written
  to `cfg.Stdout` via `gha.Info`, not via `core.setFailed` (no setFailed equivalent in Go).

---

### Phase 10 — `cmd/postrun/postrun.go`

**Purpose:** Phase 2 shell. Save the cache. Spec §1 (post-run), §5.4.

```go
func (cfg *Config) exec(ctx context.Context, _ []string) error {
    // 1. Read skip-cache, skip-save-cache inputs
    // 2. Check skip conditions (spec §5.4)
    // 3. Read primary key from GetState("CACHE_KEY")
    // 4. Read matched key from GetState("CACHE_RESULT")
    // 5. Check exact key match → skip if equal
    // 6. cache.SaveCache(client, ...) with three-way error split
}
```

On any unhandled error: `cmd/cmd.go` dispatcher catches and logs; `main.go` exits non-zero.
The spec's "Failed to post-run: ..." message is logged at this layer.

---

## 5. Modifications to Existing Files

### `cmd/root/root.go`

Add `Getenv func(string) string` to `Config`:

```go
type Config struct {
    Stdin   io.Reader
    Stdout  io.Writer
    Stderr  io.Writer
    Getenv  func(string) string // NEW: injected in main; overridden in tests
    Flags   *ff.FlagSet
    Command *ff.Command
}
```

Update `New` to accept and store `getenv`:

```go
func New(stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) *Config
```

### `cmd/cmd.go`

- Update `Run` signature to accept `getenv func(string) string`.
- Pass `getenv` to `root.New(...)`.
- Register `run.New(r)` and `postrun.New(r)` after `version.New(r)`.

### `main.go`

- Update `run(ctx context.Context) int` to pass `os.Getenv`:

```go
err := cmd.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv)
```

### `go.mod`

```
require (
    github.com/peterbourgon/ff/v4 v4.0.0-beta.1
    gopkg.in/yaml.v3 vX.X.X     // add: latest stable
)
```

---

## 6. New Supporting Files

### `action.yml`

GitHub Actions manifest defining the action's two phases:

```yaml
name: 'golangci-lint-action-go'
description: 'Go implementation of golangci-lint GitHub Action'
inputs:
  version:
    description: 'golangci-lint version'
    default: ''
  # … all 14 inputs from spec §2 …
runs:
  using: 'composite'
  steps:
    - name: Run golangci-lint
      shell: bash
      run: golangci-lint-action-go run
    - name: Save cache
      if: always()
      shell: bash
      run: golangci-lint-action-go post-run
```

All 14 inputs from spec §2 must be declared with their documented defaults.

### `problem-matchers.json`

Copy verbatim from the reference TypeScript action. Content per spec §4.1:

```json
{
  "problemMatcher": [{
    "owner": "golangci-lint-colored-line-number",
    "severity": "error",
    "pattern": [{
      "regexp": "^([^:]+):(\\d+):(?:(\\d+):)?\\s+(.+ \\(.+\\))$",
      "file": 1, "line": 2, "column": 3, "message": 4
    }]
  }]
}
```

---

## 7. Testing Strategy

**Per-package tests:**
- Each `internal/` package has a `_test.go` file using stdlib `testing` only.
- Three assertion helpers per package (`assert`, `ok`, `equals`), each calling `t.Helper()`.
- All tests are table-driven using `map[string]struct{...}` (not ordered slices).
- No `t.Parallel()` in packages with shared filesystem state; use it freely in pure-function
  tests (each creates its own input values).

**Test fixtures:**
- Placed in `testdata/` directories, referenced with relative paths (not `os.Getwd()`).
- Golden files for `AlterDiffPatch` test cases.

**Injectable boundaries:**
Every internal package is designed so that all I/O (filesystem reads, HTTP calls, exec) is
passed in as a function parameter. Tests never use `t.Setenv`; they inject a custom `getenv`
function.

**Subprocess mocking:**
For `internal/install` and `internal/lint`, use the `helperProcess` pattern (RULES.md §10)
to mock `go install` and `golangci-lint run` subprocess calls in integration tests.

**Spec test anchors (spec §14):**
All 64 test anchors in spec §14 must have a corresponding test assertion. Group them into the
package that owns the behavior: anchors 1–17 → `lintver`; 18–26 → `install`; 27–40 → `cache`;
41–50 → `patch`; 51–64 → `lint`.

**Coverage target:** Focus on the pure core (`internal/`) packages; aim for ≥90% statement
coverage there. Shell packages (`cmd/run`, `cmd/postrun`) need integration-style tests, not
unit tests of the orchestration sequence.

---

## 8. Implementation Order Summary

| # | Package/File | Depends On |
|---|---|---|
| 1 | `internal/gha` | stdlib only |
| 2 | `internal/lintver` | stdlib only |
| 3 | `internal/install` | stdlib only |
| 4 | `internal/actionscache` | stdlib + gha (for error types) |
| 5 | `internal/cache` | gha, actionscache, lintver |
| 6 | `internal/patch` | gha, stdlib |
| 7 | `internal/plugins` | gha, yaml.v3 |
| 8 | `internal/lint` | gha, lintver, patch |
| 9 | Modify root, cmd, main | existing code |
| 10 | `cmd/run` | all internal, root, cmd |
| 11 | `cmd/postrun` | gha, cache, root, cmd |
| 12 | `action.yml`, `problem-matchers.json` | none |
| 13 | `go.mod` update | none |

Each step is completable and testable independently before the next step begins.
