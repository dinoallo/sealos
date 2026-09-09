// Copyright © 2026 sealos.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package distribution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

const (
	DefaultRepositoryURL = "https://github.com/labring-sigs/sealos-package-repository.git"
	DefaultRepositoryRef = "main"
	RepositoryMaxAge     = 24 * time.Hour
	repositorySyncFile   = ".sealos-sync"
	repositoryRefFile    = ".sealos-ref"
)

var ErrRepositoryNotFound = errors.New("package repository not found")

type RepositoryConfig struct {
	URL      string `json:"url,omitempty" yaml:"url,omitempty"`
	Ref      string `json:"ref,omitempty" yaml:"ref,omitempty"`
	CacheDir string `json:"cacheDir,omitempty" yaml:"cacheDir,omitempty"`
	Refresh  bool   `json:"-" yaml:"-"`
	Offline  bool   `json:"-" yaml:"-"`
}

type repositoryConfigFile struct {
	Repository RepositoryConfig `json:"repository" yaml:"repository"`
}

type Repository struct {
	Root string
	URL  string
	Ref  string
}

// distributionFile is the on-disk representation of a distribution. Package
// definitions are resolved from the package catalog when the file is loaded.
type distributionFile struct {
	Name        string            `json:"name" yaml:"name"`
	Version     string            `json:"version" yaml:"version"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Packages    []json.RawMessage `json:"packages" yaml:"packages"`
}

type RepositoryStatus struct {
	Root      string
	URL       string
	Ref       string
	Commit    string
	LastSync  time.Time
	Stale     bool
	Available bool
}

func DefaultRepositoryConfig() RepositoryConfig {
	return normalizeRepositoryConfig(RepositoryConfig{})
}

func DefaultRepositoryConfigPath() string {
	if configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); configHome != "" {
		return filepath.Join(configHome, "sealos", "config.yaml")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "sealos", "config.yaml")
	}
	return filepath.Join(os.TempDir(), "sealos", "config.yaml")
}

func DefaultRepositoryCacheRoot() string {
	if cacheHome := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); cacheHome != "" {
		return filepath.Join(cacheHome, "sealos", "repositories")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".cache", "sealos", "repositories")
	}
	return filepath.Join(os.TempDir(), "sealos", "repositories")
}

func RepositoryConfigFromFile(path string) (RepositoryConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return RepositoryConfig{}, nil
	}
	if err != nil {
		return RepositoryConfig{}, fmt.Errorf("read repository config %q: %w", path, err)
	}
	config := repositoryConfigFile{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return RepositoryConfig{}, fmt.Errorf("decode repository config %q: %w", path, err)
	}
	return config.Repository, nil
}

func RepositoryConfigFromEnv(lookup func(string) (string, bool)) RepositoryConfig {
	var config RepositoryConfig
	if lookup == nil {
		return config
	}
	get := func(key string) string {
		value, _ := lookup(key)
		return strings.TrimSpace(value)
	}
	config.URL = get("SEALOS_PACKAGE_REPO")
	config.Ref = get("SEALOS_PACKAGE_REPO_REF")
	config.CacheDir = get("SEALOS_PACKAGE_REPO_CACHE")
	return config
}

func LoadRepositoryConfig() (RepositoryConfig, error) {
	fileConfig, err := RepositoryConfigFromFile(DefaultRepositoryConfigPath())
	if err != nil {
		return RepositoryConfig{}, err
	}
	config := mergeRepositoryConfig(RepositoryConfig{}, fileConfig)
	config = mergeRepositoryConfig(config, RepositoryConfigFromEnv(os.LookupEnv))
	return normalizeRepositoryConfig(config), nil
}

func mergeRepositoryConfig(base, override RepositoryConfig) RepositoryConfig {
	if strings.TrimSpace(override.URL) != "" {
		base.URL = override.URL
	}
	if strings.TrimSpace(override.Ref) != "" {
		base.Ref = override.Ref
	}
	if strings.TrimSpace(override.CacheDir) != "" {
		base.CacheDir = override.CacheDir
	}
	base.Refresh = override.Refresh || base.Refresh
	base.Offline = override.Offline || base.Offline
	return base
}

func normalizeRepositoryConfig(config RepositoryConfig) RepositoryConfig {
	config.URL = strings.TrimSpace(config.URL)
	config.Ref = strings.TrimSpace(config.Ref)
	config.CacheDir = strings.TrimSpace(config.CacheDir)
	if config.URL == "" {
		config.URL = DefaultRepositoryURL
	}
	if config.Ref == "" {
		config.Ref = DefaultRepositoryRef
	}
	if config.CacheDir == "" {
		config.CacheDir = RepositoryCachePathForRef(config.URL, config.Ref, DefaultRepositoryCacheRoot())
	}
	return config
}

func RepositoryCachePath(url, cacheRoot string) string {
	return repositoryCachePathKey(url, cacheRoot)
}

func RepositoryCachePathForRef(url, ref, cacheRoot string) string {
	return repositoryCachePathKey(url+"\x00"+ref, cacheRoot)
}

func repositoryCachePathKey(key, cacheRoot string) string {
	if strings.TrimSpace(cacheRoot) == "" {
		cacheRoot = DefaultRepositoryCacheRoot()
	}
	hash := sha256.Sum256([]byte(key))
	return filepath.Join(cacheRoot, hex.EncodeToString(hash[:8]))
}

func OpenRepository(ctx context.Context, config RepositoryConfig) (*Repository, error) {
	config = normalizeRepositoryConfig(config)
	if config.Offline {
		return openCachedRepository(config)
	}

	needsSync, err := repositoryNeedsSync(config)
	if err != nil {
		return nil, err
	}
	if needsSync || config.Refresh {
		if err := syncRepository(ctx, config); err != nil {
			return nil, err
		}
	}
	return openCachedRepository(config)
}

func SyncRepository(ctx context.Context, config RepositoryConfig) (*Repository, error) {
	config = normalizeRepositoryConfig(config)
	config.Refresh = true
	config.Offline = false
	return OpenRepository(ctx, config)
}

func OpenLocalRepository(root string) (*Repository, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root %q: %w", root, err)
	}
	if err := validateRepositoryRoot(root); err != nil {
		return nil, err
	}
	return &Repository{Root: root}, nil
}

func RepositoryStatusFor(config RepositoryConfig) (RepositoryStatus, error) {
	config = normalizeRepositoryConfig(config)
	status := RepositoryStatus{
		Root:  config.CacheDir,
		URL:   config.URL,
		Ref:   config.Ref,
		Stale: true,
	}
	if _, err := os.Stat(config.CacheDir); errors.Is(err, os.ErrNotExist) {
		return status, nil
	} else if err != nil {
		return status, fmt.Errorf("inspect repository cache %q: %w", config.CacheDir, err)
	}
	status.Available = validateRepositoryRoot(config.CacheDir) == nil
	if status.Available {
		status.Commit, _ = gitOutput(context.Background(), config.CacheDir, "rev-parse", "HEAD")
	}
	status.LastSync, _ = readSyncTime(config.CacheDir)
	cachedRef, refErr := readRepositoryRef(config.CacheDir)
	status.Stale = refErr != nil || cachedRef != config.Ref || status.LastSync.IsZero() || time.Since(status.LastSync) >= RepositoryMaxAge
	return status, nil
}

func (r *Repository) Status() (RepositoryStatus, error) {
	if r == nil {
		return RepositoryStatus{}, errors.New("repository is nil")
	}
	status := RepositoryStatus{Root: r.Root, URL: r.URL, Ref: r.Ref, Available: true}
	status.Commit, _ = gitOutput(context.Background(), r.Root, "rev-parse", "HEAD")
	status.LastSync, _ = readSyncTime(r.Root)
	cachedRef, refErr := readRepositoryRef(r.Root)
	status.Stale = r.Ref != "" && (refErr != nil || cachedRef != r.Ref || status.LastSync.IsZero() || time.Since(status.LastSync) >= RepositoryMaxAge)
	return status, nil
}

func (r *Repository) List() ([]Summary, error) {
	if r == nil {
		return nil, errors.New("repository is nil")
	}
	root := filepath.Join(r.Root, "distributions")
	summaries := make([]Summary, 0)
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !isManifestFile(filePath) {
			return nil
		}
		ref, err := repositoryFileRef(root, filePath)
		if err != nil {
			return fmt.Errorf("invalid distribution manifest path %s: %w", filePath, err)
		}
		manifest, err := r.loadManifest(filePath)
		if err != nil {
			return err
		}
		if manifest.Ref() != ref {
			return fmt.Errorf("distribution manifest %s does not match its path %s", filePath, ref)
		}
		summaries = append(summaries, Summary{Name: manifest.Name, Version: manifest.Version})
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrRepositoryNotFound, root)
	}
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

func (r *Repository) Load(ref string) (*Manifest, error) {
	name, version, err := ParseRef(ref)
	if err != nil {
		return nil, err
	}
	filePath := repositoryManifestPath(r.Root, "distributions", name, version)
	manifest, err := r.loadManifest(filePath)
	if err != nil {
		return nil, err
	}
	if manifest.Ref() != ref {
		return nil, fmt.Errorf("distribution manifest %s does not match its path %s", filePath, ref)
	}
	return manifest, nil
}

func (r *Repository) Show(ref string) (string, error) {
	name, version, err := ParseRef(ref)
	if err != nil {
		return "", err
	}
	filePath := repositoryManifestPath(r.Root, "distributions", name, version)
	if _, err := r.Load(ref); err != nil {
		return "", err
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read distribution manifest %s: %w", filePath, err)
	}
	return string(data), nil
}

func (r *Repository) Resolve(ref string) ([]string, error) {
	manifest, err := r.Load(ref)
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

func (r *Repository) LoadPackage(ref string) (*Package, error) {
	name, version, err := ParseRef(ref)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(repositoryManifestPath(r.Root, "packages", name, version))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrPackageNotFound, ref)
	}
	if err != nil {
		return nil, fmt.Errorf("read package %s: %w", ref, err)
	}
	pkg := &Package{}
	if err := yaml.Unmarshal(data, pkg); err != nil {
		return nil, fmt.Errorf("decode package %s: %w", ref, err)
	}
	if pkg.Ref() != ref {
		return nil, fmt.Errorf("package %s does not match its path", ref)
	}
	if err := pkg.Validate(); err != nil {
		return nil, err
	}
	return pkg, nil
}

func (r *Repository) ListPackages() ([]PackageSummary, error) {
	root := filepath.Join(r.Root, "packages")
	summaries := make([]PackageSummary, 0)
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !isManifestFile(filePath) {
			return nil
		}
		ref, err := repositoryFileRef(root, filePath)
		if err != nil {
			return err
		}
		pkg, err := r.LoadPackage(ref)
		if err != nil {
			return err
		}
		summaries = append(summaries, PackageSummary{Name: pkg.Name, Version: pkg.Version})
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrRepositoryNotFound, root)
	}
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

func (r *Repository) loadManifest(filePath string) (*Manifest, error) {
	data, err := os.ReadFile(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, filePath)
	}
	if err != nil {
		return nil, fmt.Errorf("read distribution manifest %s: %w", filePath, err)
	}
	document := distributionFile{}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode distribution manifest %s: %w", filePath, err)
	}
	manifest := &Manifest{
		Name:        document.Name,
		Version:     document.Version,
		Description: document.Description,
		Packages:    make([]Package, 0, len(document.Packages)),
	}
	for index, rawPackage := range document.Packages {
		var ref string
		if err := json.Unmarshal(rawPackage, &ref); err == nil {
			ref = strings.TrimSpace(ref)
			if _, _, err := ParseRef(ref); err != nil {
				return nil, fmt.Errorf("resolve distribution manifest %s package %d: %w", filePath, index, err)
			}
			pkg, err := r.LoadPackage(ref)
			if err != nil {
				return nil, fmt.Errorf("resolve distribution manifest %s package %d %q: %w", filePath, index, ref, err)
			}
			manifest.Packages = append(manifest.Packages, *pkg)
			continue
		}

		// Keep reading older self-contained manifests so cached repositories can
		// be upgraded without rewriting every distribution in place.
		pkg := Package{}
		if err := json.Unmarshal(rawPackage, &pkg); err != nil {
			return nil, fmt.Errorf("decode distribution manifest %s package %d: %w", filePath, index, err)
		}
		manifest.Packages = append(manifest.Packages, pkg)
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("validate distribution manifest %s: %w", filePath, err)
	}
	return manifest, nil
}

func repositoryFileRef(root, filePath string) (string, error) {
	rel, err := filepath.Rel(root, filePath)
	if err != nil {
		return "", err
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	if len(parts) != 2 || parts[0] == "" || !isManifestFile(filePath) {
		return "", errors.New("manifest must be stored as <name>/<version>.yaml")
	}
	version := strings.TrimSuffix(parts[1], filepath.Ext(parts[1]))
	name, version, err := ParseRef(parts[0] + "@" + version)
	if err != nil {
		return "", err
	}
	return name + "@" + version, nil
}

func repositoryManifestPath(root, category, name, version string) string {
	yamlPath := filepath.Join(root, category, name, version+".yaml")
	if _, err := os.Stat(yamlPath); errors.Is(err, os.ErrNotExist) {
		return filepath.Join(root, category, name, version+".yml")
	}
	return yamlPath
}

func validateRepositoryRoot(root string) error {
	info, err := os.Stat(filepath.Join(root, "distributions"))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrRepositoryNotFound, root)
	}
	if err != nil {
		return fmt.Errorf("inspect repository %q: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("repository distributions path is not a directory: %s", root)
	}
	return nil
}

func openCachedRepository(config RepositoryConfig) (*Repository, error) {
	if err := validateRepositoryRoot(config.CacheDir); err != nil {
		return nil, fmt.Errorf("open package repository cache: %w; run `sealos repo sync` or use --offline with a valid cache", err)
	}
	return &Repository{Root: config.CacheDir, URL: config.URL, Ref: config.Ref}, nil
}

func repositoryNeedsSync(config RepositoryConfig) (bool, error) {
	if err := validateRepositoryRoot(config.CacheDir); err != nil {
		if errors.Is(err, ErrRepositoryNotFound) {
			return true, nil
		}
		return false, err
	}
	lastSync, err := readSyncTime(config.CacheDir)
	if err != nil {
		return false, err
	}
	cachedRef, err := readRepositoryRef(config.CacheDir)
	if err != nil {
		return false, err
	}
	return cachedRef != config.Ref || lastSync.IsZero() || time.Since(lastSync) >= RepositoryMaxAge, nil
}

func syncRepository(ctx context.Context, config RepositoryConfig) error {
	if err := os.MkdirAll(filepath.Dir(config.CacheDir), 0o755); err != nil {
		return fmt.Errorf("create repository cache parent: %w", err)
	}
	if _, err := os.Stat(filepath.Join(config.CacheDir, ".git")); errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(config.CacheDir); err == nil {
			entries, readErr := os.ReadDir(config.CacheDir)
			if readErr != nil {
				return fmt.Errorf("inspect repository cache %q: %w", config.CacheDir, readErr)
			}
			if len(entries) > 0 {
				return fmt.Errorf("repository cache %q exists but is not a Git checkout", config.CacheDir)
			}
		}
		if err := runGit(ctx, "", "clone", "--branch", config.Ref, "--depth", "1", config.URL, config.CacheDir); err != nil {
			return fmt.Errorf("clone package repository: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect repository cache %q: %w", config.CacheDir, err)
	} else {
		if err := runGit(ctx, config.CacheDir, "fetch", "--depth", "1", "origin", config.Ref); err != nil {
			return fmt.Errorf("update package repository: %w", err)
		}
		if err := runGit(ctx, config.CacheDir, "checkout", "--detach", "FETCH_HEAD"); err != nil {
			return fmt.Errorf("checkout package repository %s: %w", config.Ref, err)
		}
	}
	if err := validateRepositoryRoot(config.CacheDir); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(config.CacheDir, repositorySyncFile), []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0o600); err != nil {
		return fmt.Errorf("record package repository sync time: %w", err)
	}
	if err := os.WriteFile(filepath.Join(config.CacheDir, repositoryRefFile), []byte(config.Ref), 0o600); err != nil {
		return fmt.Errorf("record package repository ref: %w", err)
	}
	return nil
}

func readSyncTime(root string) (time.Time, error) {
	data, err := os.ReadFile(filepath.Join(root, repositorySyncFile))
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read repository sync time: %w", err)
	}
	value, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}, fmt.Errorf("parse repository sync time: %w", err)
	}
	return value, nil
}

func readRepositoryRef(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, repositoryRefFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read repository ref: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.Output()
	return strings.TrimSpace(string(output)), err
}

func isManifestFile(filePath string) bool {
	ext := filepath.Ext(filePath)
	return ext == ".yaml" || ext == ".yml"
}
