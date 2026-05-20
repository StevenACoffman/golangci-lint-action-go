// Package install_test contains black-box tests for the install package.
package install_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/StevenACoffman/golangci-lint-action-go/internal/install"
)

// assert calls t.Fatal when condition is false.
func assert(t *testing.T, condition bool, msg string) {
	t.Helper()
	if !condition {
		t.Fatal(msg)
	}
}

// equals calls t.Fatalf when got != want.
func equals[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v; want %v", got, want)
	}
}

// ok calls t.Fatalf when err is non-nil.
func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestAssetURL covers spec §14 anchors #18, #19.
func TestAssetURL(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		version, platform, arch, ext string
		want                         string
	}{
		// Anchor #18: linux/amd64 tar.gz
		"linux amd64 tar": {
			version:  "v2.3.4",
			platform: "linux",
			arch:     "amd64",
			ext:      "tar.gz",
			want:     "https://github.com/golangci/golangci-lint/releases/download/v2.3.4/golangci-lint-2.3.4-linux-amd64.tar.gz",
		},
		// Anchor #19: windows zip
		"windows arm64 zip": {
			version:  "v2.3.4",
			platform: "windows",
			arch:     "arm64",
			ext:      "zip",
			want:     "https://github.com/golangci/golangci-lint/releases/download/v2.3.4/golangci-lint-2.3.4-windows-arm64.zip",
		},
		"darwin amd64 tar": {
			version:  "v2.3.4",
			platform: "darwin",
			arch:     "amd64",
			ext:      "tar.gz",
			want:     "https://github.com/golangci/golangci-lint/releases/download/v2.3.4/golangci-lint-2.3.4-darwin-amd64.tar.gz",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := install.AssetURL(tc.version, tc.platform, tc.arch, tc.ext)
			equals(t, got, tc.want)
		})
	}
}

// TestTarArgs covers spec §14 anchors #20, #21.
func TestTarArgs(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		goos    string
		wantLen int
		hasOver bool
	}{
		// Anchor #20: macOS omits --overwrite
		"darwin": {goos: "darwin", wantLen: 1, hasOver: false},
		// Anchor #21: linux includes --overwrite
		"linux":   {goos: "linux", wantLen: 2, hasOver: true},
		"windows": {goos: "windows", wantLen: 2, hasOver: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			args := install.TarArgs(tc.goos)
			equals(t, len(args), tc.wantLen)
			equals(t, args[0], "xz")
			if tc.hasOver {
				equals(t, args[1], "--overwrite")
			}
		})
	}
}

// TestExtractBinPath covers spec §14 anchor #22.
func TestExtractBinPath(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		url      string
		root     string
		wantSufx string
	}{
		// Anchor #22: tar.gz URL
		"tar.gz": {
			url:      "https://github.com/golangci/golangci-lint/releases/download/v2.3.4/golangci-lint-2.3.4-linux-amd64.tar.gz",
			root:     "/home/runner",
			wantSufx: "golangci-lint-2.3.4-linux-amd64/golangci-lint",
		},
		"zip": {
			url:      "https://github.com/golangci/golangci-lint/releases/download/v2.3.4/golangci-lint-2.3.4-windows-amd64.zip",
			root:     "/home/runner",
			wantSufx: "golangci-lint-2.3.4-windows-amd64/golangci-lint",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := install.ExtractBinPath(tc.url, tc.root)
			assert(t, strings.HasSuffix(got, tc.wantSufx),
				"got "+got+"; want suffix "+tc.wantSufx)
		})
	}
}

// TestPlatformStrings covers spec §14 anchors #18, #19.
func TestPlatformStrings(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		goos, goarch                    string
		wantPlatform, wantArch, wantExt string
	}{
		"linux amd64": {
			goos:         "linux",
			goarch:       "amd64",
			wantPlatform: "linux",
			wantArch:     "amd64",
			wantExt:      "tar.gz",
		},
		"linux arm64": {
			goos:         "linux",
			goarch:       "arm64",
			wantPlatform: "linux",
			wantArch:     "arm64",
			wantExt:      "tar.gz",
		},
		"linux 386": {
			goos:         "linux",
			goarch:       "386",
			wantPlatform: "linux",
			wantArch:     "386",
			wantExt:      "tar.gz",
		},
		"linux x64": {
			goos:         "linux",
			goarch:       "x64",
			wantPlatform: "linux",
			wantArch:     "amd64",
			wantExt:      "tar.gz",
		},
		"darwin amd64": {
			goos:         "darwin",
			goarch:       "amd64",
			wantPlatform: "darwin",
			wantArch:     "amd64",
			wantExt:      "tar.gz",
		},
		"windows amd64": {
			goos: "windows", goarch: "amd64",
			wantPlatform: "windows", wantArch: "amd64", wantExt: "zip",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, a, e := install.PlatformStrings(tc.goos, tc.goarch)
			equals(t, p, tc.wantPlatform)
			equals(t, a, tc.wantArch)
			equals(t, e, tc.wantExt)
		})
	}
}

// TestParseGoInstallBinPath covers spec §14 anchor goinstall path parsing.
func TestParseGoInstallBinPath(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		stderr string
		want   string
	}{
		"single touch line": {
			stderr: "WORK=/tmp/go-build\ntouch /home/runner/go/bin/golangci-lint\n",
			want:   "/home/runner/go/bin/golangci-lint",
		},
		"windows crlf": {
			stderr: "WORK=/tmp/go-build\r\ntouch /home/runner/go/bin/golangci-lint\r\n",
			want:   "/home/runner/go/bin/golangci-lint",
		},
		"empty stderr": {
			stderr: "",
			want:   "",
		},
		"no touch line": {
			stderr: "WORK=/tmp\nsome other line\n",
			want:   "",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := install.ParseGoInstallBinPath(tc.stderr)
			equals(t, got, tc.want)
		})
	}
}

// TestFindInPath covers spec §14 anchor #23.
func TestFindInPath(t *testing.T) {
	t.Parallel()

	t.Run("found in PATH", func(t *testing.T) {
		t.Parallel()
		lookPath := func(_ string) (string, error) { return "/usr/bin/golangci-lint", nil }
		path, err := install.FindInPath(lookPath)
		ok(t, err)
		equals(t, path, "/usr/bin/golangci-lint")
	})

	t.Run("not found returns ErrNotFound", func(t *testing.T) {
		t.Parallel()
		lookPath := func(_ string) (string, error) { return "", errors.New("not found") }
		_, err := install.FindInPath(lookPath)
		assert(t, err != nil, "expected error")
		// Anchor #23: exact error message
		equals(t, err.Error(), "golangci-lint binary not found in the PATH")
	})
}
