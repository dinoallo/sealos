// Copyright © 2026 sealos.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"fmt"

	"github.com/labring/sealos/pkg/distribution"
	"github.com/spf13/cobra"
)

type repositoryCommandOptions struct {
	config              distribution.RepositoryConfig
	configErr           error
	initialCacheDir     string
	initialDefaultCache string
}

func newRepositoryCommandOptions() *repositoryCommandOptions {
	config, err := distribution.LoadRepositoryConfig()
	if err != nil {
		config = distribution.DefaultRepositoryConfig()
	}
	return &repositoryCommandOptions{
		config:              config,
		configErr:           err,
		initialCacheDir:     config.CacheDir,
		initialDefaultCache: distribution.RepositoryCachePathForRef(config.URL, config.Ref, distribution.DefaultRepositoryCacheRoot()),
	}
}

func (o *repositoryCommandOptions) addFlags(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVar(&o.config.URL, "repo", o.config.URL, "Git package repository URL")
	flags.StringVar(&o.config.Ref, "repo-ref", o.config.Ref, "Git package repository ref")
	flags.StringVar(&o.config.CacheDir, "repo-cache", o.config.CacheDir, "local package repository cache directory")
	flags.BoolVar(&o.config.Refresh, "refresh", false, "refresh the package repository before running the command")
	flags.BoolVar(&o.config.Offline, "offline", false, "use the local package repository cache without network access")
}

func (o *repositoryCommandOptions) validate(cmd *cobra.Command) error {
	if o.configErr != nil {
		return o.configErr
	}
	if !cmd.Flags().Changed("repo-cache") && o.initialCacheDir == o.initialDefaultCache &&
		(cmd.Flags().Changed("repo") || cmd.Flags().Changed("repo-ref")) {
		o.config.CacheDir = distribution.RepositoryCachePathForRef(o.config.URL, o.config.Ref, distribution.DefaultRepositoryCacheRoot())
	}
	return nil
}

func newRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Manage the Sealos package repository",
	}
	cmd.AddCommand(newRepoSyncCmd(), newRepoStatusCmd())
	return cmd
}

func newRepoSyncCmd() *cobra.Command {
	opts := newRepositoryCommandOptions()
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync the package repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(cmd); err != nil {
				return err
			}
			repo, err := distribution.SyncRepository(cmd.Context(), opts.config)
			if err != nil {
				return err
			}
			status, err := repo.Status()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "synced %s at %s\n", status.URL, status.Commit)
			return nil
		},
	}
	opts.addFlags(cmd)
	return cmd
}

func newRepoStatusCmd() *cobra.Command {
	opts := newRepositoryCommandOptions()
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the package repository cache status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(cmd); err != nil {
				return err
			}
			status, err := distribution.RepositoryStatusFor(opts.config)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "url: %s\nref: %s\ncache: %s\navailable: %t\nstale: %t\n", status.URL, status.Ref, status.Root, status.Available, status.Stale)
			if status.Commit != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "commit: %s\n", status.Commit)
			}
			return nil
		},
	}
	opts.addFlags(cmd)
	return cmd
}
