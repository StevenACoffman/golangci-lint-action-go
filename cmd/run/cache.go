package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/StevenACoffman/golangci-lint-action-go/internal/actionscache"
	"github.com/StevenACoffman/golangci-lint-action-go/internal/cache"
	"github.com/StevenACoffman/golangci-lint-action-go/internal/gha"
)

func (r runner) restoreCache(ctx context.Context, inputs *runInputs) error {
	if inputs.skipCache {
		return nil
	}
	if !gha.IsValidEvent(r.getenv) {
		gha.LogWarning(r.out, fmt.Sprintf(
			"Event Validation Error: The event type %s is not supported"+
				" because it's not tied to a branch or tag ref.",
			gha.EventName(r.getenv),
		))
		return nil
	}
	return r.buildAndRestoreCache(ctx, inputs)
}

func (r runner) buildAndRestoreCache(ctx context.Context, inputs *runInputs) error {
	cacheDir := cache.LintCacheDir(
		runtime.GOOS, r.getenv("HOME"), r.getenv("USERPROFILE"),
	)
	runnerOS := gha.RunnerOS(r.getenv)
	n, _ := strconv.Atoi(inputs.cacheIntervalStr)
	bucket := cache.IntervalBucket(time.Now(), n)
	goModPath := filepath.Join(inputs.workingDirectory, "go.mod")
	checksum, err := cache.GoModChecksum(goModPath, os.ReadFile)
	if err != nil {
		return fmt.Errorf("run: cache checksum: %w", err)
	}
	primaryKey, restoreKey := cache.BuildCacheKeys(
		runnerOS, inputs.workingDirectory, bucket, checksum,
	)
	client := actionscache.NewClient(r.getenv)
	saveState := func(k, v string) error {
		return gha.SaveState(r.getenv, k, v)
	}
	err = cache.RestoreCache(
		ctx, client, r.getenv,
		os.Setenv, //nolint:forbidigo // production shell must set real env vars
		saveState,
		cacheDir, primaryKey, []string{restoreKey}, r.out,
	)
	return err //nolint:wrapcheck // error context already provided by upstream package
}
