package processor

import (
	"fmt"

	"github.com/labring/sealos/pkg/clusterfile"
	"github.com/labring/sealos/pkg/runtime"
	"github.com/labring/sealos/pkg/runtime/factory"
	v2 "github.com/labring/sealos/pkg/types/v1beta1"
	"github.com/labring/sealos/pkg/utils/logger"
)

type UpgradeProcessor struct {
	ClusterFile clusterfile.Interface
	Runtime     runtime.Interface
}

func NewUpgradeProcessor(clusterFile clusterfile.Interface, name string) (Interface, error) {
	return &UpgradeProcessor{
		ClusterFile: clusterFile,
	}, nil
}

func (c *UpgradeProcessor) Execute(cluster *v2.Cluster) error {
	pipLine, err := c.GetPipeLine()
	if err != nil {
		return err
	}

	for _, f := range pipLine {
		if err = f(cluster); err != nil {
			return err
		}
	}

	return nil
}

func (c *UpgradeProcessor) GetPipeLine() ([]func(cluster *v2.Cluster) error, error) {
	var todoList []func(cluster *v2.Cluster) error
	todoList = append(todoList,
		c.InitRuntime,
		c.UpgradeKubeletConfig,
		c.UpgradeControlPlane) //TODO: pipeline item here
	return todoList, nil
}

func (c *UpgradeProcessor) UpgradeKubeletConfig(cluster *v2.Cluster) error {
	var err error
	logger.Info("Executing UpgradeKubeletConfig Pipeline in InstallProcessor")
	err = c.Runtime.UpgradeKubeletConfig()
	if err != nil {
		logger.Error("failed to upgrade the current cluster")
		return err
	}
	return nil
}

func (c *UpgradeProcessor) InitRuntime(cluster *v2.Cluster) error {
	rt, err := factory.New(cluster, c.ClusterFile.GetRuntimeConfig())
	if err != nil {
		return fmt.Errorf("failed to init runtime, %v", err)
	}
	c.Runtime = rt
	return nil
}

func (c *UpgradeProcessor) UpgradeControlPlane(cluster *v2.Cluster) error {
	var err error
	logger.Info("Executing UpgradeKubeadmConfig Pipeline in InstallProcessor")
	err = c.Runtime.UpgradeControlPlane()
	if err != nil {
		logger.Error("failed to upgrade the current cluster")
		return err
	}
	return nil
}
