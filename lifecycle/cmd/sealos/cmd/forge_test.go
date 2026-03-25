package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForgePlanCommand(t *testing.T) {
	root := t.TempDir()
	clusterPath := filepath.Join(root, "ClusterForge.yaml")
	sourcePath := filepath.Join(root, "overlay", "apps", "infrastructure", "containerd", "1.7")

	if err := os.MkdirAll(sourcePath, 0o755); err != nil {
		t.Fatal(err)
	}

	clusterYAML := []byte(`
apiVersion: forge.sealos.io/v1alpha1
kind: ClusterForge
spec:
  base:
    image: labring/kubernetes:v1.28.0
  components:
    - name: infrastructure/containerd
      version: "1.7"
      traits: ["+ha"]
  strategy:
    allowJITBuild: true
`)
	if err := os.WriteFile(clusterPath, clusterYAML, 0o644); err != nil {
		t.Fatal(err)
	}

	forgefileYAML := []byte(`
package:
  name: infrastructure/containerd
  version: "1.7"
base:
  image: docker.io/library/alpine:3.20
`)
	if err := os.WriteFile(filepath.Join(sourcePath, "Forgefile"), forgefileYAML, 0o644); err != nil {
		t.Fatal(err)
	}

	command := newForgeCmd()
	buf := new(strings.Builder)
	command.SetOut(buf)
	command.SetErr(buf)
	command.SetArgs([]string{
		"plan",
		"-f", clusterPath,
		"--source-overlay", filepath.Join(root, "overlay"),
		"--allow-jit",
		"-o", "json",
	})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `"mode": "source-jit"`) {
		t.Fatalf("unexpected output: %s", got)
	}
}
