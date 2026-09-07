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
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"

	cloudinstall "github.com/labring/sealos/pkg/cloud/install"
	"github.com/labring/sealos/pkg/distribution"
)

func newDistributionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "distribution",
		Short: "Inspect and resolve versioned installation distributions",
	}
	cmd.AddCommand(newDistributionListCmd())
	cmd.AddCommand(newDistributionShowCmd())
	cmd.AddCommand(newDistributionResolveCmd())
	cmd.AddCommand(newDistributionValidateCmd())
	cmd.AddCommand(newDistributionInstallCmd())
	cmd.AddCommand(newDistributionDiffCmd())
	cmd.AddCommand(newDistributionUpdateCmd())
	cmd.AddCommand(newDistributionAdoptCmd())
	cmd.AddCommand(newDistributionStatusCmd())
	cmd.AddCommand(newDistributionResetCmd())
	return cmd
}

func newDistributionInstallCmd() *cobra.Command {
	cfg := cloudinstall.ConfigFromEnv(os.LookupEnv)
	repoOptions := newRepositoryCommandOptions()
	interactive := true
	positionalDistribution := false

	cmd := &cobra.Command{
		Use:   "install [distribution@version]",
		Short: "Install Sealos Cloud from a distribution manifest",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return fmt.Errorf("accepts at most one distribution reference")
			}
			if len(args) == 1 {
				positionalDistribution = true
				cfg.Distribution = args[0]
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := repoOptions.validate(cmd); err != nil {
				return err
			}
			if !interactive {
				if err := cfg.Validate(); err != nil {
					return fmt.Errorf("invalid installation configuration: %w; provide the value with a flag or enable interactive prompts", err)
				}
			}
			repo, err := distribution.OpenRepository(cmd.Context(), repoOptions.config)
			if err != nil {
				return err
			}
			if interactive {
				if err := promptInstallConfig(cmd, &cfg, positionalDistribution, repo); err != nil {
					return err
				}
				if err := cfg.Validate(); err != nil {
					return err
				}
			}
			manifest, err := repo.Load(cfg.Distribution)
			if err != nil {
				return err
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

	addDistributionInstallerFlags(cmd, &cfg, repoOptions, &interactive)
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

func addDistributionInstallerFlags(cmd *cobra.Command, cfg *cloudinstall.Config, repoOptions *repositoryCommandOptions, interactive *bool) {
	flags := cmd.Flags()
	if interactive != nil {
		flags.BoolVar(interactive, "interactive", *interactive, "prompt for missing installation values")
	}
	if repoOptions != nil {
		repoOptions.addFlags(cmd)
	}
	flags.StringVarP(&cfg.Distribution, "distribution", "d", cfg.Distribution, "distribution reference, for example cloud-pro@v5.1.2-rc6")
	flags.StringVar((*string)(&cfg.PackageMode), "package-mode", string(cfg.PackageMode), "resolve packages from remote images, source builds, or hybrid source/remote mode")
	flags.StringVar(&cfg.SourceRoot, "source-root", cfg.SourceRoot, "compatibility base for relative local package source paths")
	flags.StringVar(&cfg.SourceCache, "source-cache", cfg.SourceCache, "persistent cache directory for Git package sources")
	flags.StringVar(&cfg.ClusterName, "cluster", cfg.ClusterName, "stable cluster name or ID used to manage package state")
	flags.StringVar(&cfg.Masters, "masters", cfg.Masters, "comma-separated master nodes")
	flags.StringVar(&cfg.Nodes, "nodes", cfg.Nodes, "comma-separated worker nodes")
	flags.StringVar(&cfg.CloudDomain, "cloud-domain", cfg.CloudDomain, "domain used to expose Sealos Cloud")
	flags.Uint16Var(&cfg.CloudPort, "cloud-port", cfg.CloudPort, "Sealos Cloud HTTPS port")
	flags.StringVar(&cfg.User, "user", cfg.User, "SSH user")
	flags.StringVar(&cfg.SSHPassword, "ssh-password", cfg.SSHPassword, "SSH password")
	flags.StringVar(&cfg.SSHKey, "ssh-key", cfg.SSHKey, "SSH private key path")
	flags.StringVar(&cfg.SSHKeyPasswd, "ssh-key-passwd", cfg.SSHKeyPasswd, "SSH private key passphrase")
	flags.Uint16Var(&cfg.SSHPort, "ssh-port", cfg.SSHPort, "SSH port")
	flags.StringVar(&cfg.RegistryPass, "registry-password", cfg.RegistryPass, "local registry password")
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
	flags.StringVar(&cfg.StateDir, "state-dir", cfg.StateDir, "package state directory")
	flags.StringVar(&cfg.TargetID, "target-id", cfg.TargetID, "stable package state target identifier")
	flags.DurationVar(&cfg.WaitTimeout, "wait-timeout", cfg.WaitTimeout, "maximum time to wait for cluster resources")
}

func promptInstallConfig(cmd *cobra.Command, cfg *cloudinstall.Config, positionalDistribution bool, repo *distribution.Repository) error {
	if !cmd.Flags().Changed("distribution") && !positionalDistribution {
		summaries, err := repo.List()
		if err != nil {
			return err
		}
		items := make([]string, 0, len(summaries))
		selected := 0
		for _, summary := range summaries {
			manifest, err := repo.Load(summary.Ref())
			if err != nil {
				return err
			}
			if err := cloudinstall.ValidateManifest(manifest, distribution.ResolveOptions{Mode: cfg.PackageMode}); err != nil {
				continue
			}
			items = append(items, summary.Ref())
			if summary.Ref() == cfg.Distribution {
				selected = len(items) - 1
			}
		}
		if len(items) > 0 {
			prompt := promptui.Select{Label: "Distribution", Items: items, CursorPos: selected}
			index, _, err := prompt.Run()
			if err != nil {
				return err
			}
			cfg.Distribution = items[index]
		}
	}

	var err error
	if !cmd.Flags().Changed("cloud-domain") {
		cfg.CloudDomain, err = promptValue("Cloud domain", cfg.CloudDomain, true, false)
		if err != nil {
			return err
		}
	}
	if !cmd.Flags().Changed("masters") {
		cfg.Masters, err = promptValue("Master nodes (comma-separated)", cfg.Masters, true, false)
		if err != nil {
			return err
		}
	}
	if !cmd.Flags().Changed("nodes") {
		cfg.Nodes, err = promptValue("Worker nodes (comma-separated, optional)", cfg.Nodes, false, false)
		if err != nil {
			return err
		}
	}
	if !cmd.Flags().Changed("user") {
		cfg.User, err = promptValue("SSH user", cfg.User, true, false)
		if err != nil {
			return err
		}
	}
	if !cmd.Flags().Changed("ssh-key") {
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
	if !cmd.Flags().Changed("registry-password") {
		cfg.RegistryPass, err = promptValue("Local registry password", cfg.RegistryPass, true, true)
		if err != nil {
			return err
		}
	}
	if !cmd.Flags().Changed("enable-acme") && cfg.CertPath == "" && cfg.KeyPath == "" && !cfg.EnableACME {
		choice := promptui.Select{Label: "TLS certificate mode", Items: []string{"self-signed", "acme", "custom certificate"}}
		_, value, err := choice.Run()
		if err != nil {
			return err
		}
		switch value {
		case "acme":
			cfg.EnableACME = true
		case "custom certificate":
			cfg.CertPath, err = promptValue("TLS certificate PEM path", "", true, false)
			if err != nil {
				return err
			}
			cfg.KeyPath, err = promptValue("TLS private key PEM path", "", true, false)
			if err != nil {
				return err
			}
		}
	}
	return nil
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

func newDistributionListCmd() *cobra.Command {
	opts := newRepositoryCommandOptions()
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available distributions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(cmd); err != nil {
				return err
			}
			repo, err := distribution.OpenRepository(cmd.Context(), opts.config)
			if err != nil {
				return err
			}
			summaries, err := repo.List()
			if err != nil {
				return err
			}
			for _, summary := range summaries {
				fmt.Fprintln(cmd.OutOrStdout(), summary.Ref())
			}
			return nil
		},
	}
	opts.addFlags(cmd)
	return cmd
}

func newDistributionShowCmd() *cobra.Command {
	opts := newRepositoryCommandOptions()
	cmd := &cobra.Command{
		Use:   "show <distribution@version>",
		Short: "Show a distribution manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(cmd); err != nil {
				return err
			}
			repo, err := distribution.OpenRepository(cmd.Context(), opts.config)
			if err != nil {
				return err
			}
			data, err := repo.Show(args[0])
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), data)
			return nil
		},
	}
	opts.addFlags(cmd)
	return cmd
}

