// Copyright © 2026 sealos.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package distribution

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// distributionFile is the on-disk representation of a distribution. Package
// references are encoded as DistributionPackage objects (ref + optional remote).
type distributionFile struct {
	Name        string            `json:"name" yaml:"name"`
	Version     string            `json:"version" yaml:"version"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	PackageRepo string            `json:"packageRepo,omitempty" yaml:"packageRepo,omitempty"`
	Packages    []json.RawMessage `json:"packages" yaml:"packages"`
}

// LoadDistributionFromYAML parses a distribution manifest directly from
// YAML bytes. Package entries use the DistributionPackage format
// (ref: name@version, remote: optional image reference).
func LoadDistributionFromYAML(data []byte) (*Manifest, error) {
	var doc distributionFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("decode distribution manifest: %w", err)
	}
	if len(doc.Packages) == 0 {
		return nil, fmt.Errorf("distribution manifest has no packages")
	}
	packages := make([]DistributionPackage, 0, len(doc.Packages))
	for index, raw := range doc.Packages {
		var dp DistributionPackage
		if err := json.Unmarshal(raw, &dp); err != nil {
			return nil, fmt.Errorf("decode distribution manifest package %d: %w", index, err)
		}
		packages = append(packages, dp)
	}
	manifest := &Manifest{
		Name:        doc.Name,
		Version:     doc.Version,
		Description: doc.Description,
		PackageRepo: doc.PackageRepo,
		Packages:    packages,
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("validate distribution manifest: %w", err)
	}
	return manifest, nil
}

// isManifestFile returns true if the file path has a YAML extension.
func isManifestFile(filePath string) bool {
	ext := filepath.Ext(filePath)
	return ext == ".yaml" || ext == ".yml"
}
