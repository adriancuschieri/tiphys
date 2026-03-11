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
	"github.com/adriancuschieri/tiphys/internal/tiphys"
	"github.com/adriancuschieri/tiphys/internal/tiphys/steps"
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

	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		h.logger.Error("failed to read request body", zap.Error(err))
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

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
		h.handlePush(w, body, deliveryID)
	case "pull_request":
		h.handlePullRequest(w, body, deliveryID)
	case "ping":
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"pong"}`)
	default:
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ignored","event":"%s"}`, eventType)
	}
}

func (h *Handler) handlePush(w http.ResponseWriter, body []byte, deliveryID string) {
	var event PushEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	repo := event.Repository.FullName
	if !h.cfg.IsRepoAllowed(repo) {
		http.Error(w, "repo not allowed", http.StatusForbidden)
		return
	}

	if event.HeadCommit.ID == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprint(w, `{"status":"accepted"}`)

	go h.runPipeline(tiphys.Event{
		Type:   tiphys.EventPush,
		Ref:    event.Ref,
		Branch: event.Branch(),
		Tag:    event.Tag(),
	}, steps.RuntimeParams{
		Repo:      repo,
		Commit:    event.HeadCommit.ID,
		Branch:    event.Branch(),
		CloneURL:  event.Repository.CloneURL,
		EventType: "push",
	}, deliveryID)
}

func (h *Handler) handlePullRequest(w http.ResponseWriter, body []byte, deliveryID string) {
	var event PullRequestEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	repo := event.Repository.FullName
	if !h.cfg.IsRepoAllowed(repo) {
		http.Error(w, "repo not allowed", http.StatusForbidden)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprint(w, `{"status":"accepted"}`)

	go h.runPipeline(tiphys.Event{
		Type:     tiphys.EventPullRequest,
		Ref:      fmt.Sprintf("refs/pull/%d/head", event.PullRequest.Number),
		Branch:   event.PullRequest.Head.Ref,
		PRAction: event.Action,
		PRBase:   event.PullRequest.Base.Ref,
	}, steps.RuntimeParams{
		Repo:      repo,
		Commit:    event.PullRequest.Head.SHA,
		Branch:    event.PullRequest.Head.Ref,
		CloneURL:  event.Repository.CloneURL,
		EventType: "pull_request",
		PRNumber:  fmt.Sprintf("%d", event.PullRequest.Number),
	}, deliveryID)
}

// runPipeline is the core async flow:
//  1. Load .tiphys.yaml from the repo
//  2. Check if the event matches the trigger rules
//  3. Compile the config into an Argo Workflow
//  4. Submit it
func (h *Handler) runPipeline(event tiphys.Event, params steps.RuntimeParams, deliveryID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	logger := h.logger.With(
		zap.String("repo", params.Repo),
		zap.String("commit", params.Commit),
		zap.String("branch", params.Branch),
		zap.String("event_type", params.EventType),
		zap.String("delivery_id", deliveryID),
	)

	// 1. Load .tiphys.yaml
	cfg, err := tiphys.LoadConfig(ctx, h.githubClient, params.Repo, params.Commit)
	if err != nil {
		logger.Error("failed to load .tiphys.yaml", zap.Error(err))
		return
	}

	// 2. Check trigger rules
	if !tiphys.ShouldTrigger(cfg.On, event) {
		logger.Info("event does not match trigger rules — skipping",
			zap.String("ref", event.Ref),
		)
		return
	}

	// 3. Compile into Argo Workflow
	wf, err := tiphys.Compile(cfg, params, &tiphys.CompilerOptions{
		DefaultArrangement: tiphys.ArrangeSequential,
		ServiceAccountName: h.cfg.WorkflowServiceAccount,
	})
	if err != nil {
		logger.Error("failed to compile pipeline", zap.Error(err))
		return
	}

	// 4. Submit
	submitted, err := h.argoClient.SubmitWorkflow(ctx, wf, nil)
	if err != nil {
		logger.Error("failed to submit workflow", zap.Error(err))
		return
	}

	logger.Info("workflow submitted",
		zap.String("workflow_name", submitted.Name),
		zap.String("namespace", submitted.Namespace),
	)
}

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
