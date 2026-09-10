/*
Copyright 2026 labring.

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

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/labring/sealos/controllers/user/api/v1"
	"github.com/labring/sealos/controllers/user/pkg/broker"
	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func main() {
	var address, oidcIssuer, oidcAudience, oidcJWKSURL, adminGroup, kubernetesAudience, clusterServer string
	var internalClientCAFile string
	var tlsCertFile, tlsKeyFile, oidcCAFile, oidcServerName string
	var requestRateLimit float64
	var requestRateBurst int
	flag.StringVar(&address, "listen-address", ":8443", "The address the Broker listens on.")
	flag.StringVar(&oidcIssuer, "oidc-issuer", os.Getenv("BROKER_OIDC_ISSUER"), "The configured HTTPS OIDC issuer.")
	flag.StringVar(&oidcAudience, "oidc-audience", os.Getenv("BROKER_OIDC_AUDIENCE"), "The configured OIDC audience.")
	flag.StringVar(&oidcJWKSURL, "oidc-jwks-url", os.Getenv("BROKER_OIDC_JWKS_URL"), "Optional configured OIDC JWKS URL.")
	flag.StringVar(&oidcCAFile, "oidc-ca-file", os.Getenv("BROKER_OIDC_CA_FILE"), "Optional CA file used to verify the OIDC issuer.")
	flag.StringVar(&oidcServerName, "oidc-server-name", os.Getenv("BROKER_OIDC_SERVER_NAME"), "Optional TLS server name used for OIDC requests.")
	flag.StringVar(&adminGroup, "admin-group", envOr("BROKER_ADMIN_GROUP", "sealos.internal.admin"), "The OIDC group allowed to use the admin endpoint.")
	flag.StringVar(&kubernetesAudience, "kubernetes-audience", os.Getenv("BROKER_KUBERNETES_AUDIENCE"), "The server-fixed audience for issued Kubernetes tokens.")
	flag.StringVar(&clusterServer, "cluster-server", os.Getenv("BROKER_CLUSTER_SERVER"), "The Kubernetes API server URL embedded in returned kubeconfigs.")
	flag.StringVar(&internalClientCAFile, "internal-client-ca-file", os.Getenv("BROKER_INTERNAL_CLIENT_CA_FILE"), "Optional CA file for mTLS clients submitting approved records.")
	flag.StringVar(&tlsCertFile, "tls-cert-file", os.Getenv("BROKER_TLS_CERT_FILE"), "TLS certificate file.")
	flag.StringVar(&tlsKeyFile, "tls-key-file", os.Getenv("BROKER_TLS_KEY_FILE"), "TLS private key file.")
	flag.Float64Var(&requestRateLimit, "request-rate-limit", envFloatOr("BROKER_REQUEST_RATE_LIMIT", 10), "Maximum Broker requests per second.")
	flag.IntVar(&requestRateBurst, "request-rate-burst", envIntOr("BROKER_REQUEST_RATE_BURST", 20), "Maximum burst of Broker requests.")
	flag.Parse()
	if oidcIssuer == "" || oidcAudience == "" || kubernetesAudience == "" || clusterServer == "" || tlsCertFile == "" || tlsKeyFile == "" {
		fatal("oidc issuer/audience, Kubernetes audience, cluster server, and TLS certificate/key are required")
	}

	trustedProxyCIDRs, err := broker.ParseCIDRs(splitEnv("BROKER_TRUSTED_PROXY_CIDRS"))
	if err != nil {
		fatal(err.Error())
	}
	allowedClientCIDRs, err := broker.ParseCIDRs(splitEnv("BROKER_ALLOWED_CLIENT_CIDRS"))
	if err != nil {
		fatal(err.Error())
	}
	oidcClient, err := oidcHTTPClient(oidcCAFile, oidcServerName)
	if err != nil {
		fatal(err.Error())
	}
	verifier, err := broker.NewOIDCVerifier(broker.OIDCConfig{Issuer: oidcIssuer, Audience: oidcAudience, JWKSURL: oidcJWKSURL, HTTPClient: oidcClient})
	if err != nil {
		fatal(err.Error())
	}
	var internalClientCA *x509.CertPool
	if internalClientCAFile != "" {
		data, err := os.ReadFile(internalClientCAFile)
		if err != nil {
			fatal(fmt.Sprintf("read internal client CA file: %v", err))
		}
		internalClientCA = x509.NewCertPool()
		if !internalClientCA.AppendCertsFromPEM(data) {
			fatal("internal client CA file contains no certificates")
		}
	}

	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		fatal(err.Error())
	}
	clusterCAData := append([]byte(nil), kubeConfig.CAData...)
	if len(clusterCAData) == 0 && kubeConfig.CAFile != "" {
		clusterCAData, err = os.ReadFile(kubeConfig.CAFile)
		if err != nil {
			fatal(fmt.Sprintf("read Kubernetes CA file: %v", err))
		}
	}
	kubeClientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		fatal(err.Error())
	}
	kubeScheme := runtime.NewScheme()
	if err := v1.AddToScheme(kubeScheme); err != nil {
		fatal(err.Error())
	}
	leaseClient, err := client.New(kubeConfig, client.Options{Scheme: kubeScheme})
	if err != nil {
		fatal(err.Error())
	}
	server, err := broker.NewServer(leaseClient, verifier, &broker.KubernetesTokenIssuer{Client: kubeClientset}, broker.Config{
		AllowedIssuers:     map[string]struct{}{oidcIssuer: {}},
		AdminGroup:         adminGroup,
		KubernetesAudience: kubernetesAudience,
		ClusterServer:      clusterServer,
		ClusterCAData:      clusterCAData,
		SourceIP: broker.SourceIPPolicy{
			TrustedProxyCIDRs:     trustedProxyCIDRs,
			AllowedClientCIDRs:    allowedClientCIDRs,
			TrustedClientIPHeader: envOr("BROKER_TRUSTED_CLIENT_IP_HEADER", "X-Trusted-Client-IP"),
		},
		InternalClientCA: internalClientCA,
		RequestRateLimit: rate.Limit(requestRateLimit),
		RequestRateBurst: requestRateBurst,
	})
	if err != nil {
		fatal(err.Error())
	}
	server.Audit = &broker.LogAuditSink{Writer: os.Stderr}
	httpServer := &http.Server{Addr: address, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequestClientCert}}
	go func() {
		if err := httpServer.ListenAndServeTLS(tlsCertFile, tlsKeyFile); err != nil && err != http.ErrServerClosed {
			fatal(err.Error())
		}
	}()
	signalContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-signalContext.Done()
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownContext)
}

func splitEnv(name string) []string {
	value := os.Getenv(name)
	if value == "" {
		return nil
	}
	values := strings.Split(value, ",")
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envFloatOr(name string, fallback float64) float64 {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		fatal(fmt.Sprintf("invalid %s: %v", name, err))
	}
	return parsed
}

func envIntOr(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		fatal(fmt.Sprintf("invalid %s: %v", name, err))
	}
	return parsed
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}

func oidcHTTPClient(caFile, serverName string) (*http.Client, error) {
	if caFile == "" && serverName == "" {
		return nil, nil
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		transport = &http.Transport{}
	}
	transport = transport.Clone()
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
	if caFile != "" {
		data, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read OIDC CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("OIDC CA file contains no certificates")
		}
		tlsConfig.RootCAs = pool
	}
	transport.TLSClientConfig = tlsConfig
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}, nil
}