func newDistributionResolveCmd() *cobra.Command {
	opts := newRepositoryCommandOptions()
	cmd := &cobra.Command{
		Use:   "resolve <distribution@version>",
		Short: "Resolve a distribution to a list of image references",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(cmd); err != nil {
				return err
			}
			repo, err := distribution.OpenRepository(cmd.Context(), opts.config)
			if err != nil {
				return err
			}
			images, err := repo.Resolve(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), strings.Join(images, "\n"))
			return nil
		},
	}
	opts.addFlags(cmd)
	return cmd
}

func newDistributionValidateCmd() *cobra.Command {
	opts := newRepositoryCommandOptions()
	cmd := &cobra.Command{
		Use:   "validate <distribution@version>",
		Short: "Validate a distribution manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(cmd); err != nil {
				return err
			}
			repo, err := distribution.OpenRepository(cmd.Context(), opts.config)
			if err != nil {
				return err
			}
			_, err = repo.Load(args[0])
			return err
		},
	}
	opts.addFlags(cmd)
	return cmd
}

func addDistributionStateFlags(cmd *cobra.Command, cfg *cloudinstall.Config, repoOptions *repositoryCommandOptions) {
	if repoOptions != nil {
		repoOptions.addFlags(cmd)
	}
	flags := cmd.Flags()
	flags.StringVar((*string)(&cfg.PackageMode), "package-mode", string(cfg.PackageMode), "resolve packages from remote images, source builds, or hybrid source/remote mode")
	flags.StringVar(&cfg.SourceRoot, "source-root", cfg.SourceRoot, "compatibility base for relative local package source paths")
	flags.StringVar(&cfg.SourceCache, "source-cache", cfg.SourceCache, "persistent cache directory for Git package sources")
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

func hydrateDistributionTarget(cfg *cloudinstall.Config, state *distribution.State) {
	if cfg == nil || state == nil {
		return
	}
	if strings.TrimSpace(cfg.ClusterName) == "" && strings.TrimSpace(state.Target.Cluster) != "" {
		cfg.ClusterName = state.Target.Cluster
	}
	if strings.TrimSpace(cfg.Masters) == "" {
		cfg.Masters = state.Target.Masters
	}
	if strings.TrimSpace(cfg.Nodes) == "" {
		cfg.Nodes = state.Target.Nodes
	}
	if strings.TrimSpace(cfg.User) == "" {
		cfg.User = state.Target.User
	}
	if cfg.SSHPort == 0 {
		cfg.SSHPort = state.Target.SSHPort
	}
}

func loadDistributionDiff(repo *distribution.Repository, ref string, cfg cloudinstall.Config) (distribution.Diff, error) {
	manifest, err := repo.Load(ref)
	if err != nil {
		return distribution.Diff{}, err
	}
	resolved, err := cloudinstall.ResolveInstalledPackages(manifest, distribution.ResolveOptions{
		Mode:        cfg.PackageMode,
		SourceRoot:  cfg.SourceRoot,
		SourceCache: cfg.SourceCache,
		WorkDir:     cfg.ConfigDir,
	}, cfg.CiliumVersion)
	if err != nil {
		return distribution.Diff{}, err
	}
	store, state, err := distributionState(cfg)
	if err != nil {
		if errors.Is(err, distribution.ErrPackageStateNotFound) {
			return distribution.Diff{}, fmt.Errorf("%w; run distribution adopt %s first", err, ref)
		}
		return distribution.Diff{}, err
	}
	diff, err := distribution.Compare(manifest, resolved, state, store.TargetID)
	if err != nil {
		return distribution.Diff{}, err
	}
	return classifyIncrementalChanges(diff), nil
}

func classifyIncrementalChanges(diff distribution.Diff) distribution.Diff {
	for index := range diff.Changes {
		change := &diff.Changes[index]
		if change.Desired == nil || (change.Kind != distribution.ChangeAdded && change.Kind != distribution.ChangeChanged) {
			continue
		}
		if reason := cloudinstall.IncrementalPackageBlockReason(change.Desired.Package.Name); reason != "" {
			change.Kind = distribution.ChangeBlocked
			change.Reason = reason
		}
	}
	return diff
}

func printDistributionDiff(out io.Writer, diff distribution.Diff) {
	for _, change := range diff.Changes {
		ref := ""
		if change.Desired != nil {
			ref = change.Desired.Package.Ref()
		} else if change.Installed != nil {
			ref = change.Installed.Ref()
		}
		symbol := map[distribution.ChangeKind]string{
			distribution.ChangeAdded:     "+",
			distribution.ChangeChanged:   "~",
			distribution.ChangeUnchanged: "=",
			distribution.ChangeOrphan:    "-",
			distribution.ChangeBlocked:   "!",
		}[change.Kind]
		if change.Kind == distribution.ChangeChanged && change.Installed != nil && change.Desired != nil {
			fmt.Fprintf(out, "%s %s -> %s\n", symbol, change.Installed.Ref(), ref)
			continue
		}
		if change.Reason != "" {
			fmt.Fprintf(out, "%s %s (%s)\n", symbol, ref, change.Reason)
			continue
		}
		fmt.Fprintf(out, "%s %s\n", symbol, ref)
	}
}

func newDistributionDiffCmd() *cobra.Command {
	cfg := cloudinstall.ConfigFromEnv(os.LookupEnv)
	repoOptions := newRepositoryCommandOptions()
	cmd := &cobra.Command{
		Use:   "diff <distribution@version>",
		Short: "Compare a distribution with the recorded package state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := repoOptions.validate(cmd); err != nil {
				return err
			}
			repo, err := distribution.OpenRepository(cmd.Context(), repoOptions.config)
			if err != nil {
				return err
			}
			diff, err := loadDistributionDiff(repo, args[0], cfg)
			if err != nil {
				return err
			}
			printDistributionDiff(cmd.OutOrStdout(), diff)
			return nil
		},
	}
	addDistributionStateFlags(cmd, &cfg, repoOptions)
	return cmd
}

