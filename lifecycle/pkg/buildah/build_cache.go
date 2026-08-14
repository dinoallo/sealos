// Copyright © 2026 Sealos.
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

package buildah

import (
	"context"
	"fmt"

	"github.com/containers/buildah/pkg/parse"
	"github.com/containers/common/libimage"
	"github.com/hashicorp/go-multierror"
	"github.com/spf13/cobra"
)

type buildCacheCleanOptions struct {
	all   bool
	force bool
}

func newBuildCacheCommand() *cobra.Command {
	buildCacheCommand := &cobra.Command{
		Use:   "build-cache",
		Short: "Manage the local image build cache",
	}
	buildCacheCommand.SetUsageTemplate(UsageTemplate())

	var opts buildCacheCleanOptions
	cleanCommand := &cobra.Command{
		Use:     "clean",
		Aliases: []string{"prune"},
		Short:   "Clean the local image build cache",
		Long:    "Remove intermediate image layers and the Buildah mount cache.",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return buildCacheCleanCmd(cmd, opts)
		},
		Example: fmt.Sprintf(`%[1]s build-cache clean
  %[1]s build-cache clean --all --force`, rootCmd.CommandPath()),
	}
	cleanCommand.SetUsageTemplate(UsageTemplate())
	cleanCommand.Flags().BoolVarP(&opts.all, "all", "a", false, "remove all unused images")
	cleanCommand.Flags().BoolVarP(&opts.force, "force", "f", false, "force removal of images and any containers using them")

	buildCacheCommand.AddCommand(cleanCommand)
	return buildCacheCommand
}

func buildCacheCleanCmd(c *cobra.Command, opts buildCacheCleanOptions) error {
	store, err := getStore(c)
	if err != nil {
		return err
	}

	systemContext, err := parse.SystemContextFromOptions(c)
	if err != nil {
		return err
	}
	runtime, err := libimage.RuntimeFromStore(store, &libimage.RuntimeOptions{SystemContext: systemContext})
	if err != nil {
		return err
	}

	if err := parse.CleanCacheMount(); err != nil {
		return fmt.Errorf("clean Buildah mount cache: %w", err)
	}

	rmiReports, rmiErrors := runtime.RemoveImages(context.Background(), nil, buildCacheRemoveImagesOptions(opts))
	for _, report := range rmiReports {
		for _, untagged := range report.Untagged {
			fmt.Fprintf(c.OutOrStdout(), "untagged: %s\n", untagged)
		}
		if report.Removed {
			fmt.Fprintln(c.OutOrStdout(), report.ID)
		}
	}

	var multiE *multierror.Error
	multiE = multierror.Append(multiE, rmiErrors...)
	return multiE.ErrorOrNil()
}

func buildCacheRemoveImagesOptions(opts buildCacheCleanOptions) *libimage.RemoveImagesOptions {
	removeOptions := &libimage.RemoveImagesOptions{
		Filters: []string{"readonly=false"},
		Force:   opts.force,
	}
	if !opts.all {
		removeOptions.Filters = append(removeOptions.Filters, "dangling=true", "intermediate=true")
	}
	return removeOptions
}
