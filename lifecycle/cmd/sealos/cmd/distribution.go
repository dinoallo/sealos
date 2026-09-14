// Copyright © 2026 sealos.
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

package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"

	cloudinstall "github.com/labring/sealos/pkg/cloud/install"
	"github.com/labring/sealos/pkg/distribution"
)

func newDistributionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "distribution",
		Short: "Install and manage Sealos Cloud distributions",
	}
	cmd.AddCommand(newDistributionInstallCmd())
	cmd.AddCommand(newDistributionStatusCmd())
	cmd.AddCommand(newDistributionResetCmd())
	return cmd
}

func newDistributionInstallCmd() *cobra.Command {
	cfg := cloudinstall.ConfigFromEnv(os.LookupEnv)
	interactive := true
	configPath := ""

	cmd := &cobra.Command{
		Use:   "install [distribution@version]",
		Short: "Install Sealos Cloud from a distribution manifest",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return fmt.Errorf("accepts at most one distribution reference")
			}
			if len(args) == 1 {
				cfg.Distribution = args[0]
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := applyDistributionConfigFile(configPath, &cfg, cmd)
			if err != nil {
				return err
			}
			if !interactive {
				if err := cfg.Validate(); err != nil {
					return fmt.Errorf("invalid installation configuration: %w; provide the value with a flag or enable interactive prompts", err)
				}
			}
			if cfg.DistributionManifest == nil && strings.TrimSpace(cfg.Distribution) == "" {
				return fmt.Errorf("distribution is required; specify it in the config file or as an argument")
			}
			manifest := cfg.DistributionManifest
			if manifest == nil {
				return fmt.Errorf("distribution manifest could not be resolved; define packages inline in the config file")
			}
			installer, log, err := newDistributionInstaller(cmd, cfg)
			if err != nil {
				return err
			}
			if log != nil {
				defer log.Close()
			}
			if err := installer.Install(cmd.Context(), manifest); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Sealos Cloud installation completed")
			return nil
		},
	}

	addDistributionInstallerFlags(cmd, &cfg, &interactive, &configPath)
	return cmd
}

