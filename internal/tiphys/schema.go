package tiphys

// PipelineConfig is the top-level structure of .tiphys.yaml.
type PipelineConfig struct {
	Version  string        `yaml:"version"`
	On       TriggerConfig `yaml:"on"`
	Pipeline PipelineDef   `yaml:"pipeline"`
}

// -----------------------------------------------------------------
// Trigger configuration
// -----------------------------------------------------------------

// TriggerConfig defines when the pipeline should run.
type TriggerConfig struct {
	Push        *PushTrigger        `yaml:"push,omitempty"`
	PullRequest *PullRequestTrigger `yaml:"pull_request,omitempty"`
	Tag         *TagTrigger         `yaml:"tag,omitempty"`
}

type PushTrigger struct {
	// Branches is a list of glob patterns. Push events on matching branches trigger the pipeline.
	// Leave empty to match all branches.
	Branches []string `yaml:"branches,omitempty"`
	// Ignore is a list of glob patterns for branches to never trigger on.
	Ignore []string `yaml:"ignore,omitempty"`
}

type PullRequestTrigger struct {
	// Branches is a list of target (base) branch glob patterns to match.
	Branches []string `yaml:"branches,omitempty"`
	// Actions to react to. Defaults to [opened, reopened, synchronize].
	Actions []string `yaml:"actions,omitempty"`
}

type TagTrigger struct {
	// Pattern is a glob matched against the tag name (e.g. "v*").
	Pattern string `yaml:"pattern,omitempty"`
}

// -----------------------------------------------------------------
// Pipeline definition
// -----------------------------------------------------------------

// PipelineDef describes the steps that make up the pipeline.
type PipelineDef struct {
	// Image is the default container image used by steps that don't specify their own.
	Image string `yaml:"image,omitempty"`
	// Env are environment variables injected into every step.
	Env []EnvVar `yaml:"env,omitempty"`
	// Steps is the ordered list of CI steps.
	Steps []Step `yaml:"steps"`
}

// Step represents one unit of work in the pipeline.
type Step struct {
	// Name is a unique identifier for this step within the pipeline.
	Name string `yaml:"name"`
	// Uses is the built-in step type, e.g. "go/test", "docker/build-push".
	Uses string `yaml:"uses"`
	// Image overrides the pipeline-level default image for this step only.
	Image string `yaml:"image,omitempty"`
	// With holds step-type-specific configuration parameters.
	With map[string]string `yaml:"with,omitempty"`
	// Env are additional environment variables scoped to this step only.
	Env []EnvVar `yaml:"env,omitempty"`
	// Secrets declares secret volumes/env vars needed by this step.
	// Secrets defined here are resolved at compile time into Kubernetes volumes or env vars.
	Secrets []SecretRef `yaml:"secrets,omitempty"`
	// DependsOn lists names of steps that must complete before this step runs.
	// Steps without dependsOn run sequentially in declaration order by default.
	DependsOn []string `yaml:"dependsOn,omitempty"`
}

// -----------------------------------------------------------------
// Secret model — supports both Kubernetes Secrets and plain env vars
// -----------------------------------------------------------------

// SecretRef declares a secret needed by a step. Tiphys resolves it at compile
// time into either a Kubernetes Secret volume mount or a plain env var depending
// on the source field.
type SecretRef struct {
	// Name is the env var name that will be set inside the container.
	Name string `yaml:"name"`
	// Source defines where the secret value comes from.
	Source SecretSource `yaml:"source"`
}

// SecretSource describes how to obtain a secret value.
// Exactly one of K8sSecret or Env must be set.
type SecretSource struct {
	// K8sSecret reads the value from a Kubernetes Secret key.
	K8sSecret *K8sSecretRef `yaml:"k8sSecret,omitempty"`
	// Env reads the value from an environment variable already set on the
	// tiphys webhook Pod (e.g. injected via the Deployment env block).
	// The value is passed through to the workflow step at compile time.
	Env *EnvSecretRef `yaml:"env,omitempty"`
}

// K8sSecretRef identifies a key in a Kubernetes Secret.
type K8sSecretRef struct {
	// SecretName is the name of the Kubernetes Secret object.
	SecretName string `yaml:"secretName"`
	// Key is the key within the Secret's data map.
	Key string `yaml:"key"`
}

// EnvSecretRef reads a secret from an env var present on the webhook service Pod.
type EnvSecretRef struct {
	// VarName is the environment variable name on the webhook service Pod.
	VarName string `yaml:"varName"`
}

// -----------------------------------------------------------------
// Environment variable model
// -----------------------------------------------------------------

// EnvVar is a plain or secret-backed environment variable injected into a step.
type EnvVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value,omitempty"`
	// ValueFrom allows sourcing the value from a Kubernetes Secret (without
	// going through the SecretRef block — useful for non-sensitive config).
	ValueFrom *EnvVarSource `yaml:"valueFrom,omitempty"`
}

// EnvVarSource references a Kubernetes Secret key for use in an EnvVar.
type EnvVarSource struct {
	SecretKeyRef *SecretKeyRef `yaml:"secretKeyRef,omitempty"`
}

// SecretKeyRef identifies a key within a Kubernetes Secret.
type SecretKeyRef struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}
