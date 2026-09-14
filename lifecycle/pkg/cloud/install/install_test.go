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
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labring/sealos/pkg/constants"
	"github.com/labring/sealos/pkg/distribution"
	"github.com/stretchr/testify/require"
)

func TestResolveImagesKeepsManifestOrder(t *testing.T) {
	images, err := ResolveImages(testCloudManifest())
	require.NoError(t, err)
	require.Equal(t, "ghcr.io/labring/sealos/kubernetes:v1.28.15", images.Kubernetes)
	require.Equal(t, "ghcr.io/labring/sealos-cloud:v5.1.0", images.Cloud)
	require.Equal(t, "ghcr.io/labring/sealos-cloud-desktop-frontend:v5.1.0", images.CloudDesktopFrontend)
	require.Equal(t, "ghcr.io/labring/sealos-cloud-launchpad-service:v5.1.0", images.CloudLaunchpadService)
}

func TestResolveImagesRejectsBootstrapManifest(t *testing.T) {
	_, err := ResolveImages(&distribution.Manifest{Name: "cloud-pro", Version: "v5.1.2-rc6"})
	require.ErrorContains(t, err, "not supported")
}

func TestValidateManifestAllowsCloudProBootstrap(t *testing.T) {
	manifest := &distribution.Manifest{
		Name:    "cloud-pro",
		Version: "v5.1.2-rc6",
		Packages: []distribution.DistributionPackage{
			{Ref: "cilium@v1.16.9", Remote: &distribution.Remote{Image: "ghcr.io/sealos-apps/sealos-pro:cilium-v1.16.9"}},
			{Ref: "kubernetes@v1.28.15", Remote: &distribution.Remote{Image: "ghcr.io/sealos-apps/sealos-pro:kubernetes-v1.28.15"}},
		},
	}
	require.NoError(t, ValidateManifest(manifest, distribution.ResolveOptions{Mode: distribution.ResolveRemote}))
}

func TestSelectCiliumImage(t *testing.T) {
	resolved := []distribution.ResolvedPackage{
		{Package: distribution.Package{Name: "cilium", Version: "v1.16.9"}, Image: "registry.example/cilium:v1.16.9"},
		{Package: distribution.Package{Name: "cilium", Version: "v1.17.17"}, Image: "registry.example/cilium:v1.17.17"},
	}

	image, err := selectCiliumImage(resolved, "")
	require.NoError(t, err)
	require.Equal(t, "registry.example/cilium:v1.16.9", image)

	image, err = selectCiliumImage(resolved, "v1.17.17")
	require.NoError(t, err)
	require.Equal(t, "registry.example/cilium:v1.17.17", image)

	_, err = selectCiliumImage(resolved, "v1.13.18")
	require.ErrorContains(t, err, `version "v1.13.18" is unavailable`)
}

func TestCloudProRC6SOTWIsInstallable(t *testing.T) {
	manifest := &distribution.Manifest{
		Name:    "cloud-pro",
		Version: "v5.1.2-rc6",
		Packages: []distribution.DistributionPackage{
			{Ref: "cilium@v1.16.9", Remote: &distribution.Remote{Image: "ghcr.io/sealos-apps/sealos-pro:cilium-v1.16.9"}},
			{Ref: "kubernetes@v1.28.15", Remote: &distribution.Remote{Image: "ghcr.io/sealos-apps/sealos-pro:kubernetes-v1.28.15"}},
		},
	}

	cfg := DefaultConfig()
	cfg.Masters = "192.0.2.10"
	cfg.CloudDomain = "cloud.example.com"
	cfg.DryRun = true
	var output bytes.Buffer
	installer := Installer{Config: cfg, Runner: noopRunner{}, Stdout: &output}
	require.NoError(t, installer.Install(context.Background(), manifest))
	// Verify the bootstrap install produced expected output
	require.Contains(t, output.String(), "sealos run --force ghcr.io/sealos-apps/sealos-pro:cilium-v1.16.9")
	require.Contains(t, output.String(), "Distribution installation completed with all packages processed")
}
func TestConfigValidation(t *testing.T) {
	require.Equal(t, "cloud-pro@v5.1.2-rc6", DefaultConfig().Distribution)
	require.Equal(t, "30000-50000", DefaultConfig().ServiceNodePortRange)
	require.Empty(t, DefaultConfig().CiliumVersion)
	cfg := DefaultConfig()
	cfg.Masters = "192.0.2.10:22"
	cfg.CloudDomain = "cloud.example.com"
	require.NoError(t, cfg.Validate())
	cfg.SourceRoot = ""
	require.NoError(t, cfg.Validate())
	cfg.PackageMode = distribution.ResolveHybrid
	require.NoError(t, cfg.Validate())
	cfg.ServiceNodePortRange = "32768-30000"
	require.ErrorContains(t, cfg.Validate(), "must be within 1-65535 and ordered")

	cfg.CloudDomain = ""
	require.ErrorContains(t, cfg.Validate(), "cloud domain is required")
}

