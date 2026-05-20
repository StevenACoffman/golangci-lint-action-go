// Package lint provides pure functions for assembling golangci-lint arguments,
// detecting Go modules, and interpreting lint exit codes.
package lint

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// ErrExitCode is returned by InterpretExitCode for non-zero exit codes.
var ErrExitCode = errors.New("lint exit code non-zero")

// conflictKeys are the --new* flags that conflict with only-new-issues mode.
var conflictKeys = []string{ //nolint:gochecknoglobals // package-level constant set, read-only
	"new", "new-from-rev", "new-from-patch", "new-from-merge-base",
}

// errOnlyNewConflict is returned when conflicting --new* flags are found.
var errOnlyNewConflict = errors.New(
	"please, don't specify manually --new* args when requesting only new issues",
)

// excludedDirs lists directory names to skip during module auto-detection.
var excludedDirs = []string{ //nolint:gochecknoglobals // package-level constant set, read-only
	"/vendor/", "/node_modules/", "/.git/", "/dist/",
}

// UserArgs holds parsed user arguments from the `args` action input.
type UserArgs struct {
	Raw      string
	ArgMap   map[string]string // lowercased key → value ("" if no =)
	ArgNames map[string]bool   // lowercased keys
}

// ParseUserArgs parses the raw args string.
// Tokens not starting with "-" are discarded.
// Keys are stripped of leading "--" or "-", then lowercased.
// Values are everything after the first "="; "" if no "=" present.
// Spec §7.1.
func ParseUserArgs(raw string) UserArgs {
	tokens := strings.Fields(strings.TrimSpace(raw))
	argMap := make(map[string]string)
	argNames := make(map[string]bool)

	for _, tok := range tokens {
		if !strings.HasPrefix(tok, "-") {
			continue
		}
		stripped := strings.TrimLeft(tok, "-")
		key, value, _ := strings.Cut(stripped, "=")
		key = strings.ToLower(key)
		argMap[key] = value
		argNames[key] = true
	}

	return UserArgs{
		Raw:      raw,
		ArgMap:   argMap,
		ArgNames: argNames,
	}
}

// BuildLintCommand assembles the full lint command string.
// Result: strings.TrimRight(binPath+" run "+strings.Join(addedArgs," ")+" "+raw, " ")
// Spec §7.5.
func BuildLintCommand(binPath string, addedArgs []string, userArgs UserArgs) string {
	joined := strings.Join(addedArgs, " ")
	cmd := binPath + " run " + joined + " " + userArgs.Raw
	return strings.TrimRight(cmd, " ")
}

// OnlyNewIssuesArgs returns the addedArgs for only-new-issues mode.
// Returns error if userArgs contain any --new* flag (conflict check).
// merge_group: always returns 4 args unconditionally.
// pull_request/push: returns 4 args only when patchPath != "".
// Other events: returns nil.
// Spec §7.2, test anchors #47, #48, #49.
func OnlyNewIssuesArgs(
	eventName, baseSHA, patchPath string,
	userArgs UserArgs,
) ([]string, error) {
	for _, k := range conflictKeys {
		if userArgs.ArgNames[k] {
			return nil, errOnlyNewConflict
		}
	}

	switch eventName {
	case "merge_group":
		return []string{
			"--new-from-rev=" + baseSHA,
			"--new=false",
			"--new-from-patch=",
			"--new-from-merge-base=",
		}, nil
	case "pull_request", "push":
		if patchPath == "" {
			return nil, nil
		}
		return []string{
			"--new-from-patch=" + patchPath,
			"--new=false",
			"--new-from-rev=",
			"--new-from-merge-base=",
		}, nil
	default:
		return nil, nil
	}
}

// PathModeArg returns ["--path-mode=abs"] when workingDir is non-empty
// and userArgs has neither "path-prefix" nor "path-mode".
// Spec §7.3, test anchor #52.
func PathModeArg(workingDir string, userArgs UserArgs) []string {
	if workingDir == "" {
		return nil
	}
	if userArgs.ArgNames["path-prefix"] || userArgs.ArgNames["path-mode"] {
		return nil
	}
	return []string{"--path-mode=abs"}
}

// InterpretExitCode translates a lint process exit code.
// 0  → ("", nil)
// 1  → ("issues found", ErrExitCode)
// N  → ("golangci-lint exit with code N", ErrExitCode)
// Spec §7, test anchors #55–#57.
func InterpretExitCode(code int) (string, error) {
	switch code {
	case 0:
		return "", nil
	case 1:
		return "issues found", ErrExitCode
	default:
		return fmt.Sprintf("golangci-lint exit with code %d", code), ErrExitCode
	}
}

// isExcluded reports whether the given path contains an excluded directory segment.
func isExcluded(path string) bool {
	normalized := filepath.ToSlash(path)
	for _, dir := range excludedDirs {
		if strings.Contains(normalized, dir) {
			return true
		}
	}
	return false
}

// ModulesAutoDetection globs for go.mod files under rootDir,
// excluding vendor, node_modules, .git, dist directories.
// Returns deduplicated sorted absolute directory paths. Spec §7.6, anchors #61, #62.
func ModulesAutoDetection(
	rootDir string,
	glob func(pattern string) ([]string, error),
) ([]string, error) {
	const op = "lint.ModulesAutoDetection"

	pattern := rootDir + "/**/go.mod"
	matches, err := glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	seen := make(map[string]bool)
	for _, match := range matches {
		normalized := filepath.ToSlash(match)
		if isExcluded(normalized) {
			continue
		}
		dir := filepath.Dir(match)
		abs, absErr := filepath.Abs(dir)
		if absErr != nil {
			return nil, fmt.Errorf("%s: %w", op, absErr)
		}
		seen[abs] = true
	}

	result := make([]string, 0, len(seen))
	for dir := range seen {
		result = append(result, dir)
	}
	sort.Strings(result)
	return result, nil
}
