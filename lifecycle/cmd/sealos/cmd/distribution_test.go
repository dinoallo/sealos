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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDistributionInstallRejectsMissingNonInteractiveValues(t *testing.T) {
	cmd := newDistributionInstallCmd()
	cmd.SetArgs([]string{"--interactive=false"})

	err := cmd.Execute()
	require.ErrorContains(t, err, "masters are required")
}

func TestDistributionInstallDryRun(t *testing.T) {
	repository := t.TempDir()
	manifestDir := filepath.Join(repository, "distributions", "cloud")
	require.NoError(t, os.MkdirAll(manifestDir, 0o755))
	var manifest strings.Builder
	manifest.WriteString("name: cloud\nversion: v5.1.0\npackages:\n")
	for _, image := range testDistributionImages {
		name, version := packageNameAndVersion(image)
		fmt.Fprintf(&manifest, "  - name: %s\n    version: %s\n    remote:\n      image: %s\n", name, version, image)
	}
	require.NoError(t, os.WriteFile(filepath.Join(manifestDir, "v5.1.0.yaml"), []byte(manifest.String()), 0o644))

	cmd := newDistributionInstallCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{
		"cloud@v5.1.0",
		"--interactive=false",
		"--masters", "192.0.2.10:22",
		"--cluster", "prod",
		"--cloud-domain", "192.0.2.10.nip.io",
		"--dry-run",
		"--config-dir", "/tmp/sealos-cli-install-test",
		"--repo-cache", repository,
		"--offline",
	})

	require.NoError(t, cmd.Execute())
	require.Contains(t, output.String(), "Sealos Cloud installation completed")
	require.Contains(t, output.String(), "sealos-finish:v0.1.0")
	require.Contains(t, output.String(), "--cluster prod")
}

func TestDistributionInstallConfigFile(t *testing.T) {
	repository := t.TempDir()
	manifestDir := filepath.Join(repository, "distributions", "cloud")
	require.NoError(t, os.MkdirAll(manifestDir, 0o755))
	var manifest strings.Builder
	manifest.WriteString("name: cloud\nversion: v1.0.0\npackages:\n")
	for _, image := range testDistributionImages {
		name, version := packageNameAndVersion(image)
		fmt.Fprintf(&manifest, "  - name: %s\n    version: %s\n    remote:\n      image: %s\n", name, version, image)
	}
	require.NoError(t, os.WriteFile(filepath.Join(manifestDir, "v1.0.0.yaml"), []byte(manifest.String()), 0o644))

	configPath := filepath.Join(t.TempDir(), "install.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
cluster: configured
masters: 192.0.2.10:22
cloudDomain: config.example.com
dryRun: true
`), 0o600))

	cmd := newDistributionInstallCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{
		"cloud@v1.0.0",
		"--interactive=false",
		"--config", configPath,
		"--cloud-domain", "cli.example.com",
		"--repo-cache", repository,
		"--offline",
	})

	require.NoError(t, cmd.Execute())
	require.Contains(t, output.String(), "--cluster configured")
	require.Contains(t, output.String(), "cloudDomain=cli.example.com")
	require.NotContains(t, output.String(), "cloudDomain=config.example.com")
}

var testDistributionImages = []string{
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

func packageNameAndVersion(image string) (string, string) {
	colon := strings.LastIndex(image, ":")
	slash := strings.LastIndex(image[:colon], "/")
	return image[slash+1 : colon], image[colon+1:]
}
