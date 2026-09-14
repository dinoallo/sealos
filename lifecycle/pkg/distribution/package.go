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
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/containers/image/v5/docker/reference"
)

var ErrPackageNotFound = errors.New("package definition not found")
var ErrSourceUnavailable = errors.New("no usable package source")

// PackageRef is a minimal name+version reference.
type PackageRef struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
}

func (r PackageRef) Ref() string {
	return r.Name + "@" + r.Version
}

// Remote identifies a container image optionally pinned by content digest.
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

const (
	PackageInitLabel    = "init"
	PackageCleanupLabel = "sealos.io/package-clean"
)

// ValidateCleanupPath validates the path declared by PackageCleanupLabel.
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

// PackageDependency expresses a SemVer-constrained dependency on a package slot.
type PackageDependency struct {
	Slot    string `json:"slot" yaml:"slot"`
	Version string `json:"version" yaml:"version"`
}

// Package is the pure metadata of a package. It contains no remote image or
// source information; those are provided by the distribution manifest.
type Package struct {
	Name        string              `json:"name" yaml:"name"`
	Version     string              `json:"version" yaml:"version"`
	Description string              `json:"description,omitempty" yaml:"description,omitempty"`
	DependsOn   []PackageDependency `json:"dependsOn,omitempty" yaml:"dependsOn,omitempty"`
}

func (p Package) Ref() string {
	return p.Name + "@" + p.Version
}

// DistributionPackage is a package reference inside a distribution manifest.
// The Ref field uses "name@version" format. The optional Remote field pins
// the package to a specific container image; when omitted the package is
// resolved from source.
type DistributionPackage struct {
	Ref    string  `json:"ref" yaml:"ref"`
	Remote *Remote `json:"remote,omitempty" yaml:"remote,omitempty"`
}

func (dp DistributionPackage) Name() string {
	name, _, _ := ParseRef(dp.Ref)
	return name
}

func (dp DistributionPackage) Version() string {
	_, version, _ := ParseRef(dp.Ref)
	return version
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
	SourceRoot string
	// BuildCacheDir stores cached build artifacts (container images) by source hash.
	BuildCacheDir string
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

// DefaultBuildCacheDir returns the default cache directory for build artifacts.
func DefaultBuildCacheDir() string {
	if cacheHome := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); cacheHome != "" {
		return filepath.Join(cacheHome, "sealos", "build")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".cache", "sealos", "build")
	}
	return filepath.Join(os.TempDir(), "sealos", "build")
}

// ResolvePackages resolves a distribution manifest into an ordered list of
// ResolvedPackages. For remote mode, each DistributionPackage must provide a
// Remote image. For source mode, packages are loaded from the packageRepo and
// their build context is prepared. For hybrid mode, remote is preferred and
// source is used as a fallback when no remote is available.
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

	// Resolve all DistributionPackage refs into full Package metadata.
	// This also loads dependsOn from the packageRepo for dependency resolution.
	packages, err := resolveDistributionPackages(manifest, options.Mode)
	if err != nil {
		return nil, err
	}

	graph, err := resolvePackageGraph(packages)
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
		// Find the corresponding DistributionPackage to get the remote image
		dp := findDistributionPackage(manifest.Packages, node.Package.Ref())

		item := ResolvedPackage{
			Package:      node.Package,
			Image:        "",
			Dependencies: append([]string(nil), node.Dependencies...),
		}

		// Determine the image source
		useRemote := false
		useSource := false
		switch options.Mode {
		case ResolveRemote:
			if dp == nil || dp.Remote == nil {
				cleanupBuildPlans()
				return nil, fmt.Errorf("package %s: remote mode requires a remote image in the distribution", node.Package.Ref())
			}
			useRemote = true
		case ResolveSource:
			useSource = true
		case ResolveHybrid:
			if dp != nil && dp.Remote != nil {
				useRemote = true
			} else {
				useSource = true
			}
		}

		if useRemote {
			item.Image = dp.Remote.Reference()
		}

		if useSource {
			// In source or hybrid mode, prepare the build plan
			plan, err := buildPlanFromRepo(manifest.PackageRepo, node.Package, options)
			if err != nil {
				if options.Mode == ResolveHybrid && errors.Is(err, ErrSourceUnavailable) {
					// Hybrid: if source is unavailable but remote was already set, use remote
					if item.Image != "" {
						resolved = append(resolved, item)
						continue
					}
				}
				cleanupBuildPlans()
				return nil, fmt.Errorf("prepare package %s source: %w", node.Package.Ref(), err)
			}
			item.Image = plan.Image
			item.Build = &plan
		}

		resolved = append(resolved, item)
	}
	return resolved, nil
}

