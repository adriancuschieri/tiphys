package pipeline_test

import (
	"testing"

	"github.com/adriancuschieri/tiphys/internal/pipeline"
)

var validWorkflowYAML = []byte(`
apiVersion: argoproj.io/v1alpha1
kind: Workflow
metadata:
  generateName: ci-
spec:
  entrypoint: main
  templates:
    - name: main
      container:
        image: alpine
        command: [echo, hello]
`)

var missingEntrypointYAML = []byte(`
apiVersion: argoproj.io/v1alpha1
kind: Workflow
metadata:
  generateName: ci-
spec:
  entrypoint: does-not-exist
  templates:
    - name: main
      container:
        image: alpine
        command: [echo, hello]
`)

func TestLoadWorkflow_Success(t *testing.T) {
	files := map[string][]byte{
		"workflow.yaml": validWorkflowYAML,
	}
	wf, err := pipeline.LoadWorkflow(files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf.Spec.Entrypoint != "main" {
		t.Errorf("expected entrypoint 'main', got %q", wf.Spec.Entrypoint)
	}
}

func TestLoadWorkflow_FallbackToYml(t *testing.T) {
	files := map[string][]byte{
		"workflow.yml": validWorkflowYAML,
	}
	_, err := pipeline.LoadWorkflow(files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadWorkflow_SingleFileFallback(t *testing.T) {
	files := map[string][]byte{
		"my-pipeline.yaml": validWorkflowYAML,
	}
	_, err := pipeline.LoadWorkflow(files)
	if err != nil {
		t.Fatalf("expected single file fallback to work, got: %v", err)
	}
}

func TestLoadWorkflow_NoFiles(t *testing.T) {
	_, err := pipeline.LoadWorkflow(map[string][]byte{})
	if err == nil {
		t.Fatal("expected error for empty files map")
	}
}

func TestLoadWorkflow_MissingEntrypointTemplate(t *testing.T) {
	files := map[string][]byte{
		"workflow.yaml": missingEntrypointYAML,
	}
	_, err := pipeline.LoadWorkflow(files)
	if err == nil {
		t.Fatal("expected validation error for missing entrypoint template")
	}
}

func TestLoadWorkflow_InvalidYAML(t *testing.T) {
	files := map[string][]byte{
		"workflow.yaml": []byte(`not: valid: yaml: [`),
	}
	_, err := pipeline.LoadWorkflow(files)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}
