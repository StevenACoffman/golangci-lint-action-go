package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/StevenACoffman/golangci-lint-action-go/cmd/root"
	"github.com/StevenACoffman/golangci-lint-action-go/internal/gha"
	"github.com/StevenACoffman/golangci-lint-action-go/internal/lint"
	"github.com/StevenACoffman/golangci-lint-action-go/internal/patch"
)

func isAutoModules(experimental string) bool {
	for _, flag := range strings.Split(experimental, ",") {
		if strings.TrimSpace(flag) == "automatic-module-directories" {
			return true
		}
	}
	return false
}

func mergeGroupBaseSHA(payload map[string]any) string {
	mg, _ := payload["merge_group"].(map[string]any)
	if mg == nil {
		return ""
	}
	sha, _ := mg["base_sha"].(string)
	return sha
}

func splitRepo(repoFull string) (owner, repo string) {
	parts := strings.SplitN(repoFull, "/", 2)
	if len(parts) != 2 {
		return repoFull, ""
	}
	return parts[0], parts[1]
}

func (r runner) debug(ctx context.Context, binPath, debugInput string) error {
	for _, flag := range strings.Split(debugInput, ",") {
		flag = strings.TrimSpace(flag)
		var args []string
		switch flag {
		case "clean":
			args = []string{"cache", "clean"}
		case "cache":
			args = []string{"cache", "status"}
		default:
			continue
		}
		gha.Info(r.out, fmt.Sprintf("Running [%s %s] ...", binPath, strings.Join(args, " ")))
		//nolint:gosec // trusted golangci-lint binary
		cmd := exec.CommandContext(ctx, binPath, args...)
		out, err := cmd.CombinedOutput()
		gha.Info(r.out, string(out))
		if err != nil {
			return fmt.Errorf("run: debug %s: %w", flag, err)
		}
	}
	return nil
}

func (r runner) runLint(ctx context.Context, binPath string, inputs *runInputs) error {
	workingDir, err := resolveWorkDir(inputs)
	if err != nil {
		return err
	}
	if isAutoModules(inputs.experimental) {
		return r.runAutoModules(ctx, binPath, workingDir, inputs)
	}
	if err := gha.Group(r.out, "run golangci-lint", func() error {
		return r.runGolangciLint(ctx, binPath, workingDir, inputs)
	}); err != nil {
		return err //nolint:wrapcheck // error context already provided by upstream package
	}
	return nil
}

func (r runner) runAutoModules(
	ctx context.Context,
	binPath, workingDir string,
	inputs *runInputs,
) error {
	dirs, err := lint.ModulesAutoDetection(workingDir, filepath.Glob)
	if err != nil {
		return fmt.Errorf("run: auto modules: %w", err)
	}
	cwd, _ := os.Getwd() //nolint:forbidigo // need real cwd for group name display
	for _, dir := range dirs {
		rel, relErr := filepath.Rel(cwd, dir)
		if relErr != nil {
			rel = dir
		}
		if err := gha.Group(r.out, "run golangci-lint in "+rel, func() error {
			return r.runGolangciLint(ctx, binPath, dir, inputs)
		}); err != nil {
			return err //nolint:wrapcheck // error context already provided by upstream package
		}
	}
	return nil
}

func (r runner) runGolangciLint(
	ctx context.Context,
	binPath, workingDir string,
	inputs *runInputs,
) error {
	userArgs := lint.ParseUserArgs(inputs.args)
	addedArgs, err := r.buildLintArgs(ctx, userArgs, inputs, workingDir)
	if err != nil {
		return err
	}
	if inputs.verify {
		if err := r.runVerify(ctx, binPath, workingDir, userArgs); err != nil {
			return err
		}
	}
	return r.executeLint(ctx, binPath, workingDir, addedArgs, userArgs)
}

func (r runner) buildLintArgs(
	ctx context.Context,
	userArgs lint.UserArgs,
	inputs *runInputs,
	workingDir string,
) ([]string, error) {
	addedArgs := lint.PathModeArg(workingDir, userArgs)
	if !inputs.onlyNewIssues {
		return addedArgs, nil
	}
	newIssueArgs, err := r.buildOnlyNewIssueArgs(ctx, userArgs, inputs, workingDir)
	if err != nil {
		return nil, err
	}
	return append(addedArgs, newIssueArgs...), nil
}

