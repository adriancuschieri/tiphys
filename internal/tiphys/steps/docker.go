package steps

import (
	"fmt"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

const defaultKanikoImage = "gcr.io/kaniko-project/executor:v1.23.0"

func init() {
	Register("docker/build-push", buildDockerBuildPush)
}

// buildDockerBuildPush produces an Argo template that builds and pushes a Docker
// image using Kaniko (no Docker daemon required).
//
// Supported `with` keys:
//   image             — destination image name, required (e.g. myregistry/myapp)
//   tag               — image tag; defaults to the commit SHA
//   dockerfile        — path relative to repo root (default: Dockerfile)
//   context           — build context path relative to repo root (default: .)
//   cache             — enable Kaniko layer caching: "true"/"false" (default: true)
//   cache_ttl         — layer cache TTL (default: 24h)
//   registry_secret   — name of a Kubernetes Secret containing .dockerconfigjson;
//                       when set, credentials come from a mounted K8s Secret volume.
//                       Omit this key if you are injecting credentials via the step's
//                       secrets block using an env source instead.
//   extra_args        — raw extra flags passed directly to the Kaniko executor
func buildDockerBuildPush(name string, with map[string]string, image string, params RuntimeParams) (wfv1.Template, error) {
	if image == "" {
		image = defaultKanikoImage
	}

	destImage := with["image"]
	if destImage == "" {
		return wfv1.Template{}, fmt.Errorf("step %q (docker/build-push) requires 'with.image'", name)
	}

	tag := withDefault(with, "tag", params.Commit)
	dockerfile := withDefault(with, "dockerfile", "Dockerfile")
	buildContext := withDefault(with, "context", ".")
	cache := withDefault(with, "cache", "true")
	cacheTTL := withDefault(with, "cache_ttl", "24h")
	registrySecret := with["registry_secret"] // empty = env-based credentials
	extraArgs := with["extra_args"]

	destination := fmt.Sprintf("%s:%s", destImage, tag)

	args := []string{
		fmt.Sprintf("--context=dir:///workspace/src/%s", buildContext),
		fmt.Sprintf("--dockerfile=/workspace/src/%s", dockerfile),
		fmt.Sprintf("--destination=%s", destination),
		fmt.Sprintf("--cache=%s", cache),
		fmt.Sprintf("--cache-ttl=%s", cacheTTL),
	}
	if extraArgs != "" {
		args = append(args, extraArgs)
	}

	volumeMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: "/workspace"},
	}

	// Credential strategy A: mount a Kubernetes Secret as a volume.
	// The secret must contain a .dockerconfigjson key.
	if registrySecret != "" {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "docker-config",
			MountPath: "/kaniko/.docker",
			ReadOnly:  true,
		})
	}
	// Credential strategy B: env vars (DOCKER_USERNAME / DOCKER_PASSWORD or
	// a pre-encoded DOCKER_CONFIG_JSON) are injected via the step's secrets
	// block. The compiler merges those into the container env before submission.
	// When using this strategy, pass --skip-tls-verify if your registry requires it,
	// or configure the registry auth via DOCKER_CONFIG_JSON env var.

	return wfv1.Template{
		Name: name,
		Container: &corev1.Container{
			Image:        image,
			Args:         args,
			VolumeMounts: volumeMounts,
		},
	}, nil
}
