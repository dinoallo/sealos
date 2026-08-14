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
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/containers/image/v5/docker/reference"
	"sigs.k8s.io/yaml"
)

const dataRoot = "data"

var (
	//go:embed data/**
	manifestFS embed.FS
)

var ErrNotFound = errors.New("distribution manifest not found")

type Manifest struct {
	Name        string       `json:"name" yaml:"name"`
	Version     string       `json:"version" yaml:"version"`
	Description string       `json:"description,omitempty" yaml:"description,omitempty"`
	Packages    []PackageRef `json:"packages,omitempty" yaml:"packages,omitempty"`
	Images      []string     `json:"images,omitempty" yaml:"images,omitempty"`
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
	if name == "" || version == "" {
		return "", "", fmt.Errorf("reference must be in the form name@version")
	}
	return name, version, nil
}

func List() ([]Summary, error) {
	summaries := make([]Summary, 0)
	err := fs.WalkDir(manifestFS, dataRoot, func(filePath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(filePath, dataRoot+"/packages/") {
			return nil
		}
		if !isManifestFile(filePath) {
			return nil
		}
		manifest, err := loadFromPath(filePath)
		if err != nil {
			return err
		}
		summaries = append(summaries, Summary{
			Name:    manifest.Name,
			Version: manifest.Version,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Name == summaries[j].Name {
			return summaries[i].Version < summaries[j].Version
		}
		return summaries[i].Name < summaries[j].Name
	})
	return summaries, nil
}

func Load(ref string) (*Manifest, error) {
	name, version, err := ParseRef(ref)
	if err != nil {
		return nil, err
	}
	return load(name, version)
}

func Show(ref string) (string, error) {
	manifest, err := Load(ref)
	if err != nil {
		return "", err
	}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func Resolve(ref string) ([]string, error) {
	manifest, err := Load(ref)
	if err != nil {
		return nil, err
	}
	resolved, err := ResolvePackages(manifest, ResolveOptions{Mode: ResolveRemote})
	if err != nil {
		return nil, err
	}
	images := make([]string, 0, len(resolved))
	for _, item := range resolved {
		images = append(images, item.Image)
	}
	return images, nil
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
		for i, ref := range m.Packages {
			if strings.TrimSpace(ref.Name) == "" || strings.TrimSpace(ref.Version) == "" {
				return fmt.Errorf("distribution package %d must include name and version", i)
			}
			if _, ok := seen[ref.Ref()]; ok {
				return fmt.Errorf("duplicate distribution package %q", ref.Ref())
			}
			seen[ref.Ref()] = struct{}{}
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

func load(name, version string) (*Manifest, error) {
	return loadFromPath(manifestPath(name, version))
}

func loadFromPath(filePath string) (*Manifest, error) {
	name, version, err := parsePath(filePath)
	if err != nil {
		return nil, err
	}

	data, err := manifestFS.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, filePath)
	}

	manifest := &Manifest{}
	if err := yaml.Unmarshal(data, manifest); err != nil {
		return nil, fmt.Errorf("decode distribution manifest %s: %w", filePath, err)
	}
	if manifest.Name != name || manifest.Version != version {
		return nil, fmt.Errorf("distribution manifest %s does not match its path %s@%s", filePath, name, version)
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("validate distribution manifest %s: %w", filePath, err)
	}
	return manifest, nil
}

func manifestPath(name, version string) string {
	return path.Join(dataRoot, name, version+".yaml")
}

func parsePath(filePath string) (string, string, error) {
	rel, ok := strings.CutPrefix(filePath, dataRoot+"/")
	if !ok {
		return "", "", fmt.Errorf("distribution manifest path must live under %s: %s", dataRoot, filePath)
	}
	dir, file := path.Split(rel)
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" || file == "" {
		return "", "", fmt.Errorf("invalid distribution manifest path: %s", filePath)
	}
	version := strings.TrimSuffix(file, path.Ext(file))
	if version == "" {
		return "", "", fmt.Errorf("invalid distribution manifest filename: %s", filePath)
	}
	return dir, version, nil
}

func isManifestFile(filePath string) bool {
	ext := path.Ext(filePath)
	return ext == ".yaml" || ext == ".yml"
}
