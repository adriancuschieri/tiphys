package pipeline

import (
	"fmt"
	"strings"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	"github.com/adriancuschieri/tiphys/internal/argo"
)

const (
	// EntrypointFilename is the expected name of the main workflow file in the pipeline directory.
	EntrypointFilename = "workflow.yaml"

	// FallbackEntrypoint is checked if workflow.yaml doesn't exist.
	FallbackEntrypoint = "workflow.yml"
)

// LoadWorkflow finds and parses the entrypoint workflow from a map of pipeline files.
// It looks for workflow.yaml (or workflow.yml) in the provided files.
func LoadWorkflow(files map[string][]byte) (*wfv1.Workflow, error) {
	data, entrypoint, err := findEntrypoint(files)
	if err != nil {
		return nil, err
	}

	wf, err := argo.ParseWorkflow(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %q: %w", entrypoint, err)
	}

	if err := validateWorkflow(wf); err != nil {
		return nil, fmt.Errorf("workflow validation failed: %w", err)
	}

	return wf, nil
}

// findEntrypoint locates the main workflow YAML in the files map.
func findEntrypoint(files map[string][]byte) ([]byte, string, error) {
	for _, name := range []string{EntrypointFilename, FallbackEntrypoint} {
		if data, ok := files[name]; ok {
			return data, name, nil
		}
	}

	// If there's exactly one YAML file, use it as the entrypoint
	if len(files) == 1 {
		for name, data := range files {
			return data, name, nil
		}
	}

	// List available files to help debugging
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	return nil, "", fmt.Errorf(
		"no entrypoint found: expected %q or %q in pipeline directory, found: [%s]",
		EntrypointFilename, FallbackEntrypoint, strings.Join(names, ", "),
	)
}

// validateWorkflow performs basic sanity checks on the workflow definition.
func validateWorkflow(wf *wfv1.Workflow) error {
	if wf.Spec.Entrypoint == "" {
		return fmt.Errorf("workflow spec.entrypoint is required")
	}
	if len(wf.Spec.Templates) == 0 {
		return fmt.Errorf("workflow spec.templates must not be empty")
	}

	// Verify the named entrypoint template exists
	found := false
	for _, t := range wf.Spec.Templates {
		if t.Name == wf.Spec.Entrypoint {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("entrypoint template %q not found in spec.templates", wf.Spec.Entrypoint)
	}

	return nil
}
