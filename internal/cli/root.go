package cli

import "github.com/spf13/cobra"

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

// NewRootCommand builds the `gam` root command with global flags wired into a
// shared Options value that subcommands read.
func NewRootCommand() *cobra.Command {
	opts := &Options{}
	root := &cobra.Command{
		Use:           "gam",
		Short:         "Manage Godot project addons declared in addons.toml",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.PersistentFlags().BoolVar(&opts.JSON, "json", false, "emit machine-readable JSON output")
	root.PersistentFlags().BoolVarP(&opts.Verbose, "verbose", "v", false, "enable verbose logging")
	root.PersistentFlags().BoolVarP(&opts.Quiet, "quiet", "q", false, "suppress non-error output")
	// Each subcommand receives opts so all commands share the same flag values.
	root.AddCommand(newInitCommand(opts))
	root.AddCommand(newInstallCommand(opts))
	root.AddCommand(newListCommand(opts))
	return root
}
