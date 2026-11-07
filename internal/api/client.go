package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/airbuild/airbuild-cli/internal/download"
)

// UserAgent is sent on every request; cmd.SetVersion fills in the version.
var UserAgent = "airbuild-cli/dev"

const (
	// requestTimeout bounds ordinary JSON calls end to end.
	requestTimeout = 60 * time.Second
	// uploadResponseTimeout bounds how long the server may take to answer
	// after a build upload finished sending (it parses the IPA/APK first).
	uploadResponseTimeout = 10 * time.Minute
	// maxAttempts is the total tries for idempotent (GET) requests.
	maxAttempts = 3
)

// Client is the AirBuild API client used by the CLI.
type Client struct {
	BaseURL string
	APIKey  string
	// HTTP serves ordinary JSON calls (bounded by requestTimeout).
	HTTP *http.Client
	// UploadHTTP serves file uploads: no overall timeout (a 2 GB upload on a
	// slow link legitimately takes a long time), only connect/TLS/response limits.
	UploadHTTP *http.Client
	// Progress, when set, receives (sent, total) bytes while a file uploads.
	Progress func(sent, total int64)
	// backoff is the base retry delay (tests shrink it).
	backoff time.Duration
}

func newTransport(responseHeaderTimeout time.Duration) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// New creates a new API client.
func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL:    strings.TrimSuffix(baseURL, "/"),
		APIKey:     apiKey,
		HTTP:       &http.Client{Transport: newTransport(requestTimeout)},
		UploadHTTP: &http.Client{Transport: newTransport(uploadResponseTimeout)},
		backoff:    500 * time.Millisecond,
	}
}

// APIError represents an error returned by the AirBuild API.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error (HTTP %d): %s", e.StatusCode, e.Message)
}

// retryable reports whether a response status is worth retrying.
func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

// retryDelay honors Retry-After (seconds, capped) and otherwise backs off
// exponentially with jitter.
func (c *Client) retryDelay(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if v := resp.Header.Get("Retry-After"); v != "" {
			if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
				if secs > 30 {
					secs = 30
				}
				return time.Duration(secs) * time.Second
			}
		}
	}
	d := c.backoff << attempt
	return d + time.Duration(rand.Int63n(int64(d)/2+1))
}

// do performs an authenticated HTTP request and returns the response body.
// Bodyless requests (GET) are retried on network errors, 429 and 502/503/504;
// requests with a body are sent exactly once so a build is never uploaded
// twice behind the user's back.
func (c *Client) do(method, path string, body io.Reader, contentType string) ([]byte, int, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = io.ReadAll(body); err != nil {
			return nil, 0, fmt.Errorf("could not read request body: %w", err)
		}
	}
	attempts := 1
	if method == http.MethodGet {
		attempts = maxAttempts
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		var rdr io.Reader
		if payload != nil {
			rdr = bytes.NewReader(payload)
		}
		data, status, resp, err := c.send(c.HTTP, requestTimeout, method, path, rdr, -1, contentType)
		if err == nil {
			return data, status, nil
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) && !retryable(status) {
			return data, status, err
		}
		lastErr = err
		if attempt < attempts-1 {
			time.Sleep(c.retryDelay(attempt, resp))
		}
	}
	return nil, 0, lastErr
}

// send issues one request. timeout <= 0 means no overall deadline.
// contentLength < 0 lets net/http infer it.
func (c *Client) send(hc *http.Client, timeout time.Duration, method, path string, body io.Reader, contentLength int64, contentType string) ([]byte, int, *http.Response, error) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("could not create request: %w", err)
	}
	if contentLength >= 0 {
		req.ContentLength = contentLength
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("User-Agent", UserAgent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, resp, fmt.Errorf("could not read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return data, resp.StatusCode, resp, &APIError{StatusCode: resp.StatusCode, Message: errorMessage(data)}
	}
	return data, resp.StatusCode, resp, nil
}

func errorMessage(data []byte) string {
	var errBody struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(data, &errBody)
	if errBody.Error != "" {
		return errBody.Error
	}
	return string(data)
}

// get performs a GET request and unmarshals the JSON response.
func (c *Client) get(path string, v interface{}) error {
	data, _, err := c.do("GET", path, nil, "")
	if err != nil {
		return err
	}
	if v != nil {
		if err := json.Unmarshal(data, v); err != nil {
			return fmt.Errorf("could not parse response: %w", err)
		}
	}
	return nil
}

// postJSON performs a POST request with a JSON body and unmarshals the response.
func (c *Client) postJSON(path string, body interface{}, v interface{}) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("could not marshal request body: %w", err)
	}
	data, _, err := c.do("POST", path, bytes.NewReader(jsonBody), "application/json")
	if err != nil {
		return err
	}
	if v != nil {
		if err := json.Unmarshal(data, v); err != nil {
			return fmt.Errorf("could not parse response: %w", err)
		}
	}
	return nil
}

