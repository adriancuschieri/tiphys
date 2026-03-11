package steps

import (
	"fmt"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

const defaultGoImage = "golang:1.22-alpine"

func init() {
	Register("go/test", buildGoTest)
	Register("go/build", buildGoBuild)
}

// buildGoTest produces an Argo template that runs `go test`.
//
// Supported `with` keys:
//   packages   — package pattern (default: ./...)
//   race       — enable race detector: "true"/"false" (default: true)
//   count      — test count flag (default: 1)
//   extra_args — raw extra args appended to the go test command
func buildGoTest(name string, with map[string]string, image string, _ RuntimeParams) (wfv1.Template, error) {
	if image == "" {
		image = defaultGoImage
	}

	packages := withDefault(with, "packages", "./...")
	race := withDefault(with, "race", "true")
	count := withDefault(with, "count", "1")
	extraArgs := with["extra_args"]

	raceFlag := ""
	if race == "true" {
		raceFlag = "-race"
	}

	cmd := fmt.Sprintf(
		"set -e && cd /workspace/src && go test %s -count=%s %s %s",
		raceFlag, count, packages, extraArgs,
	)

	return wfv1.Template{
		Name: name,
		Container: &corev1.Container{
			Image:      image,
			Command:    []string{"sh", "-c"},
			Args:       []string{cmd},
			WorkingDir: "/workspace/src",
			Env: []corev1.EnvVar{
				{Name: "GOPATH", Value: "/go"},
				{Name: "GOCACHE", Value: "/tmp/go-build"},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "workspace", MountPath: "/workspace"},
			},
		},
	}, nil
}

// buildGoBuild produces an Argo template that compiles a Go binary.
//
// Supported `with` keys:
//   output     — output path (default: bin/app)
//   ldflags    — linker flags (default: -w -s)
//   main       — main package path (default: ./cmd/...)
//   extra_args — raw extra args appended to the go build command
func buildGoBuild(name string, with map[string]string, image string, _ RuntimeParams) (wfv1.Template, error) {
	if image == "" {
		image = defaultGoImage
	}

	output := withDefault(with, "output", "bin/app")
	ldflags := withDefault(with, "ldflags", "-w -s")
	main := withDefault(with, "main", "./...")
	extraArgs := with["extra_args"]

	cmd := fmt.Sprintf(
		`set -e && cd /workspace/src && CGO_ENABLED=0 go build -ldflags="%s" -o %s %s %s`,
		ldflags, output, main, extraArgs,
	)

	return wfv1.Template{
		Name: name,
		Container: &corev1.Container{
			Image:      image,
			Command:    []string{"sh", "-c"},
			Args:       []string{cmd},
			WorkingDir: "/workspace/src",
			Env: []corev1.EnvVar{
				{Name: "GOPATH", Value: "/go"},
				{Name: "GOCACHE", Value: "/tmp/go-build"},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "workspace", MountPath: "/workspace"},
			},
		},
	}, nil
}

func withDefault(m map[string]string, key, def string) string {
	if v, ok := m[key]; ok && v != "" {
		return v
	}
	return def
}
