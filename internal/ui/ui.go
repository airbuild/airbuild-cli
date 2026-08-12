package ui

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// Colors (simple ANSI — no external deps).
const (
	reset   = "\033[0m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	cyan    = "\033[36m"
	gray    = "\033[90m"
	bold    = "\033[1m"
)

// isTTY checks if stdout is a terminal.
func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func colorize(c, s string) string {
	if !isTTY() {
		return s
	}
	return c + s + reset
}

// Success prints a green success message.
func Success(format string, args ...interface{}) {
	fmt.Println(colorize(green, "✓ ") + fmt.Sprintf(format, args...))
}

// Error prints a red error message.
func Error(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, colorize(red, "✗ ")+fmt.Sprintf(format, args...)+"\n")
}

// Info prints a blue info message.
func Info(format string, args ...interface{}) {
	fmt.Println(colorize(blue, "ℹ ") + fmt.Sprintf(format, args...))
}

// Warn prints a yellow warning message.
func Warn(format string, args ...interface{}) {
	fmt.Println(colorize(yellow, "⚠ ") + fmt.Sprintf(format, args...))
}

// Header prints a bold section header.
func Header(format string, args ...interface{}) {
	fmt.Println(colorize(bold, fmt.Sprintf(format, args...)))
	fmt.Println(strings.Repeat("─", 40))
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
