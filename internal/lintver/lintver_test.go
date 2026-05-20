// Package lintver_test contains black-box tests for the lintver package.
package lintver_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/StevenACoffman/golangci-lint-action-go/internal/lintver"
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

// noerr calls t.Fatalf when err is nil.
func noerr(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error (%s), got nil", msg)
	}
}

// TestParseVersion covers spec §14 anchors #9, #10, #11, #12.
func TestParseVersion(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input   string
		wantNil bool
		wantErr string // substring that must appear in err.Error()
		wantV   *lintver.Version
	}{
		"empty string": {input: "", wantNil: true},
		"latest":       {input: "latest", wantNil: true},
		// Anchor #9: bad format (no v prefix)
		"bad format no v": {
			input:   "2.3",
			wantErr: "expected format v1.2 or v1.2.3",
		},
		// Anchor #10: wrong major
		"wrong major v1": {
			input:   "v1.50.0",
			wantErr: "golangci-lint v1 is not supported by golangci-lint-action >= v7.",
		},
		"wrong major v3": {
			input:   "v3.0.0",
			wantErr: "golangci-lint v3 is not supported by golangci-lint-action >= v7.",
		},
		// Valid minor-only
		"v2.3": {
			input: "v2.3",
			wantV: &lintver.Version{Major: 2, Minor: 3},
		},
		// Valid full triplet
		"v2.3.4": {
			input: "v2.3.4",
			wantV: func() *lintver.Version {
				p := 4
				return &lintver.Version{Major: 2, Minor: 3, Patch: &p}
			}(),
		},
		// Anchor #12: v2.1.0 is valid (not below minimum)
		"v2.1.0 valid": {
			input: "v2.1.0",
			wantV: func() *lintver.Version {
				p := 0
				return &lintver.Version{Major: 2, Minor: 1, Patch: &p}
			}(),
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v, err := lintver.ParseVersion(tc.input)
			if tc.wantErr != "" {
				noerr(t, err, "expected error containing: "+tc.wantErr)
				assert(
					t,
					strings.Contains(err.Error(), tc.wantErr),
					"error does not contain expected substring\ngot:  "+err.Error()+"\nwant: "+tc.wantErr,
				)
				return
			}
			ok(t, err)
			if tc.wantNil {
				assert(t, v == nil, "expected nil Version")
				return
			}
			assert(t, v != nil, "expected non-nil Version")
			equals(t, v.Major, tc.wantV.Major)
			equals(t, v.Minor, tc.wantV.Minor)
			if tc.wantV.Patch == nil {
				assert(t, v.Patch == nil, "expected nil Patch")
			} else {
				assert(t, v.Patch != nil, "expected non-nil Patch")
				equals(t, *v.Patch, *tc.wantV.Patch)
			}
		})
	}
}

// TestIsLessVersion covers spec §14 anchor #12.
func TestIsLessVersion(t *testing.T) {
	t.Parallel()
	ptr := func(n int) *int { return &n }
	tests := map[string]struct {
		a, b *lintver.Version
		want bool
	}{
		"nil a": {a: nil, b: &lintver.Version{Major: 2, Minor: 1}, want: false},
		"nil b": {a: &lintver.Version{Major: 2, Minor: 1}, b: nil, want: false},
		"less by major": {
			a:    &lintver.Version{Major: 1, Minor: 9},
			b:    &lintver.Version{Major: 2, Minor: 0},
			want: true,
		},
		"less by minor": {
			a:    &lintver.Version{Major: 2, Minor: 0},
			b:    &lintver.Version{Major: 2, Minor: 1},
			want: true,
		},
		"equal major and minor": {
			a:    &lintver.Version{Major: 2, Minor: 1},
			b:    &lintver.Version{Major: 2, Minor: 1},
			want: false,
		},
		// Anchor #12: patch is NOT compared — v2.1.0 is NOT less than v2.1
		"patch ignored same minor": {
			a:    &lintver.Version{Major: 2, Minor: 1, Patch: ptr(0)},
			b:    &lintver.Version{Major: 2, Minor: 1},
			want: false,
		},
		"patch ignored higher patch": {
			a:    &lintver.Version{Major: 2, Minor: 1, Patch: ptr(99)},
			b:    &lintver.Version{Major: 2, Minor: 1},
			want: false,
		},
		"greater": {
			a:    &lintver.Version{Major: 2, Minor: 3},
			b:    &lintver.Version{Major: 2, Minor: 1},
			want: false,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := lintver.IsLessVersion(tc.a, tc.b)
			equals(t, got, tc.want)
		})
	}
}

