package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// CommitState represents a GitHub commit status state.
type CommitState string

const (
	StatePending CommitState = "pending"
	StateSuccess CommitState = "success"
	StateFailure CommitState = "failure"
	StateError   CommitState = "error"
)

// commitStatusRequest is the payload for the GitHub Create Commit Status API.
type commitStatusRequest struct {
	State       CommitState `json:"state"`
	TargetURL   string      `json:"target_url,omitempty"`
	Description string      `json:"description,omitempty"`
	Context     string      `json:"context"`
}

// PostCommitStatus creates or updates a commit status on GitHub.
// repo is the full repo name (e.g. "org/repo"), sha is the full commit SHA.
// context is the status check name shown on the PR (e.g. "tiphys/ci").
func (c *Client) PostCommitStatus(ctx context.Context, repo, sha string, state CommitState, description, statusContext, targetURL string) error {
	url := fmt.Sprintf("%s/repos/%s/statuses/%s", c.baseURL, repo, sha)

	payload := commitStatusRequest{
		State:       state,
		Description: truncate(description, 140), // GitHub limit
		Context:     statusContext,
		TargetURL:   targetURL,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling status payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("posting commit status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitHub status API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// truncate shortens a string to max length, appending "..." if truncated.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
