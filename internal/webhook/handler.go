package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/adriancuschieri/tiphys/internal/argo"
	"github.com/adriancuschieri/tiphys/internal/config"
	gh "github.com/adriancuschieri/tiphys/internal/github"
	"github.com/adriancuschieri/tiphys/internal/pipeline"
	"go.uber.org/zap"
)

// Handler processes incoming GitHub webhook requests.
type Handler struct {
	cfg          *config.Config
	githubClient *gh.Client
	argoClient   *argo.Client
	logger       *zap.Logger
}

// NewHandler creates a new webhook handler.
func NewHandler(cfg *config.Config, githubClient *gh.Client, argoClient *argo.Client, logger *zap.Logger) *Handler {
	return &Handler{
		cfg:          cfg,
		githubClient: githubClient,
		argoClient:   argoClient,
		logger:       logger,
	}
}

// ServeHTTP handles incoming webhook requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024)) // 10MB max
	if err != nil {
		h.logger.Error("failed to read request body", zap.Error(err))
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	// Validate HMAC signature from GitHub
	if !h.validateSignature(r.Header.Get("X-Hub-Signature-256"), body) {
		h.logger.Warn("invalid webhook signature", zap.String("remote_addr", r.RemoteAddr))
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")

	h.logger.Info("received webhook",
		zap.String("event", eventType),
		zap.String("delivery_id", deliveryID),
	)

	switch eventType {
	case "push":
		h.handlePush(w, r, body, deliveryID)
	case "pull_request":
		h.handlePullRequest(w, r, body, deliveryID)
	case "ping":
		h.logger.Info("received ping from GitHub")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"pong"}`)
	default:
		h.logger.Debug("ignoring unsupported event type", zap.String("event", eventType))
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ignored","event":"%s"}`, eventType)
	}
}

// handlePush processes push events and triggers CI workflows.
func (h *Handler) handlePush(w http.ResponseWriter, r *http.Request, body []byte, deliveryID string) {
	var event PushEvent
	if err := json.Unmarshal(body, &event); err != nil {
		h.logger.Error("failed to parse push event", zap.Error(err))
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	// Filter out tag pushes and empty commits
	if event.IsTag() {
		h.logger.Debug("ignoring tag push", zap.String("ref", event.Ref))
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ignored","reason":"tag push"}`)
		return
	}

	if event.HeadCommit.ID == "" {
		h.logger.Debug("ignoring push with no head commit (branch deletion?)")
		w.WriteHeader(http.StatusOK)
		return
	}

	repo := event.Repository.FullName
	if !h.cfg.IsRepoAllowed(repo) {
		h.logger.Warn("webhook from non-allowed repo", zap.String("repo", repo))
		http.Error(w, "repo not allowed", http.StatusForbidden)
		return
	}

	// Acknowledge immediately; trigger async
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprint(w, `{"status":"accepted"}`)

	go h.triggerPushWorkflow(event, deliveryID)
}

// handlePullRequest processes PR events and triggers CI workflows.
func (h *Handler) handlePullRequest(w http.ResponseWriter, r *http.Request, body []byte, deliveryID string) {
	var event PullRequestEvent
	if err := json.Unmarshal(body, &event); err != nil {
		h.logger.Error("failed to parse pull_request event", zap.Error(err))
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	// Only run CI on open/reopen/sync actions
	switch event.Action {
	case "opened", "reopened", "synchronize":
		// proceed
	default:
		h.logger.Debug("ignoring PR action", zap.String("action", event.Action))
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ignored","action":"%s"}`, event.Action)
		return
	}

	repo := event.Repository.FullName
	if !h.cfg.IsRepoAllowed(repo) {
		h.logger.Warn("webhook from non-allowed repo", zap.String("repo", repo))
		http.Error(w, "repo not allowed", http.StatusForbidden)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprint(w, `{"status":"accepted"}`)

	go h.triggerPRWorkflow(event, deliveryID)
}

// triggerPushWorkflow fetches the pipeline config and submits a workflow for a push event.
func (h *Handler) triggerPushWorkflow(event PushEvent, deliveryID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	logger := h.logger.With(
		zap.String("repo", event.Repository.FullName),
		zap.String("commit", event.HeadCommit.ID),
		zap.String("branch", event.Branch()),
		zap.String("delivery_id", deliveryID),
	)

	logger.Info("triggering push workflow")

	files, err := h.githubClient.FetchPipelineFiles(ctx, event.Repository.FullName, event.HeadCommit.ID, h.cfg.PipelineDir)
	if err != nil {
		logger.Error("failed to fetch pipeline files", zap.Error(err))
		return
	}

	wf, err := pipeline.LoadWorkflow(files)
	if err != nil {
		logger.Error("failed to load workflow definition", zap.Error(err))
		return
	}

	params := map[string]string{
		"repo":       event.Repository.FullName,
		"commit":     event.HeadCommit.ID,
		"branch":     event.Branch(),
		"ref":        event.Ref,
		"clone_url":  event.Repository.CloneURL,
		"event_type": "push",
		"delivery_id": deliveryID,
	}

	submitted, err := h.argoClient.SubmitWorkflow(ctx, wf, params)
	if err != nil {
		logger.Error("failed to submit workflow", zap.Error(err))
		return
	}

	logger.Info("workflow submitted successfully",
		zap.String("workflow_name", submitted.Name),
		zap.String("namespace", submitted.Namespace),
	)
}

// triggerPRWorkflow fetches the pipeline config and submits a workflow for a PR event.
func (h *Handler) triggerPRWorkflow(event PullRequestEvent, deliveryID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	logger := h.logger.With(
		zap.String("repo", event.Repository.FullName),
		zap.String("commit", event.PullRequest.Head.SHA),
		zap.String("branch", event.PullRequest.Head.Ref),
		zap.Int("pr_number", event.PullRequest.Number),
		zap.String("delivery_id", deliveryID),
	)

	logger.Info("triggering PR workflow")

	files, err := h.githubClient.FetchPipelineFiles(ctx, event.Repository.FullName, event.PullRequest.Head.SHA, h.cfg.PipelineDir)
	if err != nil {
		logger.Error("failed to fetch pipeline files", zap.Error(err))
		return
	}

	wf, err := pipeline.LoadWorkflow(files)
	if err != nil {
		logger.Error("failed to load workflow definition", zap.Error(err))
		return
	}

	params := map[string]string{
		"repo":       event.Repository.FullName,
		"commit":     event.PullRequest.Head.SHA,
		"branch":     event.PullRequest.Head.Ref,
		"ref":        fmt.Sprintf("refs/pull/%d/head", event.PullRequest.Number),
		"clone_url":  event.Repository.CloneURL,
		"pr_number":  fmt.Sprintf("%d", event.PullRequest.Number),
		"event_type": "pull_request",
		"pr_action":  event.Action,
		"delivery_id": deliveryID,
	}

	submitted, err := h.argoClient.SubmitWorkflow(ctx, wf, params)
	if err != nil {
		logger.Error("failed to submit workflow", zap.Error(err))
		return
	}

	logger.Info("workflow submitted successfully",
		zap.String("workflow_name", submitted.Name),
		zap.String("namespace", submitted.Namespace),
	)
}

// validateSignature verifies the HMAC-SHA256 signature from GitHub.
func (h *Handler) validateSignature(signatureHeader string, body []byte) bool {
	const prefix = "sha256="
	if len(signatureHeader) <= len(prefix) {
		return false
	}

	mac := hmac.New(sha256.New, []byte(h.cfg.WebhookSecret))
	mac.Write(body)
	expected := prefix + hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signatureHeader))
}
