// Package lintver provides pure functions for parsing, validating, and
// resolving golangci-lint version strings.  No I/O is performed directly;
// all filesystem and network access is injected by callers.
package lintver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// InstallMode constants define the recognised installation strategies.
// Values are lowercase strings that match the install-mode action input.
const (
	// ModeBinary downloads a prebuilt release archive.
	ModeBinary InstallMode = "binary"
	// ModeGoInstall runs "go install" to build from source.
	ModeGoInstall InstallMode = "goinstall"
	// ModeNone expects golangci-lint to already be present in PATH.
	ModeNone InstallMode = "none"
)

// InstallMode is the requested binary installation strategy.
type InstallMode string

// Version holds the parsed components of a golangci-lint version string.
// Patch is nil when only major.minor was specified.
type Version struct {
	Major int
	Minor int
	Patch *int
}

// VersionInfo is the resolved version returned after a mapping lookup.
type VersionInfo struct {
	TargetVersion string `json:"TargetVersion"`
	Error         string `json:"Error"`
}

// ParseVersion parses a golangci-lint version string per spec §3.1.
// Returns nil for "" or "latest".  Returns an error for any other
// string that does not match the expected format or uses a wrong major.
func ParseVersion(s string) (*Version, error) {
	if s == "" || s == "latest" {
		return nil, nil //nolint:nilnil // absent version is not an error, caller treats nil as "latest"
	}
	re := regexp.MustCompile(`^v(\d+)\.(\d+)(?:\.(\d+))?$`)
	m := re.FindStringSubmatch(s)
	if m == nil {
		return nil, fmt.Errorf("invalid version string '%s', expected format v1.2 or v1.2.3", s)
	}
	if m[1] != "2" {
		// Spec §3.1: trailing period is required verbatim in this exact message.
		errMsg := "invalid version string '" + s + "', golangci-lint v" + m[1] +
			" is not supported by golangci-lint-action >= v7."
		return nil, errors.New(errMsg)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	v := &Version{Major: major, Minor: minor}
	if m[3] != "" {
		p, _ := strconv.Atoi(m[3])
		v.Patch = &p
	}
	return v, nil
}

// StringifyVersion converts a Version to "vMajor.Minor" or "vMajor.Minor.Patch".
// Returns "latest" for nil.
func StringifyVersion(v *Version) string {
	if v == nil {
		return "latest"
	}
	if v.Patch != nil {
		return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, *v.Patch)
	}
	return fmt.Sprintf("v%d.%d", v.Major, v.Minor)
}

// IsLessVersion returns true when a is strictly less than b.
// Comparison uses major then minor only; patch is intentionally ignored
// (this matches the TypeScript source which notes this explicitly).
func IsLessVersion(a, b *Version) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Major != b.Major {
		return a.Major < b.Major
	}
	return a.Minor < b.Minor
}

// RequestedVersion determines the requested version from configured inputs.
// Priority (spec §3.2): version input → go.mod → version-file → nil (latest).
// readFile and exists are injected for filesystem access so tests can avoid
// real I/O.
func RequestedVersion(
	versionInput, versionFileInput, workingDir string,
	readFile func(path string) ([]byte, error),
	exists func(path string) bool,
	warnf func(format string, args ...any),
	infof func(format string, args ...any),
) (*Version, error) {
	if versionInput != "" {
		if versionFileInput != "" {
			warnf(
				"Both version (%s) and version-file (%s) inputs are specified, only version will be used",
				versionInput,
				versionFileInput,
			)
		}
		return ParseVersion(versionInput)
	}

	// Step 2: scan go.mod.
	v, err := fromGoMod(workingDir, readFile, infof)
	if err != nil {
		return nil, err
	}
	if v != nil {
		return v, nil
	}

	// Step 3: version-file input.
	if versionFileInput == "" {
		return nil, nil //nolint:nilnil // no version input means "latest", not an error
	}
	return fromVersionFile(versionFileInput, workingDir, readFile, exists)
}

