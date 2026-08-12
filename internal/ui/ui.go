package ui

import (
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

// isTTY checks if stdout is a terminal.
func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
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
	fmt.Println(colorize(green, symSuccess+" ") + fmt.Sprintf(format, args...))
}

// Error prints a red error message.
func Error(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, colorize(red, symError+" ")+fmt.Sprintf(format, args...)+"\n")
}

// Info prints a blue info message.
func Info(format string, args ...interface{}) {
	fmt.Println(colorize(blue, symInfo+" ") + fmt.Sprintf(format, args...))
}

// Warn prints a yellow warning message.
func Warn(format string, args ...interface{}) {
	fmt.Println(colorize(yellow, symWarn+" ") + fmt.Sprintf(format, args...))
}

// Header prints a bold section header.
func Header(format string, args ...interface{}) {
	fmt.Println(colorize(bold, fmt.Sprintf(format, args...)))
	fmt.Println(strings.Repeat(symLine, 40))
}

// Table prints rows in a tab-aligned format.
// headers is a slice of column titles. rows is a slice of string slices,
// where each inner slice is one row with one value per column.
func Table(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
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
	fmt.Println(colorize(gray, fmt.Sprintf(format, args...)))
}
