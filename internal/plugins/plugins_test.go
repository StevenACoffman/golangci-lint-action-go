// Package plugins_test contains black-box tests for the plugins package.
package plugins_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StevenACoffman/golangci-lint-action-go/internal/plugins"
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

// statPresent returns a stat function that reports the named path as present.
func statPresent(present string) func(string) (os.FileInfo, error) {
	return func(p string) (os.FileInfo, error) {
		if p == present {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}
}

// statNone returns a stat function that always reports not-found.
func statNone() func(string) (os.FileInfo, error) {
	return func(_ string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
}

// TestFindConfigFile_YMLPresent covers spec §14 anchor #63 (.yml found first).
func TestFindConfigFile_YMLPresent(t *testing.T) {
	t.Parallel()
	root := "/some/root"
	want := filepath.Join(root, ".custom-gcl.yml")
	got, err := plugins.FindConfigFile(root, statPresent(want))
	ok(t, err)
	equals(t, got, want)
}

// TestFindConfigFile_YAMLPresent covers spec §14 anchor #63 (.yaml found when .yml absent).
func TestFindConfigFile_YAMLPresent(t *testing.T) {
	t.Parallel()
	root := "/some/root"
	want := filepath.Join(root, ".custom-gcl.yaml")
	got, err := plugins.FindConfigFile(root, statPresent(want))
	ok(t, err)
	equals(t, got, want)
}

// TestFindConfigFile_NonePresent covers spec §14 anchor #63 (no config file returns "").
func TestFindConfigFile_NonePresent(t *testing.T) {
	t.Parallel()
	got, err := plugins.FindConfigFile("/some/root", statNone())
	ok(t, err)
	equals(t, got, "")
}

// TestApplyDefaults covers both default fields.
func TestApplyDefaults(t *testing.T) {
	t.Parallel()

	t.Run("both empty", func(t *testing.T) {
		t.Parallel()
		cfg := &plugins.PluginConfig{}
		plugins.ApplyDefaults(cfg)
		equals(t, cfg.Destination, ".")
		equals(t, cfg.Name, "custom-gcl")
	})

	t.Run("destination already set", func(t *testing.T) {
		t.Parallel()
		cfg := &plugins.PluginConfig{Destination: "/custom/bin"}
		plugins.ApplyDefaults(cfg)
		equals(t, cfg.Destination, "/custom/bin")
		equals(t, cfg.Name, "custom-gcl")
	})

	t.Run("name already set", func(t *testing.T) {
		t.Parallel()
		cfg := &plugins.PluginConfig{Name: "my-linter"}
		plugins.ApplyDefaults(cfg)
		equals(t, cfg.Destination, ".")
		equals(t, cfg.Name, "my-linter")
	})
}

// TestParseConfig covers valid YAML content.
func TestParseConfig(t *testing.T) {
	t.Parallel()
	yamlContent := `version: "v2.1.0"
destination: "./bin"
name: "my-gcl"
`
	readFile := func(_ string) ([]byte, error) {
		return []byte(yamlContent), nil
	}
	cfg, err := plugins.ParseConfig("/fake/.custom-gcl.yml", readFile)
	ok(t, err)
	equals(t, cfg.Version, "v2.1.0")
	equals(t, cfg.Destination, "./bin")
	equals(t, cfg.Name, "my-gcl")
}

// TestInstall_HappyPath covers spec §14 anchor #64 (returned path is Join(rootDir, dest, name)).
func TestInstall_HappyPath(t *testing.T) {
	t.Parallel()
	rootDir := "/workspace"
	cfg := &plugins.PluginConfig{
		Version:     "v2.1.0",
		Destination: "bin",
		Name:        "custom-gcl",
	}
	var runCmdCalled bool
	got, err := plugins.Install(
		"/usr/local/bin/golangci-lint",
		rootDir,
		"/workspace/.custom-gcl.yml",
		cfg,
		"",
		func(_ string) bool { return true }, // dest exists
		func(_ string, _ os.FileMode) error { return nil },
		func(_ string, _ []string, _ string) error {
			runCmdCalled = true
			return nil
		},
		func(_ string, _ ...any) {},
		func(_ string, _ ...any) {},
	)
	ok(t, err)
	assert(t, runCmdCalled, "expected runCmd to be called")
	want := filepath.Join(rootDir, cfg.Destination, cfg.Name)
	equals(t, got, want)
}

// TestInstall_VersionMismatch covers spec §14 anchor #63 (warn on version mismatch).
func TestInstall_VersionMismatch(t *testing.T) {
	t.Parallel()
	cfg := &plugins.PluginConfig{
		Version:     "v2.1.0",
		Destination: ".",
		Name:        "custom-gcl",
	}
	var warnMsg string
	_, err := plugins.Install(
		"/usr/local/bin/golangci-lint",
		"/workspace",
		"/workspace/.custom-gcl.yml",
		cfg,
		"v2.2.0", // different from cfg.Version
		func(_ string) bool { return true },
		func(_ string, _ os.FileMode) error { return nil },
		func(_ string, _ []string, _ string) error { return nil },
		func(format string, args ...any) {
			warnMsg = fmt.Sprintf(format, args...)
		},
		func(_ string, _ ...any) {},
	)
	ok(t, err)
	assert(t, strings.Contains(warnMsg, "v2.1.0"), "expected cfg version in warn: "+warnMsg)
	assert(t, strings.Contains(warnMsg, "v2.2.0"), "expected input version in warn: "+warnMsg)
}

// TestInstall_MissingDestCreatesIt covers spec §14 anchor #63 (missing dest dir is created).
func TestInstall_MissingDestCreatesIt(t *testing.T) {
	t.Parallel()
	cfg := &plugins.PluginConfig{
		Version:     "v2.1.0",
		Destination: "missing-dir",
		Name:        "custom-gcl",
	}
	var mkdirPath string
	_, err := plugins.Install(
		"/usr/local/bin/golangci-lint",
		"/workspace",
		"/workspace/.custom-gcl.yml",
		cfg,
		"",
		func(_ string) bool { return false }, // dest does NOT exist
		func(path string, _ os.FileMode) error {
			mkdirPath = path
			return nil
		},
		func(_ string, _ []string, _ string) error { return nil },
		func(_ string, _ ...any) {},
		func(_ string, _ ...any) {},
	)
	ok(t, err)
	equals(t, mkdirPath, cfg.Destination)
}

// TestInstall_RunCmdError covers spec §14 anchor #63
// (runCmd error is wrapped with capitalized message).
func TestInstall_RunCmdError(t *testing.T) {
	t.Parallel()
	cfg := &plugins.PluginConfig{
		Version:     "v2.1.0",
		Destination: ".",
		Name:        "custom-gcl",
	}
	cmdErr := errors.New("build failed")
	_, err := plugins.Install(
		"/usr/local/bin/golangci-lint",
		"/workspace",
		"/workspace/.custom-gcl.yml",
		cfg,
		"",
		func(_ string) bool { return true },
		func(_ string, _ os.FileMode) error { return nil },
		func(_ string, _ []string, _ string) error { return cmdErr },
		func(_ string, _ ...any) {},
		func(_ string, _ ...any) {},
	)
	assert(t, err != nil, "expected error from runCmd failure")
	assert(
		t,
		strings.Contains(err.Error(), "Failed to build"),
		"expected capitalized message: "+err.Error(),
	)
	assert(t, errors.Is(err, cmdErr), "expected wrapped cmdErr")
}