func newDistributionInstaller(cmd *cobra.Command, cfg cloudinstall.Config) (*cloudinstall.Installer, io.WriteCloser, error) {
	runner, err := cloudinstall.NewCommandRunner(cmd.OutOrStdout(), cmd.ErrOrStderr())
	if err != nil {
		return nil, nil, err
	}
	var log io.WriteCloser
	if !cfg.DryRun {
		if err := os.MkdirAll(cfg.ConfigDir, 0o700); err != nil {
			return nil, nil, fmt.Errorf("create installer config directory: %w", err)
		}
		log, err = os.OpenFile(filepath.Join(cfg.ConfigDir, "install.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, nil, fmt.Errorf("open installer log: %w", err)
		}
	}
	return &cloudinstall.Installer{Config: cfg, Runner: runner, Stdout: cmd.OutOrStdout(), Log: log}, log, nil
}

func addDistributionInstallerFlags(cmd *cobra.Command, cfg *cloudinstall.Config, interactive *bool, configPath *string) {
	flags := cmd.Flags()
	if interactive != nil {
		flags.BoolVar(interactive, "interactive", *interactive, "prompt for missing installation values")
	}
	if configPath != nil {
		flags.StringVar(configPath, "config", "", "YAML file containing installation parameters")
	}
	flags.StringVarP(&cfg.Distribution, "distribution", "d", cfg.Distribution, "distribution reference, for example cloud-pro@v5.1.2-rc6")
	flags.StringVar((*string)(&cfg.PackageMode), "package-mode", string(cfg.PackageMode), "resolve packages from remote images, source builds, or hybrid source/remote mode")
	flags.StringVar(&cfg.SourceRoot, "source-root", cfg.SourceRoot, "compatibility base for relative local package source paths")
	flags.StringVar(&cfg.BuildCache, "build-cache", cfg.BuildCache, "cache directory for built container images")
	flags.StringVar(&cfg.StateDir, "state-dir", cfg.StateDir, "package state directory")
	flags.StringVar(&cfg.ClusterName, "cluster", cfg.ClusterName, "stable cluster name or ID used to manage package state")
	flags.StringVar(&cfg.TargetID, "target-id", cfg.TargetID, "stable package state target identifier")
	flags.StringVar(&cfg.Masters, "masters", cfg.Masters, "comma-separated master nodes")
	flags.StringVar(&cfg.Nodes, "nodes", cfg.Nodes, "comma-separated worker nodes")
	flags.StringVar(&cfg.User, "user", cfg.User, "SSH user")
	flags.StringVar(&cfg.SSHPassword, "ssh-password", cfg.SSHPassword, "SSH password")
	flags.StringVar(&cfg.SSHKey, "ssh-key", cfg.SSHKey, "SSH private key path")
	flags.StringVar(&cfg.SSHKeyPasswd, "ssh-key-passwd", cfg.SSHKeyPasswd, "SSH private key passphrase")
	flags.Uint16Var(&cfg.SSHPort, "ssh-port", cfg.SSHPort, "SSH port")
	flags.StringVar(&cfg.RegistryPass, "registry-password", cfg.RegistryPass, "local registry password")
	flags.StringVar(&cfg.CloudDomain, "cloud-domain", cfg.CloudDomain, "cloud cluster domain")
	flags.Uint16Var(&cfg.CloudPort, "cloud-port", cfg.CloudPort, "cloud cluster port")
	flags.IntVar(&cfg.MaxPods, "max-pods", cfg.MaxPods, "maximum pods per node")
	flags.StringVar(&cfg.OpenEBSStorage, "openebs-storage", cfg.OpenEBSStorage, "OpenEBS storage path")
	flags.StringVar(&cfg.ContainerdStorage, "containerd-storage", cfg.ContainerdStorage, "containerd storage path")
	flags.StringVar(&cfg.PodCIDR, "pod-cidr", cfg.PodCIDR, "Kubernetes pod CIDR")
	flags.StringVar(&cfg.ServiceCIDR, "service-cidr", cfg.ServiceCIDR, "Kubernetes service CIDR")
	flags.StringVar(&cfg.ServiceNodePortRange, "service-nodeport-range", cfg.ServiceNodePortRange, "Kubernetes NodePort range")
	flags.StringVar(&cfg.CiliumVersion, "cilium-version", cfg.CiliumVersion, "Cilium package version for distributions with multiple Cilium packages")
	flags.StringVar(&cfg.CiliumMaskSize, "cilium-masksize", cfg.CiliumMaskSize, "Cilium node mask size")
	flags.StringVar(&cfg.CertPath, "cert-path", cfg.CertPath, "TLS certificate PEM path")
	flags.StringVar(&cfg.KeyPath, "key-path", cfg.KeyPath, "TLS private key PEM path")
	flags.BoolVar(&cfg.EnableACME, "enable-acme", cfg.EnableACME, "use ACME DNS certificates")
	flags.BoolVar(&cfg.Proxy, "proxy", cfg.Proxy, "rewrite ghcr.io images through the proxy")
	flags.BoolVar(&cfg.DryRun, "dry-run", cfg.DryRun, "print the installation commands without executing them")
	flags.StringVar(&cfg.ConfigDir, "config-dir", cfg.ConfigDir, "local directory for installer logs")
	flags.DurationVar(&cfg.WaitTimeout, "wait-timeout", cfg.WaitTimeout, "maximum time to wait for cluster resources")
}

func distributionConfigOverrides(cmd *cobra.Command) map[string]bool {
	keys := map[string]string{
		"distribution":           "distribution",
		"package-mode":           "packageMode",
		"source-root":            "sourceRoot",
		"build-cache":            "buildCache",
		"state-dir":              "stateDir",
		"cluster":                "cluster",
		"target-id":              "targetID",
		"masters":                "masters",
		"nodes":                  "nodes",
		"user":                   "user",
		"ssh-password":           "sshPassword",
		"ssh-key":                "sshKey",
		"ssh-key-passwd":         "sshKeyPassphrase",
		"ssh-port":               "sshPort",
		"registry-password":      "registryPassword",
		"cloud-domain":           "cloudDomain",
		"cloud-port":             "cloudPort",
		"max-pods":               "maxPods",
		"openebs-storage":        "openebsStorage",
		"containerd-storage":     "containerdStorage",
		"pod-cidr":               "podCIDR",
		"service-cidr":           "serviceCIDR",
		"service-nodeport-range": "serviceNodePortRange",
		"cilium-version":         "ciliumVersion",
		"cilium-masksize":        "ciliumMaskSize",
		"cert-path":              "certPath",
		"key-path":               "keyPath",
		"enable-acme":            "enableACME",
		"proxy":                  "proxy",
		"dry-run":                "dryRun",
		"config-dir":             "configDir",
		"wait-timeout":           "waitTimeout",
	}
	provided := make(map[string]bool)
	for flagName, configKey := range keys {
		if cmd.Flags().Changed(flagName) {
			provided[configKey] = true
		}
	}
	return provided
}

func applyDistributionConfigFile(path string, cfg *cloudinstall.Config, cmd *cobra.Command) (map[string]bool, error) {
	provided := distributionConfigOverrides(cmd)
	fileConfig, err := cloudinstall.LoadConfigFile(path)
	if err != nil {
		return nil, err
	}
	return fileConfig.Apply(cfg, provided), nil
}

func promptValue(label, defaultValue string, required, secret bool) (string, error) {
	validate := func(value string) error {
		if required && strings.TrimSpace(value) == "" {
			return errors.New("value is required")
		}
		return nil
	}
	prompt := promptui.Prompt{Label: label, Default: defaultValue, Validate: validate}
	if secret {
		prompt.Mask = '*'
	}
	return prompt.Run()
}

func addDistributionStateFlags(cmd *cobra.Command, cfg *cloudinstall.Config) {
	flags := cmd.Flags()
	flags.StringVar((*string)(&cfg.PackageMode), "package-mode", string(cfg.PackageMode), "resolve packages from remote images, source builds, or hybrid source/remote mode")
	flags.StringVar(&cfg.SourceRoot, "source-root", cfg.SourceRoot, "compatibility base for relative local package source paths")
	flags.StringVar(&cfg.BuildCache, "build-cache", cfg.BuildCache, "cache directory for built container images")
	flags.StringVar(&cfg.ClusterName, "cluster", cfg.ClusterName, "stable cluster name or ID used to manage package state")
	flags.StringVar(&cfg.Masters, "masters", cfg.Masters, "comma-separated master nodes")
	flags.Uint16Var(&cfg.SSHPort, "ssh-port", cfg.SSHPort, "SSH port")
	flags.StringVar(&cfg.StateDir, "state-dir", cfg.StateDir, "package state directory")
	flags.StringVar(&cfg.TargetID, "target-id", cfg.TargetID, "stable package state target identifier")
	flags.StringVar(&cfg.CiliumVersion, "cilium-version", cfg.CiliumVersion, "Cilium package version for distributions with multiple Cilium packages")
}

func distributionTargetID(cfg cloudinstall.Config) (string, error) {
	if strings.TrimSpace(cfg.TargetID) != "" {
		return cfg.TargetID, nil
	}
	if strings.TrimSpace(cfg.ClusterName) == "" {
		return "", errors.New("cluster or target-id is required")
	}
	return cfg.ClusterName, nil
}

func distributionState(cfg cloudinstall.Config) (*distribution.StateStore, *distribution.State, error) {
	if strings.TrimSpace(cfg.TargetID) != "" {
		targetID, err := distributionTargetID(cfg)
		if err != nil {
			return nil, nil, err
		}
		store, err := distribution.NewStateStore(cfg.StateDir, targetID)
		if err != nil {
			return nil, nil, err
		}
		state, err := store.Load()
		return store, state, err
	}
	return distribution.FindStateStore(cfg.StateDir, cfg.ClusterName)
}

func newDistributionStatusCmd() *cobra.Command {
	cfg := cloudinstall.ConfigFromEnv(os.LookupEnv)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the recorded package state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, state, err := distributionState(cfg)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "cluster: %s\ntarget: %s\ndistribution: %s\nmanifest: %s\nmasters: %s\nnodes: %s\n", state.Target.Cluster, store.TargetID, state.Distribution, state.ManifestFingerprint, state.Target.Masters, state.Target.Nodes)
			for _, pkg := range state.Packages {
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", pkg.Ref(), pkg.Status)
			}
			return nil
		},
	}
	addDistributionStateFlags(cmd, &cfg)
	return cmd
}

