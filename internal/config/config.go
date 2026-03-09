package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds all runtime configuration for the webhook service.
type Config struct {
	// Server
	Port string

	// GitHub
	GitHubToken     string
	WebhookSecret   string
	PipelineDir     string // Directory in the repo containing pipeline files (default: .argo)
	AllowedRepos    []string // If non-empty, only accept webhooks from these repos (format: "org/repo")

	// Argo Workflows
	ArgoNamespace   string
	KubeconfigPath  string // Empty means in-cluster config

	// Logging
	LogLevel string
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	cfg := &Config{
		Port:           getEnvOrDefault("PORT", "8080"),
		GitHubToken:    os.Getenv("GITHUB_TOKEN"),
		WebhookSecret:  os.Getenv("WEBHOOK_SECRET"),
		PipelineDir:    getEnvOrDefault("PIPELINE_DIR", ".argo"),
		ArgoNamespace:  getEnvOrDefault("ARGO_NAMESPACE", "argo"),
		KubeconfigPath: os.Getenv("KUBECONFIG"),
		LogLevel:       getEnvOrDefault("LOG_LEVEL", "info"),
	}

	if repos := os.Getenv("ALLOWED_REPOS"); repos != "" {
		for _, r := range strings.Split(repos, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				cfg.AllowedRepos = append(cfg.AllowedRepos, r)
			}
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) validate() error {
	if c.WebhookSecret == "" {
		return fmt.Errorf("WEBHOOK_SECRET is required")
	}
	if c.GitHubToken == "" {
		return fmt.Errorf("GITHUB_TOKEN is required")
	}
	return nil
}

// IsRepoAllowed returns true if no allowlist is configured, or the repo is in the allowlist.
func (c *Config) IsRepoAllowed(repo string) bool {
	if len(c.AllowedRepos) == 0 {
		return true
	}
	for _, r := range c.AllowedRepos {
		if r == repo {
			return true
		}
	}
	return false
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
