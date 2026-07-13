package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-errors/errors"
)

// CloudflareClient is the subset of the Cloudflare Pages REST API prod uses for a direct
// upload. It's an interface so the upload orchestration can be unit-tested with a mock.
type CloudflareClient interface {
	ListProjects(ctx context.Context) ([]Project, error)
	CreateProject(ctx context.Context, name, productionBranch string) (*Project, error)
	GetUploadToken(ctx context.Context, projectName string) (string, error)
	CheckMissing(ctx context.Context, jwt string, hashes []string) ([]string, error)
	UploadAssets(ctx context.Context, jwt string, batch []AssetUpload) error
	UpsertHashes(ctx context.Context, jwt string, hashes []string) error
	CreateDeployment(ctx context.Context, projectName string, manifest map[string]string, specialFiles map[string][]byte) (*Deployment, error)
	DeleteProject(ctx context.Context, projectName string) error
}

// HTTPCloudflareClient talks to the Cloudflare API with the user's own token + account id.
type HTTPCloudflareClient struct {
	token     string
	accountID string
	baseURL   string
	http      *http.Client
}

// NewHTTPCloudflareClient reads CLOUDFLARE_API_TOKEN + CLOUDFLARE_ACCOUNT_ID (and the optional
// CLOUDFLARE_API_BASE_URL override) from the environment — the user's own credentials, held
// locally, never sent anywhere but Cloudflare.
func NewHTTPCloudflareClient() *HTTPCloudflareClient {
	base := os.Getenv(cfAPIBaseEnvVar)
	if base == "" {
		base = cfAPIBaseURL
	}
	return &HTTPCloudflareClient{
		token:     os.Getenv(cfTokenEnvVar),
		accountID: os.Getenv(cfAccountEnvVar),
		baseURL:   strings.TrimRight(base, "/"),
		http:      &http.Client{Timeout: 2 * time.Minute},
	}
}

