// Package actionscache_test contains black-box tests for the actionscache package.
package actionscache_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/StevenACoffman/golangci-lint-action-go/internal/actionscache"
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

// makeResp returns an *http.Response with the given status and body.
func makeResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// emptyTarGz returns the bytes of a valid but empty gzip+tar archive.
func emptyTarGz(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.String()
}

// TestCacheVersion verifies the SHA-256 version is deterministic and order-independent.
func TestCacheVersion(t *testing.T) {
	t.Parallel()
	v1 := actionscache.CacheVersion([]string{"/a", "/b"})
	v2 := actionscache.CacheVersion([]string{"/b", "/a"})
	equals(t, v1, v2)

	v3 := actionscache.CacheVersion([]string{"/c"})
	assert(t, v1 != v3, "different paths must produce different versions")
	equals(t, len(v1), 64) // SHA-256 hex
}

// TestRestoreCache_Miss verifies a 204 response produces an empty key.
func TestRestoreCache_Miss(t *testing.T) {
	t.Parallel()
	do := func(_ *http.Request) (*http.Response, error) {
		return makeResp(http.StatusNoContent, ""), nil
	}
	c := actionscache.NewClientWithHTTP("http://cache/", "tok", do)
	key, err := c.RestoreCache(context.Background(), []string{"/p"}, "primary", nil)
	ok(t, err)
	equals(t, key, "")
}

// TestRestoreCache_Hit verifies a 200 lookup triggers download and returns the cacheKey.
func TestRestoreCache_Hit(t *testing.T) {
	t.Parallel()
	calls := 0
	archiveBody := emptyTarGz(t)
	do := func(_ *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			body, _ := json.Marshal(map[string]string{
				"archiveLocation": "http://storage/archive.tar.gz",
				"cacheKey":        "primary-hit",
			})
			return makeResp(http.StatusOK, string(body)), nil
		}
		return makeResp(http.StatusOK, archiveBody), nil
	}
	c := actionscache.NewClientWithHTTP("http://cache/", "tok", do)
	key, err := c.RestoreCache(context.Background(), []string{"/p"}, "primary", nil)
	ok(t, err)
	equals(t, key, "primary-hit")
	equals(t, calls, 2)
}

// TestRestoreCache_NetworkError verifies that HTTP errors are propagated.
func TestRestoreCache_NetworkError(t *testing.T) {
	t.Parallel()
	do := func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("network failure")
	}
	c := actionscache.NewClientWithHTTP("http://cache/", "tok", do)
	_, err := c.RestoreCache(context.Background(), []string{"/p"}, "primary", nil)
	assert(t, err != nil, "expected error on network failure")
}

// TestSaveCache_ReserveConflict verifies 409 produces a ReserveCacheError (spec §5.4 anchor #38).
func TestSaveCache_ReserveConflict(t *testing.T) {
	t.Parallel()
	do := func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost {
			return makeResp(http.StatusConflict, "already reserved"), nil
		}
		return makeResp(http.StatusOK, ""), nil
	}
	c := actionscache.NewClientWithHTTP("http://cache/", "tok", do)
	err := c.SaveCache(context.Background(), []string{t.TempDir()}, "key")
	assert(t, err != nil, "expected error on conflict")
	var rce *actionscache.ReserveCacheError
	assert(t, errors.As(err, &rce), "expected ReserveCacheError, got: "+err.Error())
}

// TestSaveCache_ValidationError verifies 400 produces a ValidationError.
func TestSaveCache_ValidationError(t *testing.T) {
	t.Parallel()
	do := func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost {
			return makeResp(http.StatusBadRequest, "invalid key"), nil
		}
		return makeResp(http.StatusOK, ""), nil
	}
	c := actionscache.NewClientWithHTTP("http://cache/", "tok", do)
	err := c.SaveCache(context.Background(), []string{t.TempDir()}, "key")
	assert(t, err != nil, "expected error on bad request")
	var ve *actionscache.ValidationError
	assert(t, errors.As(err, &ve), "expected ValidationError, got: "+err.Error())
}

// TestSaveCache_Success verifies the full reserve+upload+commit flow.
func TestSaveCache_Success(t *testing.T) {
	t.Parallel()
	var methods []string
	do := func(req *http.Request) (*http.Response, error) {
		methods = append(methods, req.Method)
		switch req.Method {
		case http.MethodPost:
			if strings.HasSuffix(req.URL.Path, "/42") {
				return makeResp(http.StatusNoContent, ""), nil
			}
			body, _ := json.Marshal(map[string]int64{"cacheId": 42})
			return makeResp(http.StatusCreated, string(body)), nil
		case http.MethodPatch:
			return makeResp(http.StatusNoContent, ""), nil
		}
		return makeResp(http.StatusOK, ""), nil
	}
	c := actionscache.NewClientWithHTTP("http://cache/", "tok", do)
	err := c.SaveCache(context.Background(), []string{t.TempDir()}, "key")
	ok(t, err)
	// POST (reserve), PATCH (upload), POST (commit)
	assert(t, len(methods) >= 3, "expected reserve+upload+commit calls")
}
