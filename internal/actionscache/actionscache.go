// Package actionscache implements the GitHub Actions Cache REST API.
// It is the Go equivalent of the @actions/cache npm package.
// All HTTP is performed through the injectable do function so callers
// can supply a fake sender for tests without network access.
package actionscache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	apiVersion   = "application/json;api-version=6.0-preview.1"
	chunkSize    = 32 * 1024 * 1024 // 32 MiB
	maxFilePerms = 0o600
)

// ValidationError is returned when the cache key is invalid.
// Matches the @actions/cache ValidationError type check
// (error.name === "ValidationError").
type ValidationError struct {
	Msg string
}

// ReserveCacheError is returned when the cache slot is already taken.
// Matches the @actions/cache ReserveCacheError type check
// (error.name === "ReserveCacheError").
type ReserveCacheError struct {
	Msg string
}

// Client holds the HTTP credentials and sender for the cache API.
// Use NewClient in production and NewClientWithHTTP in tests.
type Client struct {
	baseURL string
	token   string
	do      func(req *http.Request) (*http.Response, error)
}

// lookupResult holds the parsed response from a cache lookup.
type lookupResult struct {
	Found           bool
	ArchiveLocation string
	CacheKey        string
}

// lookupResponse mirrors the JSON returned by the lookup endpoint.
type lookupResponse struct {
	ArchiveLocation string `json:"archiveLocation"`
	CacheKey        string `json:"cacheKey"`
}

// reserveResponse mirrors the JSON returned by the reserve endpoint.
type reserveResponse struct {
	CacheID int64 `json:"cacheId"`
}

func (e *ValidationError) Error() string   { return e.Msg }
func (e *ReserveCacheError) Error() string { return e.Msg }

// NewClient creates a Client using credentials from environment variables.
// Reads ACTIONS_CACHE_URL and ACTIONS_RUNTIME_TOKEN via getenv.
func NewClient(getenv func(string) string) *Client {
	return &Client{
		baseURL: getenv("ACTIONS_CACHE_URL"),
		token:   getenv("ACTIONS_RUNTIME_TOKEN"),
		do:      http.DefaultClient.Do,
	}
}

// NewClientWithHTTP creates a Client with explicit credentials and sender.
// For production use NewClient; this constructor exists for testing.
func NewClientWithHTTP(
	baseURL, token string,
	do func(*http.Request) (*http.Response, error),
) *Client {
	return &Client{baseURL: baseURL, token: token, do: do}
}

// CacheVersion returns the SHA-256 version string for a set of paths.
// Paths are sorted and joined with "\n" before hashing.
func CacheVersion(paths []string) string {
	sorted := make([]string, len(paths))
	copy(sorted, paths)
	sort.Strings(sorted)
	h := sha256.New()
	_, _ = io.WriteString(h, strings.Join(sorted, "\n"))
	return hex.EncodeToString(h.Sum(nil))
}

// RestoreCache looks up the cache and, if found, downloads and extracts
// the archive into each of paths.  Returns the matched cache key, or "" if
// not found.  Spec §5.3.
func (c *Client) RestoreCache(
	ctx context.Context,
	paths []string,
	primaryKey string,
	restoreKeys []string,
) (string, error) {
	const op = "actionscache.RestoreCache"
	version := CacheVersion(paths)
	allKeys := append([]string{primaryKey}, restoreKeys...)
	result, err := c.lookupCache(ctx, allKeys, version)
	if err != nil {
		return "", fmt.Errorf("%s: %w", op, err)
	}
	if !result.Found {
		return "", nil
	}
	if err = c.downloadAndExtract(ctx, result.ArchiveLocation); err != nil {
		return "", fmt.Errorf("%s: extract: %w", op, err)
	}
	return result.CacheKey, nil
}

// SaveCache archives paths, reserves a cache slot, uploads in chunks, and
// commits.  Spec §5.4.
func (c *Client) SaveCache(ctx context.Context, paths []string, key string) error {
	const op = "actionscache.SaveCache"
	version := CacheVersion(paths)
	data, err := createArchive(paths)
	if err != nil {
		return fmt.Errorf("%s: archive: %w", op, err)
	}
	id, err := c.reserveSlot(ctx, key, version)
	if err != nil {
		return err // already typed as ValidationError or ReserveCacheError
	}
	if err = c.uploadArchive(ctx, id, data); err != nil {
		return fmt.Errorf("%s: upload: %w", op, err)
	}
	return c.commitCache(ctx, id, len(data))
}

// lookupCache performs a cache lookup and returns the first match.
func (c *Client) lookupCache(
	ctx context.Context,
	keys []string,
	version string,
) (lookupResult, error) {
	const op = "actionscache.lookupCache"
	u := c.baseURL + "_apis/artifactcache/cache"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return lookupResult{}, fmt.Errorf("%s: %w", op, err)
	}
	q := url.Values{}
	q.Set("keys", strings.Join(keys, ","))
	q.Set("version", version)
	req.URL.RawQuery = q.Encode()
	c.setHeaders(req)
	resp, err := c.do(req)
	if err != nil {
		return lookupResult{}, fmt.Errorf("%s: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNoContent {
		return lookupResult{Found: false}, nil
	}
	var lr lookupResponse
	if err = json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return lookupResult{}, fmt.Errorf("%s: decode: %w", op, err)
	}
	return lookupResult{
		Found:           true,
		ArchiveLocation: lr.ArchiveLocation,
		CacheKey:        lr.CacheKey,
	}, nil
}

