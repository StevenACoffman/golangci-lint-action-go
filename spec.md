# golangci-lint-action — Behavioral Specification (v2)

A language-agnostic specification of observable, verifiable behavior suitable for
reimplementation. Describes every input, output, side effect, control-flow branch,
and error path. All details are derived verbatim from source files in
`/Users/steve/Documents/agent-orange/golangci-lint-action/src/`.

---

## 1. Execution Model

The action runs in two separate phases with persistent state between them.

### Phase 1 — Main (`dist/run/index.js`)

Wrapped in a single top-level try/catch. On any thrown exception:
- Log: `Failed to run: ${error}, ${error.stack}` (via `core.error`)
- Call `core.setFailed(error.message)`

Execution within Phase 1 (each step wrapped in a named group):

1. **Group "Restore cache"** — Restore lint cache from the runner cache store.
2. **Group "Install"** — Install golangci-lint binary then run plugin installer.
3. `core.addPath(path.dirname(binPath))` — Add binary directory to PATH.
4. **Group "Debug"** — Run debug commands (only if `debug` input is non-empty string).
5. If `install-only` is true: return immediately (no linting).
6. Run the linter (single run or auto-module loop).

### Phase 2 — Post (`dist/post_run/index.js`)

Wrapped in a single top-level try/catch. On any thrown exception:
- Log: `Failed to post-run: ${error}, ${error.stack}` (via `core.error`)
- Call `core.setFailed(error.message)`

1. Save lint cache to the runner cache store.

State written in Phase 1 is read in Phase 2 via the GitHub Actions state mechanism.

---

## 2. Inputs

All inputs are strings unless noted. Booleans accept `"true"` / `"false"`.

| Name | Type | Default | Description |
|------|------|---------|-------------|
| `version` | string | `""` | Requested golangci-lint version. Accepts `v2.3`, `v2.3.4`, or `"latest"`. Empty string triggers auto-detection. |
| `version-file` | string | `""` | Path to `.golangci-lint-version` or `.tool-versions` file. Ignored if `version` is set. |
| `install-mode` | string | `"binary"` | One of `"binary"`, `"goinstall"`, `"none"`. Lowercased before comparison. |
| `install-only` | boolean | `false` | If true, install but do not run the linter. |
| `working-directory` | string | `""` | Absolute or relative path to run the linter in. Empty means process working directory. |
| `github-token` | string | `${{ github.token }}` | GitHub token for API calls (PR diff, push diff). |
| `verify` | boolean | `true` | Run `golangci-lint config verify` before linting. |
| `only-new-issues` | boolean | `false` | Pass diff-based `--new-from-*` flags to restrict output to new issues. |
| `args` | string | `""` | Arbitrary flags forwarded verbatim to `golangci-lint run`. |
| `skip-cache` | boolean | `false` | Disable both cache restore and cache save. |
| `skip-save-cache` | boolean | `false` | Disable cache save only (restore still happens). |
| `cache-invalidation-interval` | integer (as string) | `"7"` | Rotate cache key every N days. See §5.2 for ≤0 behavior. |
| `problem-matchers` | boolean | `false` | If true, register the embedded problem-matcher JSON. |
| `debug` | string | `""` | Comma-separated flags. Recognized values: `"cache"`, `"clean"`. |
| `experimental` | string | `""` | Comma-separated flags. Recognized value: `"automatic-module-directories"`. |

---

## 3. Version Resolution

### 3.1 Parsing a version string

**Regex (exact):**
```
/^v(\d+)\.(\d+)(?:\.(\d+))?$/
```
- Group 1 = major (integer)
- Group 2 = minor (integer)
- Group 3 = patch (integer, optional — `null` if absent)

The string `"latest"` and `""` (empty string) both parse to `null` (no version).

**Error: bad format** (string does not match regex and is not `"latest"` or `""`):
```
invalid version string '${s}', expected format v1.2 or v1.2.3
```

**Error: wrong major** (major ≠ 2):
```
invalid version string '${s}', golangci-lint v${match[1]} is not supported by golangci-lint-action >= v7.
```
Note the trailing period inside the message.

**Minimum version check:** After parsing, the version is compared against `{major:2, minor:1, patch:0}`.
`isLessVersion` compares **major first, then minor only** — patch is NOT compared (see comment in source).
If the parsed version is less than the minimum:
```
requested golangci-lint version '${requestedVersion}' isn't supported: we support only v2.1 and later versions
```

### 3.2 Version source priority

Evaluated top-to-bottom; the first match wins.

1. **`version` input** — used directly if non-empty. Skip all remaining steps.
2. **`go.mod` file** — only when `version` is empty.
   - Path: `go.mod` (or `{working-directory}/go.mod` if set).
   - Regex (exact): `/github.com\/golangci\/golangci-lint\/v2\s(v\S+)/`
   - On match: `requestedVersion = match[1]`
   - Logs: `Found golangci-lint version '${requestedVersion}' in '${goMod}' file`
3. **`version-file` input** — only when `version` is still empty after go.mod step.
   - If `working-directory` is set, join it: `path.join(workingDirectory, versionFilePath)`.
   - **If the file does not exist**, throw:
     ```
     The specified golangci-lint version file at: ${versionFilePath} does not exist
     ```
   - File type dispatch by `path.basename(versionFilePath)`:
     - `".tool-versions"` (asdf/mise): regex `/^golangci-lint\s+([^\n#]+)/m` (multiline),
       result = `"v" + match[1].trim().replace(/^v/gi, "")`. If no match: `""`.
     - Any other name (`.golangci-lint-version`): read content, `"v" + content.trim().replace(/^v/gi, "")`.
4. **Fallthrough** — if `requestedVersion` is still `""` after all steps, parse as `null` → resolves to `"latest"`.

