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
	cmd := newDistributionInstallCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{
		"cloud@v5.1.0",
		"--interactive=false",
		"--masters", "192.0.2.10:22",
		"--cloud-domain", "192.0.2.10.nip.io",
		"--dry-run",
		"--config-dir", "/tmp/sealos-cli-install-test",
	})

	require.NoError(t, cmd.Execute())
	require.Contains(t, output.String(), "Sealos Cloud installation completed")
	require.Contains(t, output.String(), "sealos-finish:v0.1.0")
}
