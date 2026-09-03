// Copyright 2026 sealos.
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

package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labring/sealos/pkg/distribution"
	"github.com/stretchr/testify/require"
)

func TestRepoSyncCommandUsesRepositoryFlags(t *testing.T) {
	isolateRepositoryConfig(t)
	source := testRepository(t)
	cache := filepath.Join(t.TempDir(), "cache")

	cmd := newRepoSyncCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--repo", source, "--repo-ref", "main", "--repo-cache", cache})

	require.NoError(t, cmd.Execute())
	require.Contains(t, output.String(), "synced "+source+" at ")
	_, err := os.Stat(filepath.Join(cache, "distributions", "cloud", "v1.0.0.yaml"))
	require.NoError(t, err)
}

func TestRepoStatusCommandUsesCacheFlag(t *testing.T) {
	isolateRepositoryConfig(t)
	cache := t.TempDir()
	manifestDir := filepath.Join(cache, "distributions", "cloud")
	require.NoError(t, os.MkdirAll(manifestDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(manifestDir, "v1.0.0.yaml"), []byte("name: cloud\nversion: v1.0.0\nimages:\n  - ghcr.io/example/cloud:v1.0.0\n"), 0o644))

	cmd := newRepoStatusCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--repo", "https://example.com/packages.git", "--repo-ref", "stable", "--repo-cache", cache, "--offline"})

	require.NoError(t, cmd.Execute())
	require.Contains(t, output.String(), "url: https://example.com/packages.git")
	require.Contains(t, output.String(), "ref: stable")
	require.Contains(t, output.String(), "cache: "+cache)
	require.Contains(t, output.String(), "available: true")
}

func TestRepoStatusRecomputesDerivedCacheWhenRepoRefChanges(t *testing.T) {
	isolateRepositoryConfig(t)
	cmd := newRepoStatusCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--repo", "https://example.com/packages.git", "--repo-ref", "stable"})

	require.NoError(t, cmd.Execute())
	expected := distribution.RepositoryCachePathForRef("https://example.com/packages.git", "stable", distribution.DefaultRepositoryCacheRoot())
	require.Contains(t, output.String(), "cache: "+expected)
}

func TestDistributionListUsesRepositoryFlags(t *testing.T) {
	isolateRepositoryConfig(t)
	cache := t.TempDir()
	manifestDir := filepath.Join(cache, "distributions", "cloud")
	require.NoError(t, os.MkdirAll(manifestDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(manifestDir, "v1.0.0.yaml"), []byte("name: cloud\nversion: v1.0.0\nimages:\n  - ghcr.io/example/cloud:v1.0.0\n"), 0o644))

	cmd := newDistributionListCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--repo-cache", cache, "--offline"})

	require.NoError(t, cmd.Execute())
	require.Equal(t, "cloud@v1.0.0\n", output.String())
}

func isolateRepositoryConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("SEALOS_PACKAGE_REPO", "")
	t.Setenv("SEALOS_PACKAGE_REPO_REF", "")
	t.Setenv("SEALOS_PACKAGE_REPO_CACHE", "")
}

func testRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	manifestDir := filepath.Join(root, "distributions", "cloud")
	require.NoError(t, os.MkdirAll(manifestDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(manifestDir, "v1.0.0.yaml"), []byte("name: cloud\nversion: v1.0.0\nimages:\n  - ghcr.io/example/cloud:v1.0.0\n"), 0o644))
	runRepositoryGit(t, root, "init")
	runRepositoryGit(t, root, "config", "user.email", "test@example.com")
	runRepositoryGit(t, root, "config", "user.name", "Sealos Test")
	runRepositoryGit(t, root, "config", "commit.gpgsign", "false")
	runRepositoryGit(t, root, "add", ".")
	runRepositoryGit(t, root, "commit", "-m", "initial repository")
	runRepositoryGit(t, root, "branch", "-M", "main")
	return root
}

func runRepositoryGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}
