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

const (
	// statusContext is the name shown on the GitHub PR check.
	statusContext = "tiphys/ci"
	// workflowTimeout is the max time we wait for a workflow to complete.
	workflowTimeout = 60 * time.Minute
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

	go h.runPipeline(
		tiphys.Event{
			Type:   tiphys.EventPush,
			Ref:    event.Ref,
			Branch: event.Branch(),
			Tag:    event.Tag(),
		},
		steps.RuntimeParams{
			Repo:      repo,
			Commit:    event.HeadCommit.ID,
			Branch:    event.Branch(),
			CloneURL:  event.Repository.CloneURL,
			EventType: "push",
		},
		// Push events don't post PR statuses — pass empty string to skip.
		"",
		deliveryID,
	)
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

	go h.runPipeline(
		tiphys.Event{
			Type:     tiphys.EventPullRequest,
			Ref:      fmt.Sprintf("refs/pull/%d/head", event.PullRequest.Number),
			Branch:   event.PullRequest.Head.Ref,
			PRAction: event.Action,
			PRBase:   event.PullRequest.Base.Ref,
		},
		steps.RuntimeParams{
			Repo:      repo,
			Commit:    event.PullRequest.Head.SHA,
			Branch:    event.PullRequest.Head.Ref,
			CloneURL:  event.Repository.CloneURL,
			EventType: "pull_request",
			PRNumber:  fmt.Sprintf("%d", event.PullRequest.Number),
		},
		// Pass the PR head SHA so we can post a commit status against it.
		event.PullRequest.Head.SHA,
		deliveryID,
	)
}

// runPipeline is the core async flow:
//  1. Post "pending" commit status (PR events only)
//  2. Load .tiphys.yaml from the repo
//  3. Check if the event matches the trigger rules
//  4. Compile the config into an Argo Workflow
//  5. Submit it
//  6. Wait for it to complete
//  7. Post "success" or "failure" commit status (PR events only)
//
// commitSHA is the PR head SHA to post statuses against.
// Pass an empty string for push events to skip status posting.
func (h *Handler) runPipeline(event tiphys.Event, params steps.RuntimeParams, commitSHA, deliveryID string) {
	// Use a long-running context for the full pipeline lifecycle.
	ctx, cancel := context.WithTimeout(context.Background(), workflowTimeout)
	defer cancel()

	logger := h.logger.With(
		zap.String("repo", params.Repo),
		zap.String("commit", params.Commit),
		zap.String("branch", params.Branch),
		zap.String("event_type", params.EventType),
		zap.String("delivery_id", deliveryID),
	)

	postStatus := commitSHA != ""

	// 1. Post pending status immediately so the PR shows a check right away.
	if postStatus {
		if err := h.githubClient.PostCommitStatus(
			ctx, params.Repo, commitSHA,
			gh.StatePending,
			"Pipeline queued",
			statusContext,
			"",
		); err != nil {
			logger.Warn("failed to post pending status", zap.Error(err))
			// Non-fatal — continue with the pipeline.
		}
	}

	// 2. Load .tiphys.yaml
	cfg, err := tiphys.LoadConfig(ctx, h.githubClient, params.Repo, params.Commit)
	if err != nil {
		logger.Error("failed to load .tiphys.yaml", zap.Error(err))
		h.postErrorStatus(ctx, postStatus, params.Repo, commitSHA, "Could not load .tiphys.yaml", logger)
		return
	}

	// 3. Check trigger rules
	if !tiphys.ShouldTrigger(cfg.On, event) {
		logger.Info("event does not match trigger rules — skipping",
			zap.String("ref", event.Ref),
		)
		// Clear the pending status so the PR isn't left with a dangling check.
		if postStatus {
			_ = h.githubClient.PostCommitStatus(
				ctx, params.Repo, commitSHA,
				gh.StateSuccess,
				"No matching pipeline rules",
				statusContext,
				"",
			)
		}
		return
	}

	// 4. Compile
	wf, err := tiphys.Compile(cfg, params, &tiphys.CompilerOptions{
		ServiceAccountName: h.cfg.WorkflowServiceAccount,
	})
	if err != nil {
		logger.Error("failed to compile pipeline", zap.Error(err))
		h.postErrorStatus(ctx, postStatus, params.Repo, commitSHA, "Pipeline config error", logger)
		return
	}

	// 5. Submit
	submitted, err := h.argoClient.SubmitWorkflow(ctx, wf, nil)
	if err != nil {
		logger.Error("failed to submit workflow", zap.Error(err))
		h.postErrorStatus(ctx, postStatus, params.Repo, commitSHA, "Failed to submit workflow", logger)
		return
	}

	logger.Info("workflow submitted",
		zap.String("workflow_name", submitted.Name),
		zap.String("namespace", submitted.Namespace),
	)

	// Update status to show the workflow is now running.
	if postStatus {
		_ = h.githubClient.PostCommitStatus(
			ctx, params.Repo, commitSHA,
			gh.StatePending,
			fmt.Sprintf("Running workflow %s", submitted.Name),
			statusContext,
			h.argoWorkflowURL(submitted.Namespace, submitted.Name),
		)
	}

	// 6. Wait for the workflow to finish.
	result, err := h.argoClient.WaitForWorkflow(ctx, submitted.Name)
	if err != nil {
		logger.Error("error waiting for workflow", zap.Error(err))
		h.postErrorStatus(ctx, postStatus, params.Repo, commitSHA, "Lost track of workflow", logger)
		return
	}

	logger.Info("workflow finished",
		zap.String("workflow_name", result.Name),
		zap.String("phase", string(result.Phase)),
		zap.Duration("duration", result.FinishedAt.Sub(result.StartedAt)),
	)

	// 7. Post final status.
	if postStatus {
		if result.Succeeded() {
			_ = h.githubClient.PostCommitStatus(
				ctx, params.Repo, commitSHA,
				gh.StateSuccess,
				"Pipeline passed",
				statusContext,
				h.argoWorkflowURL(result.Namespace, result.Name),
			)
		} else {
			description := "Pipeline failed"
			if result.Message != "" {
				description = result.Message
			}
			_ = h.githubClient.PostCommitStatus(
				ctx, params.Repo, commitSHA,
				gh.StateFailure,
				description,
				statusContext,
				h.argoWorkflowURL(result.Namespace, result.Name),
			)
		}
	}
}

// postErrorStatus posts an error commit status and logs the reason.
func (h *Handler) postErrorStatus(ctx context.Context, post bool, repo, sha, description string, logger *zap.Logger) {
	if !post {
		return
	}
	if err := h.githubClient.PostCommitStatus(
		ctx, repo, sha,
		gh.StateError,
		description,
		statusContext,
		"",
	); err != nil {
		logger.Warn("failed to post error status", zap.Error(err))
	}
}

// argoWorkflowURL returns a deep link to the workflow in the Argo UI.
// Falls back to empty string if ARGO_UI_BASE_URL is not configured.
func (h *Handler) argoWorkflowURL(namespace, name string) string {
	if h.cfg.ArgoUIBaseURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/workflows/%s/%s", h.cfg.ArgoUIBaseURL, namespace, name)
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