func newDistributionResetCmd() *cobra.Command {
	cfg := cloudinstall.ConfigFromEnv(os.LookupEnv)
	interactive := true
	force := false
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset a distribution-installed cluster",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, state, err := distributionState(cfg)
			if err != nil {
				return fmt.Errorf("load distribution target state: %w", err)
			}
			if !cmd.Flags().Changed("masters") && state.Target.Masters != "" {
				cfg.Masters = state.Target.Masters
			}
			if !cmd.Flags().Changed("nodes") {
				cfg.Nodes = state.Target.Nodes
			}
			if !cmd.Flags().Changed("user") && state.Target.User != "" {
				cfg.User = state.Target.User
			}
			if !cmd.Flags().Changed("ssh-port") && state.Target.SSHPort != 0 {
				cfg.SSHPort = state.Target.SSHPort
			}
			if strings.TrimSpace(cfg.Masters) == "" {
				return errors.New("recorded target has no master nodes; provide --masters")
			}
			if interactive {
				if err := promptDistributionResetConfig(cmd, &cfg); err != nil {
					return err
				}
			}
			if err := cfg.ValidateReset(); err != nil {
				return fmt.Errorf("invalid reset configuration: %w", err)
			}
			clusterName := cfg.ClusterName
			if !cmd.Flags().Changed("cluster") && state.Target.Cluster != "" {
				clusterName = state.Target.Cluster
			}
			if err := validateResetTarget(state, clusterName); err != nil {
				return err
			}
			if strings.TrimSpace(cfg.TargetID) == "" {
				cfg.TargetID = store.TargetID
			}
			cfg.ClusterName = clusterName
			if !force && !cfg.DryRun {
				confirm := promptui.Select{
					Label:     fmt.Sprintf("Reset distribution cluster %s", clusterName),
					Items:     []string{"yes", "no"},
					CursorPos: 1,
				}
				_, answer, err := confirm.Run()
				if err != nil {
					return err
				}
				if answer != "yes" {
					return nil
				}
			}
			installer, log, err := newDistributionInstaller(cmd, cfg)
			if err != nil {
				return err
			}
			if log != nil {
				defer log.Close()
			}
			if err := installer.Reset(cmd.Context(), clusterName); err != nil {
				return err
			}
			if !cfg.DryRun {
				if err := store.Remove(); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "distribution reset completed: %s\n", clusterName)
			return nil
		},
	}
	addDistributionResetFlags(cmd, &cfg, &interactive, &force)
	return cmd
}

