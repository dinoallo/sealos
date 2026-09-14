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

package install

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labring/sealos/pkg/distribution"
	"github.com/stretchr/testify/require"
)

func TestSupportsIncrementalPackage(t *testing.T) {
	for _, name := range []string{"demo", "cert-manager", "openebs", "sealos-oss"} {
		require.True(t, SupportsIncrementalPackage(name), name)
	}
	for _, name := range []string{"kubernetes", "cilium", "helm", "sealos-cloud", "sealos-cloud-user-controller", "sealos-finish", "sealos-certs"} {
		require.False(t, SupportsIncrementalPackage(name), name)
		require.NotEmpty(t, IncrementalPackageBlockReason(name))
	}
}

func TestInstallIncrementalPackageRunsStandaloneImage(t *testing.T) {
	runner := &recordingRunner{}
	cfg := DefaultConfig()
	cfg.Masters = "192.0.2.10"
	cfg.CloudDomain = "cloud.example.com"
	installer := Installer{Config: cfg, Runner: runner, Stdout: io.Discard}
	pkg := distribution.Package{Name: "demo", Version: "v1"}

	require.NoError(t, installer.InstallIncrementalPackage(context.Background(), distribution.ResolvedPackage{Package: pkg, Image: "ghcr.io/example/demo:v1"}))
	require.Equal(t, []string{
		"sealos pull -q ghcr.io/example/demo:v1",
		"sealos run --force ghcr.io/example/demo:v1",
		"sealos rmi --force ghcr.io/example/demo:v1",
	}, runner.commands)
}

func TestClusterNameIsPassedToPackageCommands(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ClusterName = "prod"
	installer := Installer{Config: cfg}

	command := installer.runImage("ghcr.io/example/demo:v1")
	require.Equal(t, "sealos run --force ghcr.io/example/demo:v1 --cluster prod", command.String())
}

func TestInstallIncrementalPackageRejectsBootstrapPackage(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Masters = "192.0.2.10"
	cfg.CloudDomain = "cloud.example.com"
	installer := Installer{Config: cfg, Runner: &recordingRunner{}}
	err := installer.InstallIncrementalPackage(context.Background(), distribution.ResolvedPackage{
		Package: distribution.Package{Name: "cilium", Version: "v1"},
		Image:   "ghcr.io/example/cilium:v1",
	})
	require.ErrorIs(t, err, ErrIncrementalPackageUnsupported)
}

func TestResetRunsPackageCleanupInReverseDependencyOrder(t *testing.T) {
	runner := &recordingRunner{}
	cfg := DefaultConfig()
	cfg.Masters = "192.0.2.10"
	cfg.Nodes = "192.0.2.11"
	cfg.User = "root"
	cfg.SSHKey = "/root/.ssh/id_rsa"
	cfg.StateDir = t.TempDir()
	store, err := distribution.NewStateStore(cfg.StateDir, cfg.ClusterName)
	require.NoError(t, err)
	containerdArtifact, err := store.SaveCleanupArtifact("containerd@v1", []byte("#!/bin/sh\ntrue\n"))
	require.NoError(t, err)
	kubernetesArtifact, err := store.SaveCleanupArtifact("kubernetes@v1", []byte("#!/bin/sh\ntrue\n"))
	require.NoError(t, err)
	require.NoError(t, store.Save(&distribution.State{Packages: []distribution.InstalledPackage{
		{Name: "containerd", Version: "v1", Cleanup: &distribution.CleanupHook{Path: "/opt/clean.sh", Artifact: containerdArtifact}},
		{Name: "kubernetes", Version: "v1", DependsOn: []string{"containerd@v1"}, Cleanup: &distribution.CleanupHook{Path: "/opt/clean.sh", Artifact: kubernetesArtifact}},
	}}))
	installer := Installer{Config: cfg, Runner: runner, Stdout: io.Discard}

	require.NoError(t, installer.Reset(context.Background(), cfg.ClusterName))
	require.Len(t, runner.commands, 2)
	require.Contains(t, runner.commands[0], "SEALOS_PACKAGE_NAME=kubernetes")
	require.Contains(t, runner.commands[0], "--ips 192.0.2.10,192.0.2.11")
	require.Contains(t, runner.commands[1], "SEALOS_PACKAGE_NAME=containerd")
	state, err := store.Load()
	require.NoError(t, err)
	require.True(t, state.Packages[0].CleanupDone)
	require.True(t, state.Packages[1].CleanupDone)
}

