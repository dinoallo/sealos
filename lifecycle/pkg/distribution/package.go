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
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/containers/image/v5/docker/reference"
	"github.com/opencontainers/go-digest"
	"sigs.k8s.io/yaml"
)

const packageCatalogPath = dataRoot + "/packages/catalog.yaml"

var ErrPackageNotFound = errors.New("package definition not found")
var ErrSourceUnavailable = errors.New("no usable package source")

type PackageRef struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
}

func (r PackageRef) Ref() string {
	return r.Name + "@" + r.Version
}

type Remote struct {
	Image  string `json:"image" yaml:"image"`
	Digest string `json:"digest,omitempty" yaml:"digest,omitempty"`
}

func (r Remote) Reference() string {
	if r.Digest == "" {
		return r.Image
	}
	return r.Image + "@" + r.Digest
}

type SourceType string

const (
	SourceLocal SourceType = "local"
	SourceGit   SourceType = "git"
)

type Source struct {
	Type SourceType `json:"type" yaml:"type"`
	// Path is the package's local source checkout. Absolute paths are package-
	// specific and do not depend on ResolveOptions.SourceRoot. Relative paths
	// use SourceRoot as a compatibility base for repository-local manifests.
	Path      string            `json:"path,omitempty" yaml:"path,omitempty"`
	URL       string            `json:"url,omitempty" yaml:"url,omitempty"`
	Ref       string            `json:"ref,omitempty" yaml:"ref,omitempty"`
	Context   string            `json:"context,omitempty" yaml:"context,omitempty"`
	File      string            `json:"file" yaml:"file"`
	BuildArgs map[string]string `json:"buildArgs,omitempty" yaml:"buildArgs,omitempty"`
	Platforms []string          `json:"platforms,omitempty" yaml:"platforms,omitempty"`
}

