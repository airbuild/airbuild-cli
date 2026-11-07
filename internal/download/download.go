// Package download fetches files over HTTPS with sane timeouts, atomic
// writes and integrity helpers (SHA-256 checksum lists, Google Cloud Storage
// hashes). It is shared by `airbuild upgrade` and the CodePush toolchain
// installers so no download in the CLI can hang forever or leave a partial file.
package download

import (
	"crypto/md5" //nolint:gosec // GCS exposes md5 for integrity checks, not security
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// HTTP is the client used for every download. There is no overall Timeout for
// the body (toolchains are hundreds of MB), but connecting, TLS and waiting
// for response headers are bounded, and the body read has an idle deadline
// enforced by idleReader.
var HTTP = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// idleTimeout aborts a download whose body stalls for this long.
const idleTimeout = 60 * time.Second

// Result describes a completed download.
type Result struct {
	Size      int64
	SHA256    string // lowercase hex
	MD5       string // lowercase hex
	GoogleMD5 string // lowercase hex from the X-Goog-Hash header, "" if absent
}

// ToFile downloads url into dest. The bytes are written to dest+".part" and
// renamed on success, so dest is never left half-written. The file is created
// with mode 0o755 when executable is true and 0o644 otherwise.
func ToFile(url, dest string, executable bool) (*Result, error) {
	resp, err := HTTP.Get(url) //nolint:gosec // callers pass fixed hosts
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	mode := os.FileMode(0o644)
	if executable {
		mode = 0o755
	}
	part := dest + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return nil, err
	}
	sha, md := sha256.New(), md5.New() //nolint:gosec
	n, err := io.Copy(io.MultiWriter(f, sha, md), newIdleReader(resp.Body, idleTimeout))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(part) //nolint:errcheck
		return nil, err
	}
	if err := os.Rename(part, dest); err != nil {
		os.Remove(part) //nolint:errcheck
		return nil, err
	}
	return &Result{
		Size:      n,
		SHA256:    hex.EncodeToString(sha.Sum(nil)),
		MD5:       hex.EncodeToString(md.Sum(nil)),
		GoogleMD5: googleMD5(resp.Header.Get("X-Goog-Hash")),
	}, nil
}

// GetBytes downloads url into memory (for small files like checksums.txt),
// refusing bodies larger than max.
func GetBytes(url string, max int64) ([]byte, error) {
	resp, err := HTTP.Get(url) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(newIdleReader(resp.Body, idleTimeout), max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("GET %s: response larger than %d bytes", url, max)
	}
	return data, nil
}

// VerifyGoogleHash checks the md5 Google Cloud Storage reports for the object
// against the downloaded bytes. It guards against truncated/corrupted
// downloads; it is NOT an authenticity check. Returns nil when the server sent
// no md5.
func (r *Result) VerifyGoogleHash() error {
	if r.GoogleMD5 == "" || strings.EqualFold(r.GoogleMD5, r.MD5) {
		return nil
	}
	return fmt.Errorf("download corrupted: md5 %s does not match server hash %s", r.MD5, r.GoogleMD5)
}

// googleMD5 extracts the md5 from `crc32c=...,md5=<base64>` and returns hex.
func googleMD5(header string) string {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "md5="); ok {
			if raw, err := base64.StdEncoding.DecodeString(v); err == nil {
				return hex.EncodeToString(raw)
			}
		}
	}
	return ""
}

// ParseChecksums parses `sha256sum` output ("<hex>  <name>" per line, name
// optionally prefixed with * for binary mode) into name → lowercase hex.
func ParseChecksums(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != sha256.Size*2 {
			continue
		}
		out[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	return out
}

// VerifySHA256 compares got with the checksum listed for name.
func VerifySHA256(checksums map[string]string, name, got string) error {
	want, ok := checksums[name]
	if !ok {
		return fmt.Errorf("no checksum listed for %s", name)
	}
	if !strings.EqualFold(want, got) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", name, want, got)
	}
	return nil
}

// idleReader fails a Read that makes no progress for the timeout.
type idleReader struct {
	r       io.Reader
	timeout time.Duration
}

func newIdleReader(r io.Reader, d time.Duration) io.Reader { return &idleReader{r, d} }

func (i *idleReader) Read(p []byte) (int, error) {
	type res struct {
		n   int
		err error
	}
	ch := make(chan res, 1)
	buf := make([]byte, len(p))
	go func() {
		n, err := i.r.Read(buf)
		ch <- res{n, err}
	}()
	select {
	case r := <-ch:
		copy(p, buf[:r.n])
		return r.n, r.err
	case <-time.After(i.timeout):
		return 0, fmt.Errorf("download stalled: no data for %s", i.timeout)
	}
}