// envelope is the standard Cloudflare API response wrapper.
type envelope struct {
	Success bool            `json:"success"`
	Errors  []apiError      `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

// doJSON performs a JSON request with the given bearer token and unmarshals result (if non-nil)
// from the Cloudflare envelope. A nil bearer uses the account API token.
func (c *HTTPCloudflareClient) doJSON(ctx context.Context, method, path, bearer string, body, result any) error {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return errors.Errorf("failed to marshal request: %w", err)
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, buf)
	if err != nil {
		return errors.Errorf("failed to build request: %w", err)
	}
	if bearer == "" {
		bearer = c.token
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.send(req, result)
}

// send executes req and decodes the Cloudflare envelope, surfacing API errors.
func (c *HTTPCloudflareClient) send(req *http.Request, result any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.Errorf("cloudflare request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)

	var env envelope
	if len(data) > 0 {
		_ = json.Unmarshal(data, &env)
	}
	if resp.StatusCode >= 300 || (len(data) > 0 && !env.Success) {
		if len(env.Errors) > 0 {
			return &APIError{Status: resp.StatusCode, Code: env.Errors[0].Code, Message: env.Errors[0].Message}
		}
		return &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(data))}
	}
	if result != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, result); err != nil {
			return errors.Errorf("failed to decode cloudflare result: %w", err)
		}
	}
	return nil
}

// APIError carries a Cloudflare API failure, including the HTTP status so callers can detect
// a 401 (expired JWT → refresh) or 404 (already gone → idempotent).
type APIError struct {
	Status  int
	Code    int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("cloudflare API error (status %d, code %d): %s", e.Status, e.Code, e.Message)
}

func (c *HTTPCloudflareClient) acctPath(suffix string) string {
	return fmt.Sprintf("/accounts/%s%s", c.accountID, suffix)
}

// ListProjects returns all Pages projects on the account (following pagination).
func (c *HTTPCloudflareClient) ListProjects(ctx context.Context) ([]Project, error) {
	var all []Project
	for page := 1; ; page++ {
		var batch []Project
		p := c.acctPath("/pages/projects") + "?per_page=100&page=" + fmt.Sprint(page)
		if err := c.doJSON(ctx, http.MethodGet, p, "", nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			return all, nil
		}
	}
}

// CreateProject creates a direct-upload Pages project (no git source).
func (c *HTTPCloudflareClient) CreateProject(ctx context.Context, name, productionBranch string) (*Project, error) {
	if productionBranch == "" {
		productionBranch = defaultBranch
	}
	body := map[string]any{
		"name":              name,
		"production_branch": productionBranch,
		"deployment_configs": map[string]any{
			"production": map[string]any{},
			"preview":    map[string]any{},
		},
	}
	var proj Project
	if err := c.doJSON(ctx, http.MethodPost, c.acctPath("/pages/projects"), "", body, &proj); err != nil {
		return nil, err
	}
	return &proj, nil
}

// GetUploadToken returns the JWT that authenticates the /pages/assets/* calls.
func (c *HTTPCloudflareClient) GetUploadToken(ctx context.Context, projectName string) (string, error) {
	var res struct {
		JWT string `json:"jwt"`
	}
	p := c.acctPath("/pages/projects/" + url.PathEscape(projectName) + "/upload-token")
	if err := c.doJSON(ctx, http.MethodGet, p, "", nil, &res); err != nil {
		return "", err
	}
	return res.JWT, nil
}

// CheckMissing returns the subset of hashes Cloudflare doesn't already have (content-addressed).
func (c *HTTPCloudflareClient) CheckMissing(ctx context.Context, jwt string, hashes []string) ([]string, error) {
	var missing []string
	body := map[string]any{"hashes": hashes}
	if err := c.doJSON(ctx, http.MethodPost, "/pages/assets/check-missing", jwt, body, &missing); err != nil {
		return nil, err
	}
	return missing, nil
}

// UploadAssets uploads one batch of assets (each value base64-encoded).
func (c *HTTPCloudflareClient) UploadAssets(ctx context.Context, jwt string, batch []AssetUpload) error {
	return c.doJSON(ctx, http.MethodPost, "/pages/assets/upload", jwt, batch, nil)
}

// UpsertHashes touches every hash so the content-addressed GC doesn't reap the deployment's assets.
func (c *HTTPCloudflareClient) UpsertHashes(ctx context.Context, jwt string, hashes []string) error {
	body := map[string]any{"hashes": hashes}
	return c.doJSON(ctx, http.MethodPost, "/pages/assets/upsert-hashes", jwt, body, nil)
}

// CreateDeployment creates the deployment from the uploaded assets. manifest maps "/path" → hash;
// specialFiles carries _headers/_redirects/_routes.json contents (added as form parts) when present.
func (c *HTTPCloudflareClient) CreateDeployment(ctx context.Context, projectName string, manifest map[string]string, specialFiles map[string][]byte) (*Deployment, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, errors.Errorf("failed to marshal manifest: %w", err)
	}
	if err := mw.WriteField("manifest", string(manifestJSON)); err != nil {
		return nil, errors.Errorf("failed to write manifest field: %w", err)
	}
	for name, contents := range specialFiles {
		fw, err := mw.CreateFormFile(name, name)
		if err != nil {
			return nil, errors.Errorf("failed to add %s: %w", name, err)
		}
		if _, err := fw.Write(contents); err != nil {
			return nil, errors.Errorf("failed to write %s: %w", name, err)
		}
	}
	if err := mw.Close(); err != nil {
		return nil, errors.Errorf("failed to finalize form: %w", err)
	}

	p := c.acctPath("/pages/projects/" + url.PathEscape(projectName) + "/deployments")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+p, &buf)
	if err != nil {
		return nil, errors.Errorf("failed to build deployment request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	var dep Deployment
	if err := c.send(req, &dep); err != nil {
		return nil, err
	}
	return &dep, nil
}

// DeleteProject deletes a Pages project (and all its deployments). Idempotent on 404.
func (c *HTTPCloudflareClient) DeleteProject(ctx context.Context, projectName string) error {
	p := c.acctPath("/pages/projects/" + url.PathEscape(projectName))
	err := c.doJSON(ctx, http.MethodDelete, p, "", nil, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil
	}
	return err
}
