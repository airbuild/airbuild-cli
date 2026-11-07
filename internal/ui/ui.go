package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"
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
		fmt.Fprintf(os.Stderr, "%s Could not marshal JSON output: %v\n", colorizeFor(os.Stderr, red, symError+" "), err)
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

// colorEnabled is the platform capability (Windows must enable VT processing).
var colorEnabled = enableColor()

// useColor decides whether to emit ANSI codes on f: the platform must support
// them, f must be a terminal, and the user must not have opted out via the
// NO_COLOR convention (https://no-color.org) or TERM=dumb. Deciding per stream
// keeps escape codes out of piped output.
func useColor(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return colorEnabled && isTTYFile(f)
}

func colorizeFor(f *os.File, c, s string) string {
	if !useColor(f) {
		return s
	}
	return c + s + reset
}

// colorize colors text destined for the human-output stream.
func colorize(c, s string) string {
	return colorizeFor(out(), c, s)
}

// Success prints a green success message.
func Success(format string, args ...interface{}) {
	fmt.Fprintln(out(), colorize(green, symSuccess+" ")+fmt.Sprintf(format, args...))
}

// Error prints a red error message.
func Error(format string, args ...interface{}) {
	fmt.Fprintln(os.Stderr, colorizeFor(os.Stderr, red, symError+" ")+fmt.Sprintf(format, args...))
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

// Progress returns a callback that renders a one-line transfer bar on stderr
// ("label  45%  12.3 MB / 27.0 MB"). It draws only when stderr is a terminal,
// never touches stdout (safe with --json), and throttles redraws.
func Progress(label string) func(sent, total int64) {
	if !isTTYFile(os.Stderr) {
		return nil
	}
	var last time.Time
	return func(sent, total int64) {
		done := total > 0 && sent >= total
		if !done && time.Since(last) < 100*time.Millisecond {
			return
		}
		last = time.Now()
		pct := 0
		if total > 0 {
			pct = int(sent * 100 / total)
		}
		const width = 24
		filled := pct * width / 100
		bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
		fmt.Fprintf(os.Stderr, "\r%s %s %3d%%  %s / %s", label, bar, pct, humanBytes(sent), humanBytes(total))
		if done {
			fmt.Fprintln(os.Stderr)
		}
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