**When both `version` and `version-file` are set**, `version` wins and this warning is emitted:
```
Both version (${requestedVersion}) and version-file (${versionFilePath}) inputs are specified, only version will be used
```

### 3.3 goInstall mode version

When `install-mode` is `"goinstall"`, `getVersion` returns immediately **without** parsing or validating the version string:
```typescript
const v: string = core.getInput(`version`)
return { TargetVersion: v ? v : "latest" }
```
The `version` input is used verbatim. If empty, `"latest"` is used.
No go.mod scan, no version-file lookup, no minimum-version check.

### 3.4 Version mapping fetch (binary mode only)

Runs when `install-mode` is `"binary"` and the resolved version does NOT have all three
components (major + minor + patch). An exact triplet is used directly without a network
call:
```typescript
if (reqVersion?.major === 2 && reqVersion?.minor != null && reqVersion?.patch !== null) {
  return { TargetVersion: `v${reqVersion.major}.${reqVersion.minor}.${reqVersion.patch}` }
}
```

Otherwise, fetch:
```
GET https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/assets/github-action-config-v2.json
```
with `allowRetries: true, maxRetries: 5`.

Expected JSON shape:
```json
{
  "MinorVersionToConfig": {
    "v2.X": { "TargetVersion": "v2.X.Y", "Error": "" }
  }
}
```

Lookup key = `stringifyVersion(reqVersion)` = `"v{major}.{minor}"` (no patch).

**Error: missing field** (JSON lacks `MinorVersionToConfig`): warn JSON, then throw:
```
invalid config: no MinorVersionToConfig field
```

**Error: key not in map:**
```
requested golangci-lint version '${stringifyVersion(reqVersion)}' doesn't exist
```

**Error: entry has non-empty `Error` field:**
```
failed to use requested golangci-lint version '${stringifyVersion(reqVersion)}': ${versionInfo.Error}
```

**On fetch network failure**, the error is re-wrapped:
```
failed to get action config: ${exc.message}
```

After successful fetch, logs:
```
Requested golangci-lint '${minor}', using '${TargetVersion}', calculation took ${ms}ms
```

---

## 4. Installation

### 4.1 Problem-matcher registration

Happens **first** inside `install()`, before version resolution.

If `problem-matchers` input is `true`:
1. Compute path: `path.join(__dirname, "../..", "problem-matchers.json")`.
2. If the file exists, emit via `core.info`:
   ```
   ##[add-matcher]{absolutePathToMatchersJson}
   ```
   Note: emitted via `core.info()`, not `process.stdout.write`.

The problem-matcher JSON defines:
```json
{
  "owner": "golangci-lint-colored-line-number",
  "severity": "error",
  "pattern": [{
    "regexp": "^([^:]+):(\\d+):(?:(\\d+):)?\\s+(.+ \\(.+\\))$",
    "file": 1, "line": 2, "column": 3, "message": 4
  }]
}
```
- Group 1: file path
- Group 2: line number
- Group 3: column number (optional capture group)
- Group 4: message (contains the linter name in parentheses)

### 4.2 `install-mode: "none"`

1. Call `which("golangci-lint", { nothrow: true })`.
2. If not found: throw:
   ```
   golangci-lint binary not found in the PATH
   ```
3. Return found path.

### 4.3 `install-mode: "goinstall"`

1. Log: `Installing golangci-lint ${versionInfo.TargetVersion}...`
2. Run (with `CGO_ENABLED=1` in child process env):
   ```
   go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${versionInfo.TargetVersion}
   ```
3. Run (dry-run, same env):
   ```
   go install -n github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${versionInfo.TargetVersion}
   ```
4. Parse stderr of dry-run to find binary path:
   ```typescript
   res.stderr
     .split(/\r?\n/)
     .map(v => v.trimStart().trimEnd())
     .filter(v => v.startsWith("touch "))
     .reduce((a, b) => a + b, "")
     .split(` `, 2)[1]
   ```
   Find the first `"touch "` line, join all such lines, split on first space, take element `[1]`.
5. Log: `Installed golangci-lint into ${binPath} in ${ms}ms`
6. Return binary path.

Both commands' stdout and stderr are printed via `core.info`.

### 4.4 `install-mode: "binary"` (default)

**Log:** `Installing golangci-lint binary ${versionInfo.TargetVersion}...`

**Platform mapping** (using `os.platform().toString()`):

| `os.platform()` | golangci-lint platform string |
|----------------|-------------------------------|
| `"win32"` | `"windows"` |
| anything else | unchanged (e.g. `"linux"`, `"darwin"`) |

**Architecture mapping** (using `os.arch()` — not `process.arch`):

| `os.arch()` | golangci-lint arch string |
|-------------|--------------------------|
| `"arm64"` | `"arm64"` |
| `"x64"` | `"amd64"` |
| `"ia32"` | `"386"` |
| anything else | `"amd64"` (default) |

**Archive extension:**
- `"zip"` when `os.platform() === "win32"`, extracted to `process.env.HOME`.
- `"tar.gz"` on all other platforms, extracted to `process.env.HOME`.

**Version strip:** Remove leading `"v"` from `TargetVersion` for the filename portion.
```typescript
const noPrefix = versionInfo.TargetVersion.slice(1)
```

**Download URL:**
```
https://github.com/golangci/golangci-lint/releases/download/${TargetVersion}/golangci-lint-${noPrefix}-${platform}-${arch}.${ext}
```

**Extraction:**
- `.zip`: `tc.extractZip(archivePath, process.env.HOME)`.
- `.tar.gz`: `tc.extractTar(archivePath, process.env.HOME, args)` where `args = ["xz"]`,
  then — if `process.platform.toString() != "darwin"` — push `"--overwrite"`.
  On macOS, `args` is exactly `["xz"]` (no `--overwrite`).

