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

func TestRepositoryLoadsSelfContainedManifest(t *testing.T) {
	root := t.TempDir()
	manifestDir := filepath.Join(root, "distributions", "cloud")
	packageDir := filepath.Join(root, "packages", "example")
	require.NoError(t, os.MkdirAll(manifestDir, 0o755))
	require.NoError(t, os.MkdirAll(packageDir, 0o755))
	manifestData := "name: cloud\nversion: v1.0.0\npackages:\n  - name: example\n    version: v1.0.0\n    remote:\n      image: ghcr.io/example/package:v1.0.0\n      digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"
	require.NoError(t, os.WriteFile(filepath.Join(manifestDir, "v1.0.0.yaml"), []byte(manifestData), 0o644))
	packageData := "name: example\nversion: v1.0.0\nremote:\n  image: ghcr.io/example/package:v1.0.0\n"
	require.NoError(t, os.WriteFile(filepath.Join(packageDir, "v1.0.0.yaml"), []byte(packageData), 0o644))

	repo, err := OpenLocalRepository(root)
	require.NoError(t, err)
	manifest, err := repo.Load("cloud@v1.0.0")
	require.NoError(t, err)
	require.Equal(t, "ghcr.io/example/package:v1.0.0@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", manifest.Packages[0].Remote.Reference())
	images, err := repo.Resolve("cloud@v1.0.0")
	require.NoError(t, err)
	require.Equal(t, []string{"ghcr.io/example/package:v1.0.0@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, images)

	summaries, err := repo.List()
	require.NoError(t, err)
	require.Equal(t, []Summary{{Name: "cloud", Version: "v1.0.0"}}, summaries)
	data, err := repo.Show("cloud@v1.0.0")
	require.NoError(t, err)
	require.Contains(t, data, "digest: sha256:")
}

func TestValidateRejectsDuplicates(t *testing.T) {
	manifest := Manifest{
		Name:    "cloud",
		Version: "v5.1.0",
		Images: []string{
			"ghcr.io/labring/sealos/kubernetes:v1.28.15",
			"ghcr.io/labring/sealos/kubernetes:v1.28.15",
		},
	}
	require.Error(t, manifest.Validate())
}

func TestLoadPackageWithLocalSource(t *testing.T) {
	pkg := Package{
		Name:    "sealos-cloud-user-controller",
		Version: "v5.1.0",
		Remote:  Remote{Image: "ghcr.io/labring/sealos-cloud-user-controller:v5.1.0"},
		Sources: []Source{
			{Type: SourceLocal, Path: "controllers/user/deploy", File: "Kubefile"},
			{Type: SourceGit, URL: "https://github.com/labring/sealos.git", Ref: "v5.1.0", Context: "controllers/user/deploy", File: "Kubefile"},
		},
	}
	require.NoError(t, pkg.Validate())
	require.Equal(t, "ghcr.io/labring/sealos-cloud-user-controller:v5.1.0", pkg.Remote.Reference())
	require.Len(t, pkg.Sources, 2)
	require.Equal(t, SourceLocal, pkg.Sources[0].Type)
	require.Equal(t, "controllers/user/deploy", pkg.Sources[0].Path)
	require.Equal(t, SourceGit, pkg.Sources[1].Type)
	require.Equal(t, "v5.1.0", pkg.Sources[1].Ref)
}

func TestResolveSourceBuildPlanForLocalPackage(t *testing.T) {
	pkg := Package{
		Name:    "sealos-cloud-user-controller",
		Version: "v5.1.0",
		Remote:  Remote{Image: "ghcr.io/labring/sealos-cloud-user-controller:v5.1.0"},
		Source:  &Source{Type: SourceLocal, Path: "controllers/user/deploy", File: "Kubefile"},
	}

	sourceRoot := t.TempDir()
	contextDir := filepath.Join(sourceRoot, "controllers/user/deploy")
	require.NoError(t, os.MkdirAll(contextDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "Kubefile"), []byte("FROM scratch\n"), 0o644))
	plan, err := pkg.BuildPlan(sourceRoot, t.TempDir())
	require.NoError(t, err)
	require.Equal(t, pkg.Remote.Image, plan.Image)
	require.Len(t, plan.Commands, 1)
	require.Equal(t, "sealos", plan.Commands[0].Name)
	require.Equal(t, contextDir, plan.Commands[0].Dir)
	require.Equal(t, []string{
		"build", "-t", "ghcr.io/labring/sealos-cloud-user-controller:v5.1.0",
		"-f", filepath.Join(contextDir, "Kubefile"), contextDir,
	}, plan.Commands[0].Args)
}

func TestResolveSourceBuildPlanForIndependentLocalPackage(t *testing.T) {
	sourceRoot := t.TempDir()
	contextDir := filepath.Join(sourceRoot, "deploy")
	require.NoError(t, os.MkdirAll(contextDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "Kubefile"), []byte("FROM scratch\n"), 0o644))
	pkg := Package{
		Name:    "example",
		Version: "v1.0.0",
		Remote:  Remote{Image: "ghcr.io/example/package:v1.0.0"},
		Source: &Source{
			Type:    SourceLocal,
			Path:    sourceRoot,
			Context: "deploy",
			File:    "Kubefile",
		},
	}

	plan, err := pkg.BuildPlan("/workspace/monorepo", t.TempDir())
	require.NoError(t, err)
	expectedContext := contextDir
	require.Equal(t, expectedContext, plan.Commands[0].Dir)
	require.Equal(t, []string{
		"build", "-t", "ghcr.io/example/package:v1.0.0",
		"-f", filepath.Join(expectedContext, "Kubefile"), expectedContext,
	}, plan.Commands[0].Args)
}