func TestValidateResetRequiresMasterNodes(t *testing.T) {
	cfg := DefaultConfig()
	require.ErrorContains(t, cfg.ValidateReset(), "masters are required")
}

func TestRecordInstalledState(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Masters = "192.0.2.10"
	cfg.CloudDomain = "cloud.example.com"
	cfg.ClusterName = "prod"
	cfg.StateDir = t.TempDir()
	installer := Installer{Config: cfg}
	manifest := &distribution.Manifest{
		Name:    "cloud",
		Version: "v1.0.0",
		Packages: []distribution.DistributionPackage{
				{Ref: "demo@v1.0.0", Remote: &distribution.Remote{Image: "ghcr.io/example/demo:v1.0.0"}},		},
	}
	require.NoError(t, installer.recordInstalledState(context.Background(), manifest))
	store, err := distribution.NewStateStore(cfg.StateDir, cfg.ClusterName)
	require.NoError(t, err)
	state, err := store.Load()
	require.NoError(t, err)
	require.Equal(t, "cloud@v1.0.0", state.Distribution)
	require.Equal(t, "prod", state.Target.Cluster)
	require.Equal(t, []string{"demo@v1.0.0"}, []string{state.Packages[0].Ref()})
}

func TestInstalledPackagesPersistsResolvedDependencyRefs(t *testing.T) {
	manifest := &distribution.Manifest{
		Name:    "cloud",
		Version: "v1.0.0",
		PackageRepo: "testdata/fake-repo",
		Packages: []distribution.DistributionPackage{
			{Ref: "demo@v1.0.0", Remote: &distribution.Remote{Image: "ghcr.io/example/demo:v1.0.0"}},
		},
	}

	installed, err := InstalledPackages(manifest, distribution.ResolveRemote, "")
	require.NoError(t, err)
	require.Len(t, installed, 1)
	require.Equal(t, "demo@v1.0.0", installed[0].Ref())
}

func TestResolveInstalledPackagesSelectsOneCiliumCandidate(t *testing.T) {
	manifest := &distribution.Manifest{
		Name:    "cloud-pro",
		Version: "v1",
		Packages: []distribution.DistributionPackage{
				{Ref: "cilium@v1", Remote: &distribution.Remote{Image: "ghcr.io/example/cilium:v1"}},				{Ref: "cilium@v2", Remote: &distribution.Remote{Image: "ghcr.io/example/cilium:v2"}},				{Ref: "kubernetes@v1", Remote: &distribution.Remote{Image: "ghcr.io/example/kubernetes:v1"}},		},
	}
	resolved, err := ResolveInstalledPackages(manifest, distribution.ResolveOptions{Mode: distribution.ResolveRemote}, "v2")
	require.NoError(t, err)
	require.Len(t, resolved, 2)
	require.Equal(t, "v2", resolved[0].Package.Version)
}

func TestInstallPackageDependenciesUsesDeclaredOrder(t *testing.T) {
	runner := &recordingRunner{}
	cfg := DefaultConfig()
	cfg.Masters = "192.0.2.10"
	cfg.User = "root"
	installer := Installer{Config: cfg, Runner: runner, Stdout: io.Discard}
	resolved := []distribution.ResolvedPackage{
		{Package: distribution.Package{Name: "containerd", Version: "v1"}, Image: "ghcr.io/example/containerd:v1"},
		{Package: distribution.Package{Name: "kubernetes", Version: "v1", DependsOn: []distribution.PackageDependency{{Slot: "containerd", Version: "v1"}}}, Image: "ghcr.io/example/kubernetes:v1", Dependencies: []string{"containerd@v1"}},
	}

	installed, err := installer.installPackageDependencies(context.Background(), resolved, "kubernetes")
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"containerd@v1": true}, installed)
	require.Len(t, runner.commands, 3)
	require.Equal(t, "sealos pull -q ghcr.io/example/containerd:v1", runner.commands[0])
	require.Contains(t, runner.commands[1], "sealos run --package --force ghcr.io/example/containerd:v1")
	require.Contains(t, runner.commands[1], "--masters 192.0.2.10")
	require.Equal(t, "sealos rmi --force ghcr.io/example/containerd:v1", runner.commands[2])
}

