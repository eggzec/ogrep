// Command officegrep is an rg-style CLI search tool specialized for
// searching inside MS Office files (docx/pptx/xlsx) as well as plain
// text.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

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
	wholeWord    bool
	invertMatch  bool
	color        string
	threads      int
	jsonOutput   bool

	maxCount int

	includeGlobs []string
	excludeGlobs []string
	noIgnore     bool
	types        []string

	filesWithMatches bool
	countOnly        bool
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
	rootCmd.Flags().BoolVarP(&flags.wholeWord, "word-regexp", "w", false, "only match whole words")
	rootCmd.Flags().BoolVarP(&flags.invertMatch, "invert-match", "v", false, "select text units that do NOT match PATTERN")
	rootCmd.Flags().StringVar(&flags.color, "color", flags.color, "when to colorize output: auto, always, never")
	rootCmd.Flags().IntVarP(&flags.threads, "threads", "j", flags.threads, "number of search worker threads (default: number of CPUs)")
	rootCmd.Flags().BoolVar(&flags.jsonOutput, "json", false, "emit JSON-lines output instead of terminal formatting")

	rootCmd.Flags().IntVarP(&flags.maxCount, "max-count", "m", 0, "stop after N matches per file (0 = unlimited)")

	rootCmd.Flags().StringArrayVar(&flags.includeGlobs, "include", nil, "only search files matching this glob (repeatable)")
	rootCmd.Flags().StringArrayVar(&flags.excludeGlobs, "exclude", nil, "skip files matching this glob (repeatable)")
	rootCmd.Flags().BoolVar(&flags.noIgnore, "no-ignore", false, "don't respect .gitignore/.officegrepignore files")
	rootCmd.Flags().StringArrayVar(&flags.types, "type", nil, "only search files of this format, e.g. docx, pptx, xlsx, text (repeatable)")

	rootCmd.Flags().BoolVarP(&flags.filesWithMatches, "files-with-matches", "l", false, "print only the paths of files with a match, one per line")
	rootCmd.Flags().BoolVarP(&flags.countOnly, "count", "c", false, "print only \"path:count\" per matching file, instead of each match")

	return rootCmd
}

func runSearch(cmd *cobra.Command, args []string, flags cliFlags, cfg xdg.Config) error {
	pattern := args[0]
	roots := args[1:]
	if len(roots) == 0 {
		roots = []string{"."}
	}

	if flags.filesWithMatches && flags.countOnly {
		return fmt.Errorf("-l/--files-with-matches and -c/--count are mutually exclusive")
	}

	if len(flags.types) > 0 {
		if err := validateTypes(flags.types); err != nil {
			return err
		}
	}

	opts := domain.SearchOptions{
		IgnoreCase:   flags.ignoreCase,
		FixedStrings: flags.fixedStrings,
		WholeWord:    flags.wholeWord,
		InvertMatch:  flags.invertMatch,
		NoIgnore:     flags.noIgnore,

		MaxCount: flags.maxCount,

		IncludeGlobs: flags.includeGlobs,
		ExcludeGlobs: append(append([]string{}, cfg.Ignore...), flags.excludeGlobs...),
		Types:        flags.types,

		Threads: flags.threads,

		FilesWithMatches: flags.filesWithMatches,
		CountOnly:        flags.countOnly,
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
		summary := output.SummaryModeOff
		switch {
		case flags.filesWithMatches:
			summary = output.SummaryModePathOnly
		case flags.countOnly:
			summary = output.SummaryModeCount
		}
		sink = output.NewTerminal(cmd.OutOrStdout(), mode, os.Stdout, summary)
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

// validateTypes checks each requested --type value against the actually
// registered extractors' Name()s, returning a clear error listing valid
// choices if an unknown type is given, rather than silently matching no
// files.
func validateTypes(types []string) error {
	registered := registry.Default.All()
	valid := make(map[string]bool, len(registered))
	names := make([]string, 0, len(registered))
	for _, e := range registered {
		valid[e.Name()] = true
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, t := range types {
		if !valid[t] {
			return fmt.Errorf("unknown --type %q (valid types: %s)", t, strings.Join(names, ", "))
		}
	}
	return nil
}
