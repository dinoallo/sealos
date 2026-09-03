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

	"github.com/containers/image/v5/docker/reference"
)

var ErrNotFound = errors.New("distribution manifest not found")

type Manifest struct {
	Name        string    `json:"name" yaml:"name"`
	Version     string    `json:"version" yaml:"version"`
	Description string    `json:"description,omitempty" yaml:"description,omitempty"`
	Packages    []Package `json:"packages,omitempty" yaml:"packages,omitempty"`
	Images      []string  `json:"images,omitempty" yaml:"images,omitempty"`
}

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
	if len(m.Packages) == 0 && len(m.Images) == 0 {
		return errors.New("distribution packages are required")
	}
	if len(m.Packages) > 0 && len(m.Images) > 0 {
		return errors.New("distribution cannot define both packages and images")
	}
	if len(m.Packages) > 0 {
		seen := make(map[string]struct{}, len(m.Packages))
		for i, pkg := range m.Packages {
			if err := pkg.Validate(); err != nil {
				return fmt.Errorf("distribution package %d: %w", i, err)
			}
			if _, ok := seen[pkg.Ref()]; ok {
				return fmt.Errorf("duplicate distribution package %q", pkg.Ref())
			}
			seen[pkg.Ref()] = struct{}{}
		}
		return nil
	}

	seen := make(map[string]struct{}, len(m.Images))
	for i, image := range m.Images {
		image = strings.TrimSpace(image)
		if image == "" {
			return fmt.Errorf("distribution image %d is empty", i)
		}
		if _, ok := seen[image]; ok {
			return fmt.Errorf("duplicate distribution image %q", image)
		}
		seen[image] = struct{}{}

		named, err := reference.ParseNormalizedNamed(image)
		if err != nil {
			return fmt.Errorf("invalid distribution image %q: %w", image, err)
		}
		if _, ok := named.(reference.NamedTagged); ok {
			continue
		}
		if _, ok := named.(reference.Digested); ok {
			continue
		}
		return fmt.Errorf("distribution image %q must include a tag or digest", image)
	}

	return nil
}
