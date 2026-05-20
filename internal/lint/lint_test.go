// Package lint_test contains black-box tests for the lint package.
package lint_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/StevenACoffman/golangci-lint-action-go/internal/lint"
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

// TestParseUserArgs covers mixed flags, lowercasing, and token filtering.
func TestParseUserArgs(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		raw          string
		wantKey      string
		wantVal      string
		wantPresent  bool
		discardToken string
	}{
		"double-dash key with value": {
			raw:         "--Config=myconfig.yml",
			wantKey:     "config",
			wantVal:     "myconfig.yml",
			wantPresent: true,
		},
		"single-dash flag no value": {
			raw: "-v", wantKey: "v", wantVal: "", wantPresent: true,
		},
		"double-dash flag no value": {
			raw: "--new", wantKey: "new", wantVal: "", wantPresent: true,
		},
		"token without dash discarded": {
			raw:          "sometoken --verbose",
			wantKey:      "verbose",
			wantPresent:  true,
			discardToken: "sometoken",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ua := lint.ParseUserArgs(tc.raw)
			equals(t, ua.Raw, tc.raw)
			assert(t, ua.ArgNames[tc.wantKey], "expected key "+tc.wantKey+" to be present")
			equals(t, ua.ArgMap[tc.wantKey], tc.wantVal)
			if tc.discardToken != "" {
				assert(
					t,
					!ua.ArgNames[tc.discardToken],
					"expected "+tc.discardToken+" to be discarded",
				)
			}
		})
	}
}

// TestParseUserArgsEmpty verifies empty input produces empty maps.
func TestParseUserArgsEmpty(t *testing.T) {
	t.Parallel()
	ua := lint.ParseUserArgs("")
	equals(t, len(ua.ArgNames), 0)
	equals(t, len(ua.ArgMap), 0)
}

// TestOnlyNewIssuesArgsMergeGroup covers spec anchor #48:
// merge_group always returns 4 args unconditionally.
func TestOnlyNewIssuesArgsMergeGroup(t *testing.T) {
	t.Parallel()
	ua := lint.ParseUserArgs("")
	args, err := lint.OnlyNewIssuesArgs("merge_group", "abc123", "", ua)
	ok(t, err)
	equals(t, len(args), 4)
	equals(t, args[0], "--new-from-rev=abc123")
	equals(t, args[1], "--new=false")
	equals(t, args[2], "--new-from-patch=")
	equals(t, args[3], "--new-from-merge-base=")
}

// TestOnlyNewIssuesArgsPRNoPatch covers spec anchor #47:
// pull_request with empty patchPath returns nil (no args).
func TestOnlyNewIssuesArgsPRNoPatch(t *testing.T) {
	t.Parallel()
	ua := lint.ParseUserArgs("")
	args, err := lint.OnlyNewIssuesArgs("pull_request", "abc123", "", ua)
	ok(t, err)
	assert(t, args == nil, "expected nil args when patchPath is empty")
}

// TestOnlyNewIssuesArgsPRWithPatch covers spec anchor #47:
// pull_request with a patchPath returns 4 args.
func TestOnlyNewIssuesArgsPRWithPatch(t *testing.T) {
	t.Parallel()
	ua := lint.ParseUserArgs("")
	args, err := lint.OnlyNewIssuesArgs("pull_request", "abc123", "/tmp/patch.diff", ua)
	ok(t, err)
	equals(t, len(args), 4)
	equals(t, args[0], "--new-from-patch=/tmp/patch.diff")
	equals(t, args[1], "--new=false")
	equals(t, args[2], "--new-from-rev=")
	equals(t, args[3], "--new-from-merge-base=")
}

// TestOnlyNewIssuesArgsPushWithPatch verifies push behaves same as pull_request.
func TestOnlyNewIssuesArgsPushWithPatch(t *testing.T) {
	t.Parallel()
	ua := lint.ParseUserArgs("")
	args, err := lint.OnlyNewIssuesArgs("push", "abc123", "/tmp/patch.diff", ua)
	ok(t, err)
	equals(t, len(args), 4)
	equals(t, args[0], "--new-from-patch=/tmp/patch.diff")
}

