// Package run implements the "run" subcommand — Phase 1 of the action.
// It orchestrates cache restore, binary installation, and lint execution.
package run

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/peterbourgon/ff/v4"

	"github.com/StevenACoffman/golangci-lint-action-go/cmd/root"
	"github.com/StevenACoffman/golangci-lint-action-go/internal/gha"
)

// runInputs holds all parsed action inputs for the run phase.
type runInputs struct {
	version          string
	versionFile      string
	installMode      string
	installOnly      bool
	workingDirectory string
	githubToken      string
	verify           bool
	onlyNewIssues    bool
	args             string
	skipCache        bool
	cacheIntervalStr string
	problemMatchers  bool
	debug            string
	experimental     string
}

// runner bundles shared I/O dependencies so they don't need to be threaded
// through every helper's parameter list.
type runner struct {
	getenv func(string) string
	out    io.Writer
}

// Config holds the configuration for the run subcommand.
type Config struct {
	*root.Config
	Flags   *ff.FlagSet
	Command *ff.Command
}

// New creates and registers the "run" subcommand with the given parent.
func New(parent *root.Config) *Config {
	var cfg Config
	cfg.Config = parent
	cfg.Flags = ff.NewFlagSet("run").SetParent(parent.Flags)
	cfg.Command = &ff.Command{
		Name:      "run",
		Usage:     "golangci-lint-action-go run",
		ShortHelp: "run golangci-lint (main phase)",
		Flags:     cfg.Flags,
		Exec:      cfg.exec,
	}
	parent.Command.Subcommands = append(parent.Command.Subcommands, cfg.Command)
	return &cfg
}

func (cfg *Config) exec(ctx context.Context, _ []string) error {
	inputs := readInputs(cfg.Getenv)
	r := runner{getenv: cfg.Getenv, out: cfg.Stdout}

	if err := gha.Group(r.out, "Restore cache", func() error {
		return r.restoreCache(ctx, inputs)
	}); err != nil {
		return err //nolint:wrapcheck // error context already provided by upstream package
	}

	var binPath string
	if err := gha.Group(r.out, "Install", func() error {
		var e error
		binPath, e = r.install(ctx, inputs)
		return e
	}); err != nil {
		return err //nolint:wrapcheck // error context already provided by upstream package
	}

	if err := gha.AddPath(r.getenv, filepath.Dir(binPath)); err != nil {
		return fmt.Errorf("run: add path: %w", err)
	}

	if inputs.debug != "" {
		if err := gha.Group(r.out, "Debug", func() error {
			return r.debug(ctx, binPath, inputs.debug)
		}); err != nil {
			return err //nolint:wrapcheck // error context already provided by upstream package
		}
	}

	if inputs.installOnly {
		return nil
	}

	return r.runLint(ctx, binPath, inputs)
}

func readInputs(getenv func(string) string) *runInputs {
	boolInput := func(name string) bool {
		v, _ := gha.BoolInput(getenv, name)
		return v
	}
	return &runInputs{
		version:          gha.Input(getenv, "version"),
		versionFile:      gha.Input(getenv, "version-file"),
		installMode:      gha.Input(getenv, "install-mode"),
		installOnly:      boolInput("install-only"),
		workingDirectory: gha.Input(getenv, "working-directory"),
		githubToken:      gha.Input(getenv, "github-token"),
		verify:           boolInput("verify"),
		onlyNewIssues:    boolInput("only-new-issues"),
		args:             gha.Input(getenv, "args"),
		skipCache:        boolInput("skip-cache"),
		cacheIntervalStr: gha.Input(getenv, "cache-invalidation-interval"),
		problemMatchers:  boolInput("problem-matchers"),
		debug:            gha.Input(getenv, "debug"),
		experimental:     gha.Input(getenv, "experimental"),
	}
}

func resolveWorkDir(inputs *runInputs) (string, error) {
	wd := inputs.workingDirectory
	if wd == "" {
		got, err := os.Getwd() //nolint:forbidigo // production code: get real working directory
		if err != nil {
			return "", fmt.Errorf("run: getwd: %w", err)
		}
		return got, nil
	}
	info, err := os.Stat(wd)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("working-directory (%s) was not a path", wd)
	}
	abs, err := filepath.Abs(wd)
	if err != nil {
		return "", fmt.Errorf("run: abs: %w", err)
	}
	return abs, nil
}