func TestServiceNodePortRangeCommand(t *testing.T) {
	installer := Installer{Config: DefaultConfig()}
	command := installer.serviceNodePortRangeCommand()
	require.Equal(t, "sealos", command.Name)
	require.Equal(t, []string{"exec", "--roles", "master"}, command.Args[:3])
	require.Contains(t, command.Args[3], `node_port_range="30000-50000"`)
	require.Contains(t, command.Args[3], "if grep -Eq")
	require.Contains(t, command.Args[3], "exit 0")
	require.Contains(t, command.Args[3], "sealos.io/service-node-port-range:")
	require.Contains(t, command.Args[3], `cp "$tmp" "$manifest"`)
	require.Contains(t, command.Args[3], `awk -v node_port_range="$node_port_range"`)
	require.NotContains(t, command.Args[3], "sed -i")
}

func TestPrepareClusterBootstrapArchivesStaleLocalState(t *testing.T) {
	previousRoot := constants.DefaultRuntimeRootDir
	constants.DefaultRuntimeRootDir = t.TempDir()
	t.Cleanup(func() { constants.DefaultRuntimeRootDir = previousRoot })

	cfg := DefaultConfig()
	cfg.ClusterName = "platform-test"
	cfg.Masters = "192.0.2.10"
	clusterDir := constants.ClusterDir(cfg.ClusterName)
	require.NoError(t, os.MkdirAll(clusterDir, 0o700))
	require.NoError(t, os.WriteFile(constants.Clusterfile(cfg.ClusterName), []byte("stale"), 0o600))

	runner := &remoteStateRunner{output: []byte("absent\n")}
	installer := Installer{Config: cfg, Runner: runner, Stdout: io.Discard}
	require.NoError(t, installer.prepareClusterBootstrap(context.Background()))
	_, err := os.Stat(constants.Clusterfile(cfg.ClusterName))
	require.ErrorIs(t, err, os.ErrNotExist)
	entries, err := os.ReadDir(filepath.Dir(clusterDir))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Contains(t, entries[0].Name(), "platform-test.stale-")
	require.Contains(t, runner.commands[0].String(), "/etc/kubernetes/manifests/kube-apiserver.yaml")
}

func TestPrepareClusterBootstrapRejectsPartialRemoteState(t *testing.T) {
	previousRoot := constants.DefaultRuntimeRootDir
	constants.DefaultRuntimeRootDir = t.TempDir()
	t.Cleanup(func() { constants.DefaultRuntimeRootDir = previousRoot })

	cfg := DefaultConfig()
	cfg.ClusterName = "platform-test"
	cfg.Masters = "192.0.2.10"
	clusterDir := constants.ClusterDir(cfg.ClusterName)
	require.NoError(t, os.MkdirAll(clusterDir, 0o700))
	require.NoError(t, os.WriteFile(constants.Clusterfile(cfg.ClusterName), []byte("stale"), 0o600))

	installer := Installer{Config: cfg, Runner: &remoteStateRunner{output: []byte("partial\n")}, Stdout: io.Discard}
	err := installer.prepareClusterBootstrap(context.Background())
	require.ErrorContains(t, err, "remote Kubernetes state")
	require.FileExists(t, constants.Clusterfile(cfg.ClusterName))
}

