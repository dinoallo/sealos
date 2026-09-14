// Copyright © 2026 sealos.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package distribution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadDistributionFromYAML(t *testing.T) {
	data := []byte(`
name: cloud
version: v1.0.0
packages:
  - ref: example@v1.0.0
    remote:
      image: ghcr.io/example/package:v1.0.0
`)
	manifest, err := LoadDistributionFromYAML(data)
	require.NoError(t, err)
	require.Equal(t, "cloud", manifest.Name)
	require.Equal(t, "v1.0.0", manifest.Version)
	require.Len(t, manifest.Packages, 1)
	require.Equal(t, "ghcr.io/example/package:v1.0.0", manifest.Packages[0].Remote.Reference())
}

func TestLoadDistributionFromYAMLRejectsEmpty(t *testing.T) {
	_, err := LoadDistributionFromYAML([]byte(`name: cloud\nversion: v1.0.0\npackages: []`))
	require.Error(t, err)
}

func TestLoadDistributionFromYAMLRejectsInvalid(t *testing.T) {
	_, err := LoadDistributionFromYAML([]byte(`name: cloud\nversion: v1.0.0`))
	require.Error(t, err)
}

func TestLoadDistributionFromYAMLWithPackageRepo(t *testing.T) {
	data := []byte(`
name: platform
version: v0.1.0
packageRepo: https://github.com/dinoallo/sealos-package-repository.git
packages:
  - ref: kubernetes@v1.28.15
    remote:
      image: ghcr.io/dinoallo/sealos-platform/kubernetes:v1.28.15
`)
	manifest, err := LoadDistributionFromYAML(data)
	require.NoError(t, err)
	require.Equal(t, "platform", manifest.Name)
	require.Len(t, manifest.Packages, 1)
	require.Equal(t, "https://github.com/dinoallo/sealos-package-repository.git", manifest.PackageRepo)
	require.Equal(t, "kubernetes", manifest.Packages[0].Name())
	require.Equal(t, "v1.28.15", manifest.Packages[0].Version())
}
