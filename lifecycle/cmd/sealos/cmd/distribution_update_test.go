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
	"testing"

	"github.com/labring/sealos/pkg/distribution"
	"github.com/stretchr/testify/require"
)

func TestDistributionAdoptAndDiff(t *testing.T) {
	repository := writeUpdateTestRepository(t)
	stateDir := t.TempDir()

	adopt := newDistributionAdoptCmd()
	var adoptOutput bytes.Buffer
	adopt.SetOut(&adoptOutput)
	adopt.SetErr(&adoptOutput)
	adopt.SetArgs([]string{
		"cloud@v1.0.0",
		"--repo-cache", repository,
		"--offline",
		"--state-dir", stateDir,
		"--target-id", "target-a",
	})
	require.NoError(t, adopt.Execute())
	require.Contains(t, adoptOutput.String(), "adopted distribution: cloud@v1.0.0")
	store, err := distribution.NewStateStore(stateDir, "target-a")
	require.NoError(t, err)
	state, err := store.Load()
	require.NoError(t, err)
	require.Equal(t, distribution.PackageStatusAdopted, state.Packages[0].Status)

	diff := newDistributionDiffCmd()
	var diffOutput bytes.Buffer
	diff.SetOut(&diffOutput)
	diff.SetErr(&diffOutput)
	diff.SetArgs([]string{
		"cloud@v2.0.0",
		"--repo-cache", repository,
		"--offline",
		"--state-dir", stateDir,
		"--target-id", "target-a",
	})
	require.NoError(t, diff.Execute())
	require.Contains(t, diffOutput.String(), "= demo@v1.0.0")
	require.Contains(t, diffOutput.String(), "+ extra@v1.0.0")
}

func TestDistributionUpdateDryRunDoesNotWriteTransaction(t *testing.T) {
	repository := writeUpdateTestRepository(t)
	stateDir := t.TempDir()

	adopt := newDistributionAdoptCmd()
	adopt.SetArgs([]string{
		"cloud@v1.0.0",
		"--repo-cache", repository,
		"--offline",
		"--state-dir", stateDir,
		"--target-id", "target-a",
	})
	require.NoError(t, adopt.Execute())

	update := newDistributionUpdateCmd()
	var output bytes.Buffer
	update.SetOut(&output)
	update.SetErr(&output)
	update.SetArgs([]string{
		"cloud@v2.0.0",
		"--interactive=false",
		"--masters", "192.0.2.10",
		"--cloud-domain", "cloud.example.com",
		"--repo-cache", repository,
		"--offline",
		"--state-dir", stateDir,
		"--target-id", "target-a",
		"--dry-run",
	})
	require.NoError(t, update.Execute())
	require.Contains(t, output.String(), "+ extra@v1.0.0")
	entries, err := os.ReadDir(filepath.Join(stateDir, "targets", "target-a", "transactions"))
	require.Error(t, err)
	require.Empty(t, entries)
}

func writeUpdateTestRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	distributions := filepath.Join(root, "distributions", "cloud")
	require.NoError(t, os.MkdirAll(distributions, 0o755))
	base := "name: cloud\nversion: %s\npackages:\n  - name: demo\n    version: v1.0.0\n    remote:\n      image: ghcr.io/example/demo:v1.0.0\n"
	require.NoError(t, os.WriteFile(filepath.Join(distributions, "v1.0.0.yaml"), []byte(fmt.Sprintf(base, "v1.0.0")), 0o644))
	v2 := base + "  - name: extra\n    version: v1.0.0\n    remote:\n      image: ghcr.io/example/extra:v1.0.0\n"
	require.NoError(t, os.WriteFile(filepath.Join(distributions, "v2.0.0.yaml"), []byte(fmt.Sprintf(v2, "v2.0.0")), 0o644))
	return root
}
