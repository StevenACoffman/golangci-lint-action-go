// Package patch_test contains black-box tests for the patch package.
package patch_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/StevenACoffman/golangci-lint-action-go/internal/patch"
)

// errNeverCalled is returned by stub HTTP functions that must never be invoked.
var errNeverCalled = errors.New("http stub: must not be called")

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

// Anchor #41/#42 helpers: stubHTTP returns a do function that answers with body and status.
func stubHTTP(status int, body string) func(*http.Request) (*http.Response, error) {
	return func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
}

// noopTempDir returns a fixed temp directory path (uses t.TempDir for isolation).
func noopTempDir(dir string) func() (string, error) {
	return func() (string, error) { return dir, nil }
}

// TestEscapeRegexpMeta covers escaping of all special characters. Spec §6.4.
func TestEscapeRegexpMeta(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input string
		want  string
	}{
		"plain text":    {input: "foo", want: "foo"},
		"dot":           {input: ".", want: `\.`},
		"star":          {input: "*", want: `\*`},
		"plus":          {input: "+", want: `\+`},
		"question":      {input: "?", want: `\?`},
		"caret":         {input: "^", want: `\^`},
		"dollar":        {input: "$", want: `\$`},
		"open brace":    {input: "{", want: `\{`},
		"close brace":   {input: "}", want: `\}`},
		"open paren":    {input: "(", want: `\(`},
		"close paren":   {input: ")", want: `\)`},
		"pipe":          {input: "|", want: `\|`},
		"open bracket":  {input: "[", want: `\[`},
		"close bracket": {input: "]", want: `\]`},
		"backslash":     {input: `\`, want: `\\`},
		"dash":          {input: "-", want: `\-`},
		"mixed":         {input: "a.b*c", want: `a\.b\*c`},
		"path segment":  {input: "sub/dir", want: `sub/dir`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := patch.EscapeRegexpMeta(tc.input)
			equals(t, got, tc.want)
		})
	}
}

// TestAlterDiffPatch_NoChange covers unchanged cases. Spec §6.4.
func TestAlterDiffPatch_NoChange(t *testing.T) {
	t.Parallel()
	input := "diff --git a/foo.go b/foo.go\n--- a/foo.go\n+++ b/foo.go\n"

	t.Run("empty workingDir returns unchanged", func(t *testing.T) {
		t.Parallel()
		got := patch.AlterDiffPatch(input, "", "/workspace")
		equals(t, got, input)
	})

	t.Run("workingDir equals workspace returns unchanged", func(t *testing.T) {
		t.Parallel()
		got := patch.AlterDiffPatch(input, "/workspace", "/workspace")
		equals(t, got, input)
	})
}

// TestAlterDiffPatch_StripsPrefix covers prefix stripping. Spec §6.4.
func TestAlterDiffPatch_StripsPrefix(t *testing.T) {
	t.Parallel()
	workspace := "/home/runner/work/repo"
	workingDir := "/home/runner/work/repo/sub"

	// A diff that touches sub/ and another that does not.
	input := strings.Join([]string{
		"diff --git a/sub/main.go b/sub/main.go",
		"--- a/sub/main.go",
		"+++ b/sub/main.go",
		" some context",
		"diff --git a/other/util.go b/other/util.go",
		"--- a/other/util.go",
		"+++ b/other/util.go",
		" other context",
	}, "\n")

	got := patch.AlterDiffPatch(input, workingDir, workspace)

	// The sub/ section should be present with prefix stripped.
	assert(t, strings.Contains(got, "--- a/main.go"),
		"expected --- a/main.go after prefix strip, got:\n"+got)
	assert(t, strings.Contains(got, "+++ b/main.go"),
		"expected +++ b/main.go after prefix strip, got:\n"+got)

	// The other/ section should be dropped entirely.
	assert(t, !strings.Contains(got, "other/util.go"),
		"expected other/util.go section to be filtered out, got:\n"+got)
}

// TestFilterDiffSections covers section filtering. Spec §6.4 / anchor #46.
func TestFilterDiffSections(t *testing.T) {
	t.Parallel()
	lines := []string{
		"diff --git a/sub/foo.go b/sub/foo.go",
		"--- a/sub/foo.go",
		"+++ b/sub/foo.go",
		" ctx line",
		"diff --git a/other/bar.go b/other/bar.go",
		"--- a/other/bar.go",
		"+++ b/other/bar.go",
		" other ctx",
	}
	got := patch.FilterDiffSections(lines, "sub")

	// Lines from sub/ section should be kept.
	assert(t, contains(got, "diff --git a/sub/foo.go b/sub/foo.go"),
		"expected sub diff --git line to be kept")
	assert(t, contains(got, "--- a/sub/foo.go"),
		"expected --- a/sub/foo.go to be kept")
	assert(t, contains(got, " ctx line"),
		"expected context line to be kept")

	// Lines from other/ section should be dropped.
	assert(t, !contains(got, "diff --git a/other/bar.go b/other/bar.go"),
		"expected other diff --git line to be dropped")
	assert(t, !contains(got, "--- a/other/bar.go"),
		"expected other --- line to be dropped")
	assert(t, !contains(got, " other ctx"),
		"expected other context to be dropped")
}

// contains reports whether s is in slice ss.
func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// TestFetchPatch_MergeGroup covers immediate "" return. Spec §6.1.
func TestFetchPatch_MergeGroup(t *testing.T) {
	t.Parallel()
	called := false
	doFn := func(_ *http.Request) (*http.Response, error) {
		called = true
		return nil, errNeverCalled
	}
	path, err := patch.FetchPatch(
		context.Background(),
		"merge_group", "owner", "repo",
		map[string]any{},
		"token", "", "/workspace",
		doFn,
		func() (string, error) { return t.TempDir(), nil },
		func(_ string, _ ...any) {},
		func(_ string, _ ...any) {},
	)
	ok(t, err)
	equals(t, path, "")
	assert(t, !called, "expected no HTTP call for merge_group")
}

// TestFetchPatch_PR covers PR path with stub HTTP. Spec §6.2 / anchor #41.
func TestFetchPatch_PR(t *testing.T) {
	t.Parallel()
	diffBody := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n"
	var capturedURL string
	doFn := func(req *http.Request) (*http.Response, error) {
		capturedURL = req.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(diffBody)),
		}, nil
	}
	tmpDir := t.TempDir()
	payload := map[string]any{
		"pull_request": map[string]any{
			"number": float64(42),
		},
	}
	patchPath, err := patch.FetchPatch(
		context.Background(),
		"pull_request", "myowner", "myrepo",
		payload,
		"mytoken", "", "/workspace",
		doFn,
		noopTempDir(tmpDir),
		func(_ string, _ ...any) {},
		func(_ string, _ ...any) {},
	)
	ok(t, err)
	assert(t, strings.HasSuffix(patchPath, "pull.patch"),
		"expected patchPath ending in pull.patch, got: "+patchPath) // anchor #41
	assert(t, strings.Contains(capturedURL, "/pulls/42"),
		"expected URL to contain /pulls/42, got: "+capturedURL)
	assert(t, strings.HasPrefix(capturedURL, "https://api.github.com"),
		"expected GitHub API URL, got: "+capturedURL)
}

// TestFetchPatch_NonOKStatus covers non-200 returning "". Spec §6.2.
func TestFetchPatch_NonOKStatus(t *testing.T) {
	t.Parallel()
	var warned string
	doFn := stubHTTP(http.StatusForbidden, "")
	payload := map[string]any{
		"pull_request": map[string]any{"number": float64(1)},
	}
	patchPath, err := patch.FetchPatch(
		context.Background(),
		"pull_request", "owner", "repo",
		payload,
		"token", "", "/workspace",
		doFn,
		func() (string, error) { return t.TempDir(), nil },
		func(format string, _ ...any) { warned = format },
		func(_ string, _ ...any) {},
	)
	ok(t, err)
	equals(t, patchPath, "")
	assert(t, warned != "", "expected a warning message for non-200 status")
	assert(t, strings.Contains(warned, "403") || strings.Contains(warned, "%d"),
		"expected warning to reference status code, got: "+warned)
}

// TestFetchPatch_UnknownEvent covers the default case log + "". Spec §6.1.
func TestFetchPatch_UnknownEvent(t *testing.T) {
	t.Parallel()
	var infoMsg string
	called := false
	doFn := func(_ *http.Request) (*http.Response, error) {
		called = true
		return nil, errNeverCalled
	}
	patchPath, err := patch.FetchPatch(
		context.Background(),
		"workflow_dispatch", "owner", "repo",
		map[string]any{},
		"token", "", "/workspace",
		doFn,
		func() (string, error) { return t.TempDir(), nil },
		func(_ string, _ ...any) {},
		func(format string, _ ...any) { infoMsg = format },
	)
	ok(t, err)
	equals(t, patchPath, "")
	assert(t, !called, "expected no HTTP call for unknown event")
	assert(t, strings.Contains(infoMsg, "Not fetching patch"),
		"expected info log for unknown event, got: "+infoMsg)
}
