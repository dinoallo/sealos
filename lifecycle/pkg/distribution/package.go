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

	"github.com/Masterminds/semver/v3"
	"github.com/containers/image/v5/docker/reference"
	"github.com/opencontainers/go-digest"
)

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

const (
	PackageInitLabel    = "init"
	PackageCleanupLabel = "sealos.io/package-clean"
)

// ValidateCleanupPath validates the path declared by PackageCleanupLabel.
// Cleanup hooks are copied out of an image before it is removed from local
// storage, so the path must identify a file inside the image rootfs.
func ValidateCleanupPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("package cleanup path is empty")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("package cleanup path %q must be absolute", path)
	}
	clean := filepath.Clean(path)
	if clean != path || clean == string(filepath.Separator) || strings.Contains(clean, string(filepath.Separator)+".."+string(filepath.Separator)) || strings.HasSuffix(clean, string(filepath.Separator)+"..") {
		return fmt.Errorf("package cleanup path %q is invalid", path)
	}
	return nil
}

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

type PackageDependency struct {
	Slot    string `json:"slot" yaml:"slot"`
	Version string `json:"version" yaml:"version"`
}

type Package struct {
	Name        string `json:"name" yaml:"name"`
	Version     string `json:"version" yaml:"version"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Remote      Remote `json:"remote" yaml:"remote"`
	// DependsOn identifies package slots and the SemVer constraints accepted
	// from the packages selected by the current distribution.
	DependsOn []PackageDependency `json:"dependsOn,omitempty" yaml:"dependsOn,omitempty"`
	// Source is the legacy single-source field. New manifests should use
	// Sources to declare an ordered local-then-git fallback list.
	Source  *Source  `json:"source,omitempty" yaml:"source,omitempty"`
	Sources []Source `json:"sources,omitempty" yaml:"sources,omitempty"`
}

func (p Package) Ref() string {
	return p.Name + "@" + p.Version
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
	Package      Package
	Image        string
	Dependencies []string
	Build        *BuildPlan
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

	graph, err := resolvePackageGraph(manifest.Packages)
	if err != nil {
		return nil, err
	}
	resolved := make([]ResolvedPackage, 0, len(graph))
	cleanupBuildPlans := func() {
		for _, item := range resolved {
			if item.Build != nil && item.Build.CleanupDir != "" {
				_ = os.RemoveAll(item.Build.CleanupDir)
			}
		}
	}
	for _, node := range graph {
		item := ResolvedPackage{
			Package:      node.Package,
			Image:        node.Package.Remote.Reference(),
			Dependencies: append([]string(nil), node.Dependencies...),
		}
		if options.Mode == ResolveSource || options.Mode == ResolveHybrid {
			plan, err := node.Package.BuildPlanWithOptions(options)
			if err != nil {
				if options.Mode == ResolveHybrid && errors.Is(err, ErrSourceUnavailable) {
					resolved = append(resolved, item)
					continue
				}
				cleanupBuildPlans()
				return nil, fmt.Errorf("prepare package %s source: %w", node.Package.Ref(), err)
			}
			item.Image = node.Package.Remote.Image
			item.Build = &plan
		}
		resolved = append(resolved, item)
	}
	return resolved, nil
}

type packageGraphNode struct {
	Package      Package
	Dependencies []string
}

// resolvePackageGraph resolves slot-based SemVer dependencies against the
// packages selected by one distribution and returns a stable topological
// ordering. There is deliberately no catalog lookup or implicit version
// selection here: every dependency must match exactly one selected package.
func resolvePackageGraph(packages []Package) ([]packageGraphNode, error) {
	byRef := make(map[string]int, len(packages))
	bySlot := make(map[string][]int, len(packages))
	versions := make([]*semver.Version, len(packages))
	for index, pkg := range packages {
		if _, ok := byRef[pkg.Ref()]; ok {
			return nil, fmt.Errorf("duplicate package %q", pkg.Ref())
		}
		byRef[pkg.Ref()] = index
		bySlot[pkg.Name] = append(bySlot[pkg.Name], index)
		version, err := semver.NewVersion(strings.TrimSpace(pkg.Version))
		if err != nil {
			return nil, fmt.Errorf("package %s has invalid SemVer version %q: %w", pkg.Ref(), pkg.Version, err)
		}
		versions[index] = version
	}

	indegree := make([]int, len(packages))
	dependents := make([][]int, len(packages))
	dependencies := make([][]string, len(packages))
	for index, pkg := range packages {
		seenSlots := make(map[string]struct{}, len(pkg.DependsOn))
		for _, dependency := range pkg.DependsOn {
			slot := strings.TrimSpace(dependency.Slot)
			constraintText := strings.TrimSpace(dependency.Version)
			if slot == "" {
				return nil, fmt.Errorf("package %s has an empty dependency slot", pkg.Ref())
			}
			if constraintText == "" {
				return nil, fmt.Errorf("package %s has an empty version constraint for dependency slot %q", pkg.Ref(), slot)
			}
			if _, ok := seenSlots[slot]; ok {
				return nil, fmt.Errorf("package %s contains duplicate dependency slot %q", pkg.Ref(), slot)
			}
			seenSlots[slot] = struct{}{}
			if slot == pkg.Name {
				return nil, fmt.Errorf("package %s cannot depend on itself", pkg.Ref())
			}

			constraint, err := semver.NewConstraint(constraintText)
			if err != nil {
				return nil, fmt.Errorf("package %s has invalid version constraint %q for dependency slot %q: %w", pkg.Ref(), constraintText, slot, err)
			}
			candidates := bySlot[slot]
			matches := make([]int, 0, len(candidates))
			for _, candidate := range candidates {
				if constraint.Check(versions[candidate]) {
					matches = append(matches, candidate)
				}
			}
			switch len(matches) {
			case 0:
				if len(candidates) == 0 {
					return nil, fmt.Errorf("package %s depends on missing package slot %q", pkg.Ref(), slot)
				}
				return nil, fmt.Errorf("package %s has no selected package in slot %q matching version constraint %q", pkg.Ref(), slot, constraintText)
			case 1:
				dependencyIndex := matches[0]
				dependencyRef := packages[dependencyIndex].Ref()
				dependencies[index] = append(dependencies[index], dependencyRef)
				indegree[index]++
				dependents[dependencyIndex] = append(dependents[dependencyIndex], index)
			default:
				refs := make([]string, 0, len(matches))
				for _, match := range matches {
					refs = append(refs, packages[match].Ref())
				}
				return nil, fmt.Errorf("package %s dependency slot %q with constraint %q matches multiple selected packages: %s", pkg.Ref(), slot, constraintText, strings.Join(refs, ", "))
			}
		}
	}

	ready := make([]int, 0, len(packages))
	for index, degree := range indegree {
		if degree == 0 {
			ready = append(ready, index)
		}
	}
	ordered := make([]packageGraphNode, 0, len(packages))
	for len(ready) > 0 {
		index := ready[0]
		ready = ready[1:]
		ordered = append(ordered, packageGraphNode{
			Package:      packages[index],
			Dependencies: dependencies[index],
		})
		for _, dependent := range dependents[index] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				insertStableIndex(&ready, dependent)
			}
		}
	}
	if len(ordered) != len(packages) {
		cycle := make([]string, 0)
		for index, degree := range indegree {
			if degree > 0 {
				cycle = append(cycle, packages[index].Ref())
			}
		}
		return nil, fmt.Errorf("package dependency cycle detected: %s", strings.Join(cycle, ", "))
	}
	return ordered, nil
}

// orderPackages returns a stable topological ordering. Packages that are not
// connected by dependencies retain their manifest order.
func orderPackages(packages []Package) ([]Package, error) {
	graph, err := resolvePackageGraph(packages)
	if err != nil {
		return nil, err
	}
	ordered := make([]Package, 0, len(graph))
	for _, node := range graph {
		ordered = append(ordered, node.Package)
	}
	return ordered, nil
}

func insertStableIndex(values *[]int, value int) {
	items := *values
	position := sort.SearchInts(items, value)
	items = append(items, 0)
	copy(items[position+1:], items[position:])
	items[position] = value
	*values = items
}

func (p Package) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("package name is required")
	}
	if strings.TrimSpace(p.Version) == "" {
		return errors.New("package version is required")
	}
	if _, err := semver.NewVersion(strings.TrimSpace(p.Version)); err != nil {
		return fmt.Errorf("package %s has invalid SemVer version %q: %w", p.Ref(), p.Version, err)
	}
	if err := validateRemote(p.Remote); err != nil {
		return fmt.Errorf("package %s remote: %w", p.Ref(), err)
	}
	seenDependencySlots := make(map[string]struct{}, len(p.DependsOn))
	for _, dependency := range p.DependsOn {
		slot := strings.TrimSpace(dependency.Slot)
		if slot == "" {
			return fmt.Errorf("package %s has an empty dependency slot", p.Ref())
		}
		if !validRefComponent(slot) || strings.ContainsRune(slot, '@') {
			return fmt.Errorf("package %s has invalid dependency slot %q", p.Ref(), dependency.Slot)
		}
		if _, ok := seenDependencySlots[slot]; ok {
			return fmt.Errorf("package %s contains duplicate dependency slot %q", p.Ref(), slot)
		}
		seenDependencySlots[slot] = struct{}{}
		if slot == strings.TrimSpace(p.Name) {
			return fmt.Errorf("package %s cannot depend on itself", p.Ref())
		}
		constraint := strings.TrimSpace(dependency.Version)
		if constraint == "" {
			return fmt.Errorf("package %s has an empty version constraint for dependency slot %q", p.Ref(), slot)
		}
		if _, err := semver.NewConstraint(constraint); err != nil {
			return fmt.Errorf("package %s has invalid version constraint %q for dependency slot %q: %w", p.Ref(), constraint, slot, err)
		}
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
