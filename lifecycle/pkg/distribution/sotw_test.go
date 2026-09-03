// Copyright © 2026 sealos.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package distribution

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCloudProRC6SOTWMatchesBundleImageList(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", ".."))
	repo, err := OpenLocalRepository(filepath.Join(repoRoot, "examples", "package-repository"))
	require.NoError(t, err)

	images, err := repo.Resolve("cloud-pro@v5.1.2-rc6")
	require.NoError(t, err)
	require.Equal(t, []string{
		"ghcr.io/sealos-apps/sealos-pro:cilium-v1.16.9",
		"ghcr.io/sealos-apps/sealos-pro:cilium-v1.17.17",
		"ghcr.io/sealos-apps/sealos-pro:cilium-v1.13.18",
		"ghcr.io/sealos-apps/sealos-pro:kubernetes-v1.28.15",
		"ghcr.io/sealos-apps/sealos-pro:cert-manager-v1.19.1",
		"ghcr.io/sealos-apps/sealos-pro:openebs-v3.10.0",
		"ghcr.io/sealos-apps/sealos-pro:higress-v2.1.3",
		"ghcr.io/sealos-apps/sealos-pro:victoria-metrics-k8s-stack-v1.124.0",
		"ghcr.io/sealos-apps/sealos-pro:kubeblocks-v0.8.2",
		"ghcr.io/sealos-apps/sealos-pro:sealos-oss-v1",
		"ghcr.io/sealos-apps/sealos-pro:sealos-minio-v0.1.0",
		"ghcr.io/sealos-apps/sealos-pro:vlogs-v1.31.0",
		"ghcr.io/sealos-apps/sealos-pro:sealos-offline-v0.1.0",
		"ghcr.io/sealos-apps/sealos-pro:dnsmasq-v1",
		"ghcr.io/labring/sealos-cloud-user-controller:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-terminal-controller:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-app-controller:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-resources-controller:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-account-controller:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-license-controller:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-job-init-controller:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-job-heartbeat-controller:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-desktop-frontend:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-terminal-frontend:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-applaunchpad-frontend:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-dbprovider-frontend:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-costcenter-frontend:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-template-frontend:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-license-frontend:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-database-service:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-account-service:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-launchpad-service:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-vlogs-service:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-admission-webhook:v5.1.2-rc6",
		"ghcr.io/labring/sealos-cloud-node-controller:v5.1.2-rc6",
	}, images)
}

func TestCloudProRC6SOTWSourceRefs(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", ".."))
	repo, err := OpenLocalRepository(filepath.Join(repoRoot, "examples", "package-repository"))
	require.NoError(t, err)

	manifest, err := repo.Load("cloud-pro@v5.1.2-rc6")
	require.NoError(t, err)

	expectedContexts := map[string]string{
		"sealos-cloud-user-controller":          "controllers/user/deploy",
		"sealos-cloud-terminal-controller":      "controllers/terminal/deploy",
		"sealos-cloud-app-controller":           "controllers/app/deploy",
		"sealos-cloud-resources-controller":     "controllers/resources/deploy",
		"sealos-cloud-account-controller":       "controllers/account/deploy",
		"sealos-cloud-license-controller":       "controllers/license/deploy",
		"sealos-cloud-job-init-controller":      "controllers/job/init/deploy",
		"sealos-cloud-job-heartbeat-controller": "controllers/job/heartbeat/deploy",
		"sealos-cloud-desktop-frontend":         "frontend/desktop/deploy",
		"sealos-cloud-terminal-frontend":        "frontend/providers/terminal/deploy",
		"sealos-cloud-applaunchpad-frontend":    "frontend/providers/applaunchpad/deploy",
		"sealos-cloud-dbprovider-frontend":      "frontend/providers/dbprovider/deploy",
		"sealos-cloud-costcenter-frontend":      "frontend/providers/costcenter/deploy",
		"sealos-cloud-template-frontend":        "frontend/providers/template/deploy",
		"sealos-cloud-license-frontend":         "frontend/providers/license/deploy",
		"sealos-cloud-database-service":         "service/database/deploy",
		"sealos-cloud-account-service":          "service/account/deploy",
		"sealos-cloud-launchpad-service":        "service/launchpad/deploy",
		"sealos-cloud-vlogs-service":            "service/vlogs/deploy",
		"sealos-cloud-admission-webhook":        "webhooks/admission/deploy",
		"sealos-cloud-node-controller":          "controllers/node/deploy",
	}

	seen := make(map[string]bool, len(expectedContexts))
	for _, pkg := range manifest.Packages {
		context, ok := expectedContexts[pkg.Name]
		if !ok {
			require.Empty(t, pkg.Sources, pkg.Name)
			continue
		}
		require.False(t, seen[pkg.Name], pkg.Name)
		seen[pkg.Name] = true
		require.Len(t, pkg.Sources, 1, pkg.Name)
		source := pkg.Sources[0]
		require.Equal(t, SourceGit, source.Type, pkg.Name)
		require.Equal(t, "https://github.com/labring/sealos.git", source.URL, pkg.Name)
		require.Equal(t, "v5.1.2-rc6", source.Ref, pkg.Name)
		require.Equal(t, context, source.Context, pkg.Name)
		require.Equal(t, "Kubefile", source.File, pkg.Name)
	}
	require.Len(t, seen, len(expectedContexts))

	resolved, err := ResolvePackages(manifest, ResolveOptions{Mode: ResolveHybrid, SourceCache: t.TempDir()})
	require.NoError(t, err)
	for _, item := range resolved {
		if expectedContexts[item.Package.Name] != "" {
			require.NotNil(t, item.Build, item.Package.Ref())
		} else {
			require.Nil(t, item.Build, item.Package.Ref())
		}
	}
}
