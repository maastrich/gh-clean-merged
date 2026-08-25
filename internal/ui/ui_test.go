package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewRespectsMode(t *testing.T) {
	var buf bytes.Buffer

	// A buffer is not a terminal, so auto must stay plain.
	if New(&buf, Auto).Colored {
		t.Error("auto should not colour a non terminal stream")
	}
	if !New(&buf, Always).Colored {
		t.Error("always should colour any stream")
	}
	if New(&buf, Never).Colored {
		t.Error("never should never colour")
	}
}

func TestNoColorEnvironment(t *testing.T) {
	var buf bytes.Buffer

	// https://no-color.org: presence is what counts, whatever the value.
	t.Setenv("NO_COLOR", "")
	if enabled(&buf, Auto) {
		t.Error("NO_COLOR should disable colour")
	}
	// An explicit --color=always still wins, it is a direct instruction.
	if !enabled(&buf, Always) {
		t.Error("--color=always should override NO_COLOR")
	}
}

func TestSectionAlignsAndStaysPlain(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, Never)

	p.Section("Deleting", []Row{
		{Marker: "x", Name: "short", Reason: "PR #1 merged"},
		{Marker: "x", Name: "a-much-longer-branch", Reason: "PR #2 merged"},
	})

	got := buf.String()
	if strings.Contains(got, "\x1b[") {
		t.Errorf("plain output must carry no escape sequences: %q", got)
	}
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 || lines[0] != "Deleting" {
		t.Fatalf("unexpected output: %q", got)
	}
	// Reasons line up so the column can be skimmed.
	if strings.Index(lines[1], "PR #1") != strings.Index(lines[2], "PR #2") {
		t.Errorf("reasons are not aligned:\n%s\n%s", lines[1], lines[2])
	}
}

func TestSectionSkipsEmpty(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, Never).Section("Deleting", nil)
	if buf.Len() != 0 {
		t.Errorf("an empty section should print nothing, got %q", buf.String())
	}
}

// Only the sixteen ANSI colours are used, so the terminal theme resolves them.
func TestColoursUseTheAnsiPalette(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, Always)

	for _, painted := range []string{p.Red("x"), p.Green("x"), p.Yellow("x"), p.Blue("x"), p.Cyan("x"), p.Bold("x"), p.Dim("x")} {
		if !strings.HasSuffix(painted, reset) {
			t.Errorf("%q does not reset the style", painted)
		}
		// 38;5; and 38;2; are the palette and true colour forms, which ignore
		// the user's theme.
		if strings.Contains(painted, "38;5;") || strings.Contains(painted, "38;2;") {
			t.Errorf("%q hardcodes a colour instead of using the terminal palette", painted)
		}
	}
}
