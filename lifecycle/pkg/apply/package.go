// Copyright 2026 sealos.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package apply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/labring/sealos/pkg/apply/processor"
	"github.com/labring/sealos/pkg/buildah"
	"github.com/labring/sealos/pkg/distribution"
	"github.com/labring/sealos/pkg/exec"
	"github.com/labring/sealos/pkg/filesystem/rootfs"
	"github.com/labring/sealos/pkg/guest"
	"github.com/labring/sealos/pkg/ssh"
	v2 "github.com/labring/sealos/pkg/types/v1beta1"
	"github.com/labring/sealos/pkg/utils/iputils"
	"github.com/labring/sealos/pkg/utils/maps"
)

// PackageRunner distributes one rootfs package and executes its entrypoint.
// It deliberately uses an isolated remote work directory so package cleanup
// cannot remove the persistent data of an existing Kubernetes cluster.
type PackageRunner struct {
	Context context.Context
	Cluster *v2.Cluster
	Image   string
}

func (r *PackageRunner) Apply() error {
	if r == nil || r.Cluster == nil {
		return errors.New("package runner cluster is nil")
	}
	if r.Image == "" {
		return errors.New("package image is required")
	}

	cluster := r.Cluster.DeepCopy()
	cluster.Name = packageExecutionClusterName(cluster.Name, r.Image)
	cluster.Spec.Image = []string{r.Image}
	cluster.Status.Mounts = nil
	cluster.Spec.Command = processor.GetCommands(r.Context)

	bder, err := buildah.New(cluster.Name)
	if err != nil {
		return err
	}
	guestManager, err := guest.NewGuestManager()
	if err != nil {
		return err
	}
	if err := processor.MountClusterImages(bder, cluster, false); err != nil {
		return fmt.Errorf("prepare package image %s: %w", r.Image, err)
	}
	for index := range cluster.Status.Mounts {
		cluster.Status.Mounts[index].Env = maps.Merge(cluster.Status.Mounts[index].Env, processor.GetEnvs(r.Context))
	}
	mounts := append([]v2.MountImage(nil), cluster.Status.Mounts...)
	if len(mounts) != 1 {
		return fmt.Errorf("package image %s produced %d mounts, want one", r.Image, len(mounts))
	}
	initCommand := ""
	if mounts[0].Labels != nil {
		initCommand = mounts[0].Labels[distribution.PackageInitLabel]
	}
	if initCommand == "" {
		return fmt.Errorf("package image %s does not declare %q", r.Image, distribution.PackageInitLabel)
	}
	mounts[0].Entrypoint = []string{initCommand}
	mounts[0].Cmd = nil
	fs, err := rootfs.NewRootfsMounter(mounts)
	if err != nil {
		return err
	}
	hosts := cluster.GetAllIPS()
	if len(hosts) == 0 {
		return errors.New("package target hosts are empty")
	}
	defer func() {
		_ = fs.UnMountRootfs(cluster, hosts)
		_ = bder.Delete(mounts[0].Name)
	}()
	if err := fs.MountRootfs(cluster, hosts); err != nil {
		return fmt.Errorf("distribute package %s: %w", r.Image, err)
	}
	if err := processor.MirrorRegistry(cluster, mounts); err != nil {
		return fmt.Errorf("mirror package %s registry images: %w", r.Image, err)
	}
	removeRegistryHosts, err := addPackageRegistryHosts(cluster, mounts[0], hosts)
	if err != nil {
		return fmt.Errorf("prepare package %s registry host mapping: %w", r.Image, err)
	}
	defer removeRegistryHosts()
	if err := guestManager.Apply(cluster, mounts, hosts); err != nil {
		return fmt.Errorf("execute package %s: %w", r.Image, err)
	}
	return nil
}

func addPackageRegistryHosts(cluster *v2.Cluster, mount v2.MountImage, hosts []string) (func(), error) {
	domain := strings.TrimSpace(mount.Env["registryDomain"])
	registryIP := strings.TrimSpace(cluster.GetRegistryIP())
	if domain == "" || registryIP == "" {
		return func() {}, nil
	}

	execer, err := exec.New(ssh.NewCacheClientFromCluster(cluster, true))
	if err != nil {
		return nil, err
	}
	added := make([]string, 0, len(hosts))
	remove := func() {
		for _, host := range added {
			if err := execer.CmdAsync(host, packageRegistryHostsDeleteCommand(domain)); err != nil {
				continue
			}
		}
	}
	for _, host := range hosts {
		if err := execer.CmdAsync(host, packageRegistryHostsAddCommand(iputils.GetHostIP(registryIP), domain)); err != nil {
			remove()
			return nil, err
		}
		added = append(added, host)
	}
	return remove, nil
}

func packageRegistryHostsAddCommand(ip, domain string) string {
	marker := "sealos-package-registry:" + domain
	return fmt.Sprintf(`set -eu
marker=%s
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
awk -v marker="$marker" 'index($0, marker) == 0 { print }' /etc/hosts > "$tmp"
printf '%%s %%s # %%s\n' %s %s "$marker" >> "$tmp"
cat "$tmp" > /etc/hosts`, packageShellQuote(marker), packageShellQuote(ip), packageShellQuote(domain))
}

func packageRegistryHostsDeleteCommand(domain string) string {
	marker := "sealos-package-registry:" + domain
	return fmt.Sprintf(`set -eu
marker=%s
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
awk -v marker="$marker" 'index($0, marker) == 0 { print }' /etc/hosts > "$tmp"
cat "$tmp" > /etc/hosts`, packageShellQuote(marker))
}

func packageShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func packageExecutionClusterName(clusterName, image string) string {
	digest := sha256.Sum256([]byte(clusterName + "\x00" + image))
	return "sealos-package-" + hex.EncodeToString(digest[:6])
}
