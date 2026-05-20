// Package cache_test contains black-box tests for the cache package.
package cache_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StevenACoffman/golangci-lint-action-go/internal/actionscache"
	"github.com/StevenACoffman/golangci-lint-action-go/internal/cache"
)

// funcClient is a CacheClient backed by function fields for fine-grained test control.
type funcClient struct {
	restoreFn func(
		ctx context.Context,
		paths []string,
		primaryKey string,
		restoreKeys []string,
	) (string, error)
	saveFn func(ctx context.Context, paths []string, key string) error
}

// stubSaveClient is a CacheClient whose SaveCache returns a fixed error.
type stubSaveClient struct {
	saveErr error
}

func (f *funcClient) RestoreCache(
	ctx context.Context,
	paths []string,
	primaryKey string,
	restoreKeys []string,
) (string, error) {
	return f.restoreFn(ctx, paths, primaryKey, restoreKeys)
}

func (f *funcClient) SaveCache(ctx context.Context, paths []string, key string) error {
	return f.saveFn(ctx, paths, key)
}

func (s *stubSaveClient) RestoreCache(
	_ context.Context, _ []string, _ string, _ []string,
) (string, error) {
	return "", nil
}

func (s *stubSaveClient) SaveCache(_ context.Context, _ []string, _ string) error {
	return s.saveErr
}

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

// TestIntervalBucket_Zero checks that n=0 returns milliseconds since epoch.
func TestIntervalBucket_Zero(t *testing.T) {
	t.Parallel()
	now := time.UnixMilli(1_700_000_000_000)
	got := cache.IntervalBucket(now, 0)
	equals(t, got, "1700000000000")
}

// TestIntervalBucket_Negative checks that n=-1 returns milliseconds since epoch.
func TestIntervalBucket_Negative(t *testing.T) {
	t.Parallel()
	now := time.UnixMilli(1_700_000_000_000)
	got := cache.IntervalBucket(now, -1)
	equals(t, got, "1700000000000")
}

// TestIntervalBucket_Seven checks that n=7 returns the correct day bucket.
func TestIntervalBucket_Seven(t *testing.T) {
	t.Parallel()
	// 1_700_000_000_000 ms = 1_700_000_000 s
	// floor(1_700_000_000 / (7 * 86400)) = floor(1_700_000_000 / 604800) = floor(2810.185...)
	// = 2810
	now := time.UnixMilli(1_700_000_000_000)
	got := cache.IntervalBucket(now, 7)
	equals(t, got, "2810")
}

// TestBuildCacheKeys verifies primary contains checksum and restore ends with "-".
func TestBuildCacheKeys(t *testing.T) {
	t.Parallel()
	primary, restore := cache.BuildCacheKeys("Linux", ".", "2810", "abc123")
	equals(t, primary, "golangci-lint.cache-Linux-.-2810-abc123")
	equals(t, restore, "golangci-lint.cache-Linux-.-2810-")
	assert(t, restore[len(restore)-1] == '-', "restore key must end with '-'")
	assert(t, primary != restore, "primary and restore keys must differ")
}

// TestGoModChecksum_Present verifies SHA-1 hex is returned for existing file.
func TestGoModChecksum_Present(t *testing.T) {
	t.Parallel()
	content := []byte("module example.com/foo\n\ngo 1.21\n")
	readFile := func(_ string) ([]byte, error) { return content, nil }
	got, err := cache.GoModChecksum("go.mod", readFile)
	ok(t, err)
	assert(t, len(got) == 40, "SHA-1 hex must be 40 chars, got: "+got)
	assert(t, got != "nogomod", "expected real checksum, not nogomod")
}

// TestGoModChecksum_Absent verifies "nogomod" is returned when file is absent.
func TestGoModChecksum_Absent(t *testing.T) {
	t.Parallel()
	readFile := func(_ string) ([]byte, error) {
		return nil, errors.New("file not found")
	}
	got, err := cache.GoModChecksum("go.mod", readFile)
	ok(t, err)
	equals(t, got, "nogomod")
}

// TestLintCacheDir_Windows verifies Windows uses USERPROFILE.
func TestLintCacheDir_Windows(t *testing.T) {
	t.Parallel()
	userProfile := filepath.Join("C:", "Users", "user")
	want := filepath.Join(userProfile, ".cache", "golangci-lint")
	got := cache.LintCacheDir("windows", "/home/user", userProfile)
	equals(t, got, want)
}

// TestLintCacheDir_Linux verifies Linux uses HOME.
func TestLintCacheDir_Linux(t *testing.T) {
	t.Parallel()
	got := cache.LintCacheDir("linux", "/home/user", "")
	equals(t, got, "/home/user/.cache/golangci-lint")
}

// TestSaveCache_ValidationError verifies ValidationError is re-returned (fatal).
func TestSaveCache_ValidationError(t *testing.T) {
	t.Parallel()
	stub := &stubSaveClient{
		saveErr: &actionscache.ValidationError{Msg: "invalid key chars"},
	}
	var buf bytes.Buffer
	err := cache.SaveCache(
		context.Background(), stub, func(_ string) string { return "" },
		"/cache", "primary-key", "other-key", &buf,
	)
	assert(t, err != nil, "expected error for ValidationError")
	var ve *actionscache.ValidationError
	assert(t, errors.As(err, &ve), "expected wrapped ValidationError")
}

