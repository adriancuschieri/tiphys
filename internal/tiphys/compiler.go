package tiphys

import (
	"fmt"
	"os"
	"strings"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/adriancuschieri/tiphys/internal/tiphys/steps"
)

// CompilerOptions configures the compiler behaviour.
type CompilerOptions struct {
	// WorkspaceSize is the PVC size for the shared workspace volume (default: 1Gi).
	WorkspaceSize string
	// ServiceAccountName is the Kubernetes SA the workflow pods run as.
	ServiceAccountName string
}

func defaultOptions() CompilerOptions {
	return CompilerOptions{
		WorkspaceSize:      "1Gi",
		ServiceAccountName: "workflow",
	}
}

// Compile translates a PipelineConfig into a ready-to-submit Argo Workflow.
// Steps without explicit dependsOn run sequentially in declaration order.
// Steps with dependsOn form a DAG — the two models are unified under DAG always,
// which naturally expresses sequential chains as a linear dependency list.
func Compile(cfg *PipelineConfig, params steps.RuntimeParams, opts *CompilerOptions) (*wfv1.Workflow, error) {
	o := defaultOptions()
	if opts != nil {
		if opts.WorkspaceSize != "" {
			o.WorkspaceSize = opts.WorkspaceSize
		}
		if opts.ServiceAccountName != "" {
			o.ServiceAccountName = opts.ServiceAccountName
		}
	}

	// Resolve secrets for all steps before building templates.
	// This lets us fail fast on bad config before touching the Argo API.
	resolvedSecrets, err := resolveStepSecrets(cfg.Pipeline.Steps)
	if err != nil {
		return nil, fmt.Errorf("resolving secrets: %w", err)
	}

	// Collect any Kubernetes Secret volumes needed across all steps.
	extraVolumes := collectK8sSecretVolumes(cfg.Pipeline.Steps)

	// Build individual step Argo templates.
	stepTemplates, err := buildStepTemplates(cfg.Pipeline.Steps, cfg.Pipeline.Image, cfg.Pipeline.Env, params, resolvedSecrets)
	if err != nil {
		return nil, err
	}

	// Always use a DAG entrypoint — sequential order is expressed as a linear
	// chain of dependencies, which is simpler and more consistent than switching
	// between steps/DAG modes.
	entryTemplate := buildDAGTemplate("pipeline", cfg.Pipeline.Steps)

	cloneTemplate := buildGitCloneTemplate()
	allTemplates := []wfv1.Template{entryTemplate, cloneTemplate}
	allTemplates = append(allTemplates, stepTemplates...)

	wf := &wfv1.Workflow{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "tiphys-ci-",
			Labels: map[string]string{
				"tiphys.io/repo":   sanitizeLabel(params.Repo),
				"tiphys.io/branch": sanitizeLabel(params.Branch),
				"tiphys.io/event":  params.EventType,
			},
			Annotations: map[string]string{
				"tiphys.io/commit":    params.Commit,
				"tiphys.io/repo":      params.Repo,
				"tiphys.io/clone-url": params.CloneURL,
			},
		},
		Spec: wfv1.WorkflowSpec{
			Entrypoint:         "pipeline",
			ServiceAccountName: o.ServiceAccountName,
			Templates:          allTemplates,
			Arguments: wfv1.Arguments{
				Parameters: []wfv1.Parameter{
					{Name: "repo", Value: wfv1.AnyStringPtr(params.Repo)},
					{Name: "commit", Value: wfv1.AnyStringPtr(params.Commit)},
					{Name: "branch", Value: wfv1.AnyStringPtr(params.Branch)},
					{Name: "clone_url", Value: wfv1.AnyStringPtr(params.CloneURL)},
					{Name: "event_type", Value: wfv1.AnyStringPtr(params.EventType)},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "workspace"},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse(o.WorkspaceSize),
							},
						},
					},
				},
			},
			Volumes: extraVolumes,
		},
	}

	return wf, nil
}

// -----------------------------------------------------------------
// DAG builder — sequential by default, DAG when dependsOn is used
// -----------------------------------------------------------------

// buildDAGTemplate always produces a DAG entrypoint.
// Steps without dependsOn are chained sequentially in declaration order
// (each depends on the previous). Steps with explicit dependsOn override this.
func buildDAGTemplate(name string, pipelineSteps []Step) wfv1.Template {
	tasks := []wfv1.DAGTask{
		{Name: "git-clone", Template: "git-clone"},
	}

	for i, s := range pipelineSteps {
		task := wfv1.DAGTask{
			Name:     s.Name,
			Template: s.Name,
		}

		if len(s.DependsOn) > 0 {
			// Developer-declared explicit dependencies.
			task.Dependencies = s.DependsOn
		} else if i == 0 {
			// First step always waits for git-clone.
			task.Dependencies = []string{"git-clone"}
		} else {
			// Subsequent steps without dependsOn chain off the previous step.
			task.Dependencies = []string{pipelineSteps[i-1].Name}
		}

		tasks = append(tasks, task)
	}

	return wfv1.Template{
		Name: name,
		DAG:  &wfv1.DAGTemplate{Tasks: tasks},
	}
}

// -----------------------------------------------------------------
// Step template builder
// -----------------------------------------------------------------

// resolvedStepEnv holds the env vars to inject per step after secret resolution.
type resolvedStepEnv map[string][]corev1.EnvVar

