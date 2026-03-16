package argo

import (
	"context"
	"fmt"
	"time"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// WorkflowResult holds the final outcome of a completed workflow.
type WorkflowResult struct {
	Name      string
	Namespace string
	Phase     wfv1.WorkflowPhase
	Message   string // error message if failed
	StartedAt time.Time
	FinishedAt time.Time
}

// Succeeded returns true if the workflow completed successfully.
func (r WorkflowResult) Succeeded() bool {
	return r.Phase == wfv1.WorkflowSucceeded
}

// Failed returns true if the workflow failed or errored.
func (r WorkflowResult) Failed() bool {
	return r.Phase == wfv1.WorkflowFailed || r.Phase == wfv1.WorkflowError
}

// WaitForWorkflow watches a workflow until it reaches a terminal state
// (Succeeded, Failed, or Error), or until the context is cancelled.
// It uses the Kubernetes watch API to avoid polling.
func (c *Client) WaitForWorkflow(ctx context.Context, name string) (WorkflowResult, error) {
	// First do a Get to check if it's already done (handles fast workflows).
	wf, err := c.wfClient.ArgoprojV1alpha1().
		Workflows(c.namespace).
		Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return WorkflowResult{}, fmt.Errorf("getting workflow %q: %w", name, err)
	}
	if isTerminal(wf.Status.Phase) {
		return toResult(wf), nil
	}

	// Set up a watch from the current resource version so we don't miss updates.
	watcher, err := c.wfClient.ArgoprojV1alpha1().
		Workflows(c.namespace).
		Watch(ctx, metav1.ListOptions{
			FieldSelector:   fmt.Sprintf("metadata.name=%s", name),
			ResourceVersion: wf.ResourceVersion,
		})
	if err != nil {
		return WorkflowResult{}, fmt.Errorf("watching workflow %q: %w", name, err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return WorkflowResult{}, fmt.Errorf("timed out waiting for workflow %q: %w", name, ctx.Err())

		case event, ok := <-watcher.ResultChan():
			if !ok {
				// Watch channel closed — re-fetch and retry once.
				wf, err = c.wfClient.ArgoprojV1alpha1().
					Workflows(c.namespace).
					Get(ctx, name, metav1.GetOptions{})
				if err != nil {
					return WorkflowResult{}, fmt.Errorf("re-fetching workflow %q after watch close: %w", name, err)
				}
				if isTerminal(wf.Status.Phase) {
					return toResult(wf), nil
				}
				return WorkflowResult{}, fmt.Errorf("watch closed before workflow %q reached terminal state", name)
			}

			if event.Type == watch.Deleted {
				return WorkflowResult{}, fmt.Errorf("workflow %q was deleted before completing", name)
			}

			wf, ok = event.Object.(*wfv1.Workflow)
			if !ok {
				continue
			}

			if isTerminal(wf.Status.Phase) {
				return toResult(wf), nil
			}
		}
	}
}

func isTerminal(phase wfv1.WorkflowPhase) bool {
	switch phase {
	case wfv1.WorkflowSucceeded, wfv1.WorkflowFailed, wfv1.WorkflowError:
		return true
	}
	return false
}

func toResult(wf *wfv1.Workflow) WorkflowResult {
	r := WorkflowResult{
		Name:      wf.Name,
		Namespace: wf.Namespace,
		Phase:     wf.Status.Phase,
		Message:   wf.Status.Message,
	}
	if !wf.Status.StartedAt.IsZero() {
		r.StartedAt = wf.Status.StartedAt.Time
	}
	if !wf.Status.FinishedAt.IsZero() {
		r.FinishedAt = wf.Status.FinishedAt.Time
	}
	return r
}
