package run

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/StevenACoffman/golangci-lint-action-go/internal/gha"
	"github.com/StevenACoffman/golangci-lint-action-go/internal/install"
	"github.com/StevenACoffman/golangci-lint-action-go/internal/lintver"
	"github.com/StevenACoffman/golangci-lint-action-go/internal/plugins"
)

const versionMappingURL = "https://raw.githubusercontent.com/golangci/golangci-lint" +
	"/HEAD/assets/github-action-config-v2.json"

func (r runner) install(ctx context.Context, inputs *runInputs) (string, error) {
	r.registerMatcher(inputs.problemMatchers)
	mode := install.NormalizeMode(inputs.installMode)
	infof := func(f string, a ...any) { gha.Info(r.out, fmt.Sprintf(f, a...)) }
	warnf := func(f string, a ...any) { gha.Warning(r.out, fmt.Sprintf(f, a...)) }
	requested, err := lintver.RequestedVersion(
		inputs.version, inputs.versionFile, inputs.workingDirectory,
		os.ReadFile,
		func(p string) bool { _, e := os.Stat(p); return e == nil },
		warnf, infof,
	)
	if err != nil {
		return "", fmt.Errorf("run: version: %w", err)
	}
	versionInfo, err := lintver.GetVersion(
		ctx, mode, inputs.version, requested, fetchVersionMapping, infof,
	)
	if err != nil {
		return "", fmt.Errorf("run: version: %w", err)
	}
	binPath, err := r.installBinary(ctx, mode, versionInfo)
	if err != nil {
		return "", err
	}
	return r.applyPlugins(ctx, binPath, inputs)
}

