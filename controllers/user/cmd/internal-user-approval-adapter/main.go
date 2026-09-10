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

// Command internal-user-approval-adapter is a skeleton entry point for
// implementing a custom external approval adapter. It wires the reusable
// BrokerHTTPClient (approval submitter) and EmailNotifier (notification
// delivery), then starts an HTTPS server that exposes a /healthz endpoint.
//
// To build a functioning adapter, implement approvaladapter.ApprovalSource
// (GetInstance and GetUserEmail) and wire it into the run() function below.
// The ApprovalSource is responsible for polling or receiving events from
// the external approval system, fetching authoritative approval data, and
// resolving user email addresses.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/labring/sealos/controllers/user/pkg/approvaladapter"
)

type config struct {
	ListenAddress string
	TLSCertFile   string
	TLSKeyFile    string

	BrokerURL        string
	BrokerCAFile     string
	BrokerServerName string
	ClientCertFile   string
	ClientKeyFile    string

	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPSubject  string
}

func main() {
	config := config{
		ListenAddress:  envOr("INTERNAL_USER_APPROVAL_ADAPTER_LISTEN_ADDRESS", ":8443"),
		BrokerURL:      os.Getenv("BROKER_URL"),
		BrokerCAFile:   os.Getenv("BROKER_CA_FILE"),
		BrokerServerName: os.Getenv("BROKER_SERVER_NAME"),
		ClientCertFile: os.Getenv("BROKER_CLIENT_CERT_FILE"),
		ClientKeyFile:  os.Getenv("BROKER_CLIENT_KEY_FILE"),
		SMTPHost:       os.Getenv("SMTP_HOST"),
		SMTPPort:       envIntOr("SMTP_PORT", 587),
		SMTPUsername:   os.Getenv("SMTP_USERNAME"),
		SMTPPassword:   os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:       os.Getenv("SMTP_FROM"),
		SMTPSubject:    envOr("SMTP_SUBJECT", "Internal Kubernetes credential approval"),
	}
	flags := flag.NewFlagSet("internal-user-approval-adapter", flag.ExitOnError)
	flags.StringVar(&config.ListenAddress, "listen-address", config.ListenAddress, "HTTPS listen address.")
	flags.StringVar(&config.TLSCertFile, "tls-cert-file", os.Getenv("ADAPTER_TLS_CERT_FILE"), "HTTPS server certificate file.")
	flags.StringVar(&config.TLSKeyFile, "tls-key-file", os.Getenv("ADAPTER_TLS_KEY_FILE"), "HTTPS server private key file.")
	flags.StringVar(&config.BrokerURL, "broker-url", config.BrokerURL, "HTTPS URL of the internal credential Broker.")
	flags.StringVar(&config.BrokerCAFile, "broker-ca-file", config.BrokerCAFile, "CA file used to verify the Broker certificate.")
	flags.StringVar(&config.BrokerServerName, "broker-server-name", config.BrokerServerName, "TLS server name used for the Broker connection.")
	flags.StringVar(&config.ClientCertFile, "client-cert-file", config.ClientCertFile, "mTLS client certificate for approval submission.")
	flags.StringVar(&config.ClientKeyFile, "client-key-file", config.ClientKeyFile, "mTLS client private key for approval submission.")
	flags.StringVar(&config.SMTPHost, "smtp-host", config.SMTPHost, "SMTP relay host.")
	flags.IntVar(&config.SMTPPort, "smtp-port", config.SMTPPort, "SMTP relay port.")
	flags.StringVar(&config.SMTPUsername, "smtp-username", config.SMTPUsername, "Optional SMTP username.")
	flags.StringVar(&config.SMTPFrom, "smtp-from", config.SMTPFrom, "Email sender address.")
	flags.StringVar(&config.SMTPSubject, "smtp-subject", config.SMTPSubject, "Email subject line.")
	_ = flags.Parse(os.Args[1:])
	if err := config.validate(); err != nil {
		fatal(err)
	}
	if err := run(context.Background(), config); err != nil {
		fatal(err)
	}
}

func (c config) validate() error {
	if c.TLSCertFile == "" || c.TLSKeyFile == "" {
		return fmt.Errorf("adapter TLS certificate and key are required")
	}
	if err := validateHTTPSURL(c.BrokerURL); err != nil {
		return fmt.Errorf("invalid Broker URL: %w", err)
	}
	if c.BrokerCAFile == "" || c.ClientCertFile == "" || c.ClientKeyFile == "" {
		return fmt.Errorf("Broker CA, client certificate, and client key are required")
	}
	if c.SMTPHost == "" || c.SMTPPort <= 0 || c.SMTPFrom == "" {
		return fmt.Errorf("SMTP host, positive port, and sender are required")
	}
	return nil
}

func run(ctx context.Context, config config) error {
	// Create the Broker mTLS submitter.
	brokerHTTPClient, err := newHTTPClient(config.BrokerCAFile, config.BrokerServerName, config.ClientCertFile, config.ClientKeyFile)
	if err != nil {
		return fmt.Errorf("create Broker HTTP client: %w", err)
	}
	approver, err := approvaladapter.NewBrokerHTTPClient(config.BrokerURL, brokerHTTPClient)
	if err != nil {
		return err
	}

	// Create the email notifier.
	notifier := &approvaladapter.EmailNotifier{
		SMTPHost: config.SMTPHost, SMTPPort: config.SMTPPort, SMTPUsername: config.SMTPUsername,
		SMTPPassword: config.SMTPPassword, From: config.SMTPFrom, Subject: config.SMTPSubject,
	}

	// TODO: Implement an approvaladapter.ApprovalSource and wire it here.
	//
	//   source := &myApprovalSource{...}
	//   go source.Poll(ctx, approver, notifier)
	//
	// See the approval adapter documentation and the ApprovalSource interface
	// in controllers/user/pkg/approvaladapter/adapter.go for details.
	_ = approver
	_ = notifier

	// Serve a health check endpoint. Add custom HTTP handlers as needed.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	server := &http.Server{
		Addr: config.ListenAddress, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ListenAndServeTLS(config.TLSCertFile, config.TLSKeyFile)
	}()
	signalContext, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-signalContext.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownContext)
	case err := <-serverErr:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func newHTTPClient(caFile, serverName, certFile, keyFile string) (*http.Client, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		transport = &http.Transport{}
	}
	transport = transport.Clone()
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
	if caFile != "" {
		data, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("CA file contains no certificates")
		}
		tlsConfig.RootCAs = pool
	}
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return nil, fmt.Errorf("client certificate and key must be supplied together")
		}
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	transport.TLSClientConfig = tlsConfig
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}, nil
}

func validateHTTPSURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("must be an absolute HTTPS URL without query or fragment")
	}
	return nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envIntOr(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		fatal(fmt.Errorf("invalid %s: %w", name, err))
	}
	return parsed
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
