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
	installer := Installer{Config: cfg, Runner: runner}
	pkg := distribution.Package{Name: "demo", Version: "v1", Remote: distribution.Remote{Image: "ghcr.io/example/demo:v1"}}

	require.NoError(t, installer.InstallIncrementalPackage(context.Background(), distribution.ResolvedPackage{Package: pkg, Image: pkg.Remote.Reference()}))
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
		Package: distribution.Package{Name: "cilium", Version: "v1", Remote: distribution.Remote{Image: "ghcr.io/example/cilium:v1"}},
		Image:   "ghcr.io/example/cilium:v1",
	})
	require.ErrorIs(t, err, ErrIncrementalPackageUnsupported)
}

func TestResetRunsDistributionCleanupBeforeLegacyReset(t *testing.T) {
	runner := &recordingRunner{}
	cfg := DefaultConfig()
	cfg.Masters = "192.0.2.10"
	cfg.Nodes = "192.0.2.11"
	cfg.User = "root"
	cfg.SSHKey = "/root/.ssh/id_rsa"
	installer := Installer{Config: cfg, Runner: runner}

	require.NoError(t, installer.Reset(context.Background(), "default"))
	require.Equal(t, []string{
		"sealos exec --cluster default --user root --pk /root/.ssh/id_rsa --port 22 --roles master --capture-output 'rm -rf -- /root/.sealos/cloud'",
		"sealos reset --cluster default --force --user root --pk /root/.ssh/id_rsa --port 22",
	}, runner.commands)
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
		Packages: []distribution.Package{{
			Name: "demo", Version: "v1.0.0", Remote: distribution.Remote{Image: "ghcr.io/example/demo:v1.0.0"},
		}},
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

func TestResolveInstalledPackagesSelectsOneCiliumCandidate(t *testing.T) {
	manifest := &distribution.Manifest{
		Name:    "cloud-pro",
		Version: "v1",
		Packages: []distribution.Package{
			{Name: "cilium", Version: "v1", Remote: distribution.Remote{Image: "ghcr.io/example/cilium:v1"}},
			{Name: "cilium", Version: "v2", Remote: distribution.Remote{Image: "ghcr.io/example/cilium:v2"}},
			{Name: "kubernetes", Version: "v1", Remote: distribution.Remote{Image: "ghcr.io/example/kubernetes:v1"}},
		},
	}
	resolved, err := ResolveInstalledPackages(manifest, distribution.ResolveOptions{Mode: distribution.ResolveRemote}, "v2")
	require.NoError(t, err)
	require.Len(t, resolved, 2)
	require.Equal(t, "v2", resolved[0].Package.Version)
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