func addDistributionResetFlags(cmd *cobra.Command, cfg *cloudinstall.Config, interactive, force *bool) {
	flags := cmd.Flags()
	flags.BoolVar(interactive, "interactive", *interactive, "prompt for missing reset values")
	flags.BoolVar(force, "force", *force, "skip the reset confirmation")
	flags.BoolVar(&cfg.DryRun, "dry-run", cfg.DryRun, "print the reset commands without executing them")
	flags.StringVar(&cfg.ClusterName, "cluster", cfg.ClusterName, "stable cluster name or ID used to manage package state")
	flags.StringVar(&cfg.Masters, "masters", cfg.Masters, "comma-separated master nodes")
	flags.StringVar(&cfg.Nodes, "nodes", cfg.Nodes, "comma-separated worker nodes")
	flags.StringVar(&cfg.User, "user", cfg.User, "SSH user")
	flags.StringVar(&cfg.SSHPassword, "ssh-password", cfg.SSHPassword, "SSH password")
	flags.StringVar(&cfg.SSHKey, "ssh-key", cfg.SSHKey, "SSH private key path")
	flags.StringVar(&cfg.SSHKeyPasswd, "ssh-key-passwd", cfg.SSHKeyPasswd, "SSH private key passphrase")
	flags.Uint16Var(&cfg.SSHPort, "ssh-port", cfg.SSHPort, "SSH port")
	flags.StringVar(&cfg.StateDir, "state-dir", cfg.StateDir, "package state directory")
	flags.StringVar(&cfg.TargetID, "target-id", cfg.TargetID, "stable package state target identifier")
}

func promptDistributionResetConfig(cmd *cobra.Command, cfg *cloudinstall.Config) error {
	var err error
	if !cmd.Flags().Changed("masters") && strings.TrimSpace(cfg.Masters) == "" {
		cfg.Masters, err = promptValue("Master nodes (comma-separated)", cfg.Masters, true, false)
		if err != nil {
			return err
		}
	}
	if !cmd.Flags().Changed("nodes") && strings.TrimSpace(cfg.Nodes) == "" {
		cfg.Nodes, err = promptValue("Worker nodes (comma-separated, optional)", cfg.Nodes, false, false)
		if err != nil {
			return err
		}
	}
	if !cmd.Flags().Changed("user") && strings.TrimSpace(cfg.User) == "" {
		cfg.User, err = promptValue("SSH user", cfg.User, true, false)
		if err != nil {
			return err
		}
	}
	if !cmd.Flags().Changed("ssh-key") && strings.TrimSpace(cfg.SSHKey) == "" {
		cfg.SSHKey, err = promptValue("SSH private key", cfg.SSHKey, false, false)
		if err != nil {
			return err
		}
	}
	if !cmd.Flags().Changed("ssh-password") {
		cfg.SSHPassword, err = promptValue("SSH password (optional)", cfg.SSHPassword, false, true)
		if err != nil {
			return err
		}
	}
	return nil
}

func validateResetTarget(state *distribution.State, clusterName string) error {
	if state == nil {
		return errors.New("distribution target state is nil")
	}
	if state.Target.Cluster != "" && state.Target.Cluster != clusterName {
		return fmt.Errorf("reset cluster %q does not match recorded cluster %q", clusterName, state.Target.Cluster)
	}
	return nil
}
