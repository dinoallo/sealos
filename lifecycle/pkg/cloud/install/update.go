// Copyright 2026 sealos.
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

package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/labring/sealos/pkg/distribution"
)

var ErrIncrementalPackageUnsupported = errors.New("package does not support incremental installation")

// SupportsIncrementalPackage reports whether a package can be installed with
// the standalone Sealos image lifecycle. Cloud bootstrap and aggregate Cloud
// packages must be upgraded as a coordinated set and are not supported here.
func SupportsIncrementalPackage(name string) bool {
	switch name {
	case "kubernetes", "cilium", "helm", "sealos-cloud", "sealos-finish", "sealos-certs":
		return false
	default:
		return !strings.HasPrefix(name, "sealos-cloud-")
	}
}

func IncrementalPackageBlockReason(name string) string {
	if SupportsIncrementalPackage(name) {
		return ""
	}
	return "package requires coordinated distribution bootstrap"
}

// InstallIncrementalPackage executes one independently installable package.
// The caller is responsible for transaction and package-state persistence.
func (i *Installer) InstallIncrementalPackage(ctx context.Context, item distribution.ResolvedPackage) error {
	if err := i.Config.Validate(); err != nil {
		return err
	}
	if i.Runner == nil {
		return errors.New("installer command runner is nil")
	}
	if !SupportsIncrementalPackage(item.Package.Name) {
		return fmt.Errorf("%w: %s (%s)", ErrIncrementalPackageUnsupported, item.Package.Ref(), IncrementalPackageBlockReason(item.Package.Name))
	}
	if i.Stdout == nil {
		i.Stdout = io.Discard
	}
	if item.Build != nil {
		if item.Build.CleanupDir != "" {
			defer os.RemoveAll(item.Build.CleanupDir)
		}
		for _, command := range item.Build.Commands {
			if err := i.run(ctx, Command{Name: command.Name, Args: command.Args, Dir: command.Dir}); err != nil {
				return err
			}
		}
	}
	image := item.Image
	if i.Config.Proxy && i.Config.PackageMode == distribution.ResolveRemote && strings.HasPrefix(image, "ghcr.io/") {
		image = "ghcr.dockerproxy.net/" + strings.TrimPrefix(image, "ghcr.io/")
	}
	command := i.incrementalPackageCommand(item.Package.Name, image)
	if err := i.runPackage(ctx, command); err != nil {
		return err
	}
	if item.Package.Name == "sealos-oss" {
		if err := i.waitForConfigMap(ctx, "sealos-config", "sealos-system"); err != nil {
			return err
		}
	}
	return nil
}

func (i *Installer) incrementalPackageCommand(name, image string) Command {
	switch name {
	case "openebs":
		return i.imageWithEnv(image, "OPENEBS_STORAGE_PREFIX", i.Config.OpenEBSStorage)
	case "higress":
		return i.imageWithEnvs(image, map[string]string{
			"SEALOS_CLOUD_PORT":   strconv.Itoa(int(i.Config.CloudPort)),
			"SEALOS_CLOUD_DOMAIN": i.Config.CloudDomain,
		})
	case "sealos-oss":
		return i.imageWithEnv(image, "SEALOS_V2_SERVICE_NODEPORT_RANGE", i.Config.ServiceNodePortRange)
	default:
		return i.runImage(image)
	}
}

func (i *Installer) recordInstalledState(ctx context.Context, manifest *distribution.Manifest) error {
	if i.Config.DryRun {
		return nil
	}
	packages, err := InstalledPackages(manifest, i.Config.PackageMode, i.Config.CiliumVersion)
	if err != nil {
		return err
	}
	store, err := i.stateStore()
	if err != nil {
		return err
	}
	unlock, err := store.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	state := &distribution.State{
		Target: distribution.TargetMetadata{
			Cluster: clusterName(i.Config),
			Masters: i.Config.Masters,
			Nodes:   i.Config.Nodes,
			User:    i.Config.User,
			SSHPort: i.Config.SSHPort,
		},
		Distribution:        manifest.Ref(),
		ManifestFingerprint: manifest.Fingerprint(),
		RepositoryCommit:    i.Config.RepositoryCommit,
		Packages:            packages,
	}
	if err := store.Save(state); err != nil {
		return fmt.Errorf("save package state: %w", err)
	}
	return nil
}

func (i *Installer) stateStore() (*distribution.StateStore, error) {
	targetID := strings.TrimSpace(i.Config.TargetID)
	if targetID == "" {
		targetID = clusterName(i.Config)
	}
	return distribution.NewStateStore(i.Config.StateDir, targetID)
}

func clusterName(cfg Config) string {
	if name := strings.TrimSpace(cfg.ClusterName); name != "" {
		return name
	}
	return "default"
}

// InstalledPackages returns the package set that the Cloud installer actually
// considers installed. For Cloud-Pro manifests only the selected Cilium
// version is recorded when multiple candidates are present.
func InstalledPackages(manifest *distribution.Manifest, mode distribution.ResolveMode, ciliumVersion string) ([]distribution.InstalledPackage, error) {
	resolved, err := ResolveInstalledPackages(manifest, distribution.ResolveOptions{Mode: distribution.ResolveRemote}, ciliumVersion)
	if err != nil {
		return nil, err
	}
	packages := make([]distribution.InstalledPackage, 0, len(resolved))
	seen := make(map[string]struct{}, len(resolved))
	for _, item := range resolved {
		if _, ok := seen[item.Package.Name]; ok {
			return nil, fmt.Errorf("distribution %s contains duplicate installed package name %q", manifest.Ref(), item.Package.Name)
		}
		seen[item.Package.Name] = struct{}{}
		packages = append(packages, distribution.NewInstalledPackage(item.Package, item.Image, mode, distribution.PackageStatusInstalled))
	}
	return packages, nil
}

// ResolveInstalledPackages returns the package set relevant to the actual
// Cloud installation. A distribution may publish several Cilium candidates,
// but only the configured candidate participates in state and diff operations.
func ResolveInstalledPackages(manifest *distribution.Manifest, options distribution.ResolveOptions, ciliumVersion string) ([]distribution.ResolvedPackage, error) {
	resolved, err := distribution.ResolvePackages(manifest, options)
	if err != nil {
		return nil, err
	}
	if manifest.Name == "cloud" {
		return resolved, nil
	}
	selected, err := selectCiliumPackage(resolved, ciliumVersion)
	if err != nil {
		return nil, err
	}
	filtered := make([]distribution.ResolvedPackage, 0, len(resolved))
	for _, item := range resolved {
		if item.Package.Name == "cilium" && item.Package.Ref() != selected.Package.Ref() {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered, nil
}
