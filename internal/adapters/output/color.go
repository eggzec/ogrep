// Package output provides ports.OutputSink implementations: an
// rg-style colorized terminal sink and a JSON-lines sink for tool
// consumption.
package output

import (
	"os"

	"golang.org/x/term"
)

// ColorMode is the tri-state color setting shared by both the CLI flag
// and the XDG config file.
type ColorMode string

const (
	ColorAuto   ColorMode = "auto"
	ColorAlways ColorMode = "always"
	ColorNever  ColorMode = "never"
)

// shouldColor resolves a ColorMode against whether f looks like a
// terminal.
func shouldColor(mode ColorMode, f *os.File) bool {
	if f == nil {
		return mode == ColorAlways
	}
	switch mode {
	case ColorAlways:
		return true
	case ColorNever:
		return false
	default: // auto
		return term.IsTerminal(int(f.Fd()))
	}
}

// ANSI SGR codes used for rg-style highlighting.
const (
	ansiReset     = "\x1b[0m"
	ansiPathColor = "\x1b[1;35m" // bold magenta
	ansiLocColor  = "\x1b[32m"   // green
	ansiMatch     = "\x1b[1;31m" // bold red
)
