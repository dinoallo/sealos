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
	"strings"
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

func TestLoadCloudManifest(t *testing.T) {
	manifest, err := Load("cloud@v5.1.0")
	require.NoError(t, err)
	require.Equal(t, "cloud", manifest.Name)
	require.Equal(t, "v5.1.0", manifest.Version)
	require.Len(t, manifest.Images, 31)
	require.Equal(t, "ghcr.io/labring/sealos/kubernetes:v1.28.15", manifest.Images[0])
	require.Equal(t, "ghcr.io/labring/sealos-cloud-launchpad-service:v5.1.0", manifest.Images[len(manifest.Images)-1])
	require.NotContains(t, strings.Join(manifest.Images, "\n"), "sealos.hub:5000")
}

func TestLoadCloudProManifest(t *testing.T) {
	manifest, err := Load("cloud-pro@v5.1.2-rc5-fix01")
	require.NoError(t, err)
	require.Equal(t, "cloud-pro", manifest.Name)
	require.Equal(t, "v5.1.2-rc5-fix01", manifest.Version)
	require.Len(t, manifest.Images, 35)
	require.Equal(t, "sealos-pro.hub:19999/labring/sealos-pro:kubernetes-v1.28.15", manifest.Images[0])
	require.Contains(t, manifest.Images, "sealos-pro.hub:19999/labring/sealos-pro:devbox-v1")
	require.Contains(t, manifest.Images, "sealos-pro.hub:19999/labring/sealos-cloud-admission-webhook:sha-568d00f70")
	require.Equal(t, "sealos-pro.hub:19999/labring/sealos-cloud-vlogs-service:sha-ae2f7dc3d", manifest.Images[len(manifest.Images)-1])
}

func TestList(t *testing.T) {
	summaries, err := List()
	require.NoError(t, err)
	require.Equal(t, []Summary{
		{Name: "cloud", Version: "v5.1.0"},
		{Name: "cloud-pro", Version: "v5.1.2-rc5-fix01"},
	}, summaries)
}

func TestShowUsesManifestFieldNames(t *testing.T) {
	data, err := Show("cloud@v5.1.0")
	require.NoError(t, err)
	require.Contains(t, data, "name: cloud")
	require.Contains(t, data, "version: v5.1.0")
	require.Contains(t, data, "images:")
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
