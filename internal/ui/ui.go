package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// Colors (simple ANSI — no external deps).
const (
	reset  = "\033[0m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	blue   = "\033[34m"
	cyan   = "\033[36m"
	gray   = "\033[90m"
	bold   = "\033[1m"
)

// Symbols used in output. On Windows terminals that don't support Unicode
// (legacy cmd.exe), we fall back to ASCII equivalents.
var (
	symSuccess = "✓"
	symError   = "✗"
	symInfo    = "ℹ"
	symWarn    = "⚠"
	symLine    = "─"
)

// isTTYFile checks if the given file is a terminal.
func isTTYFile(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// isTTY returns true when either stdout or stderr is a terminal. Human
// output goes to stderr in JSON mode, so color stays enabled when stdout
// is piped but stderr is still a terminal.
func isTTY() bool {
	return isTTYFile(os.Stdout) || isTTYFile(os.Stderr)
}

// jsonMode is set when the user passes --json. Human-oriented output
// (Info/Success/Warn/Header/Table/Muted) is then routed to stderr so
// stdout carries only machine-readable JSON — safe for scripts and AI
// agents that parse stdout.
var jsonMode bool

// SetJSONMode enables or disables JSON output routing.
func SetJSONMode(on bool) {
	jsonMode = on
}

// out returns the writer human output should go to — stderr in JSON mode,
// stdout otherwise.
func out() *os.File {
	if jsonMode {
		return os.Stderr
	}
	return os.Stdout
}

// PrintJSON writes v to stdout as indented JSON. Always goes to stdout,
// even in JSON mode (it is the payload, not the log). Exits 1 if the
// value can't be marshalled — callers promise JSON on stdout, so a
// marshal failure must not look like success.
func PrintJSON(v interface{}) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s Could not marshal JSON output: %v\n", colorize(red, symError+" "), err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, string(data))
}

// Printf writes formatted text to the human-output stream (stderr in
// JSON mode, stdout otherwise). Use for prompts and menu text that must
// not pollute JSON stdout.
func Printf(format string, args ...interface{}) {
	fmt.Fprintf(out(), format, args...)
}

// Println writes a line to the human-output stream (stderr in JSON
// mode, stdout otherwise).
func Println(a ...interface{}) {
	fmt.Fprintln(out(), a...)
}

// useColor returns true if we should emit ANSI color codes. On Windows we
// only colorize when stdout is a terminal AND we've successfully enabled VT
// processing (or are running in a modern terminal that supports it natively).
var colorEnabled = enableColor()

func colorize(c, s string) string {
	if !colorEnabled {
		return s
	}
	return c + s + reset
}

// Success prints a green success message.
func Success(format string, args ...interface{}) {
	fmt.Fprintln(out(), colorize(green, symSuccess+" ")+fmt.Sprintf(format, args...))
}

// Error prints a red error message.
func Error(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, colorize(red, symError+" ")+fmt.Sprintf(format, args...)+"\n")
}

// Info prints a blue info message.
func Info(format string, args ...interface{}) {
	fmt.Fprintln(out(), colorize(blue, symInfo+" ")+fmt.Sprintf(format, args...))
}

// Warn prints a yellow warning message.
func Warn(format string, args ...interface{}) {
	fmt.Fprintln(out(), colorize(yellow, symWarn+" ")+fmt.Sprintf(format, args...))
}

// Header prints a bold section header.
func Header(format string, args ...interface{}) {
	fmt.Fprintln(out(), colorize(bold, fmt.Sprintf(format, args...)))
	fmt.Fprintln(out(), strings.Repeat(symLine, 40))
}

// Table prints rows in a tab-aligned format.
// headers is a slice of column titles. rows is a slice of string slices,
// where each inner slice is one row with one value per column.
func Table(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(out(), 0, 0, 2, ' ', 0)
	for _, h := range headers {
		fmt.Fprintf(w, "%s\t", colorize(gray, h))
	}
	fmt.Fprintln(w)
	for _, row := range rows {
		for _, cell := range row {
			fmt.Fprintf(w, "%s\t", cell)
		}
		fmt.Fprintln(w)
	}
	w.Flush()
}

// Muted prints gray text (for secondary info).
func Muted(format string, args ...interface{}) {
	fmt.Fprintln(out(), colorize(gray, fmt.Sprintf(format, args...)))
}
