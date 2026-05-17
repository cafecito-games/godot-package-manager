package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/spf13/cobra"
)

// version is the build version of gpm. It is "dev" for builds from source and
// is overridden at release time via -ldflags.
var version = "dev"

// Options holds global flags shared by all subcommands.
type Options struct {
	JSON    bool
	Verbose bool
	Quiet   bool
}

// UsageError marks an error caused by bad flags or arguments (exit code 2).
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// NewRootCommand builds the `gpm` root command with global flags wired into a
// shared Options value that subcommands read.
func NewRootCommand() *cobra.Command {
	return newRootCommand(&Options{})
}

func newRootCommand(opts *Options) *cobra.Command {
	root := &cobra.Command{
		Use:           "gpm",
		Short:         "Manage Godot project addons declared in addons.toml",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &UsageError{Err: err}
	})
	root.PersistentFlags().BoolVar(&opts.JSON, "json", false, "emit machine-readable JSON output")
	root.PersistentFlags().BoolVarP(&opts.Verbose, "verbose", "v", false, "enable verbose logging")
	root.PersistentFlags().BoolVarP(&opts.Quiet, "quiet", "q", false, "suppress non-error output")
	// Each subcommand receives opts so all commands share the same flag values.
	root.AddCommand(newInitCommand(opts))
	root.AddCommand(newInstallCommand(opts))
	root.AddCommand(newListCommand(opts))
	root.AddCommand(newUpdateCommand(opts))
	root.AddCommand(newRemoveCommand(opts))
	root.AddCommand(newAddCommand(opts))
	return root
}

// Execute runs the CLI with explicit streams and returns the mapped process exit
// code. It is used by main and tests so error rendering stays consistent.
func Execute(args []string, stdout, stderr io.Writer) output.ExitCode {
	// Cancel in-flight git/HTTP work when the user interrupts the process.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := &Options{}
	cmd := newRootCommand(opts)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)

	if err := cmd.ExecuteContext(ctx); err != nil {
		code := codeForError(err)
		if opts.JSON {
			encoder := json.NewEncoder(stdout)
			encoder.SetIndent("", "  ")
			_ = encoder.Encode(map[string]any{
				"error": err.Error(),
				"code":  code,
			})
		} else {
			_, _ = fmt.Fprintf(stderr, "gpm: %v\n", err)
		}
		return code
	}
	return output.ExitOK
}

func codeForError(err error) output.ExitCode {
	var usageErr *UsageError
	if errors.As(err, &usageErr) {
		return output.ExitUsage
	}
	return output.CodeFor(err)
}

func usageNoArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.NoArgs(cmd, args); err != nil {
		return &UsageError{Err: err}
	}
	return nil
}

func usageExactArgs(count int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(count)(cmd, args); err != nil {
			return &UsageError{Err: err}
		}
		return nil
	}
}

func verbosef(cmd *cobra.Command, opts *Options, format string, args ...any) {
	if opts.Verbose && !opts.Quiet && !opts.JSON {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), format, args...)
	}
}
