// Package postrun implements the "post-run" subcommand — Phase 2 of the action.
// It saves the golangci-lint cache that was prepared in the run phase.
package postrun

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/peterbourgon/ff/v4"

	"github.com/StevenACoffman/golangci-lint-action-go/cmd/root"
	"github.com/StevenACoffman/golangci-lint-action-go/internal/actionscache"
	"github.com/StevenACoffman/golangci-lint-action-go/internal/cache"
	"github.com/StevenACoffman/golangci-lint-action-go/internal/gha"
)

// Config holds the configuration for the post-run subcommand.
type Config struct {
	*root.Config
	Flags   *ff.FlagSet
	Command *ff.Command
}

// New creates and registers the "post-run" subcommand with the given parent.
func New(parent *root.Config) *Config {
	var cfg Config
	cfg.Config = parent
	cfg.Flags = ff.NewFlagSet("post-run").SetParent(parent.Flags)
	cfg.Command = &ff.Command{
		Name:      "post-run",
		Usage:     "golangci-lint-action-go post-run",
		ShortHelp: "save golangci-lint cache (post-run phase)",
		Flags:     cfg.Flags,
		Exec:      cfg.exec,
	}
	parent.Command.Subcommands = append(parent.Command.Subcommands, cfg.Command)
	return &cfg
}

func (cfg *Config) exec(ctx context.Context, _ []string) error {
	out := cfg.Stdout
	getenv := cfg.Getenv

	skipCache, _ := gha.BoolInput(getenv, "skip-cache")
	if skipCache {
		gha.Info(out, "Skipping cache saving")
		return nil
	}
	skipSave, _ := gha.BoolInput(getenv, "skip-save-cache")
	if skipSave {
		gha.Info(out, "Skipping cache saving")
		return nil
	}
	if !gha.IsValidEvent(getenv) {
		gha.LogWarning(out, fmt.Sprintf(
			"Event Validation Error: The event type %s is not supported because it's not tied to a branch or tag ref.",
			gha.EventName(getenv),
		))
		return nil
	}

	primaryKey := gha.GetState(getenv, "CACHE_KEY")
	if primaryKey == "" {
		gha.LogWarning(out, "Error retrieving key from state.")
		return nil
	}
	matchedKey := gha.GetState(getenv, "CACHE_RESULT")
	if strings.EqualFold(matchedKey, primaryKey) {
		gha.Info(out, fmt.Sprintf(
			"Cache hit occurred on the primary key %s, not saving cache.", primaryKey,
		))
		return nil
	}

	cacheDir := cache.LintCacheDir(runtime.GOOS, getenv("HOME"), getenv("USERPROFILE"))
	client := actionscache.NewClient(getenv)
	err := cache.SaveCache(ctx, client, getenv, cacheDir, primaryKey, matchedKey, out)
	if err != nil {
		return err //nolint:wrapcheck // error context already provided by upstream package
	}
	return nil
}
