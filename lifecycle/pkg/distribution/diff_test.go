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

package distribution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompareReportsChangesInDesiredOrderAndOrphansLast(t *testing.T) {
	manifest := &Manifest{Name: "cloud", Version: "v2"}
	demo := Package{Name: "demo", Version: "v2"}
	newPackage := Package{Name: "new", Version: "v1"}
	resolved := []ResolvedPackage{
		{Package: demo, Image: "ghcr.io/example/demo:v2"},
		{Package: newPackage, Image: "ghcr.io/example/new:v1"},
	}
	installedDemo := NewInstalledPackage(Package{Name: "demo", Version: "v1"}, "ghcr.io/example/demo:v1", ResolveRemote, PackageStatusInstalled)
	installedOld := NewInstalledPackage(Package{Name: "old", Version: "v1"}, "ghcr.io/example/old:v1", ResolveRemote, PackageStatusInstalled)
	state := &State{TargetID: "target", Packages: []InstalledPackage{installedOld, installedDemo}}

	diff, err := Compare(manifest, resolved, state, "target")
	require.NoError(t, err)
	require.Equal(t, []ChangeKind{ChangeChanged, ChangeAdded, ChangeOrphan}, []ChangeKind{
		diff.Changes[0].Kind, diff.Changes[1].Kind, diff.Changes[2].Kind,
	})
	require.Equal(t, "demo@v2", diff.Changes[0].Desired.Package.Ref())
	require.Equal(t, "new@v1", diff.Changes[1].Desired.Package.Ref())
	require.Equal(t, "old@v1", diff.Changes[2].Installed.Ref())
}

func TestCompareReportsUnchangedByPackageFingerprint(t *testing.T) {
	pkg := Package{Name: "demo", Version: "v1", Description: "same"}
	state := &State{TargetID: "target", Packages: []InstalledPackage{NewInstalledPackage(pkg, "ghcr.io/example/demo:v1", ResolveRemote, PackageStatusInstalled)}}
	diff, err := Compare(&Manifest{Name: "cloud", Version: "v1"}, []ResolvedPackage{{Package: pkg, Image: "ghcr.io/example/demo:v1"}}, state, "target")
	require.NoError(t, err)
	require.Len(t, diff.ByKind(ChangeUnchanged), 1)
	require.Empty(t, diff.ByKind(ChangeChanged))
}
