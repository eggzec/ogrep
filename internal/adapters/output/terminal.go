package output

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sync"

	"officegrep/internal/core/domain"
)

// Terminal is an rg-style OutputSink: the file path in one color, the
// human-readable location, and the matched span(s) highlighted within
// the surrounding text. It is safe for concurrent use from multiple
// goroutines — the orchestrator's per-file workers may call WriteMatch
// concurrently, and Terminal serializes writes with a mutex so results
// for a given call are never interleaved mid-line.
type Terminal struct {
	mu    sync.Mutex
	w     *bufio.Writer
	color bool
}

// NewTerminal builds a Terminal sink writing to w. tty is the *os.File
// actually behind w (typically os.Stdout); pass nil if w isn't backed
// by a real file (e.g. in tests capturing to a buffer), in which case
// ColorAuto behaves as "never colorize".
func NewTerminal(w io.Writer, mode ColorMode, tty *os.File) *Terminal {
	color := mode == ColorAlways
	if tty != nil {
		color = shouldColor(mode, tty)
	}
	return &Terminal{w: bufio.NewWriter(w), color: color}
}

// WriteMatch implements ports.OutputSink.
func (t *Terminal) WriteMatch(m domain.Match) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	loc := m.Location.String()
	if t.color {
		fmt.Fprintf(t.w, "%s%s%s\n", ansiPathColor, loc, ansiReset)
	} else {
		fmt.Fprintf(t.w, "%s\n", loc)
	}

	t.writeHighlighted(m.Text, m.Spans)
	t.w.WriteByte('\n')
	return t.w.Flush()
}

func (t *Terminal) writeHighlighted(text string, spans []domain.Span) {
	if len(spans) == 0 || !t.color {
		t.w.WriteString(text)
		return
	}
	pos := 0
	for _, sp := range spans {
		if sp.Start < pos || sp.Start > len(text) || sp.End > len(text) || sp.End < sp.Start {
			continue // defensively skip malformed spans
		}
		t.w.WriteString(text[pos:sp.Start])
		t.w.WriteString(ansiMatch)
		t.w.WriteString(text[sp.Start:sp.End])
		t.w.WriteString(ansiReset)
		pos = sp.End
	}
	t.w.WriteString(text[pos:])
}

// WriteFileSummary implements ports.OutputSink. Terminal renders it as
// a blank-line separator; most rg-style tools rely on per-match output
// alone, so this is intentionally minimal in v1.
func (t *Terminal) WriteFileSummary(path string, matchCount int) error {
	return nil
}

// Flush implements ports.OutputSink.
func (t *Terminal) Flush() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.w.Flush()
}
