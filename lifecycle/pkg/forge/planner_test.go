package forge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildPlanBinaryHit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "apps", "networking", "cilium", "1.14.0")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte(`
binaryOverlays:
  - name: cache
    manifests:
      - digest: sha256:1111111111111111111111111111111111111111111111111111111111111111
        annotations:
          forge.sealos.io/traits: +cgroupv2,+ebpf
`)
	if err := os.WriteFile(filepath.Join(path, "Forgefile"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	cluster := &ClusterForge{
		TypeMeta: TypeMeta{APIVersion: DefaultAPIVersion, Kind: ClusterForgeKind},
		Spec: ClusterForgeSpec{
			Base: ClusterForgeBase{Image: "labring/kubernetes:v1.28.0"},
			Components: []ComponentSpec{
				{Name: "networking/cilium", Version: "1.14.0", Traits: []string{"+ebpf", "+cgroupv2"}},
			},
		},
	}

	plan, err := BuildPlan(cluster, Options{BinaryOverlayPath: root})
	if err != nil {
		t.Fatalf("BuildPlan returned error: %v", err)
	}
	if got := plan.Components[0].Resolution.Mode; got != "binary-cache-hit" {
		t.Fatalf("resolution mode = %q, want binary-cache-hit", got)
	}
}

func TestBuildPlanSourceJIT(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "apps", "infrastructure", "containerd", "1.7")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte(`
package:
  name: infrastructure/containerd
  version: "1.7"
base:
  image: docker.io/library/alpine:3.20
patches:
  toml_patch:
    - path: patches/default.toml
    - path: patches/ha.toml
      traits: ["+ha"]
templates:
  - path: templates/config.tmpl
scripts:
  - path: scripts/init.sh
dependencies:
  - name: networking/cilium
    version: "1.14.0"
    traits: ["+ha"]
`)
	if err := os.WriteFile(filepath.Join(path, "Forgefile"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	cluster := &ClusterForge{
		TypeMeta: TypeMeta{APIVersion: DefaultAPIVersion, Kind: ClusterForgeKind},
		Spec: ClusterForgeSpec{
			Base: ClusterForgeBase{Image: "labring/kubernetes:v1.28.0"},
			Components: []ComponentSpec{
				{Name: "infrastructure/containerd", Version: "1.7", Traits: []string{"+ha"}},
			},
			Strategy: ClusterForgeStrategy{AllowJITBuild: true},
		},
	}

	plan, err := BuildPlan(cluster, Options{SourceOverlayPath: root})
	if err != nil {
		t.Fatalf("BuildPlan returned error: %v", err)
	}
	component := plan.Components[0]
	if got := component.Resolution.Mode; got != "source-jit" {
		t.Fatalf("resolution mode = %q, want source-jit", got)
	}
	if component.Resolution.ForgefileSummary == nil {
		t.Fatal("expected forgefile summary")
	}
	if got := component.Resolution.ForgefileSummary.EnabledPatchCount; got != 2 {
		t.Fatalf("enabled patch count = %d, want 2", got)
	}
}
