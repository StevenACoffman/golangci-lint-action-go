// Package gha provides a thin adapter over the GitHub Actions runner protocol.
// All interaction with the runner environment — workflow commands, environment
// file mutations, and state storage — goes through this package, so every
// caller remains testable with an injected getenv function and io.Writer.
package gha

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Input reads the value of the INPUT_<NAME> environment variable.
// Name normalisation: spaces are replaced with underscores, then the whole
// string is uppercased.  Dashes are left unchanged.  The return value is
// trimmed of leading and trailing whitespace.  This matches the behaviour of
// core.getInput() from @actions/toolkit.  Spec §2.
func Input(getenv func(string) string, name string) string {
	key := "INPUT_" + strings.ToUpper(strings.ReplaceAll(name, " ", "_"))
	return strings.TrimSpace(getenv(key))
}

// BoolInput parses the value of Input(getenv, name) as a boolean.
// Accepted values (case-insensitive): "true" and "false".
// An empty value is treated as false.  Any other value returns an error.
func BoolInput(getenv func(string) string, name string) (bool, error) {
	const op = "gha.BoolInput"
	v := Input(getenv, name)
	switch strings.ToLower(v) {
	case "true":
		return true, nil
	case "false", "":
		return false, nil
	default:
		return false, fmt.Errorf("%s: %q is not a valid boolean value", op, v)
	}
}

// Info writes msg to out followed by a newline.
// Mirrors core.info() from @actions/toolkit: plain text with no workflow-command prefix.
func Info(out io.Writer, msg string) {
	_, _ = fmt.Fprintln(out, msg)
}

// Warning writes a ::warning:: workflow command to out.
// Special characters in msg are percent-encoded per the workflow command spec.
func Warning(out io.Writer, msg string) {
	_, _ = fmt.Fprintf(out, "::warning::%s\n", escapeData(msg))
}

// Error writes a ::error:: workflow command to out.
// Special characters in msg are percent-encoded per the workflow command spec.
func Error(out io.Writer, msg string) {
	_, _ = fmt.Fprintf(out, "::error::%s\n", escapeData(msg))
}

// LogWarning writes "[warning]<msg>" to out via Info.
// There is no space between "]" and msg — spec §13 logWarning format.
func LogWarning(out io.Writer, msg string) {
	Info(out, "[warning]"+msg)
}

// SaveState appends "KEY=VALUE\n" to the file named by $GITHUB_STATE.
// This persists a value between the main and post-run action phases.  Spec §1.
func SaveState(getenv func(string) string, key, value string) error {
	const op = "gha.SaveState"
	path := getenv("GITHUB_STATE")
	if path == "" {
		return fmt.Errorf("%s: GITHUB_STATE env var is not set", op)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if _, err = fmt.Fprintf(f, "%s=%s\n", key, value); err != nil {
		_ = f.Close()
		return fmt.Errorf("%s: write: %w", op, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("%s: close: %w", op, err)
	}
	return nil
}

// GetState reads the STATE_<KEY> environment variable.
// Mirrors core.getState() from @actions/toolkit.
func GetState(getenv func(string) string, key string) string {
	return getenv("STATE_" + strings.ToUpper(key))
}

// AddPath appends path followed by a newline to the file named by $GITHUB_PATH.
// This adds path to $PATH for all subsequent steps in the action.
func AddPath(getenv func(string) string, path string) error {
	const op = "gha.AddPath"
	p := getenv("GITHUB_PATH")
	if p == "" {
		return fmt.Errorf("%s: GITHUB_PATH env var is not set", op)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if _, err = fmt.Fprintln(f, path); err != nil {
		_ = f.Close()
		return fmt.Errorf("%s: write: %w", op, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("%s: close: %w", op, err)
	}
	return nil
}

// Group emits a ::group:: workflow command, calls fn, then emits ::endgroup::.
// The endgroup command is always written even when fn returns an error.
func Group(out io.Writer, name string, fn func() error) error {
	_, _ = fmt.Fprintf(out, "::group::%s\n", name)
	defer func() { _, _ = fmt.Fprint(out, "::endgroup::\n") }()
	return fn()
}

// RegisterProblemMatcher emits the ##[add-matcher] workflow command.
// path must be the absolute path to the problem-matchers.json file.  Spec §4.1.
func RegisterProblemMatcher(out io.Writer, path string) {
	Info(out, "##[add-matcher]"+path)
}

// EventName returns the value of GITHUB_EVENT_NAME.
func EventName(getenv func(string) string) string {
	return getenv("GITHUB_EVENT_NAME")
}

// IsValidEvent returns true when GITHUB_REF is present and non-empty.
// Mirrors isValidEvent from utils/actionUtils.ts.  Spec §5.3.
func IsValidEvent(getenv func(string) string) bool {
	return getenv("GITHUB_REF") != ""
}

// Workspace returns GITHUB_WORKSPACE, or the empty string if unset.
func Workspace(getenv func(string) string) string {
	return getenv("GITHUB_WORKSPACE")
}

// RunnerOS returns RUNNER_OS from the environment.
func RunnerOS(getenv func(string) string) string {
	return getenv("RUNNER_OS")
}

// escapeData replaces characters that have special meaning in workflow commands
// with their percent-encoded equivalents.  The replacement order matches the
// @actions/core implementation: % must be replaced first.
func escapeData(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	s = strings.ReplaceAll(s, ":", "%3A")
	s = strings.ReplaceAll(s, ",", "%2C")
	return s
}
