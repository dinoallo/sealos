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
	"errors"
	"fmt"
	"sort"
)

type ChangeKind string

const (
	ChangeAdded     ChangeKind = "added"
	ChangeChanged   ChangeKind = "changed"
	ChangeUnchanged ChangeKind = "unchanged"
	ChangeOrphan    ChangeKind = "orphan"
	ChangeBlocked   ChangeKind = "blocked"
)

type PackageChange struct {
	Kind      ChangeKind
	Desired   *ResolvedPackage
	Installed *InstalledPackage
	Reason    string
}

type Diff struct {
	TargetID            string
	Distribution        string
	ManifestFingerprint string
	Changes             []PackageChange
}

func Compare(manifest *Manifest, resolved []ResolvedPackage, state *State, targetID string) (Diff, error) {
	if manifest == nil {
		return Diff{}, errors.New("distribution manifest is nil")
	}
	if state == nil {
		return Diff{}, ErrPackageStateNotFound
	}
	if targetID != "" && state.TargetID != "" && state.TargetID != targetID {
		return Diff{}, fmt.Errorf("package state target ID %q does not match %q", state.TargetID, targetID)
	}
	installed := make(map[string]*InstalledPackage, len(state.Packages))
	for index := range state.Packages {
		pkg := state.Packages[index]
		installed[pkg.Name] = &pkg
	}
	changes := make([]PackageChange, 0, len(resolved)+len(installed))
	desiredNames := make(map[string]struct{}, len(resolved))
	for index := range resolved {
		item := resolved[index]
		desiredNames[item.Package.Name] = struct{}{}
		installedPackage, ok := installed[item.Package.Name]
		if !ok {
			changes = append(changes, PackageChange{Kind: ChangeAdded, Desired: &item})
			continue
		}
		if installedPackage.Fingerprint == item.Package.Fingerprint() {
			changes = append(changes, PackageChange{Kind: ChangeUnchanged, Desired: &item, Installed: installedPackage})
			continue
		}
		changes = append(changes, PackageChange{Kind: ChangeChanged, Desired: &item, Installed: installedPackage})
	}
	orphanNames := make([]string, 0)
	for name := range installed {
		if _, ok := desiredNames[name]; !ok {
			orphanNames = append(orphanNames, name)
		}
	}
	sort.Strings(orphanNames)
	for _, name := range orphanNames {
		changes = append(changes, PackageChange{Kind: ChangeOrphan, Installed: installed[name]})
	}
	return Diff{
		TargetID:            targetID,
		Distribution:        manifest.Ref(),
		ManifestFingerprint: manifest.Fingerprint(),
		Changes:             changes,
	}, nil
}

func (d Diff) ByKind(kind ChangeKind) []PackageChange {
	changes := make([]PackageChange, 0)
	for _, change := range d.Changes {
		if change.Kind == kind {
			changes = append(changes, change)
		}
	}
	return changes
}
