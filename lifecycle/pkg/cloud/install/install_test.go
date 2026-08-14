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
	"testing"
	"time"

	"github.com/labring/sealos/pkg/distribution"
	"github.com/stretchr/testify/require"
)

func TestResolveImagesKeepsManifestOrder(t *testing.T) {
	manifest, err := distribution.Load("cloud@v5.1.0")
	require.NoError(t, err)

	images, err := ResolveImages(manifest)
	require.NoError(t, err)
	require.Equal(t, "ghcr.io/labring/sealos/kubernetes:v1.28.15", images.Kubernetes)
	require.Equal(t, "ghcr.io/labring/sealos-cloud:v5.1.0", images.Cloud)
	require.Equal(t, "ghcr.io/labring/sealos-cloud-desktop-frontend:v5.1.0", images.CloudDesktopFrontend)
	require.Equal(t, "ghcr.io/labring/sealos-cloud-launchpad-service:v5.1.0", images.CloudLaunchpadService)
}

func TestResolveImagesRejectsTrackingOnlyManifest(t *testing.T) {
	manifest, err := distribution.Load("cloud-pro@v5.1.2-rc5-fix01")
	require.NoError(t, err)

	_, err = ResolveImages(manifest)
	require.ErrorContains(t, err, "not supported")
}

func TestConfigValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Masters = "192.0.2.10:22"
	cfg.CloudDomain = "cloud.example.com"
	require.NoError(t, cfg.Validate())
	cfg.SourceRoot = ""
	require.NoError(t, cfg.Validate())
	cfg.PackageMode = distribution.ResolveHybrid
	require.NoError(t, cfg.Validate())

	cfg.CloudDomain = ""
	require.ErrorContains(t, cfg.Validate(), "cloud domain is required")
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
		"SEALOS_V2_SOURCE_CACHE":      "/workspace/cache",
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
	require.Equal(t, "/workspace/cache", cfg.SourceCache)
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
		Name:    "cloud",
		Version: "test",
		Images:  append([]string(nil), testImages...),
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
	manifest, err := distribution.Load("cloud@v5.1.0")
	require.NoError(t, err)
	require.NoError(t, installer.Install(context.Background(), manifest))
	require.Contains(t, output.String(), "sealos run ghcr.io/labring/sealos/sealos-certs:v0.1.0")
}

type noopRunner struct{}

func (noopRunner) Run(context.Context, Command) error {
	return nil
}

func (noopRunner) Output(context.Context, Command) ([]byte, error) {
	return nil, nil
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
