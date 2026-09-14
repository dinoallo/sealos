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

func TestCloudProRC6SOTWSourceRefsInline(t *testing.T) {
	data := []byte(`
name: cloud
version: v5.1.2-rc6
packages:
  - ref: kubernetes@v1.28.15
    remote:
      image: ghcr.io/sealos-apps/sealos-pro:kubernetes-v1.28.15
`)
	manifest, err := LoadDistributionFromYAML(data)
	require.NoError(t, err)
	require.Equal(t, "cloud", manifest.Name)
	require.Equal(t, "v5.1.2-rc6", manifest.Version)
	require.Len(t, manifest.Packages, 1)
}

func TestResolvePackagesWithInlineManifest(t *testing.T) {
	manifest := &Manifest{
		Name:    "cloud",
		Version: "v5.1.2-rc6",
		Packages: []DistributionPackage{
			{Ref: "kubernetes@v1.28.15", Remote: &Remote{Image: "ghcr.io/sealos-apps/sealos-pro:kubernetes-v1.28.15"}},
			{Ref: "cilium@v1.17.17", Remote: &Remote{Image: "ghcr.io/sealos-apps/sealos-pro:cilium-v1.17.17"}},
		},
	}

	resolved, err := ResolvePackages(manifest, ResolveOptions{Mode: ResolveRemote})
	require.NoError(t, err)
	require.Len(t, resolved, 2)
}

func TestResolvePackagesWithInlineManifestAndDependencies(t *testing.T) {
	manifest := &Manifest{
		Name:    "cloud",
		Version: "test",
		Packages: []DistributionPackage{
			{Ref: "application@v1", Remote: &Remote{Image: "ghcr.io/example/application:v1"}},
			{Ref: "unrelated@v1", Remote: &Remote{Image: "ghcr.io/example/unrelated:v1"}},
			{Ref: "kubernetes@v1", Remote: &Remote{Image: "ghcr.io/example/kubernetes:v1"}},
			{Ref: "containerd@v1", Remote: &Remote{Image: "ghcr.io/example/containerd:v1"}},
		},
	}

	resolved, err := ResolvePackages(manifest, ResolveOptions{Mode: ResolveHybrid})
	require.NoError(t, err)
	require.Len(t, resolved, 4)
}