**Directory name extraction:**
```typescript
const urlParts = assetURL.split(`/`)
const dirName = urlParts[urlParts.length - 1].replace(repl, ``)
```
where `repl = /\.zip$/` for zip or `repl = /\.tar\.gz$/` for tar.gz.

**Binary path:**
```typescript
const binPath = path.join(extractedDir, dirName, `golangci-lint`)
```

**Log:** `Installed golangci-lint into ${binPath} in ${ms}ms`

### 4.5 Plugin installation

After the primary binary is installed, `plugins.install(binPath)` runs.

**Root directory resolution:**
- If `working-directory` input is non-empty:
  - Validate: `fs.existsSync(rootDir) && fs.lstatSync(rootDir).isDirectory()`. If not: throw `working-directory (${rootDir}) was not a path`.
  - Then: `rootDir = path.resolve(rootDir)` (resolve to absolute path).
- Else: `rootDir = process.cwd()`.

**Config file search** (first match wins, in order):
1. `path.join(rootDir, ".custom-gcl.yml")`
2. `path.join(rootDir, ".custom-gcl.yaml")`
3. `path.join(rootDir, ".custom-gcl.json")`

If no config found, return `binPath` unchanged.

If config found:
- Log: `Found configuration for the plugin module system : ${configFile}` (note: space before colon)
- Log: `Building and installing custom golangci-lint binary...`
- Parse config file as YAML.
- If `version` input is non-empty AND `config.version !== version` input:
  - Warn: `The golangci-lint version (${config.version}) defined inside ${configFile} does not match the version defined in the action (${v})`
- Defaults: `config.destination = "."` if falsy; `config.name = "custom-gcl"` if falsy.
- Mkdir check: `if (!fs.existsSync(config.destination))` (checks `config.destination` directly, NOT `path.join(rootDir, config.destination)`).
  - If missing: Log `Creating destination directory: ${config.destination}`, then `fs.mkdirSync(config.destination, { recursive: true })`.
- Log: `Running [${binPath} custom] in [${rootDir}] ...`
- Execute `${binPath} custom` with `cwd: rootDir`.
- On success: log `Built custom golangci-lint binary in ${ms}ms`, return `path.join(rootDir, config.destination, config.name)`.
- On error: throw `Failed to build custom golangci-lint binary: ${exc.message}`.

### 4.6 Adding binary to PATH

After `install().then(plugins.install)` resolves (returning plugin-adjusted binPath):
```typescript
core.addPath(path.dirname(binPath))
```
The directory containing the final binary (plugin binary if installed, otherwise original) is added to PATH.

---

## 5. Cache Management

### 5.1 Cache directory

```typescript
const home = process.platform === "win32" ? process.env.USERPROFILE : process.env.HOME
return path.resolve(`${home}`, `.cache`, `golangci-lint`)
```

| Platform | Path |
|----------|------|
| Windows | `%USERPROFILE%\.cache\golangci-lint` |
| All others | `~/.cache/golangci-lint` |

### 5.2 Cache key construction

**Interval bucket** (from `cache-invalidation-interval` input, parsed as integer):
- If N ≤ 0: bucket = `${now.getTime()}` — this is **milliseconds** since epoch (not seconds).
- If N > 0: bucket = `Math.floor(now.getTime() / 1000 / (N * 86400))` — integer days since epoch divided into N-day buckets.

**go.mod checksum:**
- SHA-1 hash of `go.mod` bytes (hex string), where go.mod path = `path.join(workingDirectory, "go.mod")`.
- If `go.mod` absent: use the string `"nogomod"`.

**Key assembly:**
```
golangci-lint.cache-${RUNNER_OS}-${workingDirectory}-${intervalBucket}-
```
where `workingDirectory` is the raw input (empty string if not set). Then append SHA1 or `"nogomod"`.

**Array construction order (critical):**
```typescript
const keys = []
keys.push(cacheKey)           // [0] = key WITHOUT hash suffix (restore key)
// ... compute hash or "nogomod" ...
keys.push(cacheKey)           // [1] = key WITH hash suffix (primary key)

const primaryKey = keys.pop()     // takes element [1] = WITH hash
const restoreKeys = keys.reverse() // reverses remaining = [WITHOUT hash]
```

So:
- `primaryKey` = `golangci-lint.cache-{OS}-{wd}-{bucket}-{sha1_or_nogomod}`
- `restoreKeys[0]` = `golangci-lint.cache-{OS}-{wd}-{bucket}-` (trailing dash, no hash)

### 5.3 Cache restore (Phase 1)

**Skip conditions** (either causes early return):
1. `skip-cache` input is `true`.
2. `isValidEvent()` is false: `!(RefKey in process.env && Boolean(process.env[RefKey]))` where `RefKey = "GITHUB_REF"`.

When skipped due to invalid event, logs:
```
[warning]Event Validation Error: The event type ${GITHUB_EVENT_NAME} is not supported because it's not tied to a branch or tag ref.
```
Note: `logWarning` emits `core.info("[warning]" + message)` — **no space** between `[warning]` and the message.

**Execution order when not skipped:**
1. Build cache keys (§5.2).
2. `process.env.GOLANGCI_LINT_CACHE = getLintCacheDir()` — set **before** calling `cache.restoreCache`.
3. Validate `primaryKey` is non-empty (if empty: log warning, return).
4. `core.saveState("CACHE_KEY", primaryKey)`.
5. Call `cache.restoreCache([getLintCacheDir()], primaryKey, restoreKeys)`.
6. If no cache found (returns null/undefined): log `Cache not found for input keys: ${keys.join(", ")}`, return.
7. If found: call `core.saveState("CACHE_RESULT", matchedKey)`.
8. Log: `Restored cache for golangci-lint from key '${primaryKey}' in ${ms}ms`.

