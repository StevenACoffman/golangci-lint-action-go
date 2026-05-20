// Package cache builds golangci-lint cache keys and orchestrates restore/save
// via the GitHub Actions Cache API.
package cache

import (
	"context"
	"crypto/sha1" //nolint:gosec // SHA-1 is used for key fingerprinting, not security
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/StevenACoffman/golangci-lint-action-go/internal/actionscache"
	"github.com/StevenACoffman/golangci-lint-action-go/internal/gha"
)

// CacheClient is the narrow interface consumed by RestoreCache and SaveCache.
// Using context.Context as first param on each method.
type CacheClient interface {
	RestoreCache(
		ctx context.Context,
		paths []string,
		primaryKey string,
		restoreKeys []string,
	) (string, error)
	SaveCache(ctx context.Context, paths []string, key string) error
}

// LintCacheDir returns the golangci-lint cache directory.
// Windows (goos=="windows"): filepath.Join(userProfile, ".cache", "golangci-lint")
// Others: filepath.Join(home, ".cache", "golangci-lint")
func LintCacheDir(goos, home, userProfile string) string {
	if goos == "windows" {
		return filepath.Join(userProfile, ".cache", "golangci-lint")
	}
	return filepath.Join(home, ".cache", "golangci-lint")
}

// IntervalBucket computes the cache rotation bucket string.
// n <= 0: returns strconv.FormatInt(now.UnixMilli(), 10)   ← milliseconds (spec §5.2 anchor #28)
// n > 0:  returns strconv.Itoa(int(math.Floor(float64(now.UnixMilli()) / 1000 / float64(n*86400))))
func IntervalBucket(now time.Time, n int) string {
	if n <= 0 {
		return strconv.FormatInt(now.UnixMilli(), 10)
	}
	bucket := math.Floor(float64(now.UnixMilli()) / 1000 / float64(n*86400))
	return strconv.Itoa(int(bucket))
}

// GoModChecksum returns the SHA-1 hex of go.mod at goModPath.
// Returns "nogomod" if readFile returns an error (file absent).
func GoModChecksum(goModPath string, readFile func(string) ([]byte, error)) (string, error) {
	data, err := readFile(goModPath)
	if err != nil {
		return "nogomod", nil //nolint:nilerr // absent go.mod is a valid state; spec §5.2
	}
	h := sha1.New() //nolint:gosec // SHA-1 used for cache key fingerprinting only
	if _, err = h.Write(data); err != nil {
		return "", fmt.Errorf("cache: sha1 write: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// BuildCacheKeys returns (primaryKey, restoreKey).
// primaryKey = "golangci-lint.cache-{OS}-{wd}-{bucket}-{checksum}"
// restoreKey = "golangci-lint.cache-{OS}-{wd}-{bucket}-"   (trailing dash, no checksum)
func BuildCacheKeys(runnerOS, workingDir, bucket, checksum string) (primary, restore string) {
	prefix := fmt.Sprintf("golangci-lint.cache-%s-%s-%s-", runnerOS, workingDir, bucket)
	return prefix + checksum, prefix
}

// RestoreCache sets GOLANGCI_LINT_CACHE before calling client.RestoreCache,
// saves primary key to GITHUB_STATE["CACHE_KEY"], and on hit saves matched key
// to GITHUB_STATE["CACHE_RESULT"]. Spec §5.3 anchor #34.
func RestoreCache(
	ctx context.Context,
	client CacheClient,
	_ func(string) string,
	setenv func(k, v string) error,
	saveState func(k, v string) error,
	cacheDir, primaryKey string,
	restoreKeys []string,
	out io.Writer,
) error {
	if err := setenv("GOLANGCI_LINT_CACHE", cacheDir); err != nil {
		return fmt.Errorf("cache: set GOLANGCI_LINT_CACHE: %w", err)
	}
	if err := saveState("CACHE_KEY", primaryKey); err != nil {
		return fmt.Errorf("cache: save CACHE_KEY state: %w", err)
	}
	matched, err := client.RestoreCache(ctx, []string{cacheDir}, primaryKey, restoreKeys)
	if err != nil {
		return fmt.Errorf("cache: restore: %w", err)
	}
	if matched == "" {
		gha.Info(out, "cache miss for key: "+primaryKey)
		return nil
	}
	gha.Info(out, "cache hit for key: "+matched)
	if err = saveState("CACHE_RESULT", matched); err != nil {
		return fmt.Errorf("cache: save CACHE_RESULT state: %w", err)
	}
	return nil
}

// SaveCache wraps client.SaveCache with the three-way error split (spec §5.4):
//   - *actionscache.ValidationError  → re-return (fatal)
//   - *actionscache.ReserveCacheError → gha.Info(out, err.Error())  (plain, no [warning])
//   - other error                    → gha.Info(out, "[warning] "+err.Error())  (space after ])
//
// Skips save when matchedKey == primaryKey (case-insensitive, spec anchor #37).
func SaveCache(
	ctx context.Context,
	client CacheClient,
	_ func(string) string,
	cacheDir, primaryKey, matchedKey string,
	out io.Writer,
) error {
	if strings.EqualFold(matchedKey, primaryKey) {
		gha.Info(out, "cache already up-to-date; skipping save")
		return nil
	}
	err := client.SaveCache(ctx, []string{cacheDir}, primaryKey)
	if err == nil {
		return nil
	}
	var ve *actionscache.ValidationError
	if errors.As(err, &ve) {
		return fmt.Errorf("cache: save: %w", err)
	}
	var rce *actionscache.ReserveCacheError
	if errors.As(err, &rce) {
		gha.Info(out, err.Error())
		return nil
	}
	gha.Info(out, "[warning] "+err.Error())
	return nil
}