// TestStringifyVersion covers the round-trip with ParseVersion.
func TestStringifyVersion(t *testing.T) {
	t.Parallel()
	ptr := func(n int) *int { return &n }
	tests := map[string]struct {
		v    *lintver.Version
		want string
	}{
		"nil":        {v: nil, want: "latest"},
		"minor only": {v: &lintver.Version{Major: 2, Minor: 3}, want: "v2.3"},
		"full triplet": {
			v:    &lintver.Version{Major: 2, Minor: 3, Patch: ptr(4)},
			want: "v2.3.4",
		},
		"zero patch": {
			v:    &lintver.Version{Major: 2, Minor: 1, Patch: ptr(0)},
			want: "v2.1.0",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := lintver.StringifyVersion(tc.v)
			equals(t, got, tc.want)
		})
	}
}

// TestRequestedVersion covers spec §14 anchors #1–#8.
func TestRequestedVersion(t *testing.T) {
	t.Parallel()

	noFile := func(_ string) ([]byte, error) { return nil, errors.New("not found") }
	alwaysExists := func(_ string) bool { return true }
	neverExists := func(_ string) bool { return false }
	noop := func(_ string, _ ...any) {}

	// Anchor #1: go.mod detection.
	t.Run("go.mod detection", func(t *testing.T) {
		t.Parallel()
		gomod := []byte("module m\n\nrequire github.com/golangci/golangci-lint/v2 v2.3.1\n")
		readFile := func(p string) ([]byte, error) {
			if p == "go.mod" {
				return gomod, nil
			}
			return nil, errors.New("not found")
		}
		v, err := lintver.RequestedVersion("", "", "", readFile, neverExists, noop, noop)
		ok(t, err)
		assert(t, v != nil, "expected non-nil version")
		equals(t, lintver.StringifyVersion(v), "v2.3.1")
	})

	// Anchor #2: go.mod in working-directory.
	t.Run("go.mod in working-directory", func(t *testing.T) {
		t.Parallel()
		gomod := []byte("require github.com/golangci/golangci-lint/v2 v2.4.0\n")
		readFile := func(p string) ([]byte, error) {
			if p == "subdir/go.mod" {
				return gomod, nil
			}
			return nil, errors.New("not found")
		}
		v, err := lintver.RequestedVersion("", "", "subdir", readFile, neverExists, noop, noop)
		ok(t, err)
		assert(t, v != nil, "expected non-nil version")
		equals(t, lintver.StringifyVersion(v), "v2.4.0")
	})

	// Anchor #3: .tool-versions with comment.
	t.Run("tool-versions strips comment", func(t *testing.T) {
		t.Parallel()
		content := []byte("golangci-lint 2.3.1 # comment\n")
		readFile := func(_ string) ([]byte, error) { return content, nil }
		v, err := lintver.RequestedVersion(
			"",
			".tool-versions",
			"",
			readFile,
			alwaysExists,
			noop,
			noop,
		)
		ok(t, err)
		assert(t, v != nil, "expected non-nil version")
		equals(t, lintver.StringifyVersion(v), "v2.3.1")
	})

	// Anchor #4: .tool-versions strips leading v.
	t.Run("tool-versions strips leading v", func(t *testing.T) {
		t.Parallel()
		content := []byte("golangci-lint v2.3.1\n")
		readFile := func(_ string) ([]byte, error) { return content, nil }
		v, err := lintver.RequestedVersion(
			"",
			".tool-versions",
			"",
			readFile,
			alwaysExists,
			noop,
			noop,
		)
		ok(t, err)
		assert(t, v != nil, "expected non-nil version")
		equals(t, lintver.StringifyVersion(v), "v2.3.1")
	})

	// Anchor #5: .golangci-lint-version file.
	t.Run("golangci-lint-version file", func(t *testing.T) {
		t.Parallel()
		content := []byte("2.3.1\n")
		readFile := func(_ string) ([]byte, error) { return content, nil }
		v, err := lintver.RequestedVersion(
			"",
			".golangci-lint-version",
			"",
			readFile,
			alwaysExists,
			noop,
			noop,
		)
		ok(t, err)
		assert(t, v != nil, "expected non-nil version")
		equals(t, lintver.StringifyVersion(v), "v2.3.1")
	})

	// Anchor #6: version input wins over version-file.
	t.Run("version input wins", func(t *testing.T) {
		t.Parallel()
		v, err := lintver.RequestedVersion(
			"v2.3",
			".tool-versions",
			"",
			noFile,
			alwaysExists,
			noop,
			noop,
		)
		ok(t, err)
		assert(t, v != nil, "expected non-nil version")
		equals(t, v.Minor, 3)
	})

	// Anchor #7: both-specified warning exact text.
	t.Run("both specified warning text", func(t *testing.T) {
		t.Parallel()
		var warnMsg string
		warnf := func(f string, args ...any) {
			if warnMsg == "" {
				warnMsg = fmt.Sprintf(f, args...)
			}
		}
		_, _ = lintver.RequestedVersion(
			"v2.3",
			".tool-versions",
			"",
			noFile,
			alwaysExists,
			warnf,
			noop,
		)
		equals(
			t,
			warnMsg,
			"Both version (v2.3) and version-file (.tool-versions) inputs are specified, only version will be used",
		)
	})

	// Anchor #8: version-file absent throws exact message.
	t.Run("version-file absent throws", func(t *testing.T) {
		t.Parallel()
		_, err := lintver.RequestedVersion("", ".missing", "", noFile, neverExists, noop, noop)
		noerr(t, err, "version-file absent")
		equals(t, err.Error(),
			"The specified golangci-lint version file at: .missing does not exist")
	})
}