func newDistributionUpdateCmd() *cobra.Command {
	cfg := cloudinstall.ConfigFromEnv(os.LookupEnv)
	repoOptions := newRepositoryCommandOptions()
	interactive := true
	yes := false
	cmd := &cobra.Command{
		Use:   "update <distribution@version>",
		Short: "Apply a distribution update to the recorded target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Distribution = args[0]
			_, state, err := distributionState(cfg)
			if err != nil {
				return fmt.Errorf("load distribution target state: %w", err)
			}
			hydrateDistributionTarget(&cfg, state)
			if !cmd.Flags().Changed("cluster") && state.Target.Cluster != "" {
				cfg.ClusterName = state.Target.Cluster
			}
			if !cmd.Flags().Changed("masters") {
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
			if err := repoOptions.validate(cmd); err != nil {
				return err
			}
			repo, err := distribution.OpenRepository(cmd.Context(), repoOptions.config)
			if err != nil {
				return err
			}
			if interactive {
				if err := promptInstallConfig(cmd, &cfg, true, repo); err != nil {
					return err
				}
			}
			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("invalid update configuration: %w", err)
			}
			status, err := repo.Status()
			if err != nil {
				return err
			}
			cfg.RepositoryCommit = status.Commit
			diff, err := loadDistributionDiff(repo, args[0], cfg)
			if err != nil {
				return err
			}
			printDistributionDiff(cmd.OutOrStdout(), diff)
			if blocked := diff.ByKind(distribution.ChangeBlocked); len(blocked) > 0 {
				return fmt.Errorf("distribution update contains %d blocked package change(s)", len(blocked))
			}
			actionable := distributionActionableChanges(diff)
			if len(actionable) == 0 {
				return updateDistributionMetadata(cfg, diff, cfg.RepositoryCommit)
			}
			if cfg.DryRun {
				return nil
			}
			approved := make([]distribution.PackageChange, 0, len(actionable))
			deferred := false
			added := diff.ByKind(distribution.ChangeAdded)
			if len(added) > 0 && !yes {
				confirm := promptui.Select{Label: fmt.Sprintf("Install %d new package(s)", len(added)), Items: []string{"yes", "no"}, CursorPos: 1}
				_, answer, err := confirm.Run()
				if err != nil {
					return err
				}
				if answer != "yes" {
					return nil
				}
			}
			for _, change := range actionable {
				if change.Kind != distribution.ChangeChanged || yes {
					approved = append(approved, change)
					continue
				}
				confirm := promptui.Select{Label: fmt.Sprintf("Update %s", change.Desired.Package.Ref()), Items: []string{"yes", "no"}, CursorPos: 1}
				_, answer, err := confirm.Run()
				if err != nil {
					return err
				}
				if answer == "yes" {
					approved = append(approved, change)
				} else {
					deferred = true
				}
			}
			if len(approved) == 0 {
				return nil
			}
			return applyDistributionUpdate(cmd, cfg, args[0], diff, approved, !deferred)
		},
	}
	addDistributionInstallerFlags(cmd, &cfg, repoOptions, &interactive)
	cmd.Flags().BoolVar(&yes, "yes", false, "apply the update without confirmation")
	return cmd
}

