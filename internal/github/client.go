package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// Client handles communication with the GitHub API.
type Client struct {
	token      string
	httpClient *http.Client
	baseURL    string
}

// NewClient creates a new GitHub API client.
func NewClient(token string) *Client {
	return &Client{
		token:   token,
		baseURL: "https://api.github.com",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// contentEntry represents a single file or directory entry from the GitHub contents API.
type contentEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"` // "file" or "dir"
	Content     string `json:"content"`
	Encoding    string `json:"encoding"`
	DownloadURL string `json:"download_url"`
	HTMLURL     string `json:"html_url"`
	Size        int    `json:"size"`
}

// FetchPipelineFiles fetches all YAML files from the pipeline directory at the given ref.
// It returns a map of filename -> file contents.
func (c *Client) FetchPipelineFiles(ctx context.Context, repo, ref, pipelineDir string) (map[string][]byte, error) {
	entries, err := c.listDirectory(ctx, repo, ref, pipelineDir)
	if err != nil {
		return nil, fmt.Errorf("listing pipeline directory %q: %w", pipelineDir, err)
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("pipeline directory %q is empty in %s@%s", pipelineDir, repo, ref)
	}

	files := make(map[string][]byte)
	for _, entry := range entries {
		if entry.Type != "file" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		content, err := c.fetchFileContent(ctx, entry)
		if err != nil {
			return nil, fmt.Errorf("fetching file %q: %w", entry.Name, err)
		}

		files[entry.Name] = content
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no YAML files found in pipeline directory %q in %s@%s", pipelineDir, repo, ref)
	}

	return files, nil
}

// listDirectory returns all entries in a directory at the given ref.
func (c *Client) listDirectory(ctx context.Context, repo, ref, path string) ([]contentEntry, error) {
	url := fmt.Sprintf("%s/repos/%s/contents/%s?ref=%s", c.baseURL, repo, path, ref)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// ok
	case http.StatusNotFound:
		return nil, fmt.Errorf("path %q not found in %s@%s (HTTP 404)", path, repo, ref)
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("GitHub API authentication failed: check GITHUB_TOKEN")
	case http.StatusForbidden:
		return nil, fmt.Errorf("GitHub API access forbidden: check token permissions")
	default:
		return nil, fmt.Errorf("GitHub API returned unexpected status %d: %s", resp.StatusCode, string(body))
	}

	// The API returns either a single file object or an array of entries.
	// For directories, it's always an array.
	var entries []contentEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		// Try single entry (file, not directory)
		var single contentEntry
		if err2 := json.Unmarshal(body, &single); err2 != nil {
			return nil, fmt.Errorf("parsing GitHub API response: %w", err)
		}
		entries = []contentEntry{single}
	}

	return entries, nil
}

// fetchFileContent retrieves the raw content of a file entry.
//
// The GitHub contents API behaves differently depending on how the directory was
// listed vs fetched individually:
//
//   - Directory listing  → entries have NO inline content; encoding is "".
//     We must follow download_url to get the raw bytes.
//   - Single-file fetch  → entry has inline base64 content; encoding is "base64".
//
// We handle both cases here so the rest of the code doesn't need to care.
func (c *Client) fetchFileContent(ctx context.Context, entry contentEntry) ([]byte, error) {
	// Case 1: inline base64 content is present (single-file fetch response).
	if entry.Encoding == "base64" && entry.Content != "" {
		cleaned := strings.ReplaceAll(entry.Content, "\n", "")
		decoded, err := base64.StdEncoding.DecodeString(cleaned)
		if err != nil {
			return nil, fmt.Errorf("base64 decode: %w", err)
		}
		return decoded, nil
	}

	// Case 2: no inline content — fetch via download_url (raw file bytes).
	if entry.DownloadURL == "" {
		return nil, fmt.Errorf("entry has no content and no download_url (type=%q, encoding=%q)", entry.Type, entry.Encoding)
	}

	return c.fetchRawURL(ctx, entry.DownloadURL)
}

// fetchRawURL performs a GET on a raw URL and returns the response body.
// Used to download file contents via the download_url from the contents API.
func (c *Client) fetchRawURL(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// download_url is unauthenticated for public repos but the token doesn't
	// hurt — it's required for private repos.
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "github-argo-webhook/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET %s returned %d: %s", url, resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024)) // 5MB cap
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	return data, nil
}

// setHeaders sets the required GitHub API headers on a request.
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "github-argo-webhook/1.0")
}


// FetchRawFile fetches a single file from the repo at the given ref and returns its raw bytes.
// This satisfies the tiphys.GitHubFetcher interface.
func (c *Client) FetchRawFile(ctx context.Context, repo, ref, path string) ([]byte, error) {
	url := fmt.Sprintf("%s/repos/%s/contents/%s?ref=%s", c.baseURL, repo, path, ref)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("file %q not found in %s@%s", path, repo, ref)
	default:
		return nil, fmt.Errorf("GitHub API status %d: %s", resp.StatusCode, string(body))
	}

	var entry contentEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return c.fetchFileContent(ctx, entry)
}