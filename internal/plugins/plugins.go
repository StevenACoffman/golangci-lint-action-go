// Package plugins installs a custom golangci-lint binary from a
// .custom-gcl.{yml,yaml,json} config file when one is found in rootDir.
package plugins

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// PluginConfig holds fields parsed from .custom-gcl.{yml,yaml,json}.
type PluginConfig struct {
	Version     string `yaml:"version"     json:"version"`
	Destination string `yaml:"destination" json:"destination"`
	Name        string `yaml:"name"        json:"name"`
}

// FindConfigFile returns the path of the first .custom-gcl config file found
// in rootDir, searching in order: .yml, .yaml, .json.
// Returns ("", nil) when none exists. Spec §4.5.
func FindConfigFile(rootDir string, stat func(string) (os.FileInfo, error)) (string, error) {
	candidates := []string{
		".custom-gcl.yml",
		".custom-gcl.yaml",
		".custom-gcl.json",
	}
	for _, name := range candidates {
		p := filepath.Join(rootDir, name)
		if _, err := stat(p); err == nil {
			return p, nil
		}
	}
	return "", nil
}

// ParseConfig reads and parses a config file (YAML for all extensions).
func ParseConfig(path string, readFile func(string) ([]byte, error)) (*PluginConfig, error) {
	const op = "plugins.ParseConfig"
	data, err := readFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	var cfg PluginConfig
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &cfg, nil
}

// ApplyDefaults fills in missing fields:
//
//	destination = "." if empty
//	name        = "custom-gcl" if empty
func ApplyDefaults(cfg *PluginConfig) {
	if cfg.Destination == "" {
		cfg.Destination = "."
	}
	if cfg.Name == "" {
		cfg.Name = "custom-gcl"
	}
}

// Install runs golangci-lint custom in rootDir and returns the new binary path.
// Spec §4.5.
func Install(
	binPath, rootDir, configFile string,
	cfg *PluginConfig,
	versionInput string,
	destExists func(string) bool,
	mkdirAll func(path string, perm os.FileMode) error,
	runCmd func(name string, args []string, dir string) error,
	warnf func(format string, args ...any),
	infof func(format string, args ...any),
) (string, error) {
	if versionInput != "" && cfg.Version != versionInput {
		warnf(
			"The golangci-lint version (%s) defined inside %s does not match the version defined in the action (%s)",
			cfg.Version,
			configFile,
			versionInput,
		)
	}

	if !destExists(cfg.Destination) {
		infof("Creating destination directory: %s", cfg.Destination)
		if err := mkdirAll(cfg.Destination, 0o755); err != nil {
			return "", fmt.Errorf("plugins.Install: mkdirAll: %w", err)
		}
	}

	infof("Running [%s custom] in [%s] ...", binPath, rootDir)

	if err := runCmd(binPath, []string{"custom"}, rootDir); err != nil {
		//nolint:staticcheck // spec §4.5 requires capitalized message
		return "", fmt.Errorf("Failed to build custom golangci-lint binary: %w", err)
	}

	return filepath.Join(rootDir, cfg.Destination, cfg.Name), nil
}
