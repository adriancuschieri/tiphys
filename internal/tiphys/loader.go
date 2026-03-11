package tiphys

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const ConfigFileName = ".tiphys.yaml"

// GitHubFetcher is the interface the loader needs to fetch a raw file from a repo.
// Implemented by the github.Client.
type GitHubFetcher interface {
	FetchRawFile(ctx context.Context, repo, ref, path string) ([]byte, error)
}

// LoadConfig fetches and parses .tiphys.yaml from the repo at the given ref.
func LoadConfig(ctx context.Context, fetcher GitHubFetcher, repo, ref string) (*PipelineConfig, error) {
	data, err := fetcher.FetchRawFile(ctx, repo, ref, ConfigFileName)
	if err != nil {
		return nil, fmt.Errorf("fetching %s from %s@%s: %w", ConfigFileName, repo, ref, err)
	}

	cfg, err := parseConfig(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", ConfigFileName, err)
	}

	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", ConfigFileName, err)
	}

	return cfg, nil
}

func parseConfig(data []byte) (*PipelineConfig, error) {
	var cfg PipelineConfig
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true) // surface unknown fields as errors
	if err := decoder.Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validateConfig(cfg *PipelineConfig) error {
	if cfg.Version == "" {
		return fmt.Errorf("version is required")
	}
	if cfg.Version != "1" {
		return fmt.Errorf("unsupported version %q (supported: \"1\")", cfg.Version)
	}
	if len(cfg.Pipeline.Steps) == 0 {
		return fmt.Errorf("pipeline.steps must not be empty")
	}

	// Validate step names are unique and dependsOn refs are valid
	names := make(map[string]bool)
	for i, s := range cfg.Pipeline.Steps {
		if s.Name == "" {
			return fmt.Errorf("step[%d] is missing a name", i)
		}
		if s.Uses == "" {
			return fmt.Errorf("step %q is missing 'uses'", s.Name)
		}
		if names[s.Name] {
			return fmt.Errorf("duplicate step name %q", s.Name)
		}
		names[s.Name] = true
	}

	for _, s := range cfg.Pipeline.Steps {
		for _, dep := range s.DependsOn {
			if !names[dep] {
				return fmt.Errorf("step %q dependsOn unknown step %q", s.Name, dep)
			}
			if dep == s.Name {
				return fmt.Errorf("step %q cannot depend on itself", s.Name)
			}
		}
	}

	return nil
}