// TestSaveCache_ReserveCacheError verifies ReserveCacheError is logged as plain info.
func TestSaveCache_ReserveCacheError(t *testing.T) {
	t.Parallel()
	stub := &stubSaveClient{
		saveErr: &actionscache.ReserveCacheError{Msg: "slot already taken"},
	}
	var buf bytes.Buffer
	err := cache.SaveCache(
		context.Background(), stub, func(_ string) string { return "" },
		"/cache", "primary-key", "other-key", &buf,
	)
	ok(t, err)
	out := buf.String()
	assert(t, out != "", "expected output for ReserveCacheError")
	assert(
		t,
		!strings.Contains(out, "[warning]"),
		"ReserveCacheError must not have [warning] prefix, got: "+out,
	)
}

// TestSaveCache_OtherError verifies generic errors are logged with "[warning] " prefix.
func TestSaveCache_OtherError(t *testing.T) {
	t.Parallel()
	stub := &stubSaveClient{
		saveErr: errors.New("network timeout"),
	}
	var buf bytes.Buffer
	err := cache.SaveCache(
		context.Background(), stub, func(_ string) string { return "" },
		"/cache", "primary-key", "other-key", &buf,
	)
	ok(t, err)
	out := buf.String()
	assert(
		t,
		strings.Contains(out, "[warning] "),
		"expected [warning] prefix with space, got: "+out,
	)
}

// TestSaveCache_SkipWhenExactMatch verifies save is skipped when keys match case-insensitively.
func TestSaveCache_SkipWhenExactMatch(t *testing.T) {
	t.Parallel()
	stub := &stubSaveClient{
		saveErr: errors.New("should not be called"),
	}
	var buf bytes.Buffer
	err := cache.SaveCache(
		context.Background(), stub, func(_ string) string { return "" },
		"/cache", "Primary-Key", "primary-key", &buf,
	)
	ok(t, err)
}

// TestRestoreCache_SetsEnvBeforeCall verifies GOLANGCI_LINT_CACHE is set before RestoreCache.
func TestRestoreCache_SetsEnvBeforeCall(t *testing.T) {
	t.Parallel()

	envMap := map[string]string{}
	var envSetBeforeRestore bool

	restoreFn := func(
		_ context.Context, _ []string, _ string, _ []string,
	) (string, error) {
		envSetBeforeRestore = envMap["GOLANGCI_LINT_CACHE"] != ""
		return "", nil
	}
	saveFn := func(_ context.Context, _ []string, _ string) error { return nil }
	client := &funcClient{restoreFn: restoreFn, saveFn: saveFn}

	setenv := func(k, v string) error {
		envMap[k] = v
		return nil
	}
	states := map[string]string{}
	saveState := func(k, v string) error {
		states[k] = v
		return nil
	}

	var buf bytes.Buffer
	err := cache.RestoreCache(
		context.Background(),
		client,
		func(_ string) string { return "" },
		setenv,
		saveState,
		"/cache/dir",
		"primary-key",
		[]string{"restore-key-"},
		&buf,
	)
	ok(t, err)
	assert(
		t,
		envSetBeforeRestore,
		"GOLANGCI_LINT_CACHE must be set before client.RestoreCache is called",
	)
	equals(t, envMap["GOLANGCI_LINT_CACHE"], "/cache/dir")
	equals(t, states["CACHE_KEY"], "primary-key")
}

// TestRestoreCache_Hit verifies matched key is saved to state on cache hit.
func TestRestoreCache_Hit(t *testing.T) {
	t.Parallel()
	restoreFn := func(
		_ context.Context, _ []string, _ string, _ []string,
	) (string, error) {
		return "matched-key", nil
	}
	saveFn := func(_ context.Context, _ []string, _ string) error { return nil }
	client := &funcClient{restoreFn: restoreFn, saveFn: saveFn}

	envMap := map[string]string{}
	setenv := func(k, v string) error { envMap[k] = v; return nil }
	states := map[string]string{}
	saveState := func(k, v string) error { states[k] = v; return nil }

	var buf bytes.Buffer
	err := cache.RestoreCache(
		context.Background(), client,
		func(_ string) string { return "" },
		setenv, saveState,
		"/cache/dir", "primary-key", nil, &buf,
	)
	ok(t, err)
	equals(t, states["CACHE_RESULT"], "matched-key")
}

// TestRestoreCache_Miss verifies CACHE_RESULT is not set on cache miss.
func TestRestoreCache_Miss(t *testing.T) {
	t.Parallel()
	restoreFn := func(
		_ context.Context, _ []string, _ string, _ []string,
	) (string, error) {
		return "", nil
	}
	saveFn := func(_ context.Context, _ []string, _ string) error { return nil }
	client := &funcClient{restoreFn: restoreFn, saveFn: saveFn}

	envMap := map[string]string{}
	setenv := func(k, v string) error { envMap[k] = v; return nil }
	states := map[string]string{}
	saveState := func(k, v string) error { states[k] = v; return nil }

	var buf bytes.Buffer
	err := cache.RestoreCache(
		context.Background(), client,
		func(_ string) string { return "" },
		setenv, saveState,
		"/cache/dir", "primary-key", nil, &buf,
	)
	ok(t, err)
	_, hasResult := states["CACHE_RESULT"]
	assert(t, !hasResult, "CACHE_RESULT must not be set on cache miss")
}
