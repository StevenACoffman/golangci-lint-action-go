// Package install provides pure helper functions for resolving
// platform-specific binary installation details.  All I/O (HTTP, exec) is
// left to the caller; this package only performs mapping and string parsing.
package install

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/StevenACoffman/golangci-lint-action-go/internal/lintver"
)

// ErrNotFound is returned by FindInPath when golangci-lint is absent from PATH.
// The string matches the exact message thrown by the TypeScript source.
var ErrNotFound = errors.New("golangci-lint binary not found in the PATH")

// AssetURL builds the download URL for the given version and platform.
// targetVersion must include the leading "v" (e.g. "v2.3.4").
// Spec §4.4.
func AssetURL(targetVersion, platform, arch, ext string) string {
	noPrefix := strings.TrimPrefix(targetVersion, "v")
	return fmt.Sprintf(
		"https://github.com/golangci/golangci-lint/releases/download/%s/golangci-lint-%s-%s-%s.%s",
		targetVersion, noPrefix, platform, arch, ext,
	)
}

// ExtractBinPath returns the binary path within an extracted archive root.
// dirName = last URL segment with the archive extension stripped.
// result  = filepath.Join(extractedRoot, dirName, "golangci-lint").
// Spec §4.4.
func ExtractBinPath(assetURL, extractedRoot string) string {
	parts := strings.Split(assetURL, "/")
	last := parts[len(parts)-1]
	reZip := regexp.MustCompile(`\.zip$`)
	reTar := regexp.MustCompile(`\.tar\.gz$`)
	dirName := reTar.ReplaceAllString(reZip.ReplaceAllString(last, ""), "")
	return filepath.Join(extractedRoot, dirName, "golangci-lint")
}

// TarArgs returns the tar extraction flags for the given GOOS.
// Darwin uses ["xz"] only; all other platforms append "--overwrite".  Spec §4.4.
func TarArgs(goos string) []string {
	args := []string{"xz"}
	if goos != "darwin" {
		args = append(args, "--overwrite")
	}
	return args
}

// PlatformStrings maps GOOS and GOARCH to the golangci-lint release asset
// naming convention.  Spec §4.4.
func PlatformStrings(goos, goarch string) (platform, arch, ext string) {
	platform = goos
	ext = "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	switch goarch {
	case "arm64":
		arch = "arm64"
	case "amd64":
		arch = "amd64"
	case "386":
		arch = "386"
	default:
		arch = "amd64"
	}
	return platform, arch, ext
}

// ParseGoInstallBinPath extracts the binary path from the stderr of
// "go install -n ...".  It finds the first "touch " line, joins all such
// lines, splits on the first space, and returns element [1].
// Returns "" when no "touch " line is found.  Spec §4.3.
func ParseGoInstallBinPath(stderr string) string {
	lines := regexp.MustCompile(`\r?\n`).Split(stderr, -1)
	var parts []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "touch ") {
			parts = append(parts, trimmed)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	joined := strings.Join(parts, "")
	fields := strings.SplitN(joined, " ", 2)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

// NormalizeMode lowercases the raw install-mode input to produce an
// InstallMode value.  Spec §2 (inputs are compared case-insensitively).
func NormalizeMode(raw string) lintver.InstallMode {
	return lintver.InstallMode(strings.ToLower(strings.TrimSpace(raw)))
}

// FindInPath reports whether golangci-lint can be located via the provided
// lookPath function (which wraps exec.LookPath in production).
// Returns the absolute path and nil on success.
// Returns ("", ErrNotFound) when not found.
// Spec §4.2.
func FindInPath(lookPath func(file string) (string, error)) (string, error) {
	p, err := lookPath("golangci-lint")
	if err != nil {
		return "", ErrNotFound
	}
	return p, nil
}
