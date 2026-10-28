package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"
)

type securityWorkflowStep struct {
	Name             string            `yaml:"name"`
	Uses             string            `yaml:"uses"`
	Run              string            `yaml:"run"`
	WorkingDirectory string            `yaml:"working-directory"`
	With             map[string]string `yaml:"with"`
	If               any               `yaml:"if"`
	ContinueOnError  any               `yaml:"continue-on-error"`
}

type securityWorkflowJob struct {
	Needs           []string               `yaml:"needs"`
	Steps           []securityWorkflowStep `yaml:"steps"`
	If              any                    `yaml:"if"`
	ContinueOnError any                    `yaml:"continue-on-error"`
}

func readSecurityWorkflow(t *testing.T, name string) map[string]securityWorkflowJob {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("../../..", ".github/workflows", name))
	require.NoError(t, err)
	var workflow struct {
		Jobs map[string]securityWorkflowJob `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(content, &workflow))
	return workflow.Jobs
}

func securityStep(t *testing.T, job securityWorkflowJob, name string) (int, securityWorkflowStep) {
	t.Helper()
	for index, step := range job.Steps {
		if step.Name == name {
			require.Nil(t, step.If, "%s must not be conditional", name)
			require.Nil(t, step.ContinueOnError, "%s must fail the job on error", name)
			return index, step
		}
	}
	t.Fatalf("missing required step %q", name)
	return -1, securityWorkflowStep{}
}

func TestReleaseSecurityGatesBeforePublishing(t *testing.T) {
	release := readSecurityWorkflow(t, "release.yml")
	scan := readSecurityWorkflow(t, "security-scan.yml")
	for _, check := range []struct {
		releaseJob string
		scanJob    string
		steps      []string
	}{
		{"build-frontend", "frontend-security", []string{"Run pnpm audit", "Check audit exceptions"}},
		{"test-backend", "backend-security", []string{"Verify Go version", "Run govulncheck"}},
	} {
		job, ok := release[check.releaseJob]
		require.True(t, ok)
		require.Nil(t, job.If, "security gate jobs must always run")
		require.Nil(t, job.ContinueOnError)
		for _, name := range check.steps {
			_, actual := securityStep(t, job, name)
			_, expected := securityStep(t, scan[check.scanJob], name)
			require.Equal(t, strings.TrimSpace(expected.Run), strings.TrimSpace(actual.Run), name)
			require.Equal(t, expected.WorkingDirectory, actual.WorkingDirectory, name)
		}
	}

	for _, order := range []struct {
		job   string
		steps []string
	}{
		{"build-frontend", []string{"Install dependencies", "Run pnpm audit", "Check audit exceptions", "Build frontend", "Upload frontend artifact"}},
		{"test-backend", []string{"Setup Go", "Verify Go version", "Run govulncheck", "Unit tests", "Integration tests"}},
	} {
		previous := -1
		for _, name := range order.steps {
			index, _ := securityStep(t, release[order.job], name)
			require.Greater(t, index, previous, "%s: %s must follow its prerequisites", order.job, name)
			previous = index
		}
	}

	// Both the support images and GoReleaser must wait for both safety gates.
	for _, name := range []string{"build-runtime-support-images", "release"} {
		job, ok := release[name]
		require.True(t, ok)
		require.Contains(t, job.Needs, "build-frontend")
		require.Contains(t, job.Needs, "test-backend")
		require.Nil(t, job.If, "%s must not override failed dependency handling", name)
		require.Nil(t, job.ContinueOnError)
	}
}

func TestReleaseGoToolchainsUseSecurityPatch(t *testing.T) {
	content, err := os.ReadFile("../../go.mod")
	require.NoError(t, err)
	module, err := modfile.Parse("go.mod", content, nil)
	require.NoError(t, err)
	require.NotNil(t, module.Go)
	version := module.Go.Version
	require.GreaterOrEqual(t, semver.Compare("v"+version, "v1.27.3"), 0)

	for _, name := range []string{"Dockerfile", "backend/Dockerfile", "deploy/Dockerfile"} {
		content, err := os.ReadFile(filepath.Join("../../..", name))
		require.NoError(t, err)
		require.Contains(t, string(content), "golang:"+version+"-alpine", name)
	}
	for _, name := range []string{"backend-ci.yml", "security-scan.yml", "release.yml"} {
		for jobName, job := range readSecurityWorkflow(t, name) {
			for _, step := range job.Steps {
				if strings.HasPrefix(step.Uses, "actions/setup-go@") && step.With["go-version-file"] == "backend/go.mod" {
					_, check := securityStep(t, job, "Verify Go version")
					require.Equal(t, `test "$(go env GOVERSION)" = go`+version, strings.TrimSpace(check.Run), "%s/%s", name, jobName)
				}
			}
		}
	}
	for _, name := range []string{"xiass-codex-helper", "xiass-proxy-agent"} {
		content, err := os.ReadFile(filepath.Join("../../../tools", name, "go.mod"))
		require.NoError(t, err)
		module, err := modfile.Parse("go.mod", content, nil)
		require.NoError(t, err)
		require.NotNil(t, module.Go)
		version := "v" + module.Go.Version
		if semver.MajorMinor(version) == "v1.26" {
			require.GreaterOrEqual(t, semver.Compare(version, "v1.26.10"), 0, name)
		} else {
			require.GreaterOrEqual(t, semver.Compare(version, "v1.27.3"), 0, name)
		}
	}
	for _, job := range readSecurityWorkflow(t, "codex-helper-release.yml") {
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "actions/setup-go@") {
				require.Equal(t, "tools/xiass-codex-helper/go.mod", step.With["go-version-file"])
				require.Empty(t, step.With["go-version"])
			}
		}
	}
}