func TestCloudRuntimeConfigCommand(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PodCIDR = "10.244.0.0/16"
	cfg.ServiceCIDR = "10.96.0.0/12"
	cfg.ServiceNodePortRange = "31000-32000"
	cfg.CiliumMaskSize = "24"
	cfg.MaxPods = 200
	installer := Installer{Config: cfg}
	command := installer.cloudRuntimeConfigCommand()

	require.Equal(t, "sealos", command.Name)
	require.Equal(t, []string{"exec", "--roles", "master"}, command.Args[:3])
	require.Contains(t, command.Args[3], "mkdir -p \"$root/scripts\" \"$root/values\" \"$root/bin\"")
	require.Contains(t, command.Args[3], "mv \"$tools_tmp\" \"$root/scripts/tools.sh\"")
	require.Contains(t, command.Args[3], "mv \"$values_tmp\" \"$root/values/global.yaml\"")
	require.Contains(t, command.Args[3], "ln -s \"$(command -v yq)\" \"$root/bin/yq\"")
	require.Contains(t, command.Args[3], "base64 -d")

	toolsEncoded := command.Args[3][strings.Index(command.Args[3], "printf '%s' '")+len("printf '%s' '"):]
	toolsEncoded = toolsEncoded[:strings.Index(toolsEncoded, "' | base64 -d")]
	tools, err := base64.StdEncoding.DecodeString(toolsEncoded)
	require.NoError(t, err)
	require.Contains(t, string(tools), "ensure_global_values_ready_for_component")

	valuesEncoded := command.Args[3][strings.LastIndex(command.Args[3], "printf '%s' '")+len("printf '%s' '"):]
	valuesEncoded = valuesEncoded[:strings.Index(valuesEncoded, "' | base64 -d")]
	values, err := base64.StdEncoding.DecodeString(valuesEncoded)
	require.NoError(t, err)
	require.Contains(t, string(values), "podCIDR: \"10.244.0.0/16\"")
	require.Contains(t, string(values), "serviceNodePortRange: \"31000-32000\"")
	require.Contains(t, string(values), "domain: \"\"")
	require.Contains(t, string(values), "maxPods: 200")
}

