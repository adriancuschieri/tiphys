package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds all runtime configuration for the webhook service.
type Config struct {
	Port            string
	GitHubToken     string
	WebhookSecret   string
	AllowedRepos    []string

	ArgoNamespace          string
	KubeconfigPath         string
	WorkflowServiceAccount string
	// ArgoUIBaseURL is the base URL of the Argo Workflows UI, used to generate
	// deep links in GitHub PR status checks (e.g. https://argo.your-domain.com).
	// Leave empty to omit the link from status checks.
	ArgoUIBaseURL string

	LogLevel string
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	cfg := &Config{
		Port:                   getEnvOrDefault("PORT", "8080"),
		GitHubToken:            os.Getenv("GITHUB_TOKEN"),
		WebhookSecret:          os.Getenv("WEBHOOK_SECRET"),
		ArgoNamespace:          getEnvOrDefault("ARGO_NAMESPACE", "argo"),
		KubeconfigPath:         os.Getenv("KUBECONFIG"),
		WorkflowServiceAccount: getEnvOrDefault("WORKFLOW_SERVICE_ACCOUNT", "workflow"),
		ArgoUIBaseURL:          os.Getenv("ARGO_UI_BASE_URL"),
		LogLevel:               getEnvOrDefault("LOG_LEVEL", "info"),
	}

	if repos := os.Getenv("ALLOWED_REPOS"); repos != "" {
		for _, r := range strings.Split(repos, ",") {
			if r = strings.TrimSpace(r); r != "" {
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
