// Copyright © 2026 sealos.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package distribution

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyncRepositoryFromLocalGitCheckout(t *testing.T) {
	source := t.TempDir()
	manifestDir := filepath.Join(source, "distributions", "cloud")
	require.NoError(t, os.MkdirAll(manifestDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(manifestDir, "v1.0.0.yaml"), []byte("name: cloud\nversion: v1.0.0\nimages:\n  - ghcr.io/example/cloud:v1.0.0\n"), 0o644))
	runGitTest(t, source, "init")
	runGitTest(t, source, "config", "user.email", "test@example.com")
	runGitTest(t, source, "config", "user.name", "Sealos Test")
	runGitTest(t, source, "config", "commit.gpgsign", "false")
	runGitTest(t, source, "add", ".")
	runGitTest(t, source, "commit", "-m", "initial repository")
	runGitTest(t, source, "branch", "-M", "main")

	cache := filepath.Join(t.TempDir(), "cache")
	repo, err := OpenRepository(context.Background(), RepositoryConfig{URL: source, Ref: "main", CacheDir: cache})
	require.NoError(t, err)
	require.Equal(t, []string{"ghcr.io/example/cloud:v1.0.0"}, mustResolve(t, repo, "cloud@v1.0.0"))

	status, err := repo.Status()
	require.NoError(t, err)
	require.NotEmpty(t, status.Commit)
	require.False(t, status.LastSync.IsZero())

	offlineRepo, err := OpenRepository(context.Background(), RepositoryConfig{URL: source, Ref: "main", CacheDir: cache, Offline: true})
	require.NoError(t, err)
	require.Equal(t, repo.Root, offlineRepo.Root)
}

func TestLoadRepositoryConfigPrecedence(t *testing.T) {
	configHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	require.NoError(t, os.MkdirAll(filepath.Dir(DefaultRepositoryConfigPath()), 0o755))
	require.NoError(t, os.WriteFile(DefaultRepositoryConfigPath(), []byte("repository:\n  url: https://config.example/repo.git\n  ref: stable\n"), 0o600))
	t.Setenv("SEALOS_PACKAGE_REPO_REF", "testing")

	config, err := LoadRepositoryConfig()
	require.NoError(t, err)
	require.Equal(t, "https://config.example/repo.git", config.URL)
	require.Equal(t, "testing", config.Ref)
	require.Equal(t, RepositoryCachePathForRef(config.URL, config.Ref, filepath.Join(cacheHome, "sealos", "repositories")), config.CacheDir)
}

func TestRepositoryStatusReportsMissingCache(t *testing.T) {
	status, err := RepositoryStatusFor(RepositoryConfig{URL: "https://example.com/repo.git", CacheDir: filepath.Join(t.TempDir(), "missing")})
	require.NoError(t, err)
	require.False(t, status.Available)
	require.True(t, status.Stale)
}

func TestRepositoryCachePathSeparatesRefs(t *testing.T) {
	root := t.TempDir()
	require.NotEqual(t,
		RepositoryCachePathForRef("https://example.com/repo.git", "main", root),
		RepositoryCachePathForRef("https://example.com/repo.git", "stable", root),
	)
}

func TestRepositoryListRejectsManifestPathMismatch(t *testing.T) {
	root := t.TempDir()
	manifestDir := filepath.Join(root, "distributions", "cloud")
	require.NoError(t, os.MkdirAll(manifestDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(manifestDir, "v1.0.0.yaml"), []byte("name: other\nversion: v1.0.0\nimages:\n  - ghcr.io/example/cloud:v1.0.0\n"), 0o644))

	repo, err := OpenLocalRepository(root)
	require.NoError(t, err)
	_, err = repo.List()
	require.ErrorContains(t, err, "does not match its path")
}

func TestRepositoryLoadsYAMLVariant(t *testing.T) {
	root := t.TempDir()
	manifestDir := filepath.Join(root, "distributions", "cloud")
	require.NoError(t, os.MkdirAll(manifestDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(manifestDir, "v1.0.0.yml"), []byte("name: cloud\nversion: v1.0.0\nimages:\n  - ghcr.io/example/cloud:v1.0.0\n"), 0o644))

	repo, err := OpenLocalRepository(root)
	require.NoError(t, err)
	manifest, err := repo.Load("cloud@v1.0.0")
	require.NoError(t, err)
	require.Equal(t, "cloud@v1.0.0", manifest.Ref())
}

func mustResolve(t *testing.T, repo *Repository, ref string) []string {
	t.Helper()
	images, err := repo.Resolve(ref)
	require.NoError(t, err)
	return images
}

func runGitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}