**Error handling in restoreCache:**
- `cache.ValidationError`: re-throw (fatal).
- Any other error: `core.warning(error.message)` — uses `core.warning`, NOT `core.info`.

### 5.4 Cache save (Phase 2)

**Skip conditions** (any causes early return):
1. `skip-cache` is `true` → logs `Skipping cache saving`.
2. `skip-save-cache` is `true` → logs `Skipping cache saving`.
3. `isValidEvent()` is false → logs warning (same format as §5.3).
4. `primaryKey` from state `"CACHE_KEY"` is empty → logs `[warning]Error retrieving key from state.`
5. Exact key match: `primaryKey` locale-equals matched key from state `"CACHE_RESULT"`:
   ```typescript
   cacheKey.localeCompare(key, undefined, { sensitivity: "accent" }) === 0
   ```
   → logs `Cache hit occurred on the primary key ${primaryKey}, not saving cache.`

**Execution when not skipped:**
1. Call `cache.saveCache([getLintCacheDir()], primaryKey)`.
2. On success: log `Saved cache for golangci-lint from paths '${paths}' in ${ms}ms`.

**Error handling in saveCache (three-way split):**
- `cache.ValidationError`: re-throw (fatal).
- `cache.ReserveCacheError`: `core.info(error.message)` — plain info, **no** `[warning]` prefix.
- Any other error: `core.info(`[warning] ${error.message}`)` — via `core.info` with `[warning] ` prefix (space after `]`).

---

## 6. Diff / Patch Fetching

### 6.1 Event dispatch

`fetchPatch()` is called when `only-new-issues` is true and the event check passes.

| Event name | Action |
|-----------|--------|
| `pull_request` | Fetch PR diff via `octokit.rest.pulls.get` |
| `pull_request_target` | Same as `pull_request` |
| `push` | Fetch push diff via `octokit.rest.repos.compareCommitsWithBasehead` |
| `merge_group` | Return `""` immediately (no network call) |
| Anything else | Log info message; return `""` |

Default case log:
```
Not fetching patch for showing only new issues because it's not a pull request context: event name is ${ctx.eventName}
```

### 6.2 Pull request patch

1. Read `ctx.payload.pull_request`. If missing (`!pr`):
   - `core.warning("No pull request in context")`, return `""`.
2. Create octokit client: `github.getOctokit(token, {}, pluginRetry.retry)` (retry plugin with default 5 retries).
3. Call:
   ```
   GET /repos/{owner}/{repo}/pulls/{pr.number}
   Accept: application/vnd.github.diff
   ```
4. If `status !== 200`: `core.warning("failed to fetch pull request patch: response status is ${status}")`, return `""`.
5. Cast `patchResp.data` to string.
6. Create temp dir via `promisify(dir)()` (from `tmp` package).
7. Write to `path.join(tempDir, "pull.patch")`.
8. Content written = `alterDiffPatch(patch)` (working-directory transformation applied before write).
9. Log: `Writing patch to ${patchPath}`.
10. Return `patchPath`, or `""` on any write error.

**Network error:** `console.warn("failed to fetch pull request patch:", err)`, return `""`.
**Write error:** `console.warn("failed to save pull request patch:", err)`, return `""`.

### 6.3 Push patch

1. Create octokit client (same as §6.2).
2. Call:
   ```
   GET /repos/{owner}/{repo}/compare/${ctx.payload.before}...${ctx.payload.after}
   Accept: application/vnd.github.diff
   ```
   basehead = `` `${ctx.payload.before}...${ctx.payload.after}` ``
3. If `status !== 200`: `core.warning("failed to fetch push patch: response status is ${status}")`, return `""`.
4. Write to `path.join(tempDir, "push.patch")` (different filename from PR patch).
5. Content written = `alterDiffPatch(patch)`.
6. Return `patchPath`, or `""` on error.

**Network error:** `console.warn("failed to fetch push patch:", err)`, return `""`.
**Write error:** `console.warn("failed to save pull request patch:", err)`, return `""`.
Note: the write-error message says "pull request patch" even for push patches (source bug).

### 6.4 Patch path transformation (`alterDiffPatch`)

If `working-directory` input is empty, returns patch unchanged.

Otherwise, `alterPatchWithWorkingDirectory(patch, workingDirectory)`:

1. `wd = path.relative(process.env["GITHUB_WORKSPACE"] || "", workingDirectory)`.
2. Split patch on `"\n"`.
3. Walk lines; maintain `ignore` flag (starts `false`).
4. For `diff --git` lines:
   - `ignore = !line.includes(` a/${wd}/`)` — check for literal space + `a/` prefix.
   - If `ignore`: skip line.
   - If not ignored: append `line.replaceAll(firstLine, "$1$2")` where:
     ```
     firstLine = new RegExp(`( [ab]\\/)${escapeRegExp(wd)}\\/(.*)`, "gm")
     ```
5. For other lines:
   - If `ignore`: skip.
   - Else: append `line.replaceAll(cleanDiff, "$1$2")` where:
     ```
     cleanDiff = new RegExp(`^((?:\\+{3}|-{3}) [ab]\\/)${escapeRegExp(wd)}\\/(.*)`, "gm")
     ```
6. Join filtered lines with `"\n"` (LF only — not CRLF, not OS-dependent).

**escapeRegExp:**
```typescript
exp.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
```

---

## 7. Lint Execution

### 7.1 Argument processing

```typescript
const userArgsList = userArgs
  .trim()
  .split(/\s+/)
  .filter(arg => arg.startsWith(`-`))
  .map(arg => arg.replace(/^-+/, ``))
  .map(arg => arg.split(/=(.*)/, 2))
  .map<[string, string]>(([key, value]) => [key.toLowerCase(), value ?? ""])
```

