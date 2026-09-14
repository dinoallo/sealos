// Copyright © 2026 sealos.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package distribution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestParseRef(t *testing.T) {
	name, version, err := ParseRef("cloud@v5.1.0")
	require.NoError(t, err)
	require.Equal(t, "cloud", name)
	require.Equal(t, "v5.1.0", version)
}

func TestParseRefRejectsInvalidRef(t *testing.T) {
	_, _, err := ParseRef("cloud")
	require.Error(t, err)
}

func TestParseRefRejectsPathTraversal(t *testing.T) {
	for _, ref := range []string{"../cloud@v1.0.0", "cloud@../v1.0.0", "cloud@v1/0.0", "cloud@v1\\0.0"} {
		_, _, err := ParseRef(ref)
		require.Error(t, err, ref)
	}
}

func TestValidateRejectsDuplicates(t *testing.T) {
	manifest := Manifest{
		Name:    "cloud",
		Version: "v5.1.0",
		Packages: []DistributionPackage{
			{Ref: "demo@v1", Remote: &Remote{Image: "ghcr.io/example/demo:v1"}},
			{Ref: "demo@v1", Remote: &Remote{Image: "ghcr.io/example/demo:v1"}},
		},
	}
	require.Error(t, manifest.Validate())
}

func TestResolvePackagesOrdersDependenciesStably(t *testing.T) {
	packages := []Package{
		{Name: "application", Version: "v1", DependsOn: []PackageDependency{{Slot: "kubernetes", Version: "v1"}}},
		{Name: "unrelated", Version: "v1"},
		{Name: "kubernetes", Version: "v1", DependsOn: []PackageDependency{{Slot: "containerd", Version: "v1"}}},
		{Name: "containerd", Version: "v1"},
	}

	graph, err := resolvePackageGraph(packages)
	require.NoError(t, err)
	require.Equal(t, []string{"unrelated", "containerd", "kubernetes", "application"}, packageNamesFromGraph(graph))
	for _, node := range graph {
		if node.Package.Name == "application" {
			require.Equal(t, []string{"kubernetes@v1"}, node.Dependencies)
		}
		if node.Package.Name == "kubernetes" {
			require.Equal(t, []string{"containerd@v1"}, node.Dependencies)
		}
	}
}

func TestPackageDependencyParsesAsSlotAndConstraint(t *testing.T) {
	var pkg Package
	require.NoError(t, yaml.Unmarshal([]byte(`
name: application
version: v1.0.0
dependsOn:
  - slot: containerd
    version: ">= 1.0.0, < 2.0.0"
`), &pkg))
	require.Len(t, pkg.DependsOn, 1)
	require.Equal(t, "containerd", pkg.DependsOn[0].Slot)
	require.Equal(t, ">= 1.0.0, < 2.0.0", pkg.DependsOn[0].Version)
}

func TestResolvePackagesRejectsSelfDependency(t *testing.T) {
	packages := []Package{{
		Name:    "kubernetes",
		Version: "v1.28.15",
		DependsOn: []PackageDependency{{
			Slot:    "kubernetes",
			Version: "v1.28.15",
		}},
	}}
	_, err := resolvePackageGraph(packages)
	require.ErrorContains(t, err, "cannot depend on itself")
}

func TestResolvePackagesRejectsMissingSlot(t *testing.T) {
	packages := []Package{
		{Name: "application", Version: "v1.0.0", DependsOn: []PackageDependency{{Slot: "missing", Version: "v1.0.0"}}},
		{Name: "unrelated", Version: "v1.0.0"},
	}
	_, err := resolvePackageGraph(packages)
	require.ErrorContains(t, err, `slot "missing"`)
}

func TestResolvePackagesRejectsIncompatibleVersion(t *testing.T) {
	packages := []Package{
		{Name: "application", Version: "v1.0.0", DependsOn: []PackageDependency{{Slot: "database", Version: ">= 2.0.0"}}},
		{Name: "database", Version: "v1.0.0"},
	}
	_, err := resolvePackageGraph(packages)
	require.ErrorContains(t, err, `matching version constraint ">= 2.0.0"`)
}

func TestResolvePackagesRejectsDuplicateSlots(t *testing.T) {
	pkg := Package{
		Name:    "application",
		Version: "v1.0.0",
		DependsOn: []PackageDependency{
			{Slot: "database", Version: "v1"},
			{Slot: "database", Version: "v1"},
		},
	}
	require.ErrorContains(t, pkg.Validate(), "duplicate dependency slot")
}

func TestResolvePackagesRejectsUnmatchedDuplicatePackage(t *testing.T) {
	packages := []Package{
		{Name: "cilium", Version: "v1.16.9"},
		{Name: "cilium", Version: "v1.16.9"},
	}
	_, err := resolvePackageGraph(packages)
	require.ErrorContains(t, err, `duplicate package "cilium@v1.16.9"`)
}