func (r runner) registerMatcher(enabled bool) {
	if !enabled {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	matcherPath := filepath.Join(filepath.Dir(exe), "..", "..", "problem-matchers.json")
	abs, err := filepath.Abs(matcherPath)
	if err != nil {
		return
	}
	if _, err = os.Stat(abs); err != nil {
		return
	}
	gha.RegisterProblemMatcher(r.out, abs)
}

func (r runner) installBinary(
	ctx context.Context,
	mode lintver.InstallMode,
	versionInfo lintver.VersionInfo,
) (string, error) {
	switch mode {
	case lintver.ModeNone:
		p, err := install.FindInPath(exec.LookPath)
		if err != nil {
			return "", err //nolint:wrapcheck // ErrNotFound message is spec-exact
		}
		return p, nil
	case lintver.ModeGoInstall:
		return r.goInstallBin(ctx, versionInfo)
	default:
		return r.downloadBinary(ctx, versionInfo)
	}
}

func (r runner) goInstallBin(ctx context.Context, versionInfo lintver.VersionInfo) (string, error) {
	pkg := "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@" +
		versionInfo.TargetVersion
	env := append(os.Environ(), "CGO_ENABLED=1")
	gha.Info(r.out, fmt.Sprintf("Installing golangci-lint %s...", versionInfo.TargetVersion))
	start := time.Now()
	if err := r.runGoInstall(ctx, pkg, env); err != nil {
		return "", err
	}
	binPath, err := r.goInstallBinPath(ctx, pkg, env)
	if err != nil {
		return "", err
	}
	gha.Info(r.out, fmt.Sprintf(
		"Installed golangci-lint into %s in %dms",
		binPath, time.Since(start).Milliseconds(),
	))
	return binPath, nil
}

func (r runner) runGoInstall(ctx context.Context, pkg string, env []string) error {
	//nolint:gosec // pkg is a trusted golangci-lint module path
	cmd := exec.CommandContext(ctx, "go", "install", pkg)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	gha.Info(r.out, string(out))
	if err != nil {
		return fmt.Errorf("run: go install: %w", err)
	}
	return nil
}

func (r runner) goInstallBinPath(
	ctx context.Context,
	pkg string,
	env []string,
) (string, error) {
	//nolint:gosec // pkg is a trusted golangci-lint module path
	cmd := exec.CommandContext(ctx, "go", "install", "-n", pkg)
	cmd.Env = env
	var stderr bytes.Buffer
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	gha.Info(r.out, stdout.String())
	gha.Info(r.out, stderr.String())
	binPath := install.ParseGoInstallBinPath(stderr.String())
	if binPath == "" {
		return "", errors.New("run: go install -n: could not determine binary path")
	}
	return binPath, nil
}

func (r runner) downloadBinary(
	ctx context.Context,
	versionInfo lintver.VersionInfo,
) (string, error) {
	gha.Info(r.out, fmt.Sprintf(
		"Installing golangci-lint binary %s...", versionInfo.TargetVersion,
	))
	platform, arch, ext := install.PlatformStrings(runtime.GOOS, runtime.GOARCH)
	assetURL := install.AssetURL(versionInfo.TargetVersion, platform, arch, ext)
	home := r.getenv("HOME")
	if runtime.GOOS == "windows" {
		home = r.getenv("USERPROFILE")
	}
	start := time.Now()
	extractedRoot, err := downloadAndExtract(ctx, assetURL, runtime.GOOS, home)
	if err != nil {
		return "", fmt.Errorf("run: download binary: %w", err)
	}
	binPath := install.ExtractBinPath(assetURL, extractedRoot)
	gha.Info(r.out, fmt.Sprintf(
		"Installed golangci-lint into %s in %dms",
		binPath, time.Since(start).Milliseconds(),
	))
	return binPath, nil
}

func downloadAndExtract(ctx context.Context, assetURL, goos, home string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("run: download request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("run: download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("run: download: status %d", resp.StatusCode)
	}
	tmpFile, err := os.CreateTemp("", "golangci-lint-archive-*")
	if err != nil {
		return "", fmt.Errorf("run: download: create temp: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err = io.Copy(tmpFile, resp.Body); err != nil {
		_ = tmpFile.Close()
		return "", fmt.Errorf("run: download: write body: %w", err)
	}
	if err = tmpFile.Close(); err != nil {
		return "", fmt.Errorf("run: download: close temp: %w", err)
	}
	return extractArchive(ctx, tmpPath, goos, home, assetURL)
}

func extractArchive(ctx context.Context, archivePath, goos, home, assetURL string) (string, error) {
	if strings.HasSuffix(assetURL, ".zip") {
		return extractZip(archivePath, home)
	}
	return extractTar(ctx, archivePath, home, install.TarArgs(goos))
}

func extractTar(ctx context.Context, archivePath, dest string, args []string) (string, error) {
	cmdArgs := append(append([]string{}, args...), "-C", dest, "-f", archivePath)
	//nolint:gosec // trusted golangci-lint release archive
	cmd := exec.CommandContext(ctx, "tar", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run: extract tar: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return dest, nil
}

func extractZip(archivePath, dest string) (string, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("run: extract zip: open: %w", err)
	}
	defer func() { _ = r.Close() }()
	clean := filepath.Clean(dest) + string(os.PathSeparator)
	for _, f := range r.File {
		//nolint:gosec // G305: path validated against dest prefix below
		fpath := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(filepath.Clean(fpath), clean) {
			return "", fmt.Errorf("run: extract zip: illegal path: %s", f.Name)
		}
		if err := extractZipFile(f, fpath); err != nil {
			return "", err
		}
	}
	return dest, nil
}

func extractZipFile(f *zip.File, dest string) error {
	if f.FileInfo().IsDir() {
		if err := os.MkdirAll(dest, f.Mode()); err != nil {
			return fmt.Errorf("run: extract zip: mkdir dir: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("run: extract zip: mkdir parent: %w", err)
	}
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("run: extract zip: open entry: %w", err)
	}
	defer func() { _ = rc.Close() }()
	outFile, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return fmt.Errorf("run: extract zip: create: %w", err)
	}
	defer func() { _ = outFile.Close() }()
	if _, err = io.Copy(outFile, rc); err != nil { //nolint:gosec // G110: trusted source
		return fmt.Errorf("run: extract zip: copy: %w", err)
	}
	return nil
}

func (r runner) applyPlugins(
	ctx context.Context,
	binPath string,
	inputs *runInputs,
) (string, error) {
	workingDir, err := resolveWorkDir(inputs)
	if err != nil {
		return "", err
	}
	configFile, err := plugins.FindConfigFile(workingDir, os.Lstat)
	if err != nil {
		return "", fmt.Errorf("run: plugins find config: %w", err)
	}
	if configFile == "" {
		return binPath, nil
	}
	gha.Info(r.out, "Found configuration for the plugin module system : "+configFile)
	gha.Info(r.out, "Building and installing custom golangci-lint binary...")
	cfg, err := plugins.ParseConfig(configFile, os.ReadFile)
	if err != nil {
		return "", fmt.Errorf("run: plugins parse config: %w", err)
	}
	plugins.ApplyDefaults(cfg)
	return r.installPlugin(ctx, binPath, workingDir, configFile, cfg, inputs)
}

func (r runner) installPlugin(
	ctx context.Context,
	binPath, workingDir, configFile string,
	cfg *plugins.PluginConfig,
	inputs *runInputs,
) (string, error) {
	start := time.Now()
	infof := func(f string, a ...any) { gha.Info(r.out, fmt.Sprintf(f, a...)) }
	warnf := func(f string, a ...any) { gha.Warning(r.out, fmt.Sprintf(f, a...)) }
	runCmd := func(name string, args []string, dir string) error {
		//nolint:gosec // name is the verified golangci-lint binary
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dir
		out, runErr := cmd.CombinedOutput()
		gha.Info(r.out, string(out))
		return runErr //nolint:wrapcheck // error is wrapped by plugins.Install
	}
	newBinPath, err := plugins.Install(
		binPath, workingDir, configFile, cfg, inputs.version,
		func(p string) bool { _, e := os.Stat(p); return e == nil },
		os.MkdirAll,
		runCmd, warnf, infof,
	)
	if err != nil {
		return "", err //nolint:wrapcheck // spec-exact error message from plugins.Install
	}
	gha.Info(r.out, fmt.Sprintf(
		"Built custom golangci-lint binary in %dms", time.Since(start).Milliseconds(),
	))
	return newBinPath, nil
}

func fetchVersionMapping(ctx context.Context) (map[string]lintver.VersionInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionMappingURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("run: version mapping request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("run: version mapping fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("run: version mapping: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("run: version mapping read: %w", err)
	}
	m, err := lintver.ParseVersionMapping(body)
	if err != nil {
		return nil, fmt.Errorf("run: version mapping parse: %w", err)
	}
	return m, nil
}