// resolveDistributionPackages converts DistributionPackage refs to full Package
// metadata by loading them from the packageRepo. The dependsOn field is loaded
// from the package YAML so that dependency resolution works.
func resolveDistributionPackages(manifest *Manifest, mode ResolveMode) ([]Package, error) {
	packages := make([]Package, 0, len(manifest.Packages))
	for _, dp := range manifest.Packages {
		name, version, err := ParseRef(dp.Ref)
		if err != nil {
			return nil, fmt.Errorf("invalid package ref %q: %w", dp.Ref, err)
		}
		pkg := Package{
			Name:    name,
			Version: version,
		}

		// Load package metadata (dependsOn) from the packageRepo if available
		if manifest.PackageRepo != "" {
			loaded, err := loadPackageFromRepo(manifest.PackageRepo, name, version)
			if err == nil {
				pkg.DependsOn = loaded.DependsOn
				pkg.Description = loaded.Description
			} else if mode != ResolveRemote {
				// In remote mode, dependsOn is optional (deps resolved by the server)
				// In source/hybrid mode, we need the metadata
				return nil, fmt.Errorf("load package %s metadata: %w", dp.Ref, err)
			}
		}
		packages = append(packages, pkg)
	}
	return packages, nil
}

func findDistributionPackage(dps []DistributionPackage, ref string) *DistributionPackage {
	for i := range dps {
		if dps[i].Ref == ref {
			return &dps[i]
		}
	}
	return nil
}

// buildPlanFromRepo constructs a BuildPlan for a package by finding its build
// context inside the packageRepo.
func buildPlanFromRepo(repo string, pkg Package, options ResolveOptions) (BuildPlan, error) {
	if strings.TrimSpace(repo) == "" {
		return BuildPlan{}, fmt.Errorf("%w: package %s has no packageRepo and no remote image", ErrSourceUnavailable, pkg.Ref())
	}

	// Resolve the repository path
	repoRoot, isRemote, err := resolveRepoPath(repo)
	if err != nil {
		return BuildPlan{}, fmt.Errorf("resolve package repo for %s: %w", pkg.Ref(), err)
	}

	// If remote (git URL), clone it
	if isRemote {
		tmpDir, err := os.MkdirTemp(os.TempDir(), "sealos-pkgrepo-*")
		if err != nil {
			return BuildPlan{}, fmt.Errorf("create temp directory for package repo clone: %w", err)
		}
		buildCtx := filepath.Join(tmpDir, "packages", pkg.Name, pkg.Version)
		image := packageImageRef(pkg, "")
		plan := BuildPlan{
			PackageRef: pkg.Ref(),
			Image:      image,
			CleanupDir: tmpDir,
			Commands: []BuildCommand{
				{Name: "git", Args: []string{"clone", "--depth", "1", "--no-checkout", repo, tmpDir}},
				{Name: "git", Args: []string{"-C", tmpDir, "checkout", "--detach", "HEAD"}},
				{Name: "sealos", Args: buildArgs(image, buildCtx, "Kubefile"), Dir: buildCtx},
			},
		}
		return plan, nil
	}

	// Local path
	buildCtx := filepath.Join(repoRoot, "packages", pkg.Name, pkg.Version)
	if _, err := os.Stat(buildCtx); err != nil {
		return BuildPlan{}, fmt.Errorf("%w: package %s build context not found at %s: %v", ErrSourceUnavailable, pkg.Ref(), buildCtx, err)
	}
	kubefile := filepath.Join(buildCtx, "Kubefile")
	if _, err := os.Stat(kubefile); err != nil {
		return BuildPlan{}, fmt.Errorf("%w: package %s Kubefile not found at %s: %v", ErrSourceUnavailable, pkg.Ref(), kubefile, err)
	}

	image := packageImageRef(pkg, "")
	plan := BuildPlan{
		PackageRef: pkg.Ref(),
		Image:      image,
		Commands: []BuildCommand{
			{Name: "sealos", Args: buildArgs(image, buildCtx, "Kubefile"), Dir: buildCtx},
		},
	}
	return plan, nil
}

func buildArgs(image, contextDir, file string) []string {
	args := []string{"build", "-t", image, "-f", filepath.Join(contextDir, file), contextDir}
	return args
}

// packageImageRef generates a local image reference for a package when no
// remote image is provided. The format is repo-local and not meant for
// publication.
func packageImageRef(pkg Package, fallbackImage string) string {
	if fallbackImage != "" {
		return fallbackImage
	}
	return fmt.Sprintf("sealos-pkg/%s:%s", pkg.Name, pkg.Version)
}

type packageGraphNode struct {
	Package      Package
	Dependencies []string
}

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

// orderPackages returns a stable topological ordering.
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
	if len(p.DependsOn) > 0 {
		if _, err := semver.NewVersion(strings.TrimSpace(p.Version)); err != nil {
			return fmt.Errorf("package %s self-version is not SemVer when dependsOn is set: %w", p.Ref(), err)
		}
	}
	return nil
}

func validateRemote(r Remote) error {
	if strings.TrimSpace(r.Image) == "" {
		return errors.New("image reference is required")
	}
	ref, err := reference.ParseNormalizedNamed(r.Image)
	if err != nil {
		return fmt.Errorf("invalid image reference %q: %w", r.Image, err)
	}
	if !reference.IsNameOnly(ref) {
		return nil
	}
	return nil
}