func distributionActionableChanges(diff distribution.Diff) []distribution.PackageChange {
	changes := make([]distribution.PackageChange, 0)
	for _, change := range diff.Changes {
		if change.Kind == distribution.ChangeAdded || change.Kind == distribution.ChangeChanged {
			changes = append(changes, change)
		}
	}
	return changes
}

func updateDistributionMetadata(cfg cloudinstall.Config, diff distribution.Diff, repositoryCommit string) error {
	store, _, err := distributionState(cfg)
	if err != nil {
		return err
	}
	unlock, err := store.Lock(context.Background())
	if err != nil {
		return err
	}
	defer unlock()
	state, err := store.Load()
	if err != nil {
		return err
	}
	state.Distribution = diff.Distribution
	state.ManifestFingerprint = diff.ManifestFingerprint
	state.RepositoryCommit = repositoryCommit
	return store.Save(state)
}

func applyDistributionUpdate(cmd *cobra.Command, cfg cloudinstall.Config, ref string, diff distribution.Diff, actionable []distribution.PackageChange, advanceDistribution bool) error {
	store, _, err := distributionState(cfg)
	if err != nil {
		return err
	}
	unlock, err := store.Lock(cmd.Context())
	if err != nil {
		return err
	}
	defer unlock()
	state, err := store.Load()
	if err != nil {
		return err
	}
	transaction := &distribution.Transaction{
		ID:                  fmt.Sprintf("%d", time.Now().UnixNano()),
		Distribution:        ref,
		ManifestFingerprint: diff.ManifestFingerprint,
		Status:              distribution.TransactionPending,
		Packages:            make([]distribution.TransactionPackage, 0, len(actionable)),
	}
	for _, change := range actionable {
		action := "install"
		if change.Kind == distribution.ChangeChanged {
			action = "update"
		}
		transaction.Packages = append(transaction.Packages, distribution.TransactionPackage{Ref: change.Desired.Package.Ref(), Action: action, Status: "pending"})
	}
	if err := store.SaveTransaction(transaction); err != nil {
		return err
	}
	transaction.Status = distribution.TransactionApplying
	if err := store.SaveTransaction(transaction); err != nil {
		return err
	}
	installer, log, err := newDistributionInstaller(cmd, cfg)
	if err != nil {
		transaction.Status = distribution.TransactionFailed
		transaction.Error = err.Error()
		_ = store.SaveTransaction(transaction)
		return err
	}
	if log != nil {
		defer log.Close()
	}
	for index, change := range actionable {
		item := *change.Desired
		alreadyInstalled := false
		for _, installed := range state.Packages {
			if installed.Name == item.Package.Name && installed.Fingerprint == item.Package.Fingerprint() {
				alreadyInstalled = true
				break
			}
		}
		if alreadyInstalled {
			transaction.Packages[index].Status = distribution.PackageStatusInstalled
			if err := store.SaveTransaction(transaction); err != nil {
				return err
			}
			continue
		}
		if err := installer.InstallIncrementalPackage(cmd.Context(), item); err != nil {
			transaction.Packages[index].Status = distribution.TransactionFailed
			transaction.Packages[index].Error = err.Error()
			transaction.Status = distribution.TransactionFailed
			transaction.Error = err.Error()
			_ = store.SaveTransaction(transaction)
			return fmt.Errorf("update package %s: %w", item.Package.Ref(), err)
		}
		transaction.Packages[index].Status = distribution.PackageStatusInstalled
		if err := store.SaveTransaction(transaction); err != nil {
			return err
		}
		upsertInstalledPackage(state, item, cfg.PackageMode)
		if err := store.Save(state); err != nil {
			return fmt.Errorf("save package state after %s: %w", item.Package.Ref(), err)
		}
	}
	if advanceDistribution {
		state.Distribution = diff.Distribution
		state.ManifestFingerprint = diff.ManifestFingerprint
		state.RepositoryCommit = cfg.RepositoryCommit
	}
	if err := store.Save(state); err != nil {
		return err
	}
	transaction.Status = distribution.TransactionSucceeded
	if err := store.SaveTransaction(transaction); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "distribution update completed: %s\n", ref)
	return nil
}

