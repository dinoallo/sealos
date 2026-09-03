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
		Short: "Inspect and resolve versioned installation distributions",
	}
	cmd.AddCommand(newDistributionListCmd())
	cmd.AddCommand(newDistributionShowCmd())
	cmd.AddCommand(newDistributionResolveCmd())
	cmd.AddCommand(newDistributionValidateCmd())
	cmd.AddCommand(newDistributionInstallCmd())
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
			runner, err := cloudinstall.NewCommandRunner(cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			var log io.WriteCloser
			if !cfg.DryRun {
				if err := os.MkdirAll(cfg.ConfigDir, 0o700); err != nil {
					return fmt.Errorf("create installer config directory: %w", err)
				}
				log, err = os.OpenFile(filepath.Join(cfg.ConfigDir, "install.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					return fmt.Errorf("open installer log: %w", err)
				}
				defer log.Close()
			}
			installer := &cloudinstall.Installer{Config: cfg, Runner: runner, Stdout: cmd.OutOrStdout(), Log: log}
			if err := installer.Install(cmd.Context(), manifest); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Sealos Cloud installation completed")
			return nil
		},
	}

	flags := cmd.Flags()
	flags.BoolVar(&interactive, "interactive", interactive, "prompt for missing installation values")
	repoOptions.addFlags(cmd)
	flags.StringVarP(&cfg.Distribution, "distribution", "d", cfg.Distribution, "distribution reference, for example cloud-pro@v5.1.2-rc6")
	flags.StringVar((*string)(&cfg.PackageMode), "package-mode", string(cfg.PackageMode), "resolve packages from remote images, source builds, or hybrid source/remote mode")
	flags.StringVar(&cfg.SourceRoot, "source-root", cfg.SourceRoot, "compatibility base for relative local package source paths")
	flags.StringVar(&cfg.SourceCache, "source-cache", cfg.SourceCache, "persistent cache directory for Git package sources")
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
	flags.DurationVar(&cfg.WaitTimeout, "wait-timeout", cfg.WaitTimeout, "maximum time to wait for cluster resources")
	return cmd
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