// --- Types ---

// VerifyResponse is returned by GET /api/cli/verify.
type VerifyResponse struct {
	Organization struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"organization"`
	APIKeyID string `json:"apiKeyId"`
}

// App represents an app in list responses.
type App struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Platforms       []string `json:"platforms"`
	IOSBundleID     string   `json:"iosBundleId"`
	AndroidBundleID string   `json:"androidBundleId"`
	IconURL         string   `json:"iconUrl"`
	DistributionKey string   `json:"distributionKey"`
	CreatedAt       string   `json:"createdAt"`
	UpdatedAt       string   `json:"updatedAt"`
	Count           struct {
		Builds int `json:"builds"`
	} `json:"_count"`
}

// AppsResponse is returned by GET /api/cli/apps.
type AppsResponse struct {
	Apps []App `json:"apps"`
}

// InstallLink represents an install link.
type InstallLink struct {
	ID            string  `json:"id"`
	Slug          string  `json:"slug"`
	IsActive      bool    `json:"isActive"`
	ExpiresAt     *string `json:"expiresAt"`
	DownloadCount int     `json:"downloadCount"`
	CreatedAt     string  `json:"createdAt"`
}

// Build represents a build in list responses.
type Build struct {
	ID           string        `json:"id"`
	Version      string        `json:"version"`
	BuildNumber  string        `json:"buildNumber"`
	Platform     string        `json:"platform"`
	Status       string        `json:"status"`
	FileName     string        `json:"fileName"`
	FileSize     string        `json:"fileSizeBytes"`
	CreatedAt    string        `json:"createdAt"`
	InstallLinks []InstallLink `json:"installLinks"`
}

// BuildsResponse is returned by GET /api/cli/apps/{appId}/builds.
type BuildsResponse struct {
	Builds []Build `json:"builds"`
}

// LinkWithBuild is an install link with nested build info (for list view).
type LinkWithBuild struct {
	ID            string  `json:"id"`
	Slug          string  `json:"slug"`
	IsActive      bool    `json:"isActive"`
	ExpiresAt     *string `json:"expiresAt"`
	DownloadCount int     `json:"downloadCount"`
	CreatedAt     string  `json:"createdAt"`
	Build         struct {
		ID       string `json:"id"`
		Version  string `json:"version"`
		Platform string `json:"platform"`
		App      struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"app"`
	} `json:"build"`
}

// LinksResponse is returned by GET /api/cli/links.
type LinksResponse struct {
	Links []LinkWithBuild `json:"links"`
}

// CreateLinkResponse is returned by POST /api/cli/links.
type CreateLinkResponse struct {
	Link struct {
		ID        string `json:"id"`
		Slug      string `json:"slug"`
		IsActive  bool   `json:"isActive"`
		CreatedAt string `json:"createdAt"`
	} `json:"link"`
}

// UploadResponse is returned by POST /api/upload.
type UploadResponse struct {
	Build struct {
		ID          string `json:"id"`
		Version     string `json:"version"`
		BuildNumber string `json:"buildNumber"`
		Platform    string `json:"platform"`
		Status      string `json:"status"`
	} `json:"build"`
	InstallLink struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	} `json:"installLink"`
	InstallURL string `json:"installUrl"`
}

// --- API methods ---