// TestOnlyNewIssuesArgsOtherEvent verifies unknown events return nil.
func TestOnlyNewIssuesArgsOtherEvent(t *testing.T) {
	t.Parallel()
	ua := lint.ParseUserArgs("")
	args, err := lint.OnlyNewIssuesArgs("schedule", "", "", ua)
	ok(t, err)
	assert(t, args == nil, "expected nil for unknown event")
}

// TestOnlyNewIssuesArgsConflict covers spec anchor #49:
// conflicting --new* flags trigger an error.
func TestOnlyNewIssuesArgsConflict(t *testing.T) {
	t.Parallel()

	conflictCases := []string{
		"--new",
		"--new-from-rev=main",
		"--new-from-patch=/tmp/x.diff",
		"--new-from-merge-base=main",
	}

	for _, raw := range conflictCases {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			ua := lint.ParseUserArgs(raw)
			_, err := lint.OnlyNewIssuesArgs("pull_request", "abc", "/tmp/patch.diff", ua)
			assert(t, err != nil, "expected conflict error for "+raw)
			assert(
				t,
				strings.Contains(err.Error(), "don't specify manually --new* args"),
				"unexpected error message: "+err.Error(),
			)
		})
	}
}

// TestPathModeArgSet covers spec anchor #52:
// returns ["--path-mode=abs"] when workingDir is non-empty and no path flags set.
func TestPathModeArgSet(t *testing.T) {
	t.Parallel()
	ua := lint.ParseUserArgs("")
	args := lint.PathModeArg("/some/dir", ua)
	equals(t, len(args), 1)
	equals(t, args[0], "--path-mode=abs")
}

// TestPathModeArgEmptyDir returns nil when workingDir is empty.
func TestPathModeArgEmptyDir(t *testing.T) {
	t.Parallel()
	ua := lint.ParseUserArgs("")
	args := lint.PathModeArg("", ua)
	assert(t, args == nil, "expected nil when workingDir is empty")
}

// TestPathModeArgPathModePresent returns nil when --path-mode is already set.
func TestPathModeArgPathModePresent(t *testing.T) {
	t.Parallel()
	ua := lint.ParseUserArgs("--path-mode=prefix")
	args := lint.PathModeArg("/some/dir", ua)
	assert(t, args == nil, "expected nil when --path-mode already set")
}

// TestPathModeArgPathPrefixPresent returns nil when --path-prefix is already set.
func TestPathModeArgPathPrefixPresent(t *testing.T) {
	t.Parallel()
	ua := lint.ParseUserArgs("--path-prefix=./")
	args := lint.PathModeArg("/some/dir", ua)
	assert(t, args == nil, "expected nil when --path-prefix already set")
}

// TestInterpretExitCodeZero covers spec anchor #55.
func TestInterpretExitCodeZero(t *testing.T) {
	t.Parallel()
	msg, err := lint.InterpretExitCode(0)
	ok(t, err)
	equals(t, msg, "")
}

// TestInterpretExitCodeOne covers spec anchor #56.
func TestInterpretExitCodeOne(t *testing.T) {
	t.Parallel()
	msg, err := lint.InterpretExitCode(1)
	assert(t, err != nil, "expected error for exit code 1")
	assert(t, errors.Is(err, lint.ErrExitCode), "expected ErrExitCode")
	equals(t, msg, "issues found")
}

// TestInterpretExitCodeOther covers spec anchor #57.
func TestInterpretExitCodeOther(t *testing.T) {
	t.Parallel()
	msg, err := lint.InterpretExitCode(5)
	assert(t, err != nil, "expected error for exit code 5")
	assert(t, errors.Is(err, lint.ErrExitCode), "expected ErrExitCode")
	equals(t, msg, "golangci-lint exit with code 5")
}