func TestPackageValidationVersions(t *testing.T) {
	for _, version := range []struct {
		version string
		valid   bool
	}{
		{"v1.2.3", true},
		{"1.2.3", true},
		{"v0.1.0-rc1", true},
		{"latest", false},
	} {
		pkg := Package{Name: "demo", Version: version.version}
		if version.valid {
			require.NoError(t, pkg.Validate(), version.version)
		} else {
			require.Error(t, pkg.Validate(), version.version)
		}
	}
}

func TestPackageValidationRejectsEmptyName(t *testing.T) {
	require.Error(t, (Package{Name: "", Version: "v1"}).Validate())
}

func TestPackageValidationRejectsSelfDependency(t *testing.T) {
	err := (Package{Name: "demo", Version: "v1", DependsOn: []PackageDependency{{Slot: "demo", Version: "v1"}}}).Validate()
	require.ErrorContains(t, err, "cannot depend on itself")
}

func TestValidateRemoteImageDigest(t *testing.T) {
	for _, remote := range []Remote{
		{Image: "ghcr.io/example/package:v1.0.0"},
		{Image: "ghcr.io/example/package:v1.0.0", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	} {
		require.NoError(t, validateRemote(remote))
	}
}

func TestValidateRemoteRejectsEmptyImage(t *testing.T) {
	require.Error(t, validateRemote(Remote{Image: ""}))
}

func TestValidateRemoteRejectsInvalidImage(t *testing.T) {
	require.Error(t, validateRemote(Remote{Image: "://invalid"}))
}

func TestDefaultBuildCacheDirUsesXDG(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-cache")
	require.Equal(t, "/tmp/xdg-cache/sealos/build", DefaultBuildCacheDir())
}

func TestPackageValidateChecksDependencySlots(t *testing.T) {
	require.ErrorContains(t, (Package{Name: "demo", Version: "v1", DependsOn: []PackageDependency{{Slot: "", Version: "v1"}}}).Validate(), "empty dependency slot")
	require.ErrorContains(t, (Package{Name: "demo", Version: "v1", DependsOn: []PackageDependency{{Slot: "@invalid", Version: "v1"}}}).Validate(), "invalid dependency slot")
	require.ErrorContains(t, (Package{Name: "demo", Version: "v1", DependsOn: []PackageDependency{{Slot: "database", Version: ""}}}).Validate(), "empty version constraint")
	require.ErrorContains(t, (Package{Name: "demo", Version: "v1", DependsOn: []PackageDependency{{Slot: "database", Version: "invalid"}}}).Validate(), "invalid version constraint") // actual text may differ
}

func TestRemoteReference(t *testing.T) {
	r := Remote{Image: "ghcr.io/example/package:v1.0.0"}
	require.Equal(t, "ghcr.io/example/package:v1.0.0", r.Reference())
}

func TestRemoteReferenceWithDigest(t *testing.T) {
	r := Remote{Image: "ghcr.io/example/package:v1.0.0", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	require.Equal(t, "ghcr.io/example/package:v1.0.0@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", r.Reference())
}

func TestPackageValidationEmptyDescription(t *testing.T) {
	require.NoError(t, (Package{Name: "demo", Version: "v1", Description: ""}).Validate())
}

func TestBuildPlanFromLocalRepo(t *testing.T) {
	root := t.TempDir()
	contextDir := filepath.Join(root, "packages", "example", "v1.0.0")
	require.NoError(t, os.MkdirAll(contextDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "Kubefile"), []byte("FROM scratch\n"), 0o644))
	pkg := Package{Name: "example", Version: "v1.0.0"}

	plan, err := buildPlanFromRepo(root, pkg, ResolveOptions{})
	require.NoError(t, err)
	require.Len(t, plan.Commands, 1)
	require.Equal(t, "sealos", plan.Commands[0].Name)
	require.Equal(t, contextDir, plan.Commands[0].Dir)
}

func TestBuildPlanFromRepoRejectsMissingContext(t *testing.T) {
	root := t.TempDir()
	pkg := Package{Name: "missing", Version: "v1.0.0"}
	_, err := buildPlanFromRepo(root, pkg, ResolveOptions{})
	require.ErrorContains(t, err, "not found")
}

func TestRemoteDigestReference(t *testing.T) {
	r := Remote{
		Image:  "ghcr.io/example/package:v1.0.0",
		Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	require.Equal(t, "ghcr.io/example/package:v1.0.0@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", r.Reference())
}

func packageNamesFromGraph(graph []packageGraphNode) []string {
	names := make([]string, 0, len(graph))
	for _, node := range graph {
		names = append(names, node.Package.Name)
	}
	return names
}

func packageNames(packages []ResolvedPackage) []string {
	names := make([]string, 0, len(packages))
	for _, item := range packages {
		names = append(names, item.Package.Name)
	}
	return names
}
