// Copyright 2026 sealos.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package v1beta1

import "testing"

func TestGetVIPFallsBackWhenRootfsDoesNotDeclareVIP(t *testing.T) {
	cluster := &Cluster{Status: ClusterStatus{Mounts: []MountImage{{
		Type:   RootfsImage,
		Labels: map[string]string{},
	}}}}
	if got, want := cluster.GetVIP(), defaultVIP; got != want {
		t.Fatalf("GetVIP() = %q, want %q", got, want)
	}
}
