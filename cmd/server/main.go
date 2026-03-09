package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/adriancuschieri/tiphys/internal/argo"
	"github.com/adriancuschieri/tiphys/internal/config"
	gh "github.com/adriancuschieri/tiphys/internal/github"
	
	"github.com/adriancuschieri/tiphys/internal/webhook"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Set up structured logger
	logger, err := buildLogger(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("initialising logger: %w", err)
	}
	defer logger.Sync() //nolint:errcheck

	logger.Info("starting github-argo-webhook",
		zap.String("port", cfg.Port),
		zap.String("argo_namespace", cfg.ArgoNamespace),
		zap.String("pipeline_dir", cfg.PipelineDir),
		zap.Strings("allowed_repos", cfg.AllowedRepos),
	)

	// Initialise GitHub client
	githubClient := gh.NewClient(cfg.GitHubToken)

	// Initialise Argo Workflows client
	argoClient, err := argo.NewClient(cfg.ArgoNamespace, cfg.KubeconfigPath)
	if err != nil {
		return fmt.Errorf("initialising argo client: %w", err)
	}

	// Build HTTP router
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(zapMiddleware(logger))
	r.Use(middleware.Recoverer)

	// Health check endpoints
	r.Get("/healthz", healthzHandler)
	r.Get("/readyz", readyzHandler)

	// Webhook endpoint
	webhookHandler := webhook.NewHandler(cfg, githubClient, argoClient, logger)
	r.Post("/webhook", webhookHandler.ServeHTTP)

	// Start HTTP server
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown on SIGTERM / SIGINT
	shutdownCh := make(chan error, 1)
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
		sig := <-quit
		logger.Info("shutdown signal received", zap.String("signal", sig.String()))

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		shutdownCh <- srv.Shutdown(ctx)
	}()

	logger.Info("server listening", zap.String("addr", srv.Addr))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server error: %w", err)
	}

	if err := <-shutdownCh; err != nil {
		return fmt.Errorf("graceful shutdown error: %w", err)
	}

	logger.Info("server stopped")
	return nil
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"ok"}`)
}

func readyzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"ready"}`)
}

// zapMiddleware returns a chi-compatible request logger using zap.
func zapMiddleware(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)
			logger.Info("request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", ww.Status()),
				zap.Duration("latency", time.Since(start)),
				zap.String("request_id", middleware.GetReqID(r.Context())),
			)
		})
	}
}

// buildLogger creates a zap logger configured for the given log level.
func buildLogger(level string) (*zap.Logger, error) {
	var lvl zapcore.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = zapcore.InfoLevel
	}

	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(lvl)
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	return cfg.Build()
}
