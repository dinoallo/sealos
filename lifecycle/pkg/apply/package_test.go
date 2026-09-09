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

package apply

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPackageRegistryHostsCommandsDoNotRequireSealctl(t *testing.T) {
	add := packageRegistryHostsAddCommand("192.0.2.10", "sealos.hub")
	remove := packageRegistryHostsDeleteCommand("sealos.hub")

	for _, command := range []string{add, remove} {
		require.NotContains(t, command, "sealctl")
		require.Contains(t, command, "/etc/hosts")
	}
	require.Contains(t, add, "printf '%s %s # %s\\n' '192.0.2.10' 'sealos.hub' \"$marker\"")
	require.Contains(t, remove, "sealos-package-registry:sealos.hub")
	require.NotContains(t, strings.ReplaceAll(remove, "sealos-package-registry:sealos.hub", ""), "192.0.2.10")
}
