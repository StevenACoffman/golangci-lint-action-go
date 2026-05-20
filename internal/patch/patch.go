// Package patch fetches and transforms unified diff patches for the
// only-new-issues feature.
package patch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FetchPatch fetches the diff for the current event and writes it to a temp file.
// Returns the temp file path, or "" if the event doesn't need a patch
// (merge_group, unknown event) or if the fetch fails (non-fatal, warns).
// Spec §6.1–6.3.
func FetchPatch(
	ctx context.Context,
	eventName, owner, repo string,
	payload map[string]any,
	token, workingDir, workspace string,
	do func(*http.Request) (*http.Response, error),
	tempDir func() (string, error),
	warnf func(format string, args ...any),
	infof func(format string, args ...any),
) (string, error) {
	switch eventName {
	case "pull_request", "pull_request_target":
		return fetchPRPatch(ctx, owner, repo, payload, token, workingDir, workspace,
			do, tempDir, warnf, infof)
	case "push":
		return fetchPushPatch(ctx, owner, repo, payload, token, workingDir, workspace,
			do, tempDir, warnf, infof)
	case "merge_group":
		return "", nil
	default:
		infof("Not fetching patch for showing only new issues because it's not a pull request"+
			" context: event name is %s", eventName)
		return "", nil
	}
}

// fetchPRPatch fetches a pull request diff and writes it to a temp file.
// Spec §6.2.
func fetchPRPatch(
	ctx context.Context,
	owner, repo string,
	payload map[string]any,
	token, workingDir, workspace string,
	do func(*http.Request) (*http.Response, error),
	tempDir func() (string, error),
	warnf func(format string, args ...any),
	infof func(format string, args ...any),
) (string, error) {
	pr, _ := payload["pull_request"].(map[string]any)
	if pr == nil {
		warnf("No pull request in context")
		return "", nil
	}
	number := int(prNumber(pr))
	rawURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, number)
	body, ok := fetchDiff(ctx, rawURL, token, do, warnf, "pull request")
	if !ok {
		return "", nil
	}
	return writePatch(body, "pull.patch", workingDir, workspace, tempDir, warnf, infof)
}

// fetchPushPatch fetches a push compare diff and writes it to a temp file.
// Spec §6.3.
func fetchPushPatch(
	ctx context.Context,
	owner, repo string,
	payload map[string]any,
	token, workingDir, workspace string,
	do func(*http.Request) (*http.Response, error),
	tempDir func() (string, error),
	warnf func(format string, args ...any),
	infof func(format string, args ...any),
) (string, error) {
	before, _ := payload["before"].(string)
	after, _ := payload["after"].(string)
	basehead := before + "..." + after
	rawURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/compare/%s", owner, repo, basehead)
	body, ok := fetchDiff(ctx, rawURL, token, do, warnf, "push")
	if !ok {
		return "", nil
	}
	return writePatch(body, "push.patch", workingDir, workspace, tempDir, warnf, infof)
}

// prNumber extracts the PR number from the payload as a float64 and converts to int.
func prNumber(pr map[string]any) float64 {
	n, _ := pr["number"].(float64)
	return n
}

// fetchDiff performs a GET request with the diff Accept header and returns the body.
// Returns ("", false) on network error or non-200 status (warns caller).
func fetchDiff(
	ctx context.Context,
	rawURL, token string,
	do func(*http.Request) (*http.Response, error),
	warnf func(format string, args ...any),
	kind string,
) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		warnf("failed to fetch %s patch: %v", kind, err)
		return "", false
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github.diff")
	resp, err := do(req)
	if err != nil {
		warnf("failed to fetch %s patch: %v", kind, err)
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		warnf("failed to fetch %s patch: response status is %d", kind, resp.StatusCode)
		return "", false
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		warnf("failed to fetch %s patch: %v", kind, err)
		return "", false
	}
	return string(data), true
}

// writePatch applies AlterDiffPatch and writes the result to a temp file.
// Returns the file path on success, or "" on write error (warns).
func writePatch(
	body, filename, workingDir, workspace string,
	tempDir func() (string, error),
	warnf func(format string, args ...any),
	infof func(format string, args ...any),
) (string, error) {
	transformed := AlterDiffPatch(body, workingDir, workspace)
	dir, err := tempDir()
	if err != nil {
		warnf("failed to save pull request patch: %v", err)
		return "", nil
	}
	patchPath := filepath.Join(dir, filename)
	if err = os.WriteFile(patchPath, []byte(transformed), 0o600); err != nil {
		warnf("failed to save pull request patch: %v", err)
		return "", nil
	}
	infof("Writing patch to %s", patchPath)
	return patchPath, nil
}

// AlterDiffPatch rewrites a unified diff to strip the workingDir prefix from paths.
// Returns patch unchanged when workingDir is empty.
// Spec §6.4.
func AlterDiffPatch(patch, workingDir, workspace string) string {
	if workingDir == "" || workingDir == workspace {
		return patch
	}
	relPath, err := filepath.Rel(workspace, workingDir)
	if err != nil {
		return patch
	}
	wd := filepath.ToSlash(relPath)
	if wd == "." || wd == "" {
		return patch
	}
	lines := strings.Split(patch, "\n")
	filtered := FilterDiffSections(lines, wd)
	cleanDiff := regexp.MustCompile(
		`(?m)^((?:\+{3}|-{3}) [ab]/)`+EscapeRegexpMeta(wd)+`/(.*)`).
		ReplaceAllString(strings.Join(filtered, "\n"), "${1}${2}")
	return regexp.MustCompile(
		`(?m)( [ab]/)`+EscapeRegexpMeta(wd)+`/(.*)`).
		ReplaceAllString(cleanDiff, "${1}${2}")
}

// FilterDiffSections implements the line-by-line walk that removes sections
// not touching workingDir. Exported for unit testing.
func FilterDiffSections(lines []string, wd string) []string {
	needle := " a/" + wd + "/"
	out := make([]string, 0, len(lines))
	ignore := false
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			ignore = !strings.Contains(line, needle)
			if ignore {
				continue
			}
			out = append(out, line)
			continue
		}
		if ignore {
			continue
		}
		out = append(out, line)
	}
	return out
}

// EscapeRegexpMeta escapes all regexp special characters.
// Matches JS: /[.*+?^${}()|[\]\\]/g → "\\$&". Spec §6.4.
func EscapeRegexpMeta(s string) string {
	const specials = `.*+?^${}()|[\]-`
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(specials, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
