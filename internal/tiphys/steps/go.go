package steps

import (
	"fmt"
	"strings"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

const (
	defaultGoImageAlpine = "golang:1.26-alpine"
	defaultGoImageDebian = "golang:1.26"
)

func init() {
	Register("go/test", buildGoTest)
	Register("go/build", buildGoBuild)
}

func buildGoTest(name string, with map[string]string, image string, _ RuntimeParams) (wfv1.Template, error) {
	if image == "" {
		image = defaultGoImageAlpine
	}

	packages := withDefault(with, "packages", "./...")
	count := withDefault(with, "count", "1")
	extraArgs := with["extra_args"]

	raceDefault := "false"
	if !isAlpineImage(image) {
		raceDefault = "true"
	}
	race := withDefault(with, "race", raceDefault)

	if race == "true" && isAlpineImage(image) {
		return wfv1.Template{}, fmt.Errorf(
			"step %q: race=true requires a Debian-based Go image (e.g. golang:1.22) — "+
				"alpine images do not include gcc. Either set race=false or change the image.",
			name,
		)
	}

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

func buildGoBuild(name string, with map[string]string, image string, _ RuntimeParams) (wfv1.Template, error) {
	if image == "" {
		image = defaultGoImageAlpine
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

func isAlpineImage(image string) bool {
	return strings.Contains(strings.ToLower(image), "alpine")
}

func withDefault(m map[string]string, key, def string) string {
	if v, ok := m[key]; ok && v != "" {
		return v
	}
	return def
}