package argo

import (
	"context"
	"fmt"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	wfclientset "github.com/argoproj/argo-workflows/v3/pkg/client/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

// Client handles communication with the Argo Workflows API.
type Client struct {
	wfClient  wfclientset.Interface
	namespace string
}

// NewClient creates a new Argo Workflows client.
// If kubeconfigPath is empty, in-cluster config is used.
func NewClient(namespace, kubeconfigPath string) (*Client, error) {
	cfg, err := buildKubeConfig(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("building kube config: %w", err)
	}

	wfClient, err := wfclientset.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating argo workflow client: %w", err)
	}

	return &Client{
		wfClient:  wfClient,
		namespace: namespace,
	}, nil
}

// SubmitWorkflow submits a workflow to Argo, injecting the provided parameters.
func (c *Client) SubmitWorkflow(ctx context.Context, wf *wfv1.Workflow, params map[string]string) (*wfv1.Workflow, error) {
	// Deep copy so we don't mutate the caller's object
	wfCopy := wf.DeepCopy()

	// Inject runtime parameters into workflow arguments
	c.injectParameters(wfCopy, params)

	// Ensure generateName is set so each run gets a unique name
	if wfCopy.GenerateName == "" {
		if wfCopy.Name != "" {
			wfCopy.GenerateName = wfCopy.Name + "-"
		} else {
			wfCopy.GenerateName = "ci-"
		}
	}
	// Clear Name so Kubernetes uses GenerateName
	wfCopy.Name = ""

	submitted, err := c.wfClient.ArgoprojV1alpha1().
		Workflows(c.namespace).
		Create(ctx, wfCopy, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating workflow in namespace %q: %w", c.namespace, err)
	}

	return submitted, nil
}

// GetWorkflow retrieves a workflow by name.
func (c *Client) GetWorkflow(ctx context.Context, name string) (*wfv1.Workflow, error) {
	return c.wfClient.ArgoprojV1alpha1().
		Workflows(c.namespace).
		Get(ctx, name, metav1.GetOptions{})
}

// ListWorkflows returns all workflows in the configured namespace.
func (c *Client) ListWorkflows(ctx context.Context) (*wfv1.WorkflowList, error) {
	return c.wfClient.ArgoprojV1alpha1().
		Workflows(c.namespace).
		List(ctx, metav1.ListOptions{})
}

// ParseWorkflow parses a workflow from raw YAML bytes.
func ParseWorkflow(data []byte) (*wfv1.Workflow, error) {
	var wf wfv1.Workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("unmarshaling workflow YAML: %w", err)
	}
	if wf.Kind != "Workflow" {
		return nil, fmt.Errorf("expected kind=Workflow, got %q", wf.Kind)
	}
	return &wf, nil
}

// injectParameters merges runtime params into the workflow's top-level arguments.
// Parameters defined in the YAML take precedence; runtime params fill in any missing ones
// and also add new ones not defined in the workflow spec.
func (c *Client) injectParameters(wf *wfv1.Workflow, params map[string]string) {
	// Build a set of parameter names already defined in the workflow
	existing := make(map[string]int)
	for i, p := range wf.Spec.Arguments.Parameters {
		existing[p.Name] = i
	}

	for k, v := range params {
		val := wfv1.AnyStringPtr(v)
		if idx, ok := existing[k]; ok {
			// Only override if the workflow doesn't already have a value set
			if wf.Spec.Arguments.Parameters[idx].Value == nil {
				wf.Spec.Arguments.Parameters[idx].Value = val
			}
		} else {
			// Add new parameter
			wf.Spec.Arguments.Parameters = append(wf.Spec.Arguments.Parameters, wfv1.Parameter{
				Name:  k,
				Value: val,
			})
		}
	}
}

// buildKubeConfig builds a Kubernetes rest.Config from the kubeconfig path,
// falling back to in-cluster config if the path is empty.
func buildKubeConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("loading kubeconfig from %q: %w", kubeconfigPath, err)
		}
		return cfg, nil
	}

	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config failed (are you running inside Kubernetes?): %w", err)
	}
	return cfg, nil
}