Result structures:
- `userArgsMap = new Map(userArgsList)` — lowercased key → value
- `userArgNames = new Set(userArgsList.map(([key]) => key))` — lowercased keys only

The raw `userArgs` string (unparsed) is appended to the final command as-is.

### 7.2 only-new-issues processing

Runs when `only-new-issues` is `true`.

**Pre-check:** If `userArgNames` contains any of `new`, `new-from-rev`, `new-from-patch`,
or `new-from-merge-base`, throw:
```
please, don't specify manually --new* args when requesting only new issues
```

**Fetch patch** (always called for non-merge_group events):
```typescript
const patchPath = await fetchPatch()
core.info(`only new issues on ${ctx.eventName}: ${patchPath}`)
```

**Arg injection (switch on event name):**

For `pull_request`, `pull_request_target`, `push` — all 4 args injected **only if `patchPath` is non-empty**:
```typescript
if (patchPath) {
  addedArgs.push(`--new-from-patch=${patchPath}`)
  addedArgs.push(`--new=false`)
  addedArgs.push(`--new-from-rev=`)
  addedArgs.push(`--new-from-merge-base=`)
}
```
If patch fetch returned `""`, **none** of the args are added.

For `merge_group` — injected **unconditionally** (no patchPath check):
```typescript
addedArgs.push(`--new-from-rev=${ctx.payload.merge_group.base_sha}`)
addedArgs.push(`--new=false`)
addedArgs.push(`--new-from-patch=`)
addedArgs.push(`--new-from-merge-base=`)
```

For any other event: no args injected (switch falls to `default: break`).

### 7.3 Working directory and path mode

If `rootDir` (from `getWorkingDirectory()`) is non-empty:
- If `userArgNames` does NOT contain `"path-prefix"` AND does NOT contain `"path-mode"`:
  - Inject `--path-mode=abs` into `addedArgs`.
- Set `cmdArgs.cwd = path.resolve(rootDir)`.

`getWorkingDirectory()` validates: `fs.existsSync(workingDirectory) && fs.lstatSync(workingDirectory).isDirectory()`.
If validation fails: throw `working-directory (${workingDirectory}) was not a path`.

### 7.4 Config verification

Runs when `verify` input is `true`.

1. `getConfigPath(binPath, userArgsMap, cmdArgs)`:
   - Build command: `${binPath} config path` + optionally ` --config=${userArgsMap.get("config")}`.
   - Log: `Running [${cmd}] in [${cwd}] ...`
   - Execute; return `resPath.stderr.trim()` (reads **stderr**, not stdout).
   - On any error: return `""`.
2. If config path is empty: skip verification.
3. Build verify command: `${binPath} config verify` + optionally ` --config=${userArgsMap.get("config")}`.
   - The config key lookup uses lowercase key `"config"`.
4. Log: `Running [${cmdVerify}] in [${cwd}] ...`
5. Execute; print stdout and stderr via `core.info`.

### 7.5 Lint command construction and execution

**Command assembly:**
```typescript
const cmd = `${binPath} run ${addedArgs.join(` `)} ${userArgs}`.trimEnd()
```
Injected args come first, user args last. Final command is right-trimmed.

**Execution:**
- Log: `Running [${cmd}] in [${cmdArgs.cwd || process.cwd()}] ...`
- Record start timestamp.
- Execute command; both stdout and stderr printed via `core.info`.
- Always log (in `finally`): `Ran golangci-lint in ${ms}ms`

**Exit code handling:**
| Exit code | Behavior |
|-----------|----------|
| `0` | Log `golangci-lint found no issues` |
| `1` | `core.setFailed("issues found")` |
| any other | `core.setFailed(`golangci-lint exit with code ${exc.code}`)` |

### 7.6 Automatic module detection (experimental)

Activated when `experimental` input, split on `","`, includes `"automatic-module-directories"`.

Called with `workingDirectory` (may be empty string if not configured).

```typescript
const o: fs.GlobOptions = {
  cwd: rootDir,
  exclude: ["**/vendor/**", "**/node_modules/**", "**/.git/**", "**/dist/**"],
}
const matches = fs.globSync("**/go.mod", o)

const dirs = matches
  .filter(m => typeof m === "string")
  .map(m => path.resolve(rootDir, path.dirname(m)))
  .sort()

return [...new Set(dirs)]
```

For each detected directory `wd`:
```typescript
await core.group(`run golangci-lint in ${path.relative(cwd, wd)}`, () => runGolangciLint(binPath, wd))
```
where `cwd = process.cwd()`.

When experimental flag is NOT set, single run:
```typescript
await core.group(`run golangci-lint`, () => runGolangciLint(binPath, workingDirectory))
```

### 7.7 Debug commands

If `debug` input (split on `","`) includes `"clean"`:
- Log: `Running [${binPath} cache clean] ...`
- Execute `${binPath} cache clean`.

If `debug` input includes `"cache"`:
- Log: `Running [${binPath} cache status] ...`
- Execute `${binPath} cache status`.

Both commands' stdout and stderr are printed via `core.info`.
Debug runs before the `install-only` short-circuit check.

---

## 8. State Persistence Between Phases

| Constant | Value | State key name |
|----------|-------|----------------|
| `State.CachePrimaryKey` | `"CACHE_KEY"` | Written at Phase 1 restore; read at Phase 2 save |
| `State.CacheMatchedKey` | `"CACHE_RESULT"` | Written when cache hit in Phase 1; read at Phase 2 save |

Other constants:
- `Events.Key = "GITHUB_EVENT_NAME"`
- `RefKey = "GITHUB_REF"`

---

## 9. Environment Variables

### Read by the action

