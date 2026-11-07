package api

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(srv.URL, "airbuild_testkey")
	c.backoff = time.Millisecond
	return c
}

func TestRequestsCarryAuthAndUserAgent(t *testing.T) {
	old := UserAgent
	UserAgent = "airbuild-cli/v9.9.9 (test/arch)"
	defer func() { UserAgent = old }()

	var gotAuth, gotUA string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotUA = r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		w.Write([]byte(`{"apps":[]}`)) //nolint:errcheck
	}))
	if _, err := c.ListApps(); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer airbuild_testkey" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotUA != "airbuild-cli/v9.9.9 (test/arch)" {
		t.Errorf("User-Agent = %q", gotUA)
	}
}

func TestAPIErrorsExposeServerMessageAndAreNotRetried(t *testing.T) {
	var calls int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"plan limit reached"}`)) //nolint:errcheck
	}))
	_, err := c.ListApps()
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 403 || apiErr.Message != "plan limit reached" {
		t.Fatalf("err = %v", err)
	}
	if calls != 1 {
		t.Errorf("403 was retried: %d calls", calls)
	}
}

func TestGETRetriesTransientFailures(t *testing.T) {
	var calls int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"apps":[{"id":"a1"}]}`)) //nolint:errcheck
	}))
	resp, err := c.ListApps()
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if len(resp.Apps) != 1 || calls != 3 {
		t.Errorf("apps=%d calls=%d, want 1 app after 3 calls", len(resp.Apps), calls)
	}
}

func TestGETGivesUpAfterMaxAttempts(t *testing.T) {
	var calls int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	if _, err := c.ListApps(); err == nil {
		t.Fatal("expected an error")
	}
	if calls != maxAttempts {
		t.Errorf("calls = %d, want %d", calls, maxAttempts)
	}
}

func TestPOSTIsNeverRetried(t *testing.T) {
	var calls int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	if _, err := c.CreateLink("b1"); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Errorf("POST was retried: %d calls", calls)
	}
}

func TestRetryAfterHeaderIsHonored(t *testing.T) {
	c := New("http://x", "k")
	resp := &http.Response{Header: http.Header{"Retry-After": []string{"2"}}}
	if d := c.retryDelay(0, resp); d != 2*time.Second {
		t.Errorf("delay = %v, want 2s", d)
	}
	resp.Header.Set("Retry-After", "9999")
	if d := c.retryDelay(0, resp); d != 30*time.Second {
		t.Errorf("delay = %v, want capped at 30s", d)
	}
}

func writeTemp(t *testing.T, name string, size int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), size), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUploadStreamsWithExactContentLengthAndReportsProgress(t *testing.T) {
	const size = 3<<20 + 123
	file := writeTemp(t, "app.apk", size)

	var gotLen int64
	var gotFields = map[string]string{}
	var gotFileSize int
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLen = r.ContentLength
		if len(r.TransferEncoding) != 0 {
			t.Errorf("upload used chunked encoding %v, want fixed Content-Length", r.TransferEncoding)
		}
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("bad multipart: %v", err)
				break
			}
			b, _ := io.ReadAll(part)
			if part.FormName() == "file" {
				gotFileSize = len(b)
				if part.FileName() != "app.apk" {
					t.Errorf("filename = %q", part.FileName())
				}
			} else {
				gotFields[part.FormName()] = string(b)
			}
		}
		w.Write([]byte(`{"build":{"id":"b1","version":"1.0"},"installLink":{"slug":"s"},"installUrl":"u"}`)) //nolint:errcheck
	}))

	var lastSent, lastTotal int64
	c.Progress = func(sent, total int64) { lastSent, lastTotal = sent, total }
	resp, err := c.Upload(file, "app1", "ANDROID", "notes")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Build.ID != "b1" {
		t.Errorf("build id = %q", resp.Build.ID)
	}
	if gotFileSize != size {
		t.Errorf("server received %d bytes, want %d", gotFileSize, size)
	}
	if gotLen <= size {
		t.Errorf("Content-Length %d should include multipart framing beyond %d", gotLen, size)
	}
	if gotFields["appId"] != "app1" || gotFields["platform"] != "ANDROID" || gotFields["releaseNotes"] != "notes" {
		t.Errorf("fields = %v", gotFields)
	}
	if lastSent != size || lastTotal != size {
		t.Errorf("progress ended at %d/%d, want %d/%d", lastSent, lastTotal, size, size)
	}
}

func TestUploadOmitsEmptyOptionalFields(t *testing.T) {
	file := writeTemp(t, "a.ipa", 10)
	var names []string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			names = append(names, p.FormName())
		}
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))
	if _, err := c.Upload(file, "app1", "", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "appId,file" {
		t.Errorf("parts = %v, want appId,file", names)
	}
}

func TestMultiFileUploadAndMissingFile(t *testing.T) {
	a, b := writeTemp(t, "bundle.js", 100), writeTemp(t, "asset.png", 50)
	var parts int
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			if _, err := mr.NextPart(); err != nil {
				break
			}
			parts++
		}
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))
	_, err := c.codePushMultipartUploadMulti("/x", map[string]string{"k": "v"}, []MultipartFile{
		{FieldName: "bundle", FilePath: a}, {FieldName: "asset0", FilePath: b},
	})
	if err != nil {
		t.Fatal(err)
	}
	if parts != 3 {
		t.Errorf("parts = %d, want 3 (1 field + 2 files)", parts)
	}
	if _, err := c.Upload(filepath.Join(t.TempDir(), "missing.apk"), "a", "", ""); err == nil {
		t.Error("expected an error for a missing file")
	}
}

func TestDownloadFileIsAtomic(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("signed-URL download must not send the API key")
		}
		w.Write([]byte("payload")) //nolint:errcheck
	}))
	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := c.DownloadFile(c.BaseURL+"/ok", dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "payload" {
		t.Errorf("content = %q", b)
	}
	bad := filepath.Join(t.TempDir(), "bad.bin")
	if err := c.DownloadFile(c.BaseURL+"/bad", bad); err == nil {
		t.Error("expected an error on 404")
	}
	if _, err := os.Stat(bad); err == nil {
		t.Error("failed download left a file behind")
	}
	if _, err := os.Stat(bad + ".part"); err == nil {
		t.Error("failed download left a .part file behind")
	}
}
