package apply

import (
	"fmt"

	"github.com/labring/sealos/pkg/apply/applydrivers"
	"github.com/labring/sealos/pkg/clusterfile"
	"github.com/labring/sealos/pkg/constants"
	v2 "github.com/labring/sealos/pkg/types/v1beta1"
	fileutil "github.com/labring/sealos/pkg/utils/file"
	"github.com/spf13/cobra"
)

func NewUpgradeApplierFromArgs(cmd *cobra.Command, upgradeArgs *UpgradeArgs) (applydrivers.Interface, error) {
	var cluster *v2.Cluster
	clusterPath := constants.Clusterfile(upgradeArgs.Cluster.ClusterName)

	if !fileutil.IsExist(clusterPath) {
		cluster = initCluster(upgradeArgs.Cluster.ClusterName)
	} else {
		clusterFile := clusterfile.NewClusterFile(clusterPath)
		err := clusterFile.Process()
		if err != nil {
			return nil, err
		}
		cluster = clusterFile.GetCluster()
	}

	curr := cluster.DeepCopy()

	if upgradeArgs.Cluster.Nodes == "" && upgradeArgs.Cluster.Masters == "" {
		return nil, fmt.Errorf("the node or master parameter was not committed")
	}
	/* var err error
	switch cmd.Name() {
	case "add":
		err = verifyAndSetNodes(cmd, cluster, upgradeArgs)
	case "delete":
		err = Delete(cluster, upgradeArgs)
	}
	if err != nil {
		return nil, err
	} */

	return applydrivers.NewDefaultUpgradeApplier(cmd.Context(), curr, cluster)
}