// Verify checks if the API key is valid and returns org info.
func (c *Client) Verify() (*VerifyResponse, error) {
	var resp VerifyResponse
	if err := c.get("/api/cli/verify", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListApps returns all apps in the organization.
func (c *Client) ListApps() (*AppsResponse, error) {
	var resp AppsResponse
	if err := c.get("/api/cli/apps", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateAppResponse is returned by POST /api/cli/apps.
type CreateAppResponse struct {
	App App `json:"app"`
}

// CreateApp creates a new app in the organization.
func (c *Client) CreateApp(name string, platforms []string) (*CreateAppResponse, error) {
	body := map[string]interface{}{
		"name":      name,
		"platforms": platforms,
	}
	var resp CreateAppResponse
	if err := c.postJSON("/api/cli/apps", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListBuilds returns all builds for a specific app.
func (c *Client) ListBuilds(appID string) (*BuildsResponse, error) {
	var resp BuildsResponse
	if err := c.get("/api/cli/apps/"+appID+"/builds", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListLinks returns all install links for a specific app.
func (c *Client) ListLinks(appID string) (*LinksResponse, error) {
	var resp LinksResponse
	if err := c.get("/api/cli/links?appId="+appID, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateLink creates a new install link for a build.
func (c *Client) CreateLink(buildID string) (*CreateLinkResponse, error) {
	var resp CreateLinkResponse
	if err := c.postJSON("/api/cli/links", map[string]string{"buildId": buildID}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// --- CodePush (Flutter) types ---

// CodePushRelease represents a Flutter release in API responses.
type CodePushRelease struct {
	ID              string   `json:"id"`
	AppID           string   `json:"appId"`
	Platform        string   `json:"platform"`
	Version         string   `json:"version"`
	Channel         string   `json:"channel"`
	Status          string   `json:"status"`
	FlutterRevision string   `json:"flutterRevision"`
	Architectures   []string `json:"architectures"`
	FileSizeBytes   string   `json:"fileSizeBytes"`
	CreatedAt       string   `json:"createdAt"`
}

// CodePushUpdate represents a Flutter patch in API responses.
type CodePushUpdate struct {
	ID             string  `json:"id"`
	ReleaseID      string  `json:"releaseId"`
	AppID          string  `json:"appId"`
	Version        string  `json:"version"`
	Channel        string  `json:"channel"`
	Status         string  `json:"status"`
	RolloutPercent int     `json:"rolloutPercent"`
	PatchNumber    int     `json:"patchNumber"`
	ChecksumSha256 string  `json:"checksumSha256"`
	FileSizeBytes  string  `json:"fileSizeBytes"`
	CrashFreeRate  float64 `json:"crashFreeRate"`
	AutoPaused     bool    `json:"autoPaused"`
	CreatedAt      string  `json:"createdAt"`
}

// CodePushReleaseResponse is returned by POST /api/codepush/flutter/release.
type CodePushReleaseResponse struct {
	Release CodePushRelease `json:"release"`
}

// CodePushUpdateResponse is returned by POST /api/codepush/flutter/patch,
// /promote, and /rollback.
type CodePushUpdateResponse struct {
	Update CodePushUpdate `json:"update"`
}

// CodePushStatusResponse is returned by GET /api/codepush/flutter/status.
type CodePushStatusResponse struct {
	Releases []struct {
		CodePushRelease
		Updates []CodePushUpdate `json:"updates"`
	} `json:"releases"`
	Channels []struct {
		Name            string `json:"name"`
		CurrentUpdateID string `json:"currentUpdateId"`
	} `json:"channels"`
}

// MultipartFile is a single file to attach to a multipart upload — used for
// React Native publish, which uploads a JS bundle plus N asset files in one
// request.
type MultipartFile struct {
	FieldName string
	FilePath  string
}

// countingReader reports bytes read to a progress callback.
type countingReader struct {
	r     io.Reader
	sent  *int64
	total int64
	cb    func(sent, total int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		*c.sent += int64(n)
		if c.cb != nil {
			c.cb(*c.sent, c.total)
		}
	}
	return n, err
}

// writeMultipart writes fields (sorted, so both passes frame identically) and
// files into w. When dry is true file contents are skipped, which lets the
// caller measure the exact framing overhead.
func writeMultipart(w *multipart.Writer, fields map[string]string, files []MultipartFile, dry bool, sent *int64, total int64, cb func(int64, int64)) error {
	keys := make([]string, 0, len(fields))
	for k, v := range fields {
		if v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := w.WriteField(k, fields[k]); err != nil {
			return fmt.Errorf("could not write field %q: %w", k, err)
		}
	}
	for _, f := range files {
		part, err := w.CreateFormFile(f.FieldName, filepath.Base(f.FilePath))
		if err != nil {
			return fmt.Errorf("could not create file form field %q: %w", f.FieldName, err)
		}
		if dry {
			continue
		}
		file, err := os.Open(f.FilePath)
		if err != nil {
			return fmt.Errorf("could not open file %s: %w", f.FilePath, err)
		}
		_, err = io.Copy(part, &countingReader{r: file, sent: sent, total: total, cb: cb})
		file.Close() //nolint:errcheck
		if err != nil {
			return fmt.Errorf("could not stream file %s: %w", f.FilePath, err)
		}
	}
	return w.Close()
}

// postMultipart streams a multipart/form-data POST. Files are read straight
// from disk through a pipe — memory use stays constant however large the
// build is — and the exact Content-Length is computed up front so servers and
// proxies that dislike chunked uploads work too. The request is sent once
// (no automatic retry) so a build is never uploaded twice.
func (c *Client) postMultipart(path string, fields map[string]string, files []MultipartFile) ([]byte, error) {
	var total int64
	for _, f := range files {
		fi, err := os.Stat(f.FilePath)
		if err != nil {
			return nil, fmt.Errorf("could not open file %s: %w", f.FilePath, err)
		}
		total += fi.Size()
	}

	boundary := multipart.NewWriter(io.Discard).Boundary()

	// Pass 1: measure the framing (headers, boundaries, fields) without file bytes.
	var overhead bytes.Buffer
	dry := multipart.NewWriter(&overhead)
	if err := dry.SetBoundary(boundary); err != nil {
		return nil, err
	}
	if err := writeMultipart(dry, fields, files, true, nil, 0, nil); err != nil {
		return nil, err
	}
	contentLength := int64(overhead.Len()) + total

	// Pass 2: stream the real body.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	if err := mw.SetBoundary(boundary); err != nil {
		return nil, err
	}
	var sent int64
	go func() {
		pw.CloseWithError(writeMultipart(mw, fields, files, false, &sent, total, c.Progress)) //nolint:errcheck
	}()

	data, _, _, err := c.send(c.UploadHTTP, 0, http.MethodPost, path, pr, contentLength, mw.FormDataContentType())
	pr.Close() //nolint:errcheck
	return data, err
}

// codePushMultipartUpload is a shared helper for the release/patch endpoints,
// which upload a single artifact under the "file" field plus text fields.
func (c *Client) codePushMultipartUpload(path string, filePath string, fields map[string]string) ([]byte, error) {
	return c.postMultipart(path, fields, []MultipartFile{{FieldName: "file", FilePath: filePath}})
}

// codePushMultipartUploadMulti is like codePushMultipartUpload, but attaches
// an arbitrary number of files (each under its own field name).
func (c *Client) codePushMultipartUploadMulti(path string, fields map[string]string, files []MultipartFile) ([]byte, error) {
	return c.postMultipart(path, fields, files)
}

// --- CodePush (React Native) types ---

// CodePushAsset represents a single manifest asset (JS bundle or otherwise)
// in API responses.
type CodePushAsset struct {
	ID            string `json:"id"`
	Key           string `json:"key"`
	IsLaunchAsset bool   `json:"isLaunchAsset"`
	ContentType   string `json:"contentType"`
	FileSizeBytes string `json:"fileSizeBytes"`
}

// CodePushReactNativeUpdate represents a React Native update in API responses.
type CodePushReactNativeUpdate struct {
	ID             string          `json:"id"`
	ReleaseID      string          `json:"releaseId"`
	AppID          string          `json:"appId"`
	Channel        string          `json:"channel"`
	Status         string          `json:"status"`
	RolloutPercent int             `json:"rolloutPercent"`
	FileSizeBytes  string          `json:"fileSizeBytes"`
	AutoPaused     bool            `json:"autoPaused"`
	CreatedAt      string          `json:"createdAt"`
	Assets         []CodePushAsset `json:"assets"`
}

// CodePushReactNativePublishResponse is returned by POST /api/codepush/react-native/publish.
type CodePushReactNativePublishResponse struct {
	Update CodePushReactNativeUpdate `json:"update"`
}

// CodePushReactNativeStatusResponse is returned by GET /api/codepush/react-native/status.
type CodePushReactNativeStatusResponse struct {
	Releases []struct {
		CodePushRelease
		Updates []CodePushReactNativeUpdate `json:"updates"`
	} `json:"releases"`
	Channels []struct {
		Name            string `json:"name"`
		CurrentUpdateID string `json:"currentUpdateId"`
	} `json:"channels"`
}

// ReactNativeManifestEntry describes one file to publish, mirroring
// internal/expo.ManifestEntry (kept separate so this package doesn't
// depend on internal/expo).
type ReactNativeManifestEntry struct {
	FieldName     string
	FilePath      string
	Key           string
	IsLaunchAsset bool
	ContentType   string
	FileExtension string
}

// CodePushReactNativePublish uploads a JS bundle + assets, publishing a new
// React Native OTA update.
func (c *Client) CodePushReactNativePublish(
	entries []ReactNativeManifestEntry, appID, platform, runtimeVersion, channel, releaseNotes string,
) (*CodePushReactNativePublishResponse, error) {
	type manifestEntryJSON struct {
		FieldName     string `json:"fieldName"`
		Key           string `json:"key"`
		IsLaunchAsset bool   `json:"isLaunchAsset"`
		ContentType   string `json:"contentType"`
		FileExtension string `json:"fileExtension,omitempty"`
	}

	manifest := make([]manifestEntryJSON, 0, len(entries))
	files := make([]MultipartFile, 0, len(entries))
	for _, e := range entries {
		manifest = append(manifest, manifestEntryJSON{
			FieldName:     e.FieldName,
			Key:           e.Key,
			IsLaunchAsset: e.IsLaunchAsset,
			ContentType:   e.ContentType,
			FileExtension: e.FileExtension,
		})
		files = append(files, MultipartFile{FieldName: e.FieldName, FilePath: e.FilePath})
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("could not marshal manifest: %w", err)
	}

	data, err := c.codePushMultipartUploadMulti("/api/codepush/react-native/publish", map[string]string{
		"appId":          appID,
		"platform":       platform,
		"runtimeVersion": runtimeVersion,
		"channel":        channel,
		"releaseNotes":   releaseNotes,
		"manifest":       string(manifestJSON),
	}, files)
	if err != nil {
		return nil, err
	}
	var resp CodePushReactNativePublishResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("could not parse response: %w", err)
	}
	return &resp, nil
}

// CodePushReactNativePromote promotes a React Native update to a channel at
// a rollout percentage. Pass either UpdateID, or Platform+RuntimeVersion to
// promote the most recently published draft.
func (c *Client) CodePushReactNativePromote(appID, updateID, platform, runtimeVersion, channel string, rolloutPercent int) (*CodePushUpdateResponse, error) {
	body := map[string]interface{}{"appId": appID, "rolloutPercent": rolloutPercent}
	if updateID != "" {
		body["updateId"] = updateID
	} else {
		body["platform"] = platform
		body["runtimeVersion"] = runtimeVersion
	}
	if channel != "" {
		body["channel"] = channel
	}
	var resp CodePushUpdateResponse
	if err := c.postJSON("/api/codepush/react-native/promote", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CodePushReactNativeRollback rolls back a React Native update by ID.
func (c *Client) CodePushReactNativeRollback(appID, updateID string) (*CodePushUpdateResponse, error) {
	var resp CodePushUpdateResponse
	body := map[string]interface{}{"appId": appID, "updateId": updateID}
	if err := c.postJSON("/api/codepush/react-native/rollback", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CodePushReactNativeStatus lists releases + updates + channels for an app.
func (c *Client) CodePushReactNativeStatus(appID string) (*CodePushReactNativeStatusResponse, error) {
	var resp CodePushReactNativeStatusResponse
	if err := c.get("/api/codepush/react-native/status?appId="+appID, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CodePushFlutterRelease registers a Flutter release (uploads the original
// libapp.so / release artifact for future patch diffing).
func (c *Client) CodePushFlutterRelease(filePath, appID, platform, version, architecture, flutterRevision, shorebirdAppID, channel, releaseNotes string) (*CodePushReleaseResponse, error) {
	data, err := c.codePushMultipartUpload("/api/codepush/flutter/release", filePath, map[string]string{
		"appId":           appID,
		"platform":        platform,
		"version":         version,
		"architecture":    architecture,
		"flutterRevision": flutterRevision,
		"shorebirdAppId":  shorebirdAppID,
		"channel":         channel,
		"releaseNotes":    releaseNotes,
	})
	if err != nil {
		return nil, err
	}
	var resp CodePushReleaseResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("could not parse response: %w", err)
	}
	return &resp, nil
}

// CodePushFlutterPatch uploads a Flutter patch (binary diff) against an
// existing release. artifactSha256 is the sha256 of the patched libapp.so
// — the value devices verify after applying the diff — and may be empty
// when unknown (the update will then fail device-side verification).
func (c *Client) CodePushFlutterPatch(filePath, appID, platform, releaseVersion, architecture, channel, releaseNotes, artifactSha256 string) (*CodePushUpdateResponse, error) {
	data, err := c.codePushMultipartUpload("/api/codepush/flutter/patch", filePath, map[string]string{
		"appId":          appID,
		"platform":       platform,
		"releaseVersion": releaseVersion,
		"architecture":   architecture,
		"channel":        channel,
		"releaseNotes":   releaseNotes,
		"artifactSha256": artifactSha256,
	})
	if err != nil {
		return nil, err
	}
	var resp CodePushUpdateResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("could not parse response: %w", err)
	}
	return &resp, nil
}

// FlutterArchDiff is one per-architecture binary diff for a multi-ABI
// patch upload — Path is the .patch file, ArtifactSha256 the sha256 of the
// patched artifact devices verify after applying it.
type FlutterArchDiff struct {
	Arch           string
	Path           string
	ArtifactSha256 string
}

// CodePushFlutterPatchMulti uploads a multi-architecture Flutter patch: the
// first diff goes in the bare "file" field (server treats it as primary),
// the rest as file_<arch> parts paired with artifactSha256_<arch> fields.
func (c *Client) CodePushFlutterPatchMulti(diffs []FlutterArchDiff, appID, platform, releaseVersion, channel, releaseNotes string) (*CodePushUpdateResponse, error) {
	if len(diffs) == 0 {
		return nil, fmt.Errorf("no diffs to upload")
	}
	fields := map[string]string{
		"appId":          appID,
		"platform":       platform,
		"releaseVersion": releaseVersion,
		"channel":        channel,
		"releaseNotes":   releaseNotes,
	}
	files := make([]MultipartFile, 0, len(diffs))
	for i, d := range diffs {
		if i == 0 {
			files = append(files, MultipartFile{FieldName: "file", FilePath: d.Path})
			fields["architecture"] = d.Arch
			fields["artifactSha256"] = d.ArtifactSha256
			continue
		}
		files = append(files, MultipartFile{FieldName: "file_" + d.Arch, FilePath: d.Path})
		fields["artifactSha256_"+d.Arch] = d.ArtifactSha256
	}
	data, err := c.codePushMultipartUploadMulti("/api/codepush/flutter/patch", fields, files)
	if err != nil {
		return nil, err
	}
	var resp CodePushUpdateResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("could not parse response: %w", err)
	}
	return &resp, nil
}

// CodePushFlutterPromoteRequest / RollbackRequest identify a patch either
// directly by updateId, or by (releaseVersion, platform, patchNumber).
type CodePushFlutterPatchRef struct {
	AppID          string
	UpdateID       string
	ReleaseVersion string
	Platform       string
	PatchNumber    int
	Channel        string
}

func (r CodePushFlutterPatchRef) toBody(extra map[string]interface{}) map[string]interface{} {
	body := map[string]interface{}{"appId": r.AppID}
	if r.UpdateID != "" {
		body["updateId"] = r.UpdateID
	} else {
		body["releaseVersion"] = r.ReleaseVersion
		body["platform"] = r.Platform
		body["patchNumber"] = r.PatchNumber
	}
	if r.Channel != "" {
		body["channel"] = r.Channel
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// CodePushFlutterPromote promotes a patch to a channel at a rollout percentage.
func (c *Client) CodePushFlutterPromote(ref CodePushFlutterPatchRef, rolloutPercent int) (*CodePushUpdateResponse, error) {
	var resp CodePushUpdateResponse
	body := ref.toBody(map[string]interface{}{"rolloutPercent": rolloutPercent})
	if err := c.postJSON("/api/codepush/flutter/promote", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CodePushFlutterRollback rolls back a patch.
func (c *Client) CodePushFlutterRollback(ref CodePushFlutterPatchRef) (*CodePushUpdateResponse, error) {
	var resp CodePushUpdateResponse
	body := ref.toBody(nil)
	if err := c.postJSON("/api/codepush/flutter/rollback", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CodePushFlutterStatus lists releases + patches + channels for an app.
func (c *Client) CodePushFlutterStatus(appID string) (*CodePushStatusResponse, error) {
	var resp CodePushStatusResponse
	if err := c.get("/api/codepush/flutter/status?appId="+appID, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CodePushReleaseDownloadResponse is returned by GET /api/codepush/flutter/release/download.
type CodePushReleaseDownloadResponse struct {
	DownloadUrl string `json:"downloadUrl"`
	Release     struct {
		ID            string   `json:"id"`
		Version       string   `json:"version"`
		Platform      string   `json:"platform"`
		Architectures []string `json:"architectures"`
	} `json:"release"`
}

// CodePushFlutterReleaseDownload fetches a signed URL for the release's
// original libapp.so (the AOT snapshot that patches are diffed against).
// The CLI uses this in the auto-diff flow to download the release artifact
// locally, then creates the binary diff using Shorebird's patch binary.
func (c *Client) CodePushFlutterReleaseDownload(appID, releaseVersion, platform, architecture, channel string) (*CodePushReleaseDownloadResponse, error) {
	params := url.Values{}
	params.Set("appId", appID)
	params.Set("releaseVersion", releaseVersion)
	params.Set("platform", platform)
	if architecture != "" {
		params.Set("architecture", architecture)
	}
	if channel != "" {
		params.Set("channel", channel)
	}
	var resp CodePushReleaseDownloadResponse
	if err := c.get("/api/codepush/flutter/release/download?"+params.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CodePushSigningKeyResponse is returned by GET
// /api/codepush/flutter/signing-key — the org's RSA public key material for
// embedding `patch_public_key` into builds (SHOREBIRD_PUBLIC_KEY env).
type CodePushSigningKeyResponse struct {
	KeyID                 string `json:"keyId"`
	Algorithm             string `json:"algorithm"`
	RSAPublicKeyPem       string `json:"rsaPublicKeyPem"`
	RSAPublicKeyDerBase64 string `json:"rsaPublicKeyDerBase64"`
}

// CodePushFlutterSigningKey fetches the org's RSA patch-signing public key
// so the CLI can embed it in builds it produces.
func (c *Client) CodePushFlutterSigningKey(appID string) (*CodePushSigningKeyResponse, error) {
	var resp CodePushSigningKeyResponse
	if err := c.get("/api/codepush/flutter/signing-key?appId="+url.QueryEscape(appID), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DownloadFile fetches a (signed) URL to destPath atomically, with connect and
// stall timeouts and no Authorization header (signed URLs carry their own auth).
func (c *Client) DownloadFile(fileURL, destPath string) error {
	if _, err := download.ToFile(fileURL, destPath, false); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	return nil
}

// Upload streams a build file (IPA/APK) to a specific app. Set c.Progress to
// receive (sent, total) byte counts while it uploads.
func (c *Client) Upload(filePath, appID, platform, releaseNotes string) (*UploadResponse, error) {
	data, err := c.postMultipart("/api/upload", map[string]string{
		"appId":        appID,
		"platform":     platform,
		"releaseNotes": releaseNotes,
	}, []MultipartFile{{FieldName: "file", FilePath: filePath}})
	if err != nil {
		return nil, err
	}

	var resp UploadResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("could not parse upload response: %w", err)
	}
	return &resp, nil
}
