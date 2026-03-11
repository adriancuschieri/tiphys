package webhook_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yourorg/github-argo-webhook/internal/config"
	"github.com/yourorg/github-argo-webhook/internal/webhook"
	"go.uber.org/zap"
)

const testSecret = "test-webhook-secret"

func signPayload(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	cfg := &config.Config{
		WebhookSecret: testSecret,
		PipelineDir:   ".argo",
		AllowedRepos:  nil,
	}
	logger := zap.NewNop()
	// Pass nil clients — tests that don't reach async workflow submission won't need them.
	return webhook.NewHandler(cfg, nil, nil, logger)
}

func TestPingEvent(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"zen":"Keep it logically awesome.","hook_id":12345}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-Hub-Signature-256", signPayload(testSecret, body))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestInvalidSignatureRejected(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", "sha256=invalidsignature")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestMissingSignatureRejected(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	// No signature header

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestTagPushIgnored(t *testing.T) {
	handler := newTestHandler(t)

	event := webhook.PushEvent{
		Ref: "refs/tags/v1.0.0",
		Repository: webhook.Repository{
			FullName: "myorg/myrepo",
			CloneURL: "https://github.com/myorg/myrepo.git",
		},
		HeadCommit: webhook.Commit{ID: "abc123"},
	}
	body, _ := json.Marshal(event)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", signPayload(testSecret, body))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for tag push, got %d", rr.Code)
	}
}

func TestUnknownEventIgnored(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "star")
	req.Header.Set("X-Hub-Signature-256", signPayload(testSecret, body))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for unknown event, got %d", rr.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestRepoNotAllowed(t *testing.T) {
	cfg := &config.Config{
		WebhookSecret: testSecret,
		PipelineDir:   ".argo",
		AllowedRepos:  []string{"myorg/allowed-repo"},
	}
	logger := zap.NewNop()
	handler := webhook.NewHandler(cfg, nil, nil, logger)

	event := webhook.PushEvent{
		Ref: "refs/heads/main",
		Repository: webhook.Repository{
			FullName: "myorg/not-allowed",
			CloneURL: "https://github.com/myorg/not-allowed.git",
		},
		HeadCommit: webhook.Commit{ID: "abc123"},
	}
	body, _ := json.Marshal(event)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", signPayload(testSecret, body))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}