| Variable | Used for |
|----------|----------|
| `GITHUB_REF` | Determines whether event is valid for caching (must be present and non-empty) |
| `GITHUB_WORKSPACE` | Base path for computing relative working-directory in diff patch rewriting |
| `GITHUB_EVENT_NAME` | Event type for patch fetch dispatch and only-new-issues logic |
| `RUNNER_OS` | Component of the cache key |
| `HOME` (Unix) / `USERPROFILE` (Windows) | Base path of the lint cache directory |

### Written by the action

| Variable | Value | When set |
|----------|-------|----------|
| `GOLANGCI_LINT_CACHE` | `~/.cache/golangci-lint` (or Windows equivalent) | In `restoreCache()`, **before** `cache.restoreCache` is called |
| `CGO_ENABLED` | `"1"` | Set in child process env during `goinstall` (not in parent process) |

---

## 10. External HTTP Endpoints

| URL | Method | Purpose | Retry |
|-----|--------|---------|-------|
| `https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/assets/github-action-config-v2.json` | GET | Version mapping lookup | Yes: `allowRetries: true, maxRetries: 5` |
| `https://github.com/golangci/golangci-lint/releases/download/{TargetVersion}/golangci-lint-{noV}-{os}-{arch}.{ext}` | GET | Binary download | Via `@actions/tool-cache` |
| `https://api.github.com/repos/{owner}/{repo}/pulls/{number}` | GET | PR diff (diff media type) | `@octokit/plugin-retry` (5 retries) |
| `https://api.github.com/repos/{owner}/{repo}/compare/{before}...{after}` | GET | Push diff (diff media type) | `@octokit/plugin-retry` (5 retries) |

---

## 11. Complete Execution Flow

```
Phase 1: run()
  try {
    group("Restore cache") → restoreCache()
      skip if skip-cache=true or GITHUB_REF absent/empty
      buildCacheKeys()
      process.env.GOLANGCI_LINT_CACHE = getLintCacheDir()  ← SET HERE, before restore
      core.saveState("CACHE_KEY", primaryKey)
      cache.restoreCache(...)
        → on hit: core.saveState("CACHE_RESULT", matchedKey)

    group("Install") → install().then(plugins.install)
      install():
        if problem-matchers=true: core.info("##[add-matcher]{path}")
        mode = install-mode.toLowerCase()
        if mode="none": which() or throw
        getVersion(mode)
          if goinstall: return { TargetVersion: raw version input or "latest" }
          else: parse, validate, maybe fetch mapping
        installBinary(versionInfo, mode)
          if binary: downloadURL → extract → return binPath
          if goinstall: go install → go install -n → parse stderr for "touch "
      plugins.install(binPath):
        rootDir = working-directory resolved, or process.cwd()
        search for .custom-gcl.{yml,yaml,json}
        if found: run `{binPath} custom`, return new path
        else: return binPath unchanged

    core.addPath(path.dirname(binPath))  ← uses plugin-adjusted path

    if debug non-empty:
      group("Debug") → debugAction(binPath)

    if install-only=true: return

    runLint(binPath):
      workingDirectory = getWorkingDirectory()  ← validates existence
      if experimental includes "automatic-module-directories":
        dirs = modulesAutoDetection(workingDirectory)
        for wd in dirs: group(`run golangci-lint in {rel}`) → runGolangciLint(binPath, wd)
      else:
        group("run golangci-lint") → runGolangciLint(binPath, workingDirectory)

  } catch(error) {
    core.error(`Failed to run: ${error}, ${error.stack}`)
    core.setFailed(error.message)
  }

Phase 2: postRun()
  try {
    saveCache()
      skip if skip-cache, skip-save-cache, invalid event, no key, or exact key match
      cache.saveCache(...)
  } catch(error) {
    core.error(`Failed to post-run: ${error}, ${error.stack}`)
    core.setFailed(error.message)
  }
```

---

## 12. Error Propagation Rules

| Situation | Effect |
|-----------|--------|
| Version string bad format | Fatal: throw (caught by top-level handler) |
| Version wrong major (≠2) | Fatal: throw |
| Version below minimum | Fatal: throw |
| Version mapping fetch fails | Fatal: throw (wrapped in "failed to get action config: ...") |
| MinorVersionToConfig missing | Fatal: throw after warning |
| Version key not in mapping | Fatal: throw |
| Binary download fails | Fatal (tool-cache throws) |
| goinstall fails | Fatal (exec throws) |
| `which` returns null (mode=none) | Fatal: throw |
| version-file path not found | Fatal: throw |
| Plugin build fails | Fatal: throw (re-wrapped) |
| `config verify` fails | Fatal (exec throws, propagates to top-level catch) |
| Working directory not found | Fatal: throw `working-directory (${path}) was not a path` |
| Lint exit code = 1 | `core.setFailed("issues found")` — NOT a thrown exception |
| Lint exit code ≠ 0, 1 | `core.setFailed(`golangci-lint exit with code ${code}`)` — NOT a thrown exception |
| only-new-issues + user --new* args | Fatal: throw |
| PR has no pull_request in payload | Non-fatal: `core.warning`, return `""` |
| Patch fetch: HTTP status ≠ 200 | Non-fatal: `core.warning`, return `""` |
| Patch fetch: network error | Non-fatal: `console.warn`, return `""` |
| Patch file write error | Non-fatal: `console.warn`, return `""` |
| Cache `ValidationError` (restore) | Fatal: re-throw |
| Cache other error (restore) | Non-fatal: `core.warning(error.message)` |
| Cache `ValidationError` (save) | Fatal: re-throw |
| Cache `ReserveCacheError` (save) | Non-fatal: `core.info(error.message)` (no [warning] prefix) |
| Cache other error (save) | Non-fatal: `core.info(`[warning] ${error.message}`)` |

---

## 13. Exact String Constants

### Log messages (exact format)

