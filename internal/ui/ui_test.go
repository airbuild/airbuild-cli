package ui

import (
	"os"
	"strings"
	"testing"
)

func pipeFile(t *testing.T) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() }) //nolint:errcheck
	return w
}

func TestColorIsNeverEmittedToNonTerminals(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if got := colorizeFor(pipeFile(t), red, "x"); got != "x" {
		t.Errorf("piped output got ANSI codes: %q", got)
	}
}

func TestNoColorAndDumbTermDisableColor(t *testing.T) {
	// /dev/null is a character device, so it passes the TTY test on Unix and
	// lets us exercise the env-var switches in isolation.
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip("no /dev/null")
	}
	defer f.Close() //nolint:errcheck
	if !isTTYFile(f) {
		t.Skip("platform does not treat the null device as a character device")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm")
	if !useColor(f) {
		t.Fatal("expected color on a terminal-like stream with no opt-out")
	}
	t.Setenv("NO_COLOR", "1")
	if useColor(f) {
		t.Error("NO_COLOR must disable color")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if useColor(f) {
		t.Error("TERM=dumb must disable color")
	}
}

func TestJSONModeRoutesHumanOutputToStderr(t *testing.T) {
	defer SetJSONMode(false)
	SetJSONMode(true)
	if out() != os.Stderr {
		t.Error("JSON mode must send human output to stderr")
	}
	SetJSONMode(false)
	if out() != os.Stdout {
		t.Error("normal mode must send human output to stdout")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 1536: "1.5 KB", 5 << 20: "5.0 MB", 3 << 30: "3.0 GB"}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestProgressIsSilentWithoutATerminal(t *testing.T) {
	if isTTYFile(os.Stderr) {
		t.Skip("stderr is a terminal in this environment")
	}
	if Progress("Uploading") != nil {
		t.Error("Progress must be nil (no output) when stderr is not a terminal")
	}
}

func TestErrorDoesNotTreatMessageAsFormatString(t *testing.T) {
	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	Error("limit is 100%% of %s", "quota")
	Error("server said: %s", "50% done, 100%s left") // values containing % must survive
	w.Close()                                        //nolint:errcheck
	os.Stderr = old
	buf := make([]byte, 512)
	n, _ := r.Read(buf)
	got := string(buf[:n])
	if !strings.Contains(got, "limit is 100% of quota") || !strings.Contains(got, "server said: 50% done, 100%s left") {
		t.Errorf("percent signs were mangled: %q", got)
	}
}
