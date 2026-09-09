// Copyright 2026 sealos.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package bootstrap

import (
	"testing"

	"github.com/labring/sealos/pkg/distribution"
	v2 "github.com/labring/sealos/pkg/types/v1beta1"
)

func TestPackageManagedRootfs(t *testing.T) {
	cluster := &v2.Cluster{}
	if packageManagedRootfs(cluster) {
		t.Fatal("cluster without a rootfs must use legacy bootstrap")
	}

	cluster.Status.Mounts = []v2.MountImage{{
		Type: v2.RootfsImage,
		Labels: map[string]string{
			distribution.PackageInitLabel: "bash opt/sealos/package-init.sh",
		},
	}}
	if !packageManagedRootfs(cluster) {
		t.Fatal("rootfs with package init label must use package-owned bootstrap")
	}
}

func TestPackageManagedRootfsSkipsLegacyBootstrap(t *testing.T) {
	legacy := &realContext{cluster: &v2.Cluster{}}
	packageManaged := &realContext{cluster: &v2.Cluster{
		Status: v2.ClusterStatus{Mounts: []v2.MountImage{{
			Type:   v2.RootfsImage,
			Labels: map[string]string{distribution.PackageInitLabel: "bash /opt/package-init.sh"},
		}}},
	}}

	for _, tc := range []struct {
		name    string
		ctx     Context
		wantRun bool
	}{
		{name: "legacy rootfs", ctx: legacy, wantRun: true},
		{name: "package rootfs", ctx: packageManaged, wantRun: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (&defaultChecker{}).Filter(tc.ctx, "host"); got != tc.wantRun {
				t.Fatalf("defaultChecker.Filter() = %v, want %v", got, tc.wantRun)
			}
			if got := (&defaultCRIInitializer{}).Filter(tc.ctx, "host"); got != tc.wantRun {
				t.Fatalf("defaultCRIInitializer.Filter() = %v, want %v", got, tc.wantRun)
			}
			if got := (&defaultInitializer{}).Filter(tc.ctx, "host"); got != tc.wantRun {
				t.Fatalf("defaultInitializer.Filter() = %v, want %v", got, tc.wantRun)
			}
		})
	}
}
