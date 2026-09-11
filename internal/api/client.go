package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Client is the AirBuild API client used by the CLI.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// New creates a new API client.
func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTP: &http.Client{
			Timeout: 10 * time.Minute, // uploads can be large
		},
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

// do performs an authenticated HTTP request and returns the response body.
func (c *Client) do(method, path string, body io.Reader, contentType string) ([]byte, int, error) {
	url := c.BaseURL + path

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, 0, fmt.Errorf("could not create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("could not read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &errBody)
		msg := errBody.Error
		if msg == "" {
			msg = string(data)
		}
		return data, resp.StatusCode, &APIError{StatusCode: resp.StatusCode, Message: msg}
	}

	return data, resp.StatusCode, nil
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

// codePushMultipartUpload is a shared helper for the release/patch endpoints,
// both of which take an appId + metadata fields + a single file.
func (c *Client) codePushMultipartUpload(path string, filePath string, fields map[string]string) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("could not open file: %w", err)
	}
	defer file.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	for k, v := range fields {
		if v == "" {
			continue
		}
		if err := writer.WriteField(k, v); err != nil {
			return nil, fmt.Errorf("could not write field %q: %w", k, err)
		}
	}

	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, fmt.Errorf("could not create file form field: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("could not copy file to buffer: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("could not close multipart writer: %w", err)
	}

	data, _, err := c.do("POST", path, &buf, writer.FormDataContentType())
	return data, err
}

// MultipartFile is a single file to attach to a multi-file multipart
// upload (see codePushMultipartUploadMulti) — used for React Native
// publish, which uploads a JS bundle plus N asset files in one request.
type MultipartFile struct {
	FieldName string
	FilePath  string
}

// codePushMultipartUploadMulti is like codePushMultipartUpload, but attaches
// an arbitrary number of files (each under its own field name) instead of
// exactly one under a fixed "file" field.
func (c *Client) codePushMultipartUploadMulti(path string, fields map[string]string, files []MultipartFile) ([]byte, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	for k, v := range fields {
		if v == "" {
			continue
		}
		if err := writer.WriteField(k, v); err != nil {
			return nil, fmt.Errorf("could not write field %q: %w", k, err)
		}
	}

	for _, f := range files {
		file, err := os.Open(f.FilePath)
		if err != nil {
			return nil, fmt.Errorf("could not open file %s: %w", f.FilePath, err)
		}
		part, err := writer.CreateFormFile(f.FieldName, filepath.Base(f.FilePath))
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("could not create file form field %q: %w", f.FieldName, err)
		}
		if _, err := io.Copy(part, file); err != nil {
			file.Close()
			return nil, fmt.Errorf("could not copy file %s to buffer: %w", f.FilePath, err)
		}
		file.Close()
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("could not close multipart writer: %w", err)
	}

	data, _, err := c.do("POST", path, &buf, writer.FormDataContentType())
	return data, err
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
// existing release.
func (c *Client) CodePushFlutterPatch(filePath, appID, platform, releaseVersion, architecture, channel, releaseNotes string) (*CodePushUpdateResponse, error) {
	data, err := c.codePushMultipartUpload("/api/codepush/flutter/patch", filePath, map[string]string{
		"appId":          appID,
		"platform":       platform,
		"releaseVersion": releaseVersion,
		"architecture":   architecture,
		"channel":        channel,
		"releaseNotes":   releaseNotes,
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

// DownloadFile downloads a URL to a local file, returning the file path.
// Used to fetch release artifacts from signed storage URLs.
func (c *Client) DownloadFile(fileURL, destPath string) error {
	resp, err := c.HTTP.Get(fileURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("download failed (HTTP %d)", resp.StatusCode)
	}
	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("could not create file: %w", err)
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("could not write file: %w", err)
	}
	return nil
}

// Upload uploads a build file (IPA/APK) to a specific app.
// progress is an optional callback called with bytes uploaded (not implemented
// yet — net/http doesn't easily expose multipart progress without a custom
// reader).
func (c *Client) Upload(filePath, appID, platform, releaseNotes string) (*UploadResponse, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("could not open file: %w", err)
	}
	defer file.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// appId field
	if err := writer.WriteField("appId", appID); err != nil {
		return nil, fmt.Errorf("could not write appId field: %w", err)
	}

	// platform field (optional — server auto-detects from extension)
	if platform != "" {
		if err := writer.WriteField("platform", platform); err != nil {
			return nil, fmt.Errorf("could not write platform field: %w", err)
		}
	}

	// releaseNotes field (optional)
	if releaseNotes != "" {
		if err := writer.WriteField("releaseNotes", releaseNotes); err != nil {
			return nil, fmt.Errorf("could not write releaseNotes field: %w", err)
		}
	}

	// file field
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, fmt.Errorf("could not create file form field: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("could not copy file to buffer: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("could not close multipart writer: %w", err)
	}

	data, _, err := c.do("POST", "/api/upload", &buf, writer.FormDataContentType())
	if err != nil {
		return nil, err
	}

	var resp UploadResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("could not parse upload response: %w", err)
	}
	return &resp, nil
}