// resolveStepSecrets resolves all SecretRef entries for each step into concrete
// corev1.EnvVar values. K8sSecret refs become valueFrom entries; Env refs are
// read from the webhook Pod's environment at compile time and baked in as plain
// string values (so the workflow step does not need access to the webhook's env).
func resolveStepSecrets(pipelineSteps []Step) (resolvedStepEnv, error) {
	result := make(resolvedStepEnv)

	for _, s := range pipelineSteps {
		var envVars []corev1.EnvVar

		for _, ref := range s.Secrets {
			if ref.Name == "" {
				return nil, fmt.Errorf("step %q: secret ref is missing 'name'", s.Name)
			}

			switch {
			case ref.Source.K8sSecret != nil:
				// Reference a Kubernetes Secret key — resolved at runtime inside the Pod.
				k := ref.Source.K8sSecret
				if k.SecretName == "" || k.Key == "" {
					return nil, fmt.Errorf("step %q: secret %q k8sSecret requires both secretName and key", s.Name, ref.Name)
				}
				envVars = append(envVars, corev1.EnvVar{
					Name: ref.Name,
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: k.SecretName},
							Key:                  k.Key,
						},
					},
				})

			case ref.Source.Env != nil:
				// Read from the webhook service's own environment and bake the value
				// into the workflow at compile time.
				varName := ref.Source.Env.VarName
				if varName == "" {
					return nil, fmt.Errorf("step %q: secret %q env source requires varName", s.Name, ref.Name)
				}
				val := os.Getenv(varName)
				if val == "" {
					return nil, fmt.Errorf("step %q: secret %q references env var %q which is not set on the webhook service", s.Name, ref.Name, varName)
				}
				envVars = append(envVars, corev1.EnvVar{
					Name:  ref.Name,
					Value: val,
				})

			default:
				return nil, fmt.Errorf("step %q: secret %q must have either k8sSecret or env source", s.Name, ref.Name)
			}
		}

		result[s.Name] = envVars
	}

	return result, nil
}

// collectK8sSecretVolumes builds the workflow-level volume list for any
// docker/build-push steps that need a registry credential secret volume.
func collectK8sSecretVolumes(pipelineSteps []Step) []corev1.Volume {
	seen := map[string]bool{}
	var volumes []corev1.Volume

	for _, s := range pipelineSteps {
		if s.Uses != "docker/build-push" {
			continue
		}
		secretName := withDefault2(s.With, "registry_secret", "")
		if secretName == "" {
			continue // using env-based credentials, no volume needed
		}
		if seen[secretName] {
			continue
		}
		seen[secretName] = true
		volumes = append(volumes, corev1.Volume{
			Name: "docker-config",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
					Items: []corev1.KeyToPath{
						{Key: ".dockerconfigjson", Path: "config.json"},
					},
				},
			},
		})
	}

	return volumes
}

// buildStepTemplates converts each Step into an Argo Template, merging env vars
// from the global pipeline config, the step itself, and resolved secrets.
func buildStepTemplates(
	pipelineSteps []Step,
	defaultImage string,
	globalEnv []EnvVar,
	params steps.RuntimeParams,
	resolvedSecrets resolvedStepEnv,
) ([]wfv1.Template, error) {
	var templates []wfv1.Template

	for _, s := range pipelineSteps {
		builder, err := steps.Get(s.Uses)
		if err != nil {
			return nil, err
		}

		img := s.Image
		if img == "" {
			img = defaultImage
		}

		tmpl, err := builder(s.Name, s.With, img, params)
		if err != nil {
			return nil, fmt.Errorf("building step %q: %w", s.Name, err)
		}

		if tmpl.Container != nil {
			// Env priority (lowest → highest): global pipeline env, step env, secrets.
			var merged []corev1.EnvVar
			merged = append(merged, toK8sEnv(globalEnv)...)
			merged = append(merged, toK8sEnv(s.Env)...)
			merged = append(merged, resolvedSecrets[s.Name]...)
			// Step builder's own env entries (image-specific defaults) come last.
			tmpl.Container.Env = append(merged, tmpl.Container.Env...)
		}

		templates = append(templates, tmpl)
	}

	return templates, nil
}

// buildGitCloneTemplate produces the shared git-clone step prepended to all pipelines.
func buildGitCloneTemplate() wfv1.Template {
	return wfv1.Template{
		Name: "git-clone",
		Container: &corev1.Container{
			Image:   "alpine/git:2.43.0",
			Command: []string{"sh", "-c"},
			Args: []string{
				`set -e
git clone "{{workflow.parameters.clone_url}}" /workspace/src
cd /workspace/src
git checkout "{{workflow.parameters.commit}}"
echo "Checked out {{workflow.parameters.commit}} in {{workflow.parameters.repo}}"`,
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "workspace", MountPath: "/workspace"},
			},
		},
	}
}

// -----------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------

func toK8sEnv(envs []EnvVar) []corev1.EnvVar {
	result := make([]corev1.EnvVar, 0, len(envs))
	for _, e := range envs {
		kv := corev1.EnvVar{Name: e.Name, Value: e.Value}
		if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			kv.Value = ""
			kv.ValueFrom = &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: e.ValueFrom.SecretKeyRef.Name,
					},
					Key: e.ValueFrom.SecretKeyRef.Key,
				},
			}
		}
		result = append(result, kv)
	}
	return result
}

func sanitizeLabel(s string) string {
	s = strings.ReplaceAll(s, "/", "-")
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

func withDefault2(m map[string]string, key, def string) string {
	if v, ok := m[key]; ok && v != "" {
		return v
	}
	return def
}
