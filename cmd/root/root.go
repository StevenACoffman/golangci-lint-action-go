// Package root defines the root configuration for the CLI.
package root

import (
	"fmt"
	"io"

	"github.com/peterbourgon/ff/v4"
)

// ExitError is returned by commands that want a specific non-zero exit code
// without printing an additional error message. run() in main.go checks for
// ExitError with errors.As and calls os.Exit(int(e)) directly, bypassing the
// default "error: ..." printer.
type ExitError int

// Config holds shared I/O writers, an env reader, and the root ff.Command.
// All subcommand configs embed *Config to inherit these.
type Config struct {
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Getenv  func(string) string // injected in main; overridden in tests
	Flags   *ff.FlagSet
	Command *ff.Command
}

func (e ExitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// New returns a new root Config with the given I/O writers and env reader.
func New(stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) *Config {
	var cfg Config
	cfg.Stdin = stdin
	cfg.Stdout = stdout
	cfg.Stderr = stderr
	cfg.Getenv = getenv
	cfg.Command = &ff.Command{
		Name:      "golangci-lint-action-go",
		Usage:     "golangci-lint-action-go <SUBCOMMAND> ...",
		ShortHelp: "golangci-lint GitHub Action implemented in Go",
	}
	return &cfg
}
