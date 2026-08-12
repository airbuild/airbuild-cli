//go:build windows

package ui

import (
	"os"
	"syscall"
	"unsafe"
)

const (
	ENABLE_VIRTUAL_TERMINAL_PROCESSING = 0x0004
	ENABLE_PROCESSED_OUTPUT            = 0x0001
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode             = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
	procGetStdHandle               = kernel32.NewProc("GetStdHandle")
	procSetConsoleOutputCP         = kernel32.NewProc("SetConsoleOutputCP")
	STD_OUTPUT_HANDLE              = ^uintptr(0) // -1
)

// enableVT enables Virtual Terminal processing on the Windows console so
// ANSI escape sequences are interpreted. Returns true if successful.
func enableVT() bool {
	h, _, _ := procGetStdHandle.Call(uintptr(STD_OUTPUT_HANDLE))
	if h == 0 {
		return false
	}

	var mode uint32
	r, _, _ := procGetConsoleMode.Call(h, uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return false
	}

	mode |= ENABLE_VIRTUAL_TERMINAL_PROCESSING
	r, _, _ = procSetConsoleMode.Call(h, uintptr(mode))
	return r != 0
}

// enableColor returns true if ANSI color codes should be emitted on Windows.
// We try to enable VT processing; if that fails (legacy cmd.exe), colors are
// disabled. We also set the output code page to UTF-8 so Unicode symbols
// render correctly.
func enableColor() bool {
	// Set console output code page to UTF-8 (65001)
	procSetConsoleOutputCP.Call(uintptr(65001))

	// Try to enable VT processing
	if enableVT() {
		return isTTY()
	}

	// VT not available — check if we're in a modern terminal (Windows Terminal,
	// etc.) that supports ANSI natively via TERM env var or WT_SESSION
	if os.Getenv("WT_SESSION") != "" || os.Getenv("TERM") != "" {
		return isTTY()
	}

	return false
}
