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
	"errors"
	"fmt"
	"strings"
)

var ErrNotFound = errors.New("distribution manifest not found")

// Manifest is a distribution manifest that selects packages from a package
// repository and optionally pins them to remote container images.
type Manifest struct {
	Name        string                `json:"name" yaml:"name"`
	Version     string                `json:"version" yaml:"version"`
	Description string                `json:"description,omitempty" yaml:"description,omitempty"`
	// PackageRepo is a Git URL or local filesystem path to a package
	// repository. It is optional: when omitted, source/hybrid resolution
	// modes fail and only remote resolution is available. When set, the
	// repository is used to look up package metadata (dependsOn) and
	// build context (Kubefile) for source-mode builds.
	PackageRepo string                `json:"packageRepo,omitempty" yaml:"packageRepo,omitempty"`
	Packages    []DistributionPackage `json:"packages" yaml:"packages"`
}

// Summary is a compact distribution reference for listing purposes.
type Summary struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
}

func (s Summary) Ref() string {
	return s.Name + "@" + s.Version
}

func (m Manifest) Ref() string {
	return m.Name + "@" + m.Version
}

func ParseRef(ref string) (string, string, error) {
	parts := strings.Split(ref, "@")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("reference must be in the form name@version")
	}
	name := strings.TrimSpace(parts[0])
	version := strings.TrimSpace(parts[1])
	if !validRefComponent(name) || !validRefComponent(version) {
		return "", "", fmt.Errorf("reference must be in the form name@version")
	}
	return name, version, nil
}

func validRefComponent(value string) bool {
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, `/\\`) {
		return false
	}
	return !strings.ContainsRune(value, '\x00')
}

func (m Manifest) Validate() error {
	if m.Name == "" {
		return errors.New("distribution name is required")
	}
	if m.Version == "" {
		return errors.New("distribution version is required")
	}
	if len(m.Packages) == 0 {
		return errors.New("distribution packages are required")
	}
	seen := make(map[string]struct{}, len(m.Packages))
	for i, dp := range m.Packages {
		name, version, err := ParseRef(dp.Ref)
		if err != nil {
			return fmt.Errorf("distribution package %d: invalid ref %q: %w", i, dp.Ref, err)
		}
		if _, ok := seen[dp.Ref]; ok {
			return fmt.Errorf("duplicate distribution package %q", dp.Ref)
		}
		seen[dp.Ref] = struct{}{}
		// Validate remote image if present
		if dp.Remote != nil {
			if err := validateRemote(*dp.Remote); err != nil {
				return fmt.Errorf("distribution package %s remote: %w", dp.Ref, err)
			}
		}
		_ = name
		_ = version
	}
	return nil
}
