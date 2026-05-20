// Package cmd is the dispatcher for the golangci-lint-action-go CLI.
// It registers all commands and routes incoming arguments
// to the matching command implementation.
package cmd

// climax:name golangci-lint-action-go
// climax:root-pkg root
// climax:env-prefix GOLANGCI_LINT_ACTION_GO

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"

	"github.com/StevenACoffman/golangci-lint-action-go/cmd/postrun"
	"github.com/StevenACoffman/golangci-lint-action-go/cmd/root"
	"github.com/StevenACoffman/golangci-lint-action-go/cmd/run"
	"github.com/StevenACoffman/golangci-lint-action-go/cmd/version"
)

// Run parses args and dispatches to the matching command.
// args must not include the executable name (pass os.Args[1:]).
// getenv is injected so subcommands can read environment variables in a
// testable way without touching the real process environment.
//
// Every flag can be set via a GOLANGCI_LINT_ACTION_GO_-prefixed environment variable.
// The mapping rule is: prepend GOLANGCI_LINT_ACTION_GO_, uppercase, replace dashes with
// underscores.
//
// Flags supplied on the command line always take precedence over env vars.
func Run(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	getenv func(string) string,
) error {
	r := root.New(stdin, stdout, stderr, getenv)
	version.New(r)
	run.New(r)
	postrun.New(r)

	if err := r.Command.Parse(args, ff.WithEnvVarPrefix("GOLANGCI_LINT_ACTION_GO")); err != nil {
		_, _ = fmt.Fprintf(stderr, "\n%s\n", ffhelp.Command(r.Command))
		return fmt.Errorf("parse: %w", err)
	}

	if err := r.Command.Run(ctx); err != nil {
		// Don't print usage help for ErrNoExec (no subcommand given) or
		// ExitError (command already reported its own outcome).
		var exitErr root.ExitError
		if !errors.Is(err, ff.ErrNoExec) && !errors.As(err, &exitErr) {
			_, _ = fmt.Fprintf(stderr, "\n%s\n", ffhelp.Command(r.Command.GetSelected()))
		}
		return err
	}

	return nil
}
