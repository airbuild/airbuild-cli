//go:build !windows

package ui

// enableColor returns true if ANSI color codes should be emitted.
// On Unix systems, we colorize when stdout is a TTY.
func enableColor() bool {
	return isTTY()
}
