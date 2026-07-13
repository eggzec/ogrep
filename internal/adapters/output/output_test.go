package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"officegrep/internal/core/domain"
)

func sampleMatch() domain.Match {
	return domain.Match{
		Location: domain.Location{Format: "text", Path: "a.txt", Human: "line 3", Line: 3},
		Text:     "hello world",
		Spans:    []domain.Span{{Start: 0, End: 5}},
	}
}

func TestTerminalNoColorWhenNever(t *testing.T) {
	var buf bytes.Buffer
	term := NewTerminal(&buf, ColorNever, nil)
	if err := term.WriteMatch(sampleMatch()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("expected no ANSI codes with ColorNever, got %q", out)
	}
	if !strings.Contains(out, "a.txt:line 3") {
		t.Errorf("expected location prefix in output, got %q", out)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("expected matched text in output, got %q", out)
	}
}

func TestTerminalColorWhenAlways(t *testing.T) {
	var buf bytes.Buffer
	term := NewTerminal(&buf, ColorAlways, nil)
	if err := term.WriteMatch(sampleMatch()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected ANSI codes with ColorAlways, got %q", out)
	}
	if !strings.Contains(out, ansiMatch+"hello"+ansiReset) {
		t.Errorf("expected highlighted span around %q, got %q", "hello", out)
	}
}

func TestTerminalAutoWithNilTTYIsNoColor(t *testing.T) {
	var buf bytes.Buffer
	term := NewTerminal(&buf, ColorAuto, nil)
	if err := term.WriteMatch(sampleMatch()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "\x1b[") {
		t.Error("expected ColorAuto with no tty file to disable color")
	}
}

func TestJSONWriteMatch(t *testing.T) {
	var buf bytes.Buffer
	sink := NewJSON(&buf)
	if err := sink.WriteMatch(sampleMatch()); err != nil {
		t.Fatal(err)
	}

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output was not valid JSON: %v (%q)", err, buf.String())
	}
	if rec["path"] != "a.txt" {
		t.Errorf("path = %v, want a.txt", rec["path"])
	}
	if rec["text"] != "hello world" {
		t.Errorf("text = %v, want %q", rec["text"], "hello world")
	}
	spans, ok := rec["spans"].([]any)
	if !ok || len(spans) != 1 {
		t.Errorf("spans = %v, want one span", rec["spans"])
	}
}

func TestJSONWriteFileSummary(t *testing.T) {
	var buf bytes.Buffer
	sink := NewJSON(&buf)
	if err := sink.WriteFileSummary("a.txt", 5); err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output was not valid JSON: %v", err)
	}
	if rec["type"] != "summary" || rec["match_count"].(float64) != 5 {
		t.Errorf("unexpected summary record: %v", rec)
	}
}

func TestJSONIsLineDelimited(t *testing.T) {
	var buf bytes.Buffer
	sink := NewJSON(&buf)
	sink.WriteMatch(sampleMatch())
	sink.WriteMatch(sampleMatch())
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("got %d lines, want 2", len(lines))
	}
}
