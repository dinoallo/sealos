// Copyright © 2026 sealos.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package distribution

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)


// resolveRepoPath determines if repo is a remote Git URL or a local path.
// Returns the effective path (or raw URL for git), a boolean indicating
// whether it's remote, and any error encountered.
func resolveRepoPath(repo string) (string, bool, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return "", false, errors.New("package repository path is empty")
	}
	// Detect Git remote URLs
	if strings.HasPrefix(repo, "http://") || strings.HasPrefix(repo, "https://") ||
		strings.HasPrefix(repo, "git@") || strings.HasPrefix(repo, "ssh://") {
		return repo, true, nil
	}
	// Treat as local filesystem path
	absPath, err := filepath.Abs(repo)
	if err != nil {
		return "", false, fmt.Errorf("resolve local repository path %q: %w", repo, err)
	}
	return absPath, false, nil
}

// loadPackageFromRepo loads a Package's metadata (name, version, dependsOn,
// description) from a package repository. The repository can be a Git URL
// or a local filesystem path.
//
// The package metadata is read from packages/<name>/<version>.yaml relative
// to the repository root.
func loadPackageFromRepo(repo, name, version string) (*Package, error) {
	repoRoot, isRemote, err := resolveRepoPath(repo)
	if err != nil {
		return nil, fmt.Errorf("resolve repository: %w", err)
	}

	if isRemote {
		// For remote repos, we cannot read directly — the caller is
		// expected to clone first. Return a not-found error so the
		// caller can decide whether to clone.
		return nil, fmt.Errorf("%w: cannot load package %s@%s from remote repo without cloning", ErrPackageNotFound, name, version)
	}

	pkgPath := filepath.Join(repoRoot, "packages", name, version+".yaml")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Try without the version extension appended twice
			// (the version in the filename already includes "v1..." etc.)
			altPath := filepath.Join(repoRoot, "packages", name, name+"_"+version+".yaml")
			data, err = os.ReadFile(altPath)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil, fmt.Errorf("%w: package %s@%s not found at %s", ErrPackageNotFound, name, version, pkgPath)
				}
				return nil, fmt.Errorf("read package %s@%s: %w", name, version, err)
			}
		} else {
			return nil, fmt.Errorf("read package %s@%s: %w", name, version, err)
		}
	}

	var pkg Package
	if err := yaml.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("decode package %s@%s: %w", name, version, err)
	}
	return &pkg, nil
}

// FindBuildContext returns the absolute path to a package's build context
// directory (the one containing Kubefile) inside a local repository.
func FindBuildContext(repoRoot, name, version string) (string, error) {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	contextDir := filepath.Join(absRoot, "packages", name, version)
	info, err := os.Stat(contextDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: build context for package %s@%s not found at %s", ErrSourceUnavailable, name, version, contextDir)
		}
		return "", fmt.Errorf("stat build context %s: %w", contextDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: build context path %s is not a directory", ErrSourceUnavailable, contextDir)
	}
	kubefile := filepath.Join(contextDir, "Kubefile")
	if _, err := os.Stat(kubefile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: Kubefile not found in build context %s", ErrSourceUnavailable, contextDir)
		}
		return "", fmt.Errorf("stat Kubefile %s: %w", kubefile, err)
	}
	return contextDir, nil
}