| Location | Message |
|----------|---------|
| version.ts | `Finding needed golangci-lint version...` |
| version.ts | `Found golangci-lint version '${v}' in '${goMod}' file` |
| version.ts | `Requested golangci-lint '${minor}', using '${target}', calculation took ${ms}ms` |
| install.ts | `Installing golangci-lint ${TargetVersion}...` (goinstall) |
| install.ts | `Installing golangci-lint binary ${TargetVersion}...` (binary) |
| install.ts | `Downloading binary ${assetURL} ...` |
| install.ts | `Installed golangci-lint into ${binPath} in ${ms}ms` |
| install.ts | `##[add-matcher]${matchersPath}` (via core.info) |
| plugins.ts | `Found configuration for the plugin module system : ${configFile}` (space before `:`) |
| plugins.ts | `Building and installing custom golangci-lint binary...` |
| plugins.ts | `Creating destination directory: ${config.destination}` |
| plugins.ts | `Running [${cmd}] in [${rootDir}] ...` |
| plugins.ts | `Built custom golangci-lint binary in ${ms}ms` |
| cache.ts | `Checking for go.mod: ${goModPath}` |
| cache.ts | `Skipping cache restoration` |
| cache.ts | `Skipping cache saving` |
| cache.ts | `Cache not found for input keys: ${keys.join(", ")}` |
| cache.ts | `Restored cache for golangci-lint from key '${primaryKey}' in ${ms}ms` |
| cache.ts | `Saved cache for golangci-lint from paths '${paths}' in ${ms}ms` |
| cache.ts | `Cache hit occurred on the primary key ${primaryKey}, not saving cache.` |
| patch.ts | `Writing patch to ${patchPath}` |
| run.ts | `Running [${cmd}] in [${cwd}] ...` |
| run.ts | `golangci-lint found no issues` |
| run.ts | `Ran golangci-lint in ${ms}ms` |
| run.ts | `only new issues on ${eventName}: ${patchPath}` |
| run.ts | `Failed to run: ${error}, ${error.stack}` (via core.error) |
| run.ts | `Failed to post-run: ${error}, ${error.stack}` (via core.error) |

### logWarning format

```typescript
const warningPrefix = "[warning]"
core.info(`${warningPrefix}${message}`)
```
No space between `[warning]` and the message body.

---

## 14. Verifiable Behaviors (Test Anchors)

### Version resolution

1. **go.mod detection:** Given `go.mod` containing `github.com/golangci/golangci-lint/v2 v2.3.1` (with a single space before the version), `version` input unset, `version-file` unset → resolves to `TargetVersion = "v2.3.1"`.

2. **go.mod in working-directory:** Given `working-directory = "subdir"` and `subdir/go.mod` containing the module line → resolves from `subdir/go.mod`, not root `go.mod`.

3. **tool-versions file:** Given `version-file = ".tool-versions"` and file content `golangci-lint 2.3.1 # comment`, result = `"v2.3.1"` (strips trailing comment, strips trailing space, prepends `v`).

4. **tool-versions strips leading v:** Given content `golangci-lint v2.3.1`, result = `"v2.3.1"` (not `"vv2.3.1"`).

5. **golangci-lint-version file:** Given `version-file = ".golangci-lint-version"` and content `2.3.1\n`, result = `"v2.3.1"`.

6. **version input wins over file:** Given `version = "v2.3"` and `version-file = ".tool-versions"` → warning logged containing both values; `v2.3.*` used.

7. **Both-specified warning exact text:** Warning message matches `Both version (v2.3) and version-file (.tool-versions) inputs are specified, only version will be used`.

8. **version-file absent throws:** Given `version-file = ".missing"` and file does not exist → fatal error message: `The specified golangci-lint version file at: .missing does not exist`.

9. **Bad format error:** Given `version = "2.3"` (no leading v) → fatal, message contains `expected format v1.2 or v1.2.3`.

10. **Wrong major error:** Given `version = "v1.50.0"` → fatal, message contains `golangci-lint v1 is not supported by golangci-lint-action >= v7.` (with trailing period).

11. **Below minimum error:** Given `version = "v2.0.0"` → fatal, message contains `v2.1 and later versions`. Note: `isLessVersion` compares only major then minor — patch is not compared.

12. **isLessVersion patch ignored:** Given `version = "v2.1.0"` and minimum is `{major:2, minor:1}` → NOT rejected (v2.1.0 is not less than v2.1.x since minor is equal).

13. **Exact triplet skips network:** Given `version = "v2.3.4"` → no HTTP request to version mapping URL; `TargetVersion = "v2.3.4"`.

14. **Minor-only triggers network:** Given `version = "v2.3"` → HTTP GET to `raw.githubusercontent.com/.../github-action-config-v2.json`.

15. **"latest" triggers network:** Given `version = ""` with no go.mod → resolves to `null` → HTTP GET to version mapping URL with key `"latest"`.

16. **goInstall uses raw input:** Given `install-mode = "goinstall"` and `version = "v2.3"` → `TargetVersion = "v2.3"` verbatim, no network call, no parsing.

17. **goInstall with empty version:** Given `install-mode = "goinstall"` and `version = ""` → `TargetVersion = "latest"`.

### Binary installation

18. **Download URL construction:** Given `os.platform() = "linux"`, `os.arch() = "x64"`, `TargetVersion = "v2.3.4"` → URL = `https://github.com/golangci/golangci-lint/releases/download/v2.3.4/golangci-lint-2.3.4-linux-amd64.tar.gz`.

19. **Windows uses zip:** Given `os.platform() = "win32"` → URL ends with `.zip`, extraction uses `tc.extractZip`.

20. **macOS omits --overwrite:** Given `process.platform = "darwin"` → tar extraction args = `["xz"]` only (no `"--overwrite"`).

21. **Linux includes --overwrite:** Given `process.platform = "linux"` → tar extraction args = `["xz", "--overwrite"]`.

