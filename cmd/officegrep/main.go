// Command officegrep is an rg-style CLI search tool specialized for
// searching inside MS Office files (docx/pptx/xlsx) as well as plain
// text.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	_ "officegrep/internal/adapters/extract/all"
	"officegrep/internal/adapters/match"
	"officegrep/internal/adapters/output"
	"officegrep/internal/adapters/walk"
	"officegrep/internal/adapters/xdg"
	"officegrep/internal/core/app"
	"officegrep/internal/core/domain"
	"officegrep/internal/core/ports"
	"officegrep/internal/registry"
)

const longHelp = `officegrep searches for a pattern in files, including MS Office
documents (docx/pptx/xlsx) and plain text, streaming through each
document instead of loading it fully into memory.

Usage:

  officegrep [flags] PATTERN [PATH...]

If no PATH is given, the current directory is searched. PATTERN is a
regular expression by default; use -F/--fixed-strings for a literal
search.

Config file:

  An optional TOML config file is read from
  $XDG_CONFIG_HOME/officegrep/config.toml (typically
  ~/.config/officegrep/config.toml) to seed default values for --color,
  --threads, and extra ignore globs. Command-line flags always override
  the config file.

Shell completion:

  This build includes cobra's built-in "completion" subcommand. Install
  it, e.g.:

    bash: officegrep completion bash > \
            "${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions/officegrep"
    zsh:  officegrep completion zsh > "/path/on/\$fpath/_officegrep"
    fish: officegrep completion fish > \
            "$HOME/.config/fish/completions/officegrep.fish"

  Run "officegrep completion --help" for full details.
`

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(2)
	}
}

// cliFlags holds the values bound to command-line flags, pre-seeded
// from the XDG config file (if present) so that config values act as
// defaults that flags can override.
type cliFlags struct {
	ignoreCase   bool
	fixedStrings bool
	color        string
	threads      int
	jsonOutput   bool
}

func newRootCmd() *cobra.Command {
	cfg, cfgErr := xdg.LoadConfig()

	flags := cliFlags{
		color:   "auto",
		threads: 0,
	}
	if cfg.Color != "" {
		flags.color = cfg.Color
	}
	if cfg.Threads > 0 {
		flags.threads = cfg.Threads
	}

	rootCmd := &cobra.Command{
		Use:           "officegrep PATTERN [PATH...]",
		Short:         "Search plain text and MS Office documents for a pattern",
		Long:          longHelp,
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "officegrep: warning: reading config: %v\n", cfgErr)
			}
			return runSearch(cmd, args, flags, cfg)
		},
	}

	// Leave completion enabled: cobra's built-in "completion" command
	// (bash/zsh/fish/powershell) is generated automatically as long as
	// we don't disable it here.
	rootCmd.CompletionOptions.DisableDefaultCmd = false

	rootCmd.Flags().BoolVarP(&flags.ignoreCase, "ignore-case", "i", false, "case-insensitive search")
	rootCmd.Flags().BoolVarP(&flags.fixedStrings, "fixed-strings", "F", false, "treat PATTERN as a literal string, not a regex")
	rootCmd.Flags().StringVar(&flags.color, "color", flags.color, "when to colorize output: auto, always, never")
	rootCmd.Flags().IntVarP(&flags.threads, "threads", "j", flags.threads, "number of search worker threads (default: number of CPUs)")
	rootCmd.Flags().BoolVar(&flags.jsonOutput, "json", false, "emit JSON-lines output instead of terminal formatting")

	return rootCmd
}

func runSearch(cmd *cobra.Command, args []string, flags cliFlags, cfg xdg.Config) error {
	pattern := args[0]
	roots := args[1:]
	if len(roots) == 0 {
		roots = []string{"."}
	}

	opts := domain.SearchOptions{
		IgnoreCase:   flags.ignoreCase,
		FixedStrings: flags.fixedStrings,
		Threads:      flags.threads,
		ExcludeGlobs: cfg.Ignore,
	}

	var sink ports.OutputSink
	if flags.jsonOutput {
		sink = output.NewJSON(cmd.OutOrStdout())
	} else {
		mode := output.ColorMode(flags.color)
		switch mode {
		case output.ColorAuto, output.ColorAlways, output.ColorNever:
		default:
			return fmt.Errorf("invalid --color value %q (want auto, always, or never)", flags.color)
		}
		sink = output.NewTerminal(cmd.OutOrStdout(), mode, os.Stdout)
	}

	orch := app.New(registry.Default, walk.New(), match.NewFactory(), sink)

	stats, err := orch.Run(context.Background(), pattern, roots, opts)
	if err != nil {
		return err
	}

	if stats.TotalMatches == 0 {
		os.Exit(1)
	}
	return nil
}