func upsertInstalledPackage(state *distribution.State, item distribution.ResolvedPackage, mode distribution.ResolveMode) {
	installed := distribution.NewInstalledPackage(item.Package, item.Image, mode, distribution.PackageStatusInstalled)
	for index := range state.Packages {
		if state.Packages[index].Name == installed.Name {
			state.Packages[index] = installed
			return
		}
	}
	state.Packages = append(state.Packages, installed)
}

func newDistributionAdoptCmd() *cobra.Command {
	cfg := cloudinstall.ConfigFromEnv(os.LookupEnv)
	repoOptions := newRepositoryCommandOptions()
	yes := false
	cmd := &cobra.Command{
		Use:   "adopt <distribution@version>",
		Short: "Record an existing distribution as installed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := repoOptions.validate(cmd); err != nil {
				return err
			}
			repo, err := distribution.OpenRepository(cmd.Context(), repoOptions.config)
			if err != nil {
				return err
			}
			manifest, err := repo.Load(args[0])
			if err != nil {
				return err
			}
			status, err := repo.Status()
			if err != nil {
				return err
			}
			targetID, err := distributionTargetID(cfg)
			if err != nil {
				return err
			}
			store, err := distribution.NewStateStore(cfg.StateDir, targetID)
			if err != nil {
				return err
			}
			unlock, err := store.Lock(cmd.Context())
			if err != nil {
				return err
			}
			defer unlock()
			if _, err := store.Load(); err == nil && !yes {
				return errors.New("package state already exists; use --yes to replace it")
			} else if err != nil && !errors.Is(err, distribution.ErrPackageStateNotFound) {
				return err
			}
			packages, err := cloudinstall.InstalledPackages(manifest, cfg.PackageMode, cfg.CiliumVersion)
			if err != nil {
				return err
			}
			for index := range packages {
				packages[index].Status = distribution.PackageStatusAdopted
			}
			if err := store.Save(&distribution.State{
				Target: distribution.TargetMetadata{
					Cluster: cfg.ClusterName,
					Masters: cfg.Masters,
					Nodes:   cfg.Nodes,
					User:    cfg.User,
					SSHPort: cfg.SSHPort,
				},
				Distribution:        manifest.Ref(),
				ManifestFingerprint: manifest.Fingerprint(),
				RepositoryCommit:    status.Commit,
				Packages:            packages,
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "adopted distribution: %s\n", manifest.Ref())
			return nil
		},
	}
	addDistributionStateFlags(cmd, &cfg, repoOptions)
	cmd.Flags().BoolVar(&yes, "yes", false, "replace an existing package state")
	return cmd
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
	addDistributionStateFlags(cmd, &cfg, nil)
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
