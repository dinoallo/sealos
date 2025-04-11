package applydrivers

import (
	"context"
	"fmt"

	"github.com/labring/sealos/pkg/apply/processor"
	"github.com/labring/sealos/pkg/clusterfile"
	"github.com/labring/sealos/pkg/constants"
	v2 "github.com/labring/sealos/pkg/types/v1beta1"
	"github.com/labring/sealos/pkg/utils/logger"
)

func NewDefaultUpgradeApplier(ctx context.Context, current, cluster *v2.Cluster) (Interface, error) {
	if cluster.Name == "" {
		cluster.Name = current.Name
	}
	cFile := clusterfile.NewClusterFile(constants.Clusterfile(cluster.Name))
	return &UpgradeApplier{
		Context:        ctx,
		ClusterFile:    cFile,
		ClusterCurrent: current,
	}, nil
}

type UpgradeApplier struct {
	context.Context
	ClusterCurrent *v2.Cluster
	ClusterFile    clusterfile.Interface
}

func (c *UpgradeApplier) Apply() error {
	var clusterErr error
	/* defer func() {
		var checkError *processor.CheckError
		var preProcessError *processor.PreProcessError
		switch {
		case errors.As(clusterErr, &checkError):
			return
		case errors.As(clusterErr, &preProcessError):
			return
		}
		c.applyAfter()
	}() */
	if c.ClusterCurrent == nil || c.ClusterCurrent.CreationTimestamp.IsZero() {
		clusterErr = processor.NewPreProcessError(fmt.Errorf("there is no cluster existing currently. canceled upgrading cluster"))
		return nil
	}
	clusterErr = c.upgradeCluster()
	// c.updateStatus(clusterErr)
	return clusterErr
}

func (c *UpgradeApplier) upgradeCluster() (clusterErr error) {
	/* // sync newVersion pki and etc dir in `.sealos/default/pki` and `.sealos/default/etc`
	processor.SyncNewVersionConfig(c.ClusterCurrent.Name) */
	logger.Info("start to upgrade the current cluster")
	logger.Debug("current cluster: master %s, worker %s", c.ClusterCurrent.GetMasterIPAndPortList(), c.ClusterCurrent.GetNodeIPAndPortList())
	localpath := constants.Clusterfile(c.ClusterCurrent.Name)
	cf := clusterfile.NewClusterFile(localpath)
	upgradeProcessor, err := processor.NewUpgradeProcessor(cf, c.ClusterCurrent.Name)
	if err != nil {
		return err
	}
	cluster := c.ClusterCurrent
	err = upgradeProcessor.Execute(cluster)
	if err != nil {
		return err
	}
	//TODO: main logic here
	logger.Info("successfully upgraded the current cluster")
	return nil
}

func (c *UpgradeApplier) Delete() error {
	return nil
}