// TestBuildLintCommand verifies the assembled command contains binPath and args.
func TestBuildLintCommand(t *testing.T) {
	t.Parallel()

	t.Run("with added args and raw", func(t *testing.T) {
		t.Parallel()
		ua := lint.ParseUserArgs("--timeout=5m")
		cmd := lint.BuildLintCommand("/usr/bin/golangci-lint", []string{"--new=false"}, ua)
		assert(t, strings.Contains(cmd, "/usr/bin/golangci-lint"), "missing binPath")
		assert(t, strings.Contains(cmd, "--new=false"), "missing added arg")
		assert(t, strings.Contains(cmd, "--timeout=5m"), "missing raw arg")
		assert(t, strings.HasPrefix(cmd, "/usr/bin/golangci-lint run"), "must start with run")
	})

	t.Run("no trailing space when raw is empty", func(t *testing.T) {
		t.Parallel()
		ua := lint.ParseUserArgs("")
		cmd := lint.BuildLintCommand("/usr/bin/golangci-lint", []string{"--new=false"}, ua)
		assert(t, !strings.HasSuffix(cmd, " "), "must not end with trailing space")
	})

	t.Run("no added args", func(t *testing.T) {
		t.Parallel()
		ua := lint.ParseUserArgs("--timeout=5m")
		cmd := lint.BuildLintCommand("/bin/golangci-lint", nil, ua)
		assert(t, strings.Contains(cmd, "--timeout=5m"), "missing raw arg")
	})
}

// TestModulesAutoDetectionExclusion covers spec anchor #61:
// vendor, node_modules, .git, dist paths are excluded.
func TestModulesAutoDetectionExclusion(t *testing.T) {
	t.Parallel()

	globFn := func(_ string) ([]string, error) {
		return []string{
			"/root/go.mod",
			"/root/vendor/sub/go.mod",
			"/root/node_modules/pkg/go.mod",
			"/root/.git/go.mod",
			"/root/dist/go.mod",
			"/root/subpkg/go.mod",
		}, nil
	}

	dirs, err := lint.ModulesAutoDetection("/root", globFn)
	ok(t, err)

	// Only /root and /root/subpkg should remain.
	equals(t, len(dirs), 2)
	assert(t, dirs[0] == "/root" || dirs[1] == "/root", "expected /root in results")

	for _, d := range dirs {
		assert(t, !strings.Contains(d, "vendor"), "vendor should be excluded: "+d)
		assert(t, !strings.Contains(d, "node_modules"), "node_modules should be excluded: "+d)
		assert(t, !strings.Contains(d, ".git"), ".git should be excluded: "+d)
		assert(t, !strings.Contains(d, "dist"), "dist should be excluded: "+d)
	}
}

// TestModulesAutoDetectionDedup covers spec anchor #62:
// duplicate go.mod directories are returned only once, sorted.
func TestModulesAutoDetectionDedup(t *testing.T) {
	t.Parallel()

	globFn := func(_ string) ([]string, error) {
		return []string{
			"/root/pkg/go.mod",
			"/root/pkg/go.mod", // duplicate
			"/root/alpha/go.mod",
		}, nil
	}

	dirs, err := lint.ModulesAutoDetection("/root", globFn)
	ok(t, err)
	equals(t, len(dirs), 2)
	// sorted: /root/alpha comes before /root/pkg
	equals(t, dirs[0], "/root/alpha")
	equals(t, dirs[1], "/root/pkg")
}

// TestModulesAutoDetectionGlobError verifies glob errors are propagated.
func TestModulesAutoDetectionGlobError(t *testing.T) {
	t.Parallel()

	globFn := func(_ string) ([]string, error) {
		return nil, errors.New("glob failed")
	}

	_, err := lint.ModulesAutoDetection("/root", globFn)
	assert(t, err != nil, "expected error from glob failure")
}

// TestModulesAutoDetectionEmpty verifies empty glob result returns empty slice.
func TestModulesAutoDetectionEmpty(t *testing.T) {
	t.Parallel()

	globFn := func(_ string) ([]string, error) {
		return nil, nil
	}

	dirs, err := lint.ModulesAutoDetection("/root", globFn)
	ok(t, err)
	equals(t, len(dirs), 0)
}
