// Package gha_test contains black-box tests for the gha package.
package gha_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/StevenACoffman/golangci-lint-action-go/internal/gha"
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

func TestInput(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		name   string
		envKey string
		envVal string
		want   string
	}{
		"plain name": {
			name: "version", envKey: "INPUT_VERSION", envVal: "v1.2.3", want: "v1.2.3",
		},
		"dashes kept": {
			name: "install-mode", envKey: "INPUT_INSTALL-MODE", envVal: "binary", want: "binary",
		},
		"spaces to underscores": {
			name: "only new issues", envKey: "INPUT_ONLY_NEW_ISSUES", envVal: "true", want: "true",
		},
		"trimmed whitespace": {
			name: "version", envKey: "INPUT_VERSION", envVal: "  v1.2  ", want: "v1.2",
		},
		"mixed case name uppercased": {
			name: "Version", envKey: "INPUT_VERSION", envVal: "v2.0", want: "v2.0",
		},
		"missing env var": {
			name: "missing", envKey: "", envVal: "", want: "",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			getenv := func(key string) string {
				if tc.envKey != "" && key == tc.envKey {
					return tc.envVal
				}
				return ""
			}
			got := gha.Input(getenv, tc.name)
			equals(t, got, tc.want)
		})
	}
}

func TestBoolInput(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		val     string
		want    bool
		wantErr bool
	}{
		"true lowercase":  {val: "true", want: true},
		"false lowercase": {val: "false", want: false},
		"TRUE uppercase":  {val: "TRUE", want: true},
		"FALSE uppercase": {val: "FALSE", want: false},
		"True mixed":      {val: "True", want: true},
		"empty string":    {val: "", want: false},
		"yes invalid":     {val: "yes", wantErr: true},
		"1 invalid":       {val: "1", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			getenv := func(key string) string {
				if key == "INPUT_FLAG" {
					return tc.val
				}
				return ""
			}
			got, err := gha.BoolInput(getenv, "flag")
			if tc.wantErr {
				assert(t, err != nil, "expected error, got nil")
				return
			}
			ok(t, err)
			equals(t, got, tc.want)
		})
	}
}

func TestLogWarning(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	gha.LogWarning(&buf, "something went wrong")
	got := buf.String()
	// Spec §13: no space between "]" and the message body.
	assert(t, strings.HasPrefix(got, "[warning]something went wrong"),
		"expected [warning] prefix with no space, got: "+got)
}

func TestLogWarningNoSpace(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	gha.LogWarning(&buf, "msg")
	got := strings.TrimRight(buf.String(), "\n")
	equals(t, got, "[warning]msg")
}

func TestIsValidEvent(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		refVal string
		want   bool
	}{
		"present and non-empty": {refVal: "refs/heads/main", want: true},
		"present but empty":     {refVal: "", want: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			getenv := func(key string) string {
				if key == "GITHUB_REF" {
					return tc.refVal
				}
				return ""
			}
			got := gha.IsValidEvent(getenv)
			equals(t, got, tc.want)
		})
	}
}

func TestGroup(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := gha.Group(&buf, "my-group", func() error {
		gha.Info(&buf, "inside")
		return nil
	})
	ok(t, err)
	got := buf.String()
	assert(t, strings.Contains(got, "::group::my-group"), "missing group start")
	assert(t, strings.Contains(got, "::endgroup::"), "missing group end")
	assert(t, strings.Contains(got, "inside"), "missing inner content")
}

func TestWarningEscaping(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input string
		want  string
	}{
		"plain": {input: "bad thing", want: "::warning::bad thing"},
		"newline": {
			input: "line1\nline2",
			want:  "::warning::line1%0Aline2",
		},
		"colon": {
			input: "err: bad",
			want:  "::warning::err%3A bad",
		},
		"percent": {
			input: "50%",
			want:  "::warning::50%25",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			gha.Warning(&buf, tc.input)
			got := strings.TrimRight(buf.String(), "\n")
			equals(t, got, tc.want)
		})
	}
}

func TestGetState(t *testing.T) {
	t.Parallel()
	getenv := func(key string) string {
		if key == "STATE_CACHE_KEY" {
			return "golangci-lint.cache-Linux-.-7-abc123"
		}
		return ""
	}
	got := gha.GetState(getenv, "CACHE_KEY")
	equals(t, got, "golangci-lint.cache-Linux-.-7-abc123")
}

func TestRegisterProblemMatcher(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	gha.RegisterProblemMatcher(&buf, "/path/to/problem-matchers.json")
	got := strings.TrimRight(buf.String(), "\n")
	equals(t, got, "##[add-matcher]/path/to/problem-matchers.json")
}