// downloadAndExtract downloads the archive at archiveLocation and extracts it.
func (c *Client) downloadAndExtract(ctx context.Context, archiveLocation string) error {
	const op = "actionscache.downloadAndExtract"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveLocation, http.NoBody)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err = extractArchive(resp.Body); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// reserveSlot sends the reserve POST and returns the cacheId.
// Returns typed errors on 400 (ValidationError) or 409 (ReserveCacheError).
func (c *Client) reserveSlot(ctx context.Context, key, version string) (int64, error) {
	const op = "actionscache.reserveSlot"
	body, _ := json.Marshal(map[string]string{
		"key": key, "version": version, "compressionMethod": "gzip",
	})
	u := c.baseURL + "_apis/artifactcache/caches"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", op, err)
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusBadRequest:
		msg, _ := io.ReadAll(resp.Body)
		return 0, &ValidationError{Msg: string(msg)}
	case http.StatusConflict:
		msg, _ := io.ReadAll(resp.Body)
		return 0, &ReserveCacheError{Msg: string(msg)}
	}
	var rr reserveResponse
	if err = json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return 0, fmt.Errorf("%s: decode: %w", op, err)
	}
	return rr.CacheID, nil
}

// uploadArchive uploads the archive data in chunks via PATCH.
func (c *Client) uploadArchive(ctx context.Context, id int64, data []byte) error {
	total := len(data)
	for start := 0; start < total; start += chunkSize {
		end := start + chunkSize
		if end > total {
			end = total
		}
		if err := c.uploadChunk(ctx, id, data[start:end], start, total); err != nil {
			return err
		}
	}
	return nil
}

// uploadChunk sends one PATCH chunk to the cache API.
func (c *Client) uploadChunk(
	ctx context.Context, id int64, chunk []byte, start, total int,
) error {
	const op = "actionscache.uploadChunk"
	u := fmt.Sprintf("%s_apis/artifactcache/caches/%d", c.baseURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u, bytes.NewReader(chunk))
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	c.setHeaders(req)
	req.Header.Set("Content-Range",
		fmt.Sprintf("bytes %d-%d/%d", start, start+len(chunk)-1, total))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: status %d", op, resp.StatusCode)
	}
	return nil
}

// commitCache sends the commit POST to finalise the upload.
func (c *Client) commitCache(ctx context.Context, id int64, size int) error {
	const op = "actionscache.commitCache"
	body, _ := json.Marshal(map[string]int{"size": size})
	u := fmt.Sprintf("%s_apis/artifactcache/caches/%d", c.baseURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: status %d", op, resp.StatusCode)
	}
	return nil
}

// setHeaders adds the standard API authentication and version headers.
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", apiVersion)
}

// createArchive builds an in-memory gzip+tar archive of the given paths.
func createArchive(paths []string) ([]byte, error) {
	const op = "actionscache.createArchive"
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, p := range paths {
		if err := addToTar(tw, p); err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("%s: close tar: %w", op, err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("%s: close gzip: %w", op, err)
	}
	return buf.Bytes(), nil
}

// addToTar walks path and adds all files and directories to the tar writer.
func addToTar(tw *tar.Writer, root string) error {
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		hdr, tarErr := tar.FileInfoHeader(info, "")
		if tarErr != nil {
			return fmt.Errorf("tar header: %w", tarErr)
		}
		hdr.Name = path
		if writeErr := tw.WriteHeader(hdr); writeErr != nil {
			return fmt.Errorf("write header: %w", writeErr)
		}
		if info.IsDir() {
			return nil
		}
		return copyFileToTar(tw, path)
	})
	if err != nil {
		return fmt.Errorf("addToTar: walk: %w", err)
	}
	return nil
}

// copyFileToTar copies the contents of a regular file into the tar writer.
func copyFileToTar(tw *tar.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(tw, f)
	return err //nolint:wrapcheck // io.Copy error context is already clear
}

// extractArchive reads a gzip+tar stream and writes each entry to disk.
func extractArchive(r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		if err := extractEntry(tr, hdr); err != nil {
			return err
		}
	}
	return nil
}

// extractEntry writes one tar entry to the filesystem.
func extractEntry(tr *tar.Reader, hdr *tar.Header) error {
	//nolint:gosec // G115: integer conversion from trusted archive; mode values come from archive metadata
	mode := os.FileMode(hdr.Mode)
	switch hdr.Typeflag {
	case tar.TypeDir:
		if err := os.MkdirAll(hdr.Name, mode); err != nil {
			return fmt.Errorf("extractEntry: mkdir: %w", err)
		}
		return nil
	case tar.TypeReg:
		return writeFile(tr, hdr.Name, mode)
	default:
		return nil
	}
}

// writeFile creates a file at path and writes tr's content into it.
func writeFile(tr *tar.Reader, path string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(f, tr)
	return err //nolint:wrapcheck // io.Copy error context is already clear
}