func (r runner) buildOnlyNewIssueArgs(
	ctx context.Context,
	userArgs lint.UserArgs,
	inputs *runInputs,
	workingDir string,
) ([]string, error) {
	eventName := gha.EventName(r.getenv)
	payload := r.loadEventPayload()
	baseSHA := ""
	if eventName == "merge_group" {
		baseSHA = mergeGroupBaseSHA(payload)
	}
	owner, repo := splitRepo(r.getenv("GITHUB_REPOSITORY"))
	workspace := gha.Workspace(r.getenv)
	warnf := func(f string, a ...any) { gha.Warning(r.out, fmt.Sprintf(f, a...)) }
	infof := func(f string, a ...any) { gha.Info(r.out, fmt.Sprintf(f, a...)) }
	patchPath, err := patch.FetchPatch(
		ctx, eventName, owner, repo, payload,
		inputs.githubToken, workingDir, workspace,
		http.DefaultClient.Do,
		func() (string, error) { return os.MkdirTemp("", "golangci-lint-patch-*") },
		warnf, infof,
	)
	if err != nil {
		return nil, fmt.Errorf("run: fetch patch: %w", err)
	}
	args, err := lint.OnlyNewIssuesArgs(eventName, baseSHA, patchPath, userArgs)
	if err != nil {
		return nil, fmt.Errorf("run: only-new-issues: %w", err)
	}
	return args, nil
}

func (r runner) loadEventPayload() map[string]any {
	p := r.getenv("GITHUB_EVENT_PATH")
	if p == "" {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var payload map[string]any
	if err = json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	return payload
}

func (r runner) runVerify(
	ctx context.Context,
	binPath, workingDir string,
	userArgs lint.UserArgs,
) error {
	configPath := r.getConfigPath(ctx, binPath, workingDir, userArgs)
	if configPath == "" {
		return nil
	}
	configArg := userArgs.ArgMap["config"]
	verifyArgs := []string{"config", "verify"}
	if configArg != "" {
		verifyArgs = append(verifyArgs, "--config="+configArg)
	}
	cwd := workingDir
	if cwd == "" {
		cwd, _ = os.Getwd() //nolint:forbidigo // need real cwd for display
	}
	cmdStr := binPath + " " + strings.Join(verifyArgs, " ")
	gha.Info(r.out, fmt.Sprintf("Running [%s] in [%s] ...", cmdStr, cwd))
	verifyCmd := exec.CommandContext(ctx, binPath, verifyArgs...)
	if workingDir != "" {
		verifyCmd.Dir = workingDir
	}
	out, err := verifyCmd.CombinedOutput()
	gha.Info(r.out, string(out))
	if err != nil {
		return fmt.Errorf("run: config verify: %w", err)
	}
	return nil
}

func (r runner) getConfigPath(
	ctx context.Context,
	binPath, workingDir string,
	userArgs lint.UserArgs,
) string {
	configArg := userArgs.ArgMap["config"]
	pathArgs := []string{"config", "path"}
	if configArg != "" {
		pathArgs = append(pathArgs, "--config="+configArg)
	}
	cwd := workingDir
	if cwd == "" {
		cwd, _ = os.Getwd() //nolint:forbidigo // need real cwd for display
	}
	cmdStr := binPath + " " + strings.Join(pathArgs, " ")
	gha.Info(r.out, fmt.Sprintf("Running [%s] in [%s] ...", cmdStr, cwd))
	pathCmd := exec.CommandContext(ctx, binPath, pathArgs...)
	if workingDir != "" {
		pathCmd.Dir = workingDir
	}
	var stderr bytes.Buffer
	pathCmd.Stderr = &stderr
	_ = pathCmd.Run()
	return strings.TrimSpace(stderr.String())
}

func (r runner) executeLint(
	ctx context.Context,
	binPath, workingDir string,
	addedArgs []string,
	userArgs lint.UserArgs,
) error {
	displayCmd := lint.BuildLintCommand(binPath, addedArgs, userArgs)
	cwd := workingDir
	if cwd == "" {
		cwd, _ = os.Getwd() //nolint:forbidigo // need real cwd for display
	}
	gha.Info(r.out, fmt.Sprintf("Running [%s] in [%s] ...", displayCmd, cwd))
	start := time.Now()
	defer func() {
		ms := time.Since(start).Milliseconds()
		gha.Info(r.out, fmt.Sprintf("Ran golangci-lint in %dms", ms))
	}()
	execArgs := append([]string{"run"}, addedArgs...)
	execArgs = append(execArgs, strings.Fields(userArgs.Raw)...)
	//nolint:gosec // trusted golangci-lint binary
	lintCmd := exec.CommandContext(ctx, binPath, execArgs...)
	if workingDir != "" {
		lintCmd.Dir = workingDir
	}
	out, runErr := lintCmd.CombinedOutput()
	gha.Info(r.out, string(out))
	return r.interpretLintResult(runErr)
}

func (r runner) interpretLintResult(cmdErr error) error {
	if cmdErr == nil {
		gha.Info(r.out, "golangci-lint found no issues")
		return nil
	}
	var exitErr *exec.ExitError
	if !errors.As(cmdErr, &exitErr) {
		return fmt.Errorf("run: golangci-lint: %w", cmdErr)
	}
	msg, _ := lint.InterpretExitCode(exitErr.ExitCode())
	gha.Info(r.out, msg)
	return root.ExitError(exitErr.ExitCode())
}
