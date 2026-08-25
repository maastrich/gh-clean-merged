// Package ui renders the output, in colour when the terminal wants it.
//
// Only the sixteen ANSI colours are used, never 24 bit values: those sixteen
// are what the user's terminal theme defines, so the output follows whatever
// palette they picked instead of imposing shades that vanish on a light
// background or glare on a dark one. Emphasis leans on bold and dim, which are
// theme independent.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Colour mode, as accepted by the --color flag.
const (
	Auto   = "auto"
	Always = "always"
	Never  = "never"
)

const (
	reset     = "\x1b[0m"
	boldSeq   = "\x1b[1m"
	dimSeq    = "\x1b[2m"
	redSeq    = "\x1b[31m"
	greenSeq  = "\x1b[32m"
	yellowSeq = "\x1b[33m"
	blueSeq   = "\x1b[34m"
	cyanSeq   = "\x1b[36m"
)

// Printer writes to a stream, with colour turned on or off once and for all.
type Printer struct {
	out     io.Writer
	Colored bool
}

// New builds a printer for the given stream. mode is one of auto, always, never.
func New(out io.Writer, mode string) *Printer {
	return &Printer{out: out, Colored: enabled(out, mode)}
}

// enabled decides whether colour is welcome here.
func enabled(out io.Writer, mode string) bool {
	switch mode {
	case Always:
		return true
	case Never:
		return false
	}

	// https://no-color.org: any value means the user opted out everywhere.
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	// Piped output is read by something that does not want escape sequences.
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (p *Printer) paint(seq, text string) string {
	if !p.Colored || text == "" {
		return text
	}
	return seq + text + reset
}

// Bold marks a heading.
func (p *Printer) Bold(text string) string { return p.paint(boldSeq, text) }

// Dim marks secondary detail, such as the reason behind a verdict.
func (p *Printer) Dim(text string) string { return p.paint(dimSeq, text) }

// Red marks a deletion.
func (p *Printer) Red(text string) string { return p.paint(redSeq, text) }

// Green marks a completed action.
func (p *Printer) Green(text string) string { return p.paint(greenSeq, text) }

// Yellow marks something needing the user's attention.
func (p *Printer) Yellow(text string) string { return p.paint(yellowSeq, text) }

// Blue marks a branch name.
func (p *Printer) Blue(text string) string { return p.paint(blueSeq, text) }

// Cyan marks a reference such as a remote or a base branch.
func (p *Printer) Cyan(text string) string { return p.paint(cyanSeq, text) }

// Printf writes a formatted line.
func (p *Printer) Printf(format string, args ...interface{}) {
	fmt.Fprintf(p.out, format, args...)
}

// Println writes a line.
func (p *Printer) Println(args ...interface{}) {
	fmt.Fprintln(p.out, args...)
}

// Row is one branch line: a marker, the branch name and the reason behind it.
type Row struct {
	Marker string
	Name   string
	Reason string
	// Paint colours the marker and the branch name.
	Paint func(string) string
}

// Section writes a titled block of rows, with the branch names aligned so the
// reasons line up and can be skimmed as a column.
func (p *Printer) Section(title string, rows []Row) {
	if len(rows) == 0 {
		return
	}

	// One unusually long branch name would otherwise push every reason halfway
	// across the terminal, so the column stops growing past a readable width.
	const maxNameWidth = 48
	width := 0
	for _, row := range rows {
		if len(row.Name) > width && len(row.Name) <= maxNameWidth {
			width = len(row.Name)
		}
	}

	p.Println(p.Bold(title))
	for _, row := range rows {
		paint := row.Paint
		if paint == nil {
			paint = func(text string) string { return text }
		}
		padding := ""
		if pad := width - len(row.Name); pad > 0 {
			padding = strings.Repeat(" ", pad)
		}
		p.Printf("  %s %s%s  %s\n", paint(row.Marker), paint(row.Name), padding, p.Dim(row.Reason))
	}
	p.Println()
}
