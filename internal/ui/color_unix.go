//go:build !windows

package ui

// enableColor reports whether this platform can render ANSI colors. Whether a
// given stream actually gets colors is decided per stream in useColor.
func enableColor() bool {
	return true
}
