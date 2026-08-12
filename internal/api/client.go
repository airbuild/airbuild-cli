package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
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