type Package struct {
	Name        string `json:"name" yaml:"name"`
	Version     string `json:"version" yaml:"version"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Remote      Remote `json:"remote" yaml:"remote"`
	// Source is the legacy single-source field. New manifests should use
	// Sources to declare an ordered local-then-git fallback list.
	Source  *Source  `json:"source,omitempty" yaml:"source,omitempty"`
	Sources []Source `json:"sources,omitempty" yaml:"sources,omitempty"`
}

func (p Package) Ref() string {
	return p.Name + "@" + p.Version
}

type packageCatalog struct {
	Packages []Package `json:"packages" yaml:"packages"`
}

type PackageSummary struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
}

func (s PackageSummary) Ref() string {
	return s.Name + "@" + s.Version
}

type ResolveMode string

const (
	ResolveRemote ResolveMode = "remote"
	ResolveSource ResolveMode = "source"
	ResolveHybrid ResolveMode = "hybrid"
)

type ResolveOptions struct {
	Mode ResolveMode
	// SourceRoot is only used as a base for relative local source paths.
	// Packages with absolute source paths are independent of this option.
	SourceRoot string
	// SourceCache stores persistent Git checkouts used by source resolution.
	SourceCache string
	WorkDir     string
}

type BuildCommand struct {
	Name string
	Args []string
	Dir  string
}

type BuildPlan struct {
	PackageRef string
	Image      string
	Commands   []BuildCommand
	CleanupDir string
}

type ResolvedPackage struct {
	Package Package
	Image   string
	Build   *BuildPlan
}

// DefaultSourceCache returns the persistent cache used for Git package sources.
func DefaultSourceCache() string {
	if cacheHome := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); cacheHome != "" {
		return filepath.Join(cacheHome, "sealos", "sources")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".cache", "sealos", "sources")
	}
	return filepath.Join(os.TempDir(), "sealos", "sources")
}

func LoadPackage(ref string) (*Package, error) {
	name, version, err := ParseRef(ref)
	if err != nil {
		return nil, err
	}

	catalog, err := loadCatalog()
	if err != nil {
		return nil, err
	}
	for index := range catalog.Packages {
		pkg := &catalog.Packages[index]
		if pkg.Name == name && pkg.Version == version {
			return pkg, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrPackageNotFound, ref)
}

func ListPackages() ([]PackageSummary, error) {
	catalog, err := loadCatalog()
	if err != nil {
		return nil, err
	}
	summaries := make([]PackageSummary, 0, len(catalog.Packages))
	for _, pkg := range catalog.Packages {
		summaries = append(summaries, PackageSummary{Name: pkg.Name, Version: pkg.Version})
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Name == summaries[j].Name {
			return summaries[i].Version < summaries[j].Version
		}
		return summaries[i].Name < summaries[j].Name
	})
	return summaries, nil
}

func ResolvePackages(manifest *Manifest, options ResolveOptions) ([]ResolvedPackage, error) {
	if manifest == nil {
		return nil, errors.New("distribution manifest is nil")
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if options.Mode == "" {
		options.Mode = ResolveRemote
	}
	if options.Mode != ResolveRemote && options.Mode != ResolveSource && options.Mode != ResolveHybrid {
		return nil, fmt.Errorf("unsupported package resolution mode %q", options.Mode)
	}

	if len(manifest.Packages) == 0 {
		resolved := make([]ResolvedPackage, 0, len(manifest.Images))
		for index, image := range manifest.Images {
			resolved = append(resolved, ResolvedPackage{
				Package: Package{Name: fmt.Sprintf("legacy-%d", index), Version: "legacy", Remote: Remote{Image: image}},
				Image:   image,
			})
		}
		if options.Mode == ResolveSource {
			return nil, errors.New("legacy image-only distributions do not support source resolution")
		}
		return resolved, nil
	}

	resolved := make([]ResolvedPackage, 0, len(manifest.Packages))
	cleanupBuildPlans := func() {
		for _, item := range resolved {
			if item.Build != nil && item.Build.CleanupDir != "" {
				_ = os.RemoveAll(item.Build.CleanupDir)
			}
		}
	}
	for _, ref := range manifest.Packages {
		pkg, err := LoadPackage(ref.Ref())
		if err != nil {
			cleanupBuildPlans()
			return nil, fmt.Errorf("resolve package %s: %w", ref.Ref(), err)
		}
		item := ResolvedPackage{Package: *pkg, Image: pkg.Remote.Reference()}
		if options.Mode == ResolveSource || options.Mode == ResolveHybrid {
			plan, err := pkg.BuildPlanWithOptions(options)
			if err != nil {
				if options.Mode == ResolveHybrid && errors.Is(err, ErrSourceUnavailable) {
					resolved = append(resolved, item)
					continue
				}
				cleanupBuildPlans()
				return nil, fmt.Errorf("prepare package %s source: %w", pkg.Ref(), err)
			}
			item.Image = pkg.Remote.Image
			item.Build = &plan
		}
		resolved = append(resolved, item)
	}
	return resolved, nil
}

func (p Package) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("package name is required")
	}
	if strings.TrimSpace(p.Version) == "" {
		return errors.New("package version is required")
	}
	if err := validateRemote(p.Remote); err != nil {
		return fmt.Errorf("package %s remote: %w", p.Ref(), err)
	}
	if p.Source != nil && len(p.Sources) > 0 {
		return fmt.Errorf("package %s cannot define both source and sources", p.Ref())
	}
	for index, source := range p.sourceCandidates() {
		if err := source.Validate(); err != nil {
			return fmt.Errorf("package %s source %d: %w", p.Ref(), index, err)
		}
	}
	return nil
}

func (p Package) sourceCandidates() []Source {
	if len(p.Sources) > 0 {
		return p.Sources
	}
	if p.Source != nil {
		return []Source{*p.Source}
	}
	return nil
}

func validateRemote(remote Remote) error {
	if strings.TrimSpace(remote.Image) == "" {
		return errors.New("image is required")
	}
	named, err := reference.ParseNormalizedNamed(remote.Image)
	if err != nil {
		return fmt.Errorf("invalid image %q: %w", remote.Image, err)
	}
	if _, ok := named.(reference.NamedTagged); !ok {
		return errors.New("image must include a tag")
	}
	if remote.Digest != "" {
		parsed, err := digest.Parse(remote.Digest)
		if err != nil {
			return fmt.Errorf("invalid digest %q: %w", remote.Digest, err)
		}
		if parsed.Algorithm() == "" || parsed.Encoded() == "" {
			return fmt.Errorf("invalid digest %q", remote.Digest)
		}
	}
	return nil
}

func (s Source) Validate() error {
	if s.Type != SourceLocal && s.Type != SourceGit {
		return fmt.Errorf("type must be %q or %q", SourceLocal, SourceGit)
	}
	if strings.TrimSpace(s.File) == "" {
		return errors.New("file is required")
	}
	if filepath.IsAbs(s.File) || filepath.Clean(s.File) == ".." || strings.HasPrefix(filepath.Clean(s.File), ".."+string(filepath.Separator)) {
		return errors.New("file must be a relative path inside the source context")
	}
	if s.Context != "" && (filepath.IsAbs(s.Context) || filepath.Clean(s.Context) == ".." || strings.HasPrefix(filepath.Clean(s.Context), ".."+string(filepath.Separator))) {
		return errors.New("context must be a relative path")
	}
	for key := range s.BuildArgs {
		if strings.TrimSpace(key) == "" {
			return errors.New("build args cannot contain an empty key")
		}
	}
	for _, platform := range s.Platforms {
		if strings.TrimSpace(platform) == "" {
			return errors.New("platforms cannot contain an empty value")
		}
	}
	switch s.Type {
	case SourceLocal:
		if strings.TrimSpace(s.Path) == "" {
			return errors.New("path is required for local source")
		}
		if s.URL != "" || s.Ref != "" {
			return errors.New("url and ref are not valid for local source")
		}
	case SourceGit:
		if strings.TrimSpace(s.URL) == "" {
			return errors.New("url is required for git source")
		}
		if strings.TrimSpace(s.Ref) == "" {
			return errors.New("ref is required for git source")
		}
	}
	return nil
}

func (p Package) BuildPlan(sourceRoot, workDir string) (BuildPlan, error) {
	return p.BuildPlanWithOptions(ResolveOptions{
		Mode:       ResolveSource,
		SourceRoot: sourceRoot,
		WorkDir:    workDir,
	})
}

// BuildPlanWithOptions selects the first usable source and prepares its build
// commands. Git sources are checked out into a persistent cache.
func (p Package) BuildPlanWithOptions(options ResolveOptions) (BuildPlan, error) {
	if err := p.Validate(); err != nil {
		return BuildPlan{}, err
	}
	if strings.TrimSpace(options.SourceRoot) == "" {
		options.SourceRoot = "."
	}

	candidates := p.sourceCandidates()
	if len(candidates) == 0 {
		return BuildPlan{}, fmt.Errorf("%w: package %s has no source candidates", ErrSourceUnavailable, p.Ref())
	}

	localErrors := make([]string, 0, len(candidates))
	for _, source := range candidates {
		switch source.Type {
		case SourceLocal:
			root := source.Path
			if !filepath.IsAbs(root) {
				root = filepath.Join(options.SourceRoot, root)
			}
			if err := validateLocalSource(root, source); err != nil {
				localErrors = append(localErrors, err.Error())
				continue
			}
			return p.buildPlanForSource(source, root), nil
		case SourceGit:
			cacheRoot := options.SourceCache
			if strings.TrimSpace(cacheRoot) == "" {
				cacheRoot = DefaultSourceCache()
			}
			cacheDir := packageSourceCachePath(p, source, cacheRoot)
			ready, err := sourceCacheReady(cacheDir)
			if err != nil {
				return BuildPlan{}, err
			}
			plan := p.buildPlanForSource(source, cacheDir)
			if !ready {
				plan.Commands = append([]BuildCommand{
					{Name: "mkdir", Args: []string{"-p", cacheRoot}},
					{Name: "git", Args: []string{"clone", "--no-checkout", source.URL, cacheDir}},
					{Name: "git", Args: []string{"-C", cacheDir, "checkout", "--detach", source.Ref}},
				}, plan.Commands...)
			}
			return plan, nil
		}
	}

	return BuildPlan{}, fmt.Errorf("%w: package %s: %s", ErrSourceUnavailable, p.Ref(), strings.Join(localErrors, "; "))
}

func validateLocalSource(root string, source Source) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("local source %q is unavailable: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("local source %q is not a directory", root)
	}
	contextDir := filepath.Join(root, sourceContext(source))
	contextInfo, err := os.Stat(contextDir)
	if err != nil {
		return fmt.Errorf("local source context %q is unavailable: %w", contextDir, err)
	}
	if !contextInfo.IsDir() {
		return fmt.Errorf("local source context %q is not a directory", contextDir)
	}
	file := filepath.Join(contextDir, source.File)
	fileInfo, err := os.Stat(file)
	if err != nil {
		return fmt.Errorf("local source file %q is unavailable: %w", file, err)
	}
	if fileInfo.IsDir() {
		return fmt.Errorf("local source file %q is a directory", file)
	}
	return nil
}

func sourceContext(source Source) string {
	if source.Context == "" {
		return "."
	}
	return source.Context
}

func packageSourceCachePath(p Package, source Source, cacheRoot string) string {
	key := sha256.Sum256([]byte(p.Ref() + "\x00" + source.URL + "\x00" + source.Ref))
	return filepath.Join(cacheRoot, fmt.Sprintf("%x", key[:16]))
}

func sourceCacheReady(cacheDir string) (bool, error) {
	info, err := os.Stat(cacheDir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect package source cache %q: %w", cacheDir, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("package source cache %q is not a directory", cacheDir)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, ".git")); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("inspect package source cache %q: %w", cacheDir, err)
	}
	return false, fmt.Errorf("package source cache %q is incomplete; remove it and retry", cacheDir)
}

func (p Package) buildPlanForSource(source Source, sourceRoot string) BuildPlan {
	contextDir := filepath.Join(sourceRoot, sourceContext(source))
	buildArgs := []string{"build", "-t", p.Remote.Image}
	keys := make([]string, 0, len(source.BuildArgs))
	for key := range source.BuildArgs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		buildArgs = append(buildArgs, "--build-arg", key+"="+source.BuildArgs[key])
	}
	for _, platform := range source.Platforms {
		buildArgs = append(buildArgs, "--platform", platform)
	}
	buildArgs = append(buildArgs, "-f", filepath.Join(contextDir, source.File), contextDir)
	return BuildPlan{
		PackageRef: p.Ref(),
		Image:      p.Remote.Image,
		Commands:   []BuildCommand{{Name: "sealos", Args: buildArgs, Dir: contextDir}},
	}
}

func loadCatalog() (*packageCatalog, error) {
	data, err := manifestFS.ReadFile(packageCatalogPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrPackageNotFound, packageCatalogPath)
	}
	catalog := &packageCatalog{}
	if err := yaml.Unmarshal(data, catalog); err != nil {
		return nil, fmt.Errorf("decode package catalog: %w", err)
	}
	seen := make(map[string]struct{}, len(catalog.Packages))
	for _, pkg := range catalog.Packages {
		if err := pkg.Validate(); err != nil {
			return nil, fmt.Errorf("validate package %s: %w", pkg.Ref(), err)
		}
		if _, ok := seen[pkg.Ref()]; ok {
			return nil, fmt.Errorf("duplicate package %s", pkg.Ref())
		}
		seen[pkg.Ref()] = struct{}{}
	}
	return catalog, nil
}