22. **Binary path uses URL basename:** Given URL `…/golangci-lint-2.3.4-linux-amd64.tar.gz` → dirName = `"golangci-lint-2.3.4-linux-amd64"`, binPath ends with `/golangci-lint-2.3.4-linux-amd64/golangci-lint`.

23. **mode=none throws exact message:** When `golangci-lint` not in PATH → fatal with `golangci-lint binary not found in the PATH`.

24. **Problem matcher via core.info:** Given `problem-matchers = true` → annotation `##[add-matcher]{path}` emitted via `core.info`, not written directly to stdout/stderr.

25. **Problem matcher owner:** The registered problem matcher JSON has `"owner": "golangci-lint-colored-line-number"`.

26. **Problem matcher regexp:** Exactly `^([^:]+):(\d+):(?:(\d+):)?\s+(.+ \(.+\))$`.

### Cache

27. **Cache key includes RUNNER_OS:** Cache primary key string contains the literal value of `RUNNER_OS` env var.

28. **Interval key is milliseconds when ≤0:** Given `cache-invalidation-interval = "0"`, the interval bucket = `new Date().getTime()` (milliseconds since epoch, not seconds).

29. **Interval key is day-bucket when >0:** Given `cache-invalidation-interval = "7"`, bucket = `Math.floor(now_ms / 1000 / (7 * 86400))`.

30. **Two runs 7 days apart produce different keys:** Day-bucket changes every 7 days.

31. **Primary key has hash:** Primary key ends with the SHA-1 hex of `go.mod` when `go.mod` exists.

32. **Restore key has no hash:** Fallback restore key ends with the trailing `-` after the bucket (before the hash would go), when a go.mod exists.

33. **No go.mod key:** When `go.mod` absent, both primary and restore keys use `nogomod` suffix.

34. **GOLANGCI_LINT_CACHE set before restore call:** `process.env.GOLANGCI_LINT_CACHE` is set to the lint cache dir path before `cache.restoreCache()` is invoked.

35. **skip-cache disables both phases:** With `skip-cache = true`, neither `cache.restoreCache` nor `cache.saveCache` is called.

36. **skip-save-cache allows restore:** With `skip-save-cache = true`, restore runs but save is skipped.

37. **Exact key match skips save:** When Phase 1 matched key equals primary key (locale-insensitive), Phase 2 skips saving.

38. **saveCache ReserveCacheError is plain info:** On `ReserveCacheError`, `core.info(error.message)` is called (no `[warning]` prefix).

39. **saveCache other error uses [warning] prefix:** On other errors, `core.info("[warning] " + error.message)` is called.

40. **restoreCache other error uses core.warning:** On non-ValidationError in restore, `core.warning(error.message)` is called.

### Patch and only-new-issues

41. **PR patch written to pull.patch:** PR patch file is `path.join(tempDir, "pull.patch")`.

42. **Push patch written to push.patch:** Push patch file is `path.join(tempDir, "push.patch")`.

43. **Push basehead format:** Basehead string is `` `${before}...${after}` `` (three dots).

44. **alterDiffPatch applied before write:** File content is `alterDiffPatch(rawPatch)`, not the raw patch.

45. **diffUtils joins with LF:** `filteredLines.join("\n")` — output uses LF line endings regardless of OS.

46. **diffUtils filters by ` a/${wd}/`:** A `diff --git` line is ignored if it does not contain ` a/${wd}/` (space then `a/` then working-directory relative path then `/`).

47. **No patchPath → no PR/push args:** When patch fetch returns `""` for a PR event, NONE of `--new-from-patch`, `--new=false`, `--new-from-rev=`, `--new-from-merge-base=` are injected.

48. **merge_group args unconditional:** For `merge_group` event, `--new-from-rev={base_sha}`, `--new=false`, `--new-from-patch=`, `--new-from-merge-base=` are always injected (no patch fetch needed).

49. **only-new-issues conflict check:** If `args` contains `--new-from-rev=abc` and `only-new-issues = true` → fatal before any patch fetch.

50. **logWarning no space:** `logWarning("foo")` produces `core.info("[warning]foo")` — no space between `]` and `f`.

### Lint execution

51. **install-only skips lint:** With `install-only = true`, `runGolangciLint` is never called.

52. **working-directory injects --path-mode=abs:** When `working-directory` is set and `args` does not include `--path-mode` or `--path-prefix` → `--path-mode=abs` prepended to lint command.

53. **getConfigPath reads stderr:** `golangci-lint config path` stdout is discarded; `resPath.stderr.trim()` is the config file path.

54. **config flag lookup lowercase:** `userArgsMap.get("config")` uses lowercase key — `--Config=x` in `args` IS recognized.

55. **Lint exit code 1:** `core.setFailed("issues found")` called with exactly that string.

56. **Lint exit code 2:** `core.setFailed("golangci-lint exit with code 2")`.

57. **Lint exit code 0:** `"golangci-lint found no issues"` logged via `core.info`.

58. **Lint timing always logged:** `"Ran golangci-lint in ${ms}ms"` logged in `finally` block regardless of exit code.

59. **Auto-module group name:** For each module dir `wd`, group name = `` `run golangci-lint in ${path.relative(process.cwd(), wd)}` ``.

60. **Single run group name:** `"run golangci-lint"` (exact literal string).

61. **Auto-module excludes vendor:** `go.mod` files under `**/vendor/**`, `**/node_modules/**`, `**/.git/**`, `**/dist/**` are excluded.

62. **Auto-module deduplication:** Returned dirs are sorted then deduplicated via `Set`.

63. **plugins rootDir defaults to cwd:** When `working-directory` input is empty, `rootDir = process.cwd()` in plugin installer.

64. **plugins return path uses rootDir:** Returned path = `path.join(rootDir, config.destination, config.name)`.
