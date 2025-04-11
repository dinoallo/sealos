/*
Copyright 2025 sealos.io.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cmd

import (
	"errors"

	"github.com/labring/sealos/pkg/apply"
	"github.com/labring/sealos/pkg/utils/logger"
	"github.com/spf13/cobra"
)

// TODO: fix me
const upgradeExampleText = `
Upgrade all nodes:
sealos upgrade all
Upgrade only the control plane nodes:
sealos upgrade control-planes
Upgrade only the non control plane nodes:
sealos upgrade nodes


sealos upgrade --masters x.x.x.x --nodes y.y.y.y
`

func newUpgradeCmd() *cobra.Command {
	//upgradeArgs := &apply.UpgradeArgs{}
	upgradeArgs := &apply.ScaleArgs{}
	var upgradeCmd = &cobra.Command{
		Use:     "upgrade",
		Short:   "Upgrade your Sealos cluster",
		Args:    cobra.NoArgs,
		Example: upgradeExampleText,
		RunE: func(cmd *cobra.Command, args []string) error {
			applier, err := apply.NewScaleApplierFromArgs(cmd, upgradeArgs)
			if err != nil {
				return err
			}
			return applier.Apply()
		},
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if upgradeArgs.Nodes == "" && upgradeArgs.Masters == "" {
				return errors.New("nodes and masters can't both be empty")
			}
			return nil
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			logger.Info(getContact())
		},
	}

	upgradeArgs.RegisterFlags(upgradeCmd.Flags(), "upgrade", "upgrading")
	return upgradeCmd
}