// TestGetVersion covers spec §14 anchors #11, #13, #15, #16, #17.
func TestGetVersion(t *testing.T) {
	t.Parallel()

	noFetch := func(_ context.Context) (map[string]lintver.VersionInfo, error) {
		return nil, errors.New("unexpected fetch call")
	}
	noop := func(_ string, _ ...any) {}
	ptr := func(n int) *int { return &n }

	// Anchor #16: goInstall returns raw input verbatim.
	t.Run("goInstall raw input", func(t *testing.T) {
		t.Parallel()
		info, err := lintver.GetVersion(
			context.Background(), lintver.ModeGoInstall, "v2.3", nil, noFetch, noop,
		)
		ok(t, err)
		equals(t, info.TargetVersion, "v2.3")
	})

	// Anchor #17: goInstall with empty version uses "latest".
	t.Run("goInstall empty uses latest", func(t *testing.T) {
		t.Parallel()
		info, err := lintver.GetVersion(
			context.Background(), lintver.ModeGoInstall, "", nil, noFetch, noop,
		)
		ok(t, err)
		equals(t, info.TargetVersion, "latest")
	})

	// Anchor #11: below minimum version.
	t.Run("below minimum version", func(t *testing.T) {
		t.Parallel()
		v, _ := lintver.ParseVersion("v2.0.0")
		_, err := lintver.GetVersion(
			context.Background(), lintver.ModeBinary, "v2.0.0", v, noFetch, noop,
		)
		noerr(t, err, "below minimum")
		assert(t, strings.Contains(err.Error(), "v2.1 and later versions"),
			"error does not mention minimum: "+err.Error())
	})

	// Anchor #13: exact triplet skips network.
	t.Run("exact triplet skips fetch", func(t *testing.T) {
		t.Parallel()
		v := &lintver.Version{Major: 2, Minor: 3, Patch: ptr(4)}
		info, err := lintver.GetVersion(
			context.Background(), lintver.ModeBinary, "v2.3.4", v, noFetch, noop,
		)
		ok(t, err)
		equals(t, info.TargetVersion, "v2.3.4")
	})

	// Anchor #15: "latest" triggers network (nil requested).
	t.Run("latest triggers fetch", func(t *testing.T) {
		t.Parallel()
		fetched := false
		fetch := func(_ context.Context) (map[string]lintver.VersionInfo, error) {
			fetched = true
			return map[string]lintver.VersionInfo{
				"latest": {TargetVersion: "v2.9.0"},
			}, nil
		}
		info, err := lintver.GetVersion(
			context.Background(), lintver.ModeBinary, "", nil, fetch, noop,
		)
		ok(t, err)
		equals(t, info.TargetVersion, "v2.9.0")
		assert(t, fetched, "expected fetch to be called")
	})

	// Anchor #14: minor-only triggers network.
	t.Run("minor-only triggers fetch", func(t *testing.T) {
		t.Parallel()
		fetched := false
		fetch := func(_ context.Context) (map[string]lintver.VersionInfo, error) {
			fetched = true
			return map[string]lintver.VersionInfo{
				"v2.3": {TargetVersion: "v2.3.4"},
			}, nil
		}
		v := &lintver.Version{Major: 2, Minor: 3}
		info, err := lintver.GetVersion(
			context.Background(), lintver.ModeBinary, "v2.3", v, fetch, noop,
		)
		ok(t, err)
		equals(t, info.TargetVersion, "v2.3.4")
		assert(t, fetched, "expected fetch to be called")
	})
}

// TestParseVersionMapping covers spec §3.4.
func TestParseVersionMapping(t *testing.T) {
	t.Parallel()

	t.Run("valid mapping", func(t *testing.T) {
		t.Parallel()
		body := []byte(`{"MinorVersionToConfig":{"v2.3":{"TargetVersion":"v2.3.4","Error":""}}}`)
		m, err := lintver.ParseVersionMapping(body)
		ok(t, err)
		assert(t, m != nil, "expected non-nil map")
		info, ok2 := m["v2.3"]
		assert(t, ok2, "expected v2.3 key")
		equals(t, info.TargetVersion, "v2.3.4")
	})

	t.Run("missing MinorVersionToConfig", func(t *testing.T) {
		t.Parallel()
		body := []byte(`{"SomeOtherField":{}}`)
		_, err := lintver.ParseVersionMapping(body)
		noerr(t, err, "missing MinorVersionToConfig")
		assert(t, strings.Contains(err.Error(), "no MinorVersionToConfig field"),
			"unexpected error: "+err.Error())
	})
}