func TestCapturePackageCleanupHookExtractsExecutableScript(t *testing.T) {
	root := t.TempDir()
	hookPath := filepath.Join(root, "opt", "package-clean.sh")
	require.NoError(t, os.MkdirAll(filepath.Dir(hookPath), 0o755))
	require.NoError(t, os.WriteFile(hookPath, []byte("#!/bin/sh\necho cleaned\n"), 0o755))
	runner := &cleanupMetadataRunner{mountPoint: root}
	installer := Installer{Config: DefaultConfig(), Runner: runner, Stdout: io.Discard}

	require.NoError(t, installer.capturePackageCleanupHook(context.Background(), "ghcr.io/example/demo:v1"))
	artifact, ok := installer.cleanupArtifactForImage("ghcr.io/example/demo:v1")
	require.True(t, ok)
	require.Equal(t, "/opt/package-clean.sh", artifact.Path)
	require.Equal(t, "#!/bin/sh\necho cleaned\n", string(artifact.Script))
	require.Contains(t, runner.commands[0], "sealos create")
	require.Contains(t, runner.commands[1], "sealos umount")
	require.Contains(t, runner.commands[2], "sealos rm")
}

func TestResetContinuesAfterPackageCleanupFailure(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Masters = "192.0.2.10"
	cfg.User = "root"
	cfg.StateDir = t.TempDir()
	store, err := distribution.NewStateStore(cfg.StateDir, cfg.ClusterName)
	require.NoError(t, err)
	baseArtifact, err := store.SaveCleanupArtifact("base@v1", []byte("#!/bin/sh\ntrue\n"))
	require.NoError(t, err)
	failedArtifact, err := store.SaveCleanupArtifact("failed@v1", []byte("#!/bin/sh\nfalse\n"))
	require.NoError(t, err)
	require.NoError(t, store.Save(&distribution.State{Packages: []distribution.InstalledPackage{
		{Name: "base", Version: "v1", Cleanup: &distribution.CleanupHook{Path: "/opt/clean.sh", Artifact: baseArtifact}},
		{Name: "failed", Version: "v1", DependsOn: []string{"base@v1"}, Cleanup: &distribution.CleanupHook{Path: "/opt/clean.sh", Artifact: failedArtifact}},
	}}))
	runner := &failingCleanupRunner{failOn: "SEALOS_PACKAGE_NAME=failed"}
	installer := Installer{Config: cfg, Runner: runner, Stdout: io.Discard}

	err = installer.Reset(context.Background(), cfg.ClusterName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed@v1")
	require.Len(t, runner.commands, 2)
	state, err := store.Load()
	require.NoError(t, err)
	require.False(t, state.Packages[1].CleanupDone)
	require.True(t, state.Packages[0].CleanupDone)
}

type recordingRunner struct {
	commands []string
}

func (r *recordingRunner) Run(_ context.Context, command Command) error {
	r.commands = append(r.commands, command.String())
	return nil
}

func (r *recordingRunner) Output(context.Context, Command) ([]byte, error) {
	return []byte(""), nil
}

type cleanupMetadataRunner struct {
	commands   []string
	mountPoint string
}

type failingCleanupRunner struct {
	commands []string
	failOn   string
}

func (r *failingCleanupRunner) Run(_ context.Context, command Command) error {
	r.commands = append(r.commands, command.String())
	if strings.Contains(command.String(), r.failOn) {
		return errors.New("cleanup failed")
	}
	return nil
}

func (r *failingCleanupRunner) Output(context.Context, Command) ([]byte, error) {
	return nil, nil
}

func (r *cleanupMetadataRunner) Run(_ context.Context, command Command) error {
	r.commands = append(r.commands, command.String())
	return nil
}

func (r *cleanupMetadataRunner) Output(_ context.Context, command Command) ([]byte, error) {
	if len(command.Args) > 0 && command.Args[0] == "inspect" {
		return []byte(`{"OCIv1":{"Config":{"Labels":{"sealos.io/package-clean":"/opt/package-clean.sh"}}}}`), nil
	}
	return []byte(`[{"mountPoint":"` + r.mountPoint + `"}]`), nil
}