func TestConfigFromEnv(t *testing.T) {
	values := map[string]string{
		"SEALOS_V2_MASTERS":           "192.0.2.10:22",
		"SEALOS_V2_CLOUD_DOMAIN":      "cloud.example.com",
		"SEALOS_V2_CLOUD_PORT":        "8443",
		"SEALOS_V2_MAX_POD":           "200",
		"SEALOS_V2_ENABLE_ACME":       "true",
		"SEALOS_V2_REGISTRY_PASSWORD": "secret",
		"SEALOS_V2_PACKAGE_MODE":      "source",
		"SEALOS_V2_SOURCE_ROOT":       "/workspace/sealos",
		"SEALOS_V2_BUILD_CACHE":      "/workspace/cache",
		"SEALOS_V2_CILIUM_VERSION":    "v1.17.17",
	}
	cfg := ConfigFromEnv(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	require.Equal(t, "192.0.2.10:22", cfg.Masters)
	require.Equal(t, uint16(8443), cfg.CloudPort)
	require.Equal(t, 200, cfg.MaxPods)
	require.True(t, cfg.EnableACME)
	require.Equal(t, "secret", cfg.RegistryPass)
	require.Equal(t, distribution.ResolveSource, cfg.PackageMode)
	require.Equal(t, "/workspace/sealos", cfg.SourceRoot)
	require.Equal(t, "/workspace/cache", cfg.BuildCache)
	require.Equal(t, "v1.17.17", cfg.CiliumVersion)
}

func TestLoadConfigFileAndApply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
cluster: prod
masters: 192.0.2.10
cloudDomain: cloud.example.com
cloudPort: 8443
packageMode: hybrid
enableACME: false
dryRun: false
maxPods: 200
waitTimeout: 45m
targetID: target-prod
`), 0o600))

	fileConfig, err := LoadConfigFile(path)
	require.NoError(t, err)
	cfg := DefaultConfig()
	cfg.CloudDomain = "cli.example.com"
	cfg.EnableACME = true
	cfg.DryRun = true
	provided := fileConfig.Apply(&cfg, map[string]bool{"cloudDomain": true})

	require.Equal(t, "prod", cfg.ClusterName)
	require.Equal(t, "192.0.2.10", cfg.Masters)
	require.Equal(t, "cli.example.com", cfg.CloudDomain)
	require.Equal(t, uint16(8443), cfg.CloudPort)
	require.Equal(t, distribution.ResolveHybrid, cfg.PackageMode)
	require.False(t, cfg.EnableACME)
	require.False(t, cfg.DryRun)
	require.Equal(t, 200, cfg.MaxPods)
	require.Equal(t, 45*time.Minute, cfg.WaitTimeout)
	require.Equal(t, "target-prod", cfg.TargetID)
	require.True(t, provided["cloudDomain"])
	require.True(t, provided["enableACME"])

	require.NoError(t, os.WriteFile(path, []byte("unknown: value\n"), 0o600))
	_, err = LoadConfigFile(path)
	require.ErrorContains(t, err, "unknown field")
}

func TestCommandRedactsSecrets(t *testing.T) {
	command := Command{Name: "sealos", Args: []string{
		"login", "-u", "admin", "-p", "secret-password", "sealos.hub:5000", "--pk-passwd", "key-secret",
		"--env", "PASSWORD_SALT=another-secret",
		"--env", "databaseMongodbURI=mongodb://user:password@example.com",
		"--build-arg", "REGISTRY_TOKEN=build-secret",
	}}
	redacted := command.RedactedString()
	require.NotContains(t, redacted, "secret-password")
	require.NotContains(t, redacted, "key-secret")
	require.NotContains(t, redacted, "another-secret")
	require.NotContains(t, redacted, "mongodb://user:password@example.com")
	require.NotContains(t, redacted, "build-secret")
	require.Contains(t, redacted, "<redacted>")
	require.Equal(t, "sealos run image:v1 --env certSecretName=wildcard-cert", (Command{Name: "sealos", Args: []string{"run", "image:v1", "--env", "certSecretName=wildcard-cert"}}).RedactedString())
}

func TestImageWithEnvsIncludesAllKeys(t *testing.T) {
	installer := Installer{Config: Config{WaitTimeout: time.Minute}}
	command := installer.imageWithEnvs("image:v1", map[string]string{"cloudDomain": "example.com", "PASSWORD_SALT": "secret"})
	require.Contains(t, command.Args, "cloudDomain=example.com")
	require.Contains(t, command.Args, "PASSWORD_SALT=secret")
}

func TestRewriteProxy(t *testing.T) {
	images, err := ResolveImages(&distribution.Manifest{
		Name:     "cloud",
		Version:  "test",
		Packages: testCloudManifest().Packages,
	})
	require.NoError(t, err)
	images = images.RewriteProxy()
	require.Equal(t, "ghcr.dockerproxy.net/labring/sealos/kubernetes:v1.28.15", images.Kubernetes)
	require.Equal(t, "ghcr.dockerproxy.net/labring/sealos-cloud-launchpad-service:v5.1.0", images.CloudLaunchpadService)
}

func TestDryRunDoesNotWaitForKubernetes(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Masters = "192.0.2.10:22"
	cfg.CloudDomain = "cloud.example.com"
	cfg.DryRun = true
	var output bytes.Buffer
	installer := Installer{Config: cfg, Runner: noopRunner{}, Stdout: &output}
	require.NoError(t, installer.Install(context.Background(), testCloudManifest()))
	require.Contains(t, output.String(), "sealos run --force ghcr.io/labring/sealos/sealos-certs:v0.1.0")
}

func TestNodesReadyRequiresAtLeastOneReadyNode(t *testing.T) {
	require.False(t, nodesReady(nil))
	require.False(t, nodesReady([]byte("")))
	require.False(t, nodesReady([]byte("node-a   NotReady   control-plane")))
	require.True(t, nodesReady([]byte("node-a   Ready   control-plane\nnode-b   Ready   <none>\n")))
}

func TestDaemonSetReadyRequiresAllDesiredPods(t *testing.T) {
	require.False(t, daemonSetReady([]byte(`{"status":{"desiredNumberScheduled":0,"numberReady":0}}`)))
	require.False(t, daemonSetReady([]byte(`{"status":{"desiredNumberScheduled":2,"numberReady":1}}`)))
	require.True(t, daemonSetReady([]byte(`{"status":{"desiredNumberScheduled":2,"numberReady":2}}`)))
	require.False(t, daemonSetReady([]byte(`not-json`)))
}

func TestClusterStatusUsesMasterExecWhenMastersAreConfigured(t *testing.T) {
	installer := Installer{Config: Config{Masters: "192.0.2.10:22"}}
	require.Equal(t, Command{
		Name: "sealos",
		Args: []string{"exec", "--roles", "master", "--capture-output", "kubectl get nodes --no-headers"},
	}, installer.clusterStatusCommand())
}

func TestClusterStatusUsesLocalKubeconfigWithoutMasters(t *testing.T) {
	installer := Installer{}
	command := installer.clusterStatusCommand()
	require.Equal(t, "kubectl", command.Name)
	require.Equal(t, []string{"--kubeconfig", constants.NewPathResolver("default").AdminFile(), "get", "nodes", "--no-headers"}, command.Args)
}

func TestCloudConfigReadsConfigMapAsJSON(t *testing.T) {
	runner := &configMapRunner{}
	installer := Installer{Config: Config{WaitTimeout: time.Second}, Runner: runner}

	config, err := installer.cloudConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, "cloud.example.com", config.CloudDomain)
	require.Equal(t, "postgres://global", config.DatabaseGlobalCockroach)
	require.True(t, config.TLSRejectUnauthorized)
	require.Len(t, runner.commands, 2)
	require.Contains(t, runner.commands[0].Args, "json")
}

func TestObjectStorageCredentialsDecodeSecretData(t *testing.T) {
	runner := &configMapRunner{}
	installer := Installer{Config: Config{WaitTimeout: time.Second}, Runner: runner}

	accessKey, secretKey, err := installer.objectStorageCredentials(context.Background())
	require.NoError(t, err)
	require.Equal(t, "minio-admin", accessKey)
	require.Equal(t, "minio-secret", secretKey)
}

func TestObjectStorageCredentialsFallsBackToMinioUserSecret(t *testing.T) {
	runner := &objectStorageCredentialRunner{}
	installer := Installer{Config: Config{WaitTimeout: time.Second}, Runner: runner}

	accessKey, secretKey, err := installer.objectStorageCredentials(context.Background())
	require.NoError(t, err)
	require.Equal(t, "user-access", accessKey)
	require.Equal(t, "user-secret", secretKey)
}

func TestInstallBootstrapsNonCloudDistribution(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Masters = "192.0.2.10:22"
	cfg.CloudDomain = "cloud.example.com"
	cfg.CiliumVersion = "v1.16.9"
	cfg.DryRun = true
	manifest := &distribution.Manifest{
		Name:    "cloud-pro",
		Version: "v5.1.2-rc6",
		Packages: []distribution.DistributionPackage{
			{Ref: "kubernetes@v1.28.15", Remote: &distribution.Remote{Image: "registry.example/kubernetes:v1.28.15"}},
			{Ref: "cilium@v1.16.9", Remote: &distribution.Remote{Image: "registry.example/cilium:v1.16.9"}},
			{Ref: "cert-manager@v1.19.1", Remote: &distribution.Remote{Image: "registry.example/cert-manager:v1.19.1"}},
		},
	}
	var output bytes.Buffer
	installer := Installer{Config: cfg, Runner: noopRunner{}, Stdout: &output}
	require.NoError(t, installer.Install(context.Background(), manifest))
	require.Contains(t, output.String(), "sealos run --force --allow-existing-runtime registry.example/kubernetes:v1.28.15")
	require.Contains(t, output.String(), "sealos run --force registry.example/cilium:v1.16.9")
	require.Contains(t, output.String(), "sealos pull -q registry.example/cert-manager:v1.19.1")
	require.Contains(t, output.String(), "sealos run --force registry.example/cert-manager:v1.19.1")
	require.Contains(t, output.String(), "Distribution installation completed with all packages processed")
}

func TestInstallBootstrapsNonCloudDistributionSelectsManifestCiliumByDefault(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Masters = "192.0.2.10:22"
	cfg.CloudDomain = "cloud.example.com"
	cfg.DryRun = true
	manifest := &distribution.Manifest{
		Name:    "platform",
		Version: "v0.1.0",
		Packages: []distribution.DistributionPackage{
			{Ref: "kubernetes@v1.28.15", Remote: &distribution.Remote{Image: "registry.example/kubernetes:v1.28.15"}},
			{Ref: "cilium@v1.17.1", Remote: &distribution.Remote{Image: "registry.example/cilium:v1.17.1"}},
		},
	}
	var output bytes.Buffer
	installer := Installer{Config: cfg, Runner: noopRunner{}, Stdout: &output}
	require.NoError(t, installer.Install(context.Background(), manifest))
	require.Equal(t, "v1.17.1", installer.Config.CiliumVersion)
	require.Contains(t, output.String(), "sealos run --force registry.example/cilium:v1.17.1")
}

func TestInstallRejectsIncompleteCloudPackageSet(t *testing.T) {
	installer := Installer{Config: DefaultConfig()}
	err := installer.installDistributionPackages(context.Background(), []distribution.ResolvedPackage{{
		Package: distribution.Package{Name: "sealos-cloud-desktop-frontend", Version: "v1"},
		Image:   "ghcr.io/example/desktop:v1",
	}}, nil)
	require.ErrorContains(t, err, `missing Cloud package "sealos-cloud-user-controller"`)
}

func TestWaitForDesktopAcceptsSealosDesktopPod(t *testing.T) {
	runner := &podOutputRunner{output: []byte("sealos-desktop-7b7b7b7b7b-abcde 1/1 Running 0 10s\n")}
	installer := Installer{Config: Config{WaitTimeout: time.Second}, Runner: runner}

	require.NoError(t, installer.waitForDesktop(context.Background()))
}

type noopRunner struct{}

type remoteStateRunner struct {
	commands []Command
	output   []byte
}

func (r *remoteStateRunner) Run(context.Context, Command) error {
	return nil
}

func (r *remoteStateRunner) Output(_ context.Context, command Command) ([]byte, error) {
	r.commands = append(r.commands, command)
	return r.output, nil
}

type podOutputRunner struct {
	output []byte
}

func (r *podOutputRunner) Run(context.Context, Command) error {
	return nil
}

func (r *podOutputRunner) Output(context.Context, Command) ([]byte, error) {
	return r.output, nil
}

type objectStorageCredentialRunner struct{}

func (objectStorageCredentialRunner) Run(context.Context, Command) error {
	return nil
}

func (objectStorageCredentialRunner) Output(_ context.Context, command Command) ([]byte, error) {
	if strings.Contains(command.String(), "object-storage-secret") {
		return []byte(`{"data":{"accesskey":"","secretkey":""}}`), nil
	}
	if strings.Contains(command.String(), "object-storage-user-0") {
		return []byte(`{"data":{"CONSOLE_ACCESS_KEY":"dXNlci1hY2Nlc3M=","CONSOLE_SECRET_KEY":"dXNlci1zZWNyZXQ="}}`), nil
	}
	return nil, errors.New("unexpected command")
}

type configMapRunner struct {
	commands []Command
}

func testCloudManifest() *distribution.Manifest {
	packages := make([]distribution.DistributionPackage, 0, len(testImages))
	for _, image := range testImages {
		colon := strings.LastIndex(image, ":")
		slash := strings.LastIndex(image[:colon], "/")
		packages = append(packages, distribution.DistributionPackage{
			Ref:    image[slash+1 : colon] + "@" + image[colon+1:],
			Remote: &distribution.Remote{Image: image},
		})
	}
	return &distribution.Manifest{Name: "cloud", Version: "v5.1.0", Packages: packages}
}

func (noopRunner) Run(context.Context, Command) error {
	return nil
}

func (noopRunner) Output(context.Context, Command) ([]byte, error) {
	return nil, nil
}

func (r *configMapRunner) Run(context.Context, Command) error {
	return nil
}

func (r *configMapRunner) Output(_ context.Context, command Command) ([]byte, error) {
	r.commands = append(r.commands, command)
	if strings.Contains(command.String(), "object-storage-secret") {
		return []byte(`{"data":{"accesskey":"bWluaW8tYWRtaW4=","secretkey":"bWluaW8tc2VjcmV0"}}`), nil
	}
	if strings.Contains(command.String(), "cert-config") {
		return []byte("self-signed"), nil
	}
	return []byte(`{"data":{"cloudDomain":"cloud.example.com","cloudPort":"443","regionUID":"region-1","databaseGlobalCockroachdbURI":"postgres://global","databaseLocalCockroachdbURI":"postgres://local","databaseMongodbURI":"mongodb://mongo","passwordSalt":"salt","jwtInternal":"internal","jwtRegional":"regional","jwtGlobal":"global"}}`), nil
}

var testImages = []string{
	"ghcr.io/labring/sealos/kubernetes:v1.28.15",
	"ghcr.io/labring/sealos/cilium:v1.17.1",
	"ghcr.io/labring/sealos/cert-manager:v1.14.6",
	"ghcr.io/labring/sealos/helm:v3.16.2",
	"ghcr.io/labring/sealos/openebs:v3.10.0",
	"ghcr.io/labring/sealos/higress:v2.1.3",
	"ghcr.io/labring/sealos/kubeblocks:v0.8.2",
	"ghcr.io/labring/sealos/cockroach:v2.12.0",
	"ghcr.io/labring/sealos/metrics-server:v0.6.4",
	"ghcr.io/labring/sealos/victoria-metrics-k8s-stack:v1.124.0",
	"ghcr.io/labring/sealos-cloud:v5.1.0",
	"ghcr.io/labring/sealos/sealos-finish:v0.1.0",
	"ghcr.io/labring/sealos/sealos-certs:v0.1.0",
	"ghcr.io/labring/sealos-cloud-desktop-frontend:v5.1.0",
	"ghcr.io/labring/sealos-cloud-user-controller:v5.1.0",
	"ghcr.io/labring/sealos-cloud-terminal-controller:v5.1.0",
	"ghcr.io/labring/sealos-cloud-app-controller:v5.1.0",
	"ghcr.io/labring/sealos-cloud-resources-controller:v5.1.0",
	"ghcr.io/labring/sealos-cloud-account-controller:v5.1.0",
	"ghcr.io/labring/sealos-cloud-account-service:v5.1.0",
	"ghcr.io/labring/sealos-cloud-license-controller:v5.1.0",
	"ghcr.io/labring/sealos-cloud-job-init-controller:v5.1.0",
	"ghcr.io/labring/sealos-cloud-job-heartbeat-controller:v5.1.0",
	"ghcr.io/labring/sealos-cloud-applaunchpad-frontend:v5.1.0",
	"ghcr.io/labring/sealos-cloud-terminal-frontend:v5.1.0",
	"ghcr.io/labring/sealos-cloud-dbprovider-frontend:v5.1.0",
	"ghcr.io/labring/sealos-cloud-costcenter-frontend:v5.1.0",
	"ghcr.io/labring/sealos-cloud-template-frontend:v5.1.0",
	"ghcr.io/labring/sealos-cloud-license-frontend:v5.1.0",
	"ghcr.io/labring/sealos-cloud-database-service:v5.1.0",
	"ghcr.io/labring/sealos-cloud-launchpad-service:v5.1.0",
}