func TestBuildPlanForGitSource(t *testing.T) {
	pkg := Package{
		Name:    "example",
		Version: "v1.0.0",
		Remote:  Remote{Image: "ghcr.io/example/package:v1.0.0"},
		Source: &Source{
			Type:    SourceGit,
			URL:     "https://github.com/example/package.git",
			Ref:     "v1.0.0",
			Context: "deploy",
			File:    "Kubefile",
		},
	}
	sourceCache := t.TempDir()
	plan, err := pkg.BuildPlanWithOptions(ResolveOptions{Mode: ResolveSource, SourceCache: sourceCache})
	require.NoError(t, err)
	require.Empty(t, plan.CleanupDir)
	require.Len(t, plan.Commands, 4)
	cacheDir := packageSourceCachePath(pkg, *pkg.Source, sourceCache)
	require.Equal(t, "mkdir", plan.Commands[0].Name)
	require.Equal(t, []string{"-p", sourceCache}, plan.Commands[0].Args)
	require.Equal(t, "git", plan.Commands[1].Name)
	require.Equal(t, []string{"clone", "--no-checkout", "https://github.com/example/package.git", cacheDir}, plan.Commands[1].Args)
	require.Equal(t, []string{"-C", cacheDir, "checkout", "--detach", "v1.0.0"}, plan.Commands[2].Args)
	require.Equal(t, filepath.Join(cacheDir, "deploy"), plan.Commands[3].Dir)
}

func TestResolveSourceRejectsPackageWithoutSource(t *testing.T) {
	manifest := &Manifest{
		Name:    "cloud",
		Version: "test",
		Packages: []Package{{
			Name:    "kubernetes",
			Version: "v1.28.15",
			Remote:  Remote{Image: "ghcr.io/labring/sealos/kubernetes:v1.28.15"},
		}},
	}
	_, err := ResolvePackages(manifest, ResolveOptions{Mode: ResolveSource})
	require.ErrorContains(t, err, "no usable package source")
}

func TestSourceFallbackUsesGitWhenLocalIsMissing(t *testing.T) {
	pkg := Package{
		Name:    "example",
		Version: "v1.0.0",
		Remote:  Remote{Image: "ghcr.io/example/package:v1.0.0"},
		Sources: []Source{
			{Type: SourceLocal, Path: filepath.Join(t.TempDir(), "missing"), File: "Kubefile"},
			{Type: SourceGit, URL: "https://github.com/example/package.git", Ref: "v1.0.0", File: "Kubefile"},
		},
	}

	plan, err := pkg.BuildPlanWithOptions(ResolveOptions{Mode: ResolveSource, SourceCache: t.TempDir()})
	require.NoError(t, err)
	require.Equal(t, "git", plan.Commands[1].Name)
	require.Equal(t, "https://github.com/example/package.git", plan.Commands[1].Args[2])
}

func TestSourceCacheReusesGitCheckout(t *testing.T) {
	pkg := Package{
		Name:    "example",
		Version: "v1.0.0",
		Remote:  Remote{Image: "ghcr.io/example/package:v1.0.0"},
		Source: &Source{
			Type: SourceGit,
			URL:  "https://github.com/example/package.git",
			Ref:  "v1.0.0",
			File: "Kubefile",
		},
	}
	sourceCache := t.TempDir()
	cacheDir := packageSourceCachePath(pkg, *pkg.Source, sourceCache)
	require.NoError(t, os.MkdirAll(filepath.Join(cacheDir, ".git"), 0o755))

	plan, err := pkg.BuildPlanWithOptions(ResolveOptions{Mode: ResolveSource, SourceCache: sourceCache})
	require.NoError(t, err)
	require.Len(t, plan.Commands, 1)
	require.Equal(t, "sealos", plan.Commands[0].Name)
}

func TestHybridUsesRemoteWhenSourceIsUnavailable(t *testing.T) {
	manifest := &Manifest{
		Name:    "cloud",
		Version: "test",
		Packages: []Package{{
			Name:    "kubernetes",
			Version: "v1.28.15",
			Remote:  Remote{Image: "ghcr.io/labring/sealos/kubernetes:v1.28.15"},
		}},
	}
	resolved, err := ResolvePackages(manifest, ResolveOptions{Mode: ResolveHybrid})
	require.NoError(t, err)
	require.Len(t, resolved, 1)
	require.Nil(t, resolved[0].Build)
	require.Equal(t, "ghcr.io/labring/sealos/kubernetes:v1.28.15", resolved[0].Image)
}

func TestRemoteDigestReference(t *testing.T) {
	pkg := Package{
		Name:    "example",
		Version: "v1.0.0",
		Remote: Remote{
			Image:  "ghcr.io/example/package:v1.0.0",
			Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}
	require.NoError(t, pkg.Validate())
	require.Equal(t, "ghcr.io/example/package:v1.0.0@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", pkg.Remote.Reference())
}
