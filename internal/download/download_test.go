package download

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToFileComputesHashesAndIsAtomic(t *testing.T) {
	body := "hello world"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body)) //nolint:errcheck
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "f")
	res, err := ToFile(srv.URL, dest, true)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	if res.SHA256 != hex.EncodeToString(sum[:]) || res.Size != int64(len(body)) {
		t.Errorf("bad result %+v", res)
	}
	fi, _ := os.Stat(dest)
	if fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("executable flag not applied: %v", fi.Mode())
	}
	if _, err := os.Stat(dest + ".part"); err == nil {
		t.Error(".part file left behind")
	}
}

func TestToFileFailsOnHTTPErrorWithoutCreatingFile(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "f")
	if _, err := ToFile(srv.URL, dest, false); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("file created on failure")
	}
}

func TestGoogleHashVerification(t *testing.T) {
	// md5("hello world") = 5eb63bbbe01eeed093cb22bb8f5acdc3
	raw, _ := hex.DecodeString("5eb63bbbe01eeed093cb22bb8f5acdc3")
	good := "crc32c=abc,md5=" + base64.StdEncoding.EncodeToString(raw)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := good
		if r.URL.Path == "/bad" {
			hdr = "md5=" + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
		}
		if r.URL.Path != "/none" {
			w.Header().Set("X-Goog-Hash", hdr)
		}
		w.Write([]byte("hello world")) //nolint:errcheck
	}))
	defer srv.Close()
	d := t.TempDir()
	for path, wantErr := range map[string]bool{"/good": false, "/bad": true, "/none": false} {
		res, err := ToFile(srv.URL+path, filepath.Join(d, strings.TrimPrefix(path, "/")), false)
		if err != nil {
			t.Fatal(err)
		}
		if gotErr := res.VerifyGoogleHash() != nil; gotErr != wantErr {
			t.Errorf("%s: verify error = %v, want %v", path, gotErr, wantErr)
		}
	}
}

func TestChecksumParsingAndVerification(t *testing.T) {
	h := strings.Repeat("a", 64)
	sums := ParseChecksums(h + "  airbuild-linux-amd64\n" + strings.ToUpper(h) + " *airbuild-windows-amd64.exe\nnot a line\n")
	if len(sums) != 2 {
		t.Fatalf("parsed %v", sums)
	}
	if err := VerifySHA256(sums, "airbuild-linux-amd64", h); err != nil {
		t.Error(err)
	}
	if err := VerifySHA256(sums, "airbuild-linux-amd64", strings.Repeat("b", 64)); err == nil {
		t.Error("mismatch not detected")
	}
	if err := VerifySHA256(sums, "airbuild-darwin-arm64", h); err == nil {
		t.Error("missing entry not detected")
	}
}

func TestGetBytesRejectsOversizedBodies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", 100))) //nolint:errcheck
	}))
	defer srv.Close()
	if _, err := GetBytes(srv.URL, 50); err == nil {
		t.Error("expected size error")
	}
	if b, err := GetBytes(srv.URL, 200); err != nil || len(b) != 100 {
		t.Errorf("got %d bytes, err %v", len(b), err)
	}
}
