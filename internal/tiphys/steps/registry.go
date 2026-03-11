package steps

import (
	"fmt"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
)

// RuntimeParams are the runtime variables injected by the webhook service
// (commit SHA, branch, repo, etc.) available to step builders for interpolation.
type RuntimeParams struct {
	Repo      string
	Commit    string
	Branch    string
	Tag       string
	CloneURL  string
	EventType string
	PRNumber  string
}

// Builder is a function that produces an Argo template for a given step configuration.
// defaultImage is the pipeline-level image fallback.
type Builder func(name string, with map[string]string, image string, params RuntimeParams) (wfv1.Template, error)

var registry = map[string]Builder{}

// Register adds a step type to the registry.
func Register(uses string, builder Builder) {
	registry[uses] = builder
}

// Get returns the builder for a step type, or an error if unknown.
func Get(uses string) (Builder, error) {
	b, ok := registry[uses]
	if !ok {
		return nil, fmt.Errorf("unknown step type %q — supported types: %v", uses, Supported())
	}
	return b, nil
}

// Supported returns all registered step type names.
func Supported() []string {
	keys := make([]string, 0, len(registry))
	for k := range registry {
		keys = append(keys, k)
	}
	return keys
}
