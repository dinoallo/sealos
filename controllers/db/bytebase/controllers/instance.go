package controllers

import (
	"context"
	"fmt"
	bbv1 "github.com/labring/sealos/controllers/db/bytebase/api/v1"
	api "github.com/labring/sealos/controllers/db/bytebase/client/api"
	acidv1 "github.com/zalando/postgres-operator/pkg/apis/acid.zalan.do/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
)

const (
	defaultEnvironmentID  string             = "prod"
	defaultDataSourceType api.DataSourceType = "ADMIN"
)

func (r *BytebaseReconciler) syncInstance(ctx context.Context, req ctrl.Request, bb *bbv1.Bytebase) error {
	c := r.Bc
	logger := r.Logger
	/// check the default environment exists
	if _, err := c.GetEnvironment(ctx, defaultEnvironmentID); err != nil {
		errorMessage := "failed to get the default environment. No environment to set up instances at this time"
		logger.Error(err, errorMessage)
		return err
	}
	if err := r.syncPostgresInstance(ctx, req, bb); err != nil {
		errorMessage := "failed to set up postgres instance"
		logger.Error(err, errorMessage)
		return err
	}
	return nil
}

func (r *BytebaseReconciler) syncPostgresInstance(ctx context.Context, req ctrl.Request, bb *bbv1.Bytebase) error {
	logger := r.Logger
	c := r.Bc
	var (
		dataSourceType       api.DataSourceType
		dataSourceUserName   string
		dataSourceUserPasswd string
		dataSourceHost       string
		dataSourcePort       string
	)
	dataSourceType = api.DataSourceType(defaultDataSourceType)
	pgInstanceList := acidv1.PostgresqlList{}
	if err := r.List(ctx, &pgInstanceList, client.InNamespace(req.Namespace)); err != nil {
		logger.Error(err, "failed to get postgresql instance. Make sure postgresql instances are running")
		return err
	}
	logger.Info("ready to initialize database...")

	for _, instance := range pgInstanceList.Items {

		// get database credentials
		instanceName := instance.ObjectMeta.Name
		// logger.Info("Find instance: %v", instanceName)
		secret := corev1.Secret{}
		secretName := fmt.Sprintf("zalando.%s.credentials.postgresql.acid.zalan.do", instanceName)
		r.Get(ctx, client.ObjectKey{
			Namespace: req.Namespace,
			Name:      secretName,
		}, &secret)

		dataSourceUserName = string(secret.Data["username"])
		dataSourceUserPasswd = string(secret.Data["password"])

		// get database service
		svc := corev1.Service{}

		svcName := instanceName
		r.Get(ctx, client.ObjectKey{
			Namespace: req.Namespace,
			Name:      svcName,
		}, &svc)

		dataSourceHost = svc.Spec.ClusterIP
		// dataSourceHost = "192.168.2.29" // for testing
		ports := svc.Spec.Ports
		for _, p := range ports {
			if p.Name == "postgresql" {
				dataSourcePort = strconv.FormatInt(int64(p.Port), 10)
				break
			}
		}
		environmentID := defaultEnvironmentID

		// dataSourcePort = strconv.FormatInt(30009, 10) // for testing
		ifm := api.InstanceFindMessage{
			EnvironmentID: environmentID,
			InstanceID:    instanceName,
			ShowDeleted:   false,
		}
		logger.Info("try to fetch instance...")
		if _, err := c.GetInstance(ctx, &ifm); err == nil {
			logger.Info("fetch instance success, skipping...")
			continue
		}
		// register instances to bytebase
		/// Create instance
		dsm := api.DataSourceMessage{
			Title:    "foo",
			Type:     dataSourceType,
			Username: dataSourceUserName,
			Password: dataSourceUserPasswd,
			Host:     dataSourceHost,
			Port:     dataSourcePort,
			Database: "foo",
		}
		dataSources := []*api.DataSourceMessage{&dsm}
		im := api.InstanceMessage{
			UID:         instanceName,
			Name:        instanceName,
			Engine:      api.EngineTypePostgres,
			DataSources: dataSources,
			Title:       instanceName,
		}
		logger.Info("the instance doesn't exists, try to create one...")

		if _, err := c.CreateInstance(ctx, environmentID, instanceName, &im); err != nil {
			logger.Error(err, "failed to add an instance.")
			return err
		}
	}
	return nil

}