// GetVersion resolves the final VersionInfo from the requested version.
// For goinstall mode the raw version input is returned verbatim without
// parsing or validation.  For binary/none mode a mapping lookup may be
// performed.  Spec §3.3, §3.4.
func GetVersion(
	ctx context.Context,
	mode InstallMode,
	versionInput string,
	requested *Version,
	fetchMapping func(ctx context.Context) (map[string]VersionInfo, error),
	infof func(format string, args ...any),
) (VersionInfo, error) {
	// goinstall: use raw input verbatim; no parse, no min check, no network.
	if mode == ModeGoInstall {
		target := versionInput
		if target == "" {
			target = "latest"
		}
		return VersionInfo{TargetVersion: target}, nil
	}

	// Minimum version check (applies to binary and none).
	if requested != nil {
		minVersion := &Version{Major: 2, Minor: 1}
		if IsLessVersion(requested, minVersion) {
			return VersionInfo{}, fmt.Errorf(
				"requested golangci-lint version '%s' isn't supported: we support only v2.1 and later versions",
				StringifyVersion(requested),
			)
		}
	}

	// Exact triplet: skip network.
	if isExactTriplet(requested) {
		return VersionInfo{TargetVersion: StringifyVersion(requested)}, nil
	}

	// Fetch version mapping.
	mapping, err := fetchMapping(ctx)
	if err != nil {
		return VersionInfo{}, fmt.Errorf("failed to get action config: %w", err)
	}

	key := StringifyVersion(requested)
	info, ok := mapping[key]
	if !ok {
		return VersionInfo{}, fmt.Errorf(
			"requested golangci-lint version '%s' doesn't exist",
			key,
		)
	}
	if info.Error != "" {
		return VersionInfo{}, fmt.Errorf(
			"failed to use requested golangci-lint version '%s': %s",
			key, info.Error,
		)
	}
	// infof is used to log the timing message in the production path.
	_ = infof
	return info, nil
}

// ParseVersionMapping parses the version mapping JSON returned by the GitHub
// raw content URL.  Returns an error when the MinorVersionToConfig field is
// absent.  Spec §3.4.
func ParseVersionMapping(body []byte) (map[string]VersionInfo, error) {
	const op = "lintver.ParseVersionMapping"
	var raw struct {
		MinorVersionToConfig map[string]VersionInfo `json:"MinorVersionToConfig"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	if raw.MinorVersionToConfig == nil {
		return nil, fmt.Errorf("%s: invalid config: no MinorVersionToConfig field", op)
	}
	return raw.MinorVersionToConfig, nil
}

// fromGoMod attempts to extract a version from go.mod.  Returns (nil, nil)
// when go.mod is absent or contains no golangci-lint require directive.
func fromGoMod(
	workingDir string,
	readFile func(string) ([]byte, error),
	infof func(format string, args ...any),
) (*Version, error) {
	goModPath := "go.mod"
	if workingDir != "" {
		goModPath = filepath.Join(workingDir, "go.mod")
	}
	data, err := readFile(goModPath)
	if err != nil {
		// go.mod not present or not readable — not fatal, try next source.
		return nil, nil //nolint:nilnil,nilerr // absent go.mod is not an error; intentionally discarding read error
	}
	re := regexp.MustCompile(`github\.com/golangci/golangci-lint/v2\s(v\S+)`)
	m := re.FindSubmatch(data)
	if m == nil {
		return nil, nil //nolint:nilnil // no version directive found — not an error
	}
	requestedVersion := string(m[1])
	infof("Found golangci-lint version '%s' in '%s' file", requestedVersion, goModPath)
	return ParseVersion(requestedVersion)
}

// fromVersionFile reads a version-file and parses the version it contains.
func fromVersionFile(
	versionFileInput, workingDir string,
	readFile func(string) ([]byte, error),
	exists func(string) bool,
) (*Version, error) {
	versionFilePath := versionFileInput
	if workingDir != "" {
		versionFilePath = filepath.Join(workingDir, versionFileInput)
	}
	if !exists(versionFilePath) {
		// Spec §3.2: capitalized message is required verbatim.
		return nil, fmt.Errorf( //nolint:staticcheck // spec §3.2 requires capitalized message
			"The specified golangci-lint version file at: %s does not exist",
			versionFilePath,
		)
	}
	data, err := readFile(versionFilePath)
	if err != nil {
		return nil, fmt.Errorf("lintver.fromVersionFile: %w", err)
	}
	raw := parseVersionFileContent(filepath.Base(versionFilePath), data)
	if raw == "" || raw == "v" {
		return nil, nil //nolint:nilnil // empty file means no version — not an error
	}
	return ParseVersion(raw)
}

// parseVersionFileContent extracts a raw version string from file content.
func parseVersionFileContent(basename string, data []byte) string {
	if basename == ".tool-versions" {
		return parseToolVersions(data)
	}
	return "v" + stripLeadingV(strings.TrimSpace(string(data)))
}

// parseToolVersions extracts the golangci-lint version from .tool-versions content.
// The regex matches a line like: golangci-lint 2.3.1 # comment
func parseToolVersions(data []byte) string {
	re := regexp.MustCompile(`(?m)^golangci-lint\s+([^\n#]+)`)
	m := re.FindSubmatch(data)
	if m == nil {
		return ""
	}
	return "v" + stripLeadingV(strings.TrimSpace(string(m[1])))
}

// isExactTriplet reports whether v has all three version components (major.minor.patch).
func isExactTriplet(v *Version) bool {
	return v != nil && v.Patch != nil
}

// stripLeadingV removes a single leading "v" or "V" from s.
func stripLeadingV(s string) string {
	if s != "" && (s[0] == 'v' || s[0] == 'V') {
		return s[1:]
	}
	return s
}
