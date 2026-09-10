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

	FeishuBaseURL           string
	FeishuAppID             string
	FeishuAppSecret         string
	FeishuVerificationToken string
	FeishuEncryptKey        string
	FeishuApprovalCode      string
	FeishuUserIDType        string
	FeishuCAFile            string
	FeishuServerName        string

	TargetIssuer string
	SubjectField string
	TTLField     string
	ReasonField  string

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
		ListenAddress:           envOr("INTERNAL_USER_APPROVAL_ADAPTER_LISTEN_ADDRESS", ":8443"),
		FeishuBaseURL:           envOr("FEISHU_BASE_URL", approvaladapter.DefaultFeishuBaseURL),
		FeishuAppID:             os.Getenv("FEISHU_APP_ID"),
		FeishuAppSecret:         os.Getenv("FEISHU_APP_SECRET"),
		FeishuVerificationToken: os.Getenv("FEISHU_VERIFICATION_TOKEN"),
		FeishuEncryptKey:        os.Getenv("FEISHU_ENCRYPT_KEY"),
		FeishuApprovalCode:      os.Getenv("FEISHU_APPROVAL_CODE"),
		FeishuUserIDType:        envOr("FEISHU_USER_ID_TYPE", "open_id"),
		FeishuCAFile:            os.Getenv("FEISHU_CA_FILE"),
		FeishuServerName:        os.Getenv("FEISHU_SERVER_NAME"),
		TargetIssuer:            os.Getenv("INTERNAL_USER_TARGET_ISSUER"),
		SubjectField:            envOr("FEISHU_SUBJECT_FIELD", approvaladapter.DefaultSubjectField),
		TTLField:                envOr("FEISHU_TTL_FIELD", approvaladapter.DefaultTTLField),
		ReasonField:             envOr("FEISHU_REASON_FIELD", approvaladapter.DefaultReasonField),
		BrokerURL:               os.Getenv("BROKER_URL"),
		BrokerCAFile:            os.Getenv("BROKER_CA_FILE"),
		BrokerServerName:        os.Getenv("BROKER_SERVER_NAME"),
		ClientCertFile:          os.Getenv("BROKER_CLIENT_CERT_FILE"),
		ClientKeyFile:           os.Getenv("BROKER_CLIENT_KEY_FILE"),
		SMTPHost:                os.Getenv("SMTP_HOST"),
		SMTPPort:                envIntOr("SMTP_PORT", 587),
		SMTPUsername:            os.Getenv("SMTP_USERNAME"),
		SMTPPassword:            os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:                os.Getenv("SMTP_FROM"),
		SMTPSubject:             envOr("SMTP_SUBJECT", "Internal Kubernetes credential approval"),
	}
	flags := flag.NewFlagSet("internal-user-approval-adapter", flag.ExitOnError)
	flags.StringVar(&config.ListenAddress, "listen-address", config.ListenAddress, "HTTPS listen address.")
	flags.StringVar(&config.TLSCertFile, "tls-cert-file", os.Getenv("ADAPTER_TLS_CERT_FILE"), "HTTPS server certificate file.")
	flags.StringVar(&config.TLSKeyFile, "tls-key-file", os.Getenv("ADAPTER_TLS_KEY_FILE"), "HTTPS server private key file.")
	flags.StringVar(&config.FeishuBaseURL, "feishu-base-url", config.FeishuBaseURL, "Feishu API base URL.")
	flags.StringVar(&config.FeishuAppID, "feishu-app-id", config.FeishuAppID, "Feishu application ID.")
	flags.StringVar(&config.FeishuAppSecret, "feishu-app-secret", config.FeishuAppSecret, "Feishu application secret.")
	flags.StringVar(&config.FeishuVerificationToken, "feishu-verification-token", config.FeishuVerificationToken, "Feishu callback verification token.")
	flags.StringVar(&config.FeishuEncryptKey, "feishu-encrypt-key", config.FeishuEncryptKey, "Optional Feishu callback encryption key for signature verification.")
	flags.StringVar(&config.FeishuApprovalCode, "feishu-approval-code", config.FeishuApprovalCode, "The only Feishu approval definition accepted by this adapter.")
	flags.StringVar(&config.FeishuUserIDType, "feishu-user-id-type", config.FeishuUserIDType, "Feishu user ID type used for API lookups.")
	flags.StringVar(&config.FeishuCAFile, "feishu-ca-file", config.FeishuCAFile, "Optional CA file used to verify Feishu API TLS.")
	flags.StringVar(&config.FeishuServerName, "feishu-server-name", config.FeishuServerName, "Optional TLS server name used for Feishu API requests.")
	flags.StringVar(&config.TargetIssuer, "target-issuer", config.TargetIssuer, "Fixed OIDC issuer for approved InternalUsers.")
	flags.StringVar(&config.SubjectField, "subject-field", config.SubjectField, "Feishu form field ID or name containing the OIDC subject.")
	flags.StringVar(&config.TTLField, "ttl-field", config.TTLField, "Feishu form field ID or name containing TTL seconds.")
	flags.StringVar(&config.ReasonField, "reason-field", config.ReasonField, "Feishu form field ID or name containing the approval reason.")
	flags.StringVar(&config.BrokerURL, "broker-url", config.BrokerURL, "HTTPS URL of the internal credential Broker.")
	flags.StringVar(&config.BrokerCAFile, "broker-ca-file", config.BrokerCAFile, "CA file used to verify the Broker certificate.")
	flags.StringVar(&config.BrokerServerName, "broker-server-name", config.BrokerServerName, "TLS server name used for Broker requests.")
	flags.StringVar(&config.ClientCertFile, "broker-client-cert-file", config.ClientCertFile, "mTLS client certificate for the Broker.")
	flags.StringVar(&config.ClientKeyFile, "broker-client-key-file", config.ClientKeyFile, "mTLS client private key for the Broker.")
	flags.StringVar(&config.SMTPHost, "smtp-host", config.SMTPHost, "SMTP relay host used to deliver references.")
	flags.IntVar(&config.SMTPPort, "smtp-port", config.SMTPPort, "SMTP relay port.")
	flags.StringVar(&config.SMTPUsername, "smtp-username", config.SMTPUsername, "SMTP username.")
	flags.StringVar(&config.SMTPPassword, "smtp-password", config.SMTPPassword, "SMTP password.")
	flags.StringVar(&config.SMTPFrom, "smtp-from", config.SMTPFrom, "Reference notification sender address.")
	flags.StringVar(&config.SMTPSubject, "smtp-subject", config.SMTPSubject, "Reference notification subject.")
	flags.Parse(os.Args[1:])
	if err := run(context.Background(), config); err != nil {
		fatal(err)
	}
}

func run(ctx context.Context, config config) error {
	if config.TLSCertFile == "" || config.TLSKeyFile == "" {
		return fmt.Errorf("adapter TLS certificate and key are required")
	}
	if config.FeishuAppID == "" || config.FeishuAppSecret == "" || config.FeishuVerificationToken == "" || config.FeishuApprovalCode == "" {
		return fmt.Errorf("Feishu app ID, app secret, verification token, and approval code are required")
	}
	if err := validateHTTPSURL(config.FeishuBaseURL); err != nil {
		return fmt.Errorf("invalid Feishu base URL: %w", err)
	}
	if err := validateHTTPSURL(config.BrokerURL); err != nil {
		return fmt.Errorf("invalid Broker URL: %w", err)
	}
	if config.BrokerCAFile == "" || config.ClientCertFile == "" || config.ClientKeyFile == "" {
		return fmt.Errorf("Broker CA, client certificate, and client key are required")
	}
	if config.SMTPHost == "" || config.SMTPPort <= 0 || config.SMTPFrom == "" {
		return fmt.Errorf("SMTP host, positive port, and sender are required")
	}

	feishuHTTPClient, err := newHTTPClient(config.FeishuCAFile, config.FeishuServerName, "", "")
	if err != nil {
		return fmt.Errorf("create Feishu HTTP client: %w", err)
	}
	feishu, err := approvaladapter.NewFeishuClient(approvaladapter.FeishuClientConfig{
		BaseURL: config.FeishuBaseURL, AppID: config.FeishuAppID, AppSecret: config.FeishuAppSecret,
		UserIDType: config.FeishuUserIDType, HTTPClient: feishuHTTPClient,
	})
	if err != nil {
		return err
	}
	brokerHTTPClient, err := newHTTPClient(config.BrokerCAFile, config.BrokerServerName, config.ClientCertFile, config.ClientKeyFile)
	if err != nil {
		return fmt.Errorf("create Broker HTTP client: %w", err)
	}
	approver, err := approvaladapter.NewBrokerHTTPClient(config.BrokerURL, brokerHTTPClient)
	if err != nil {
		return err
	}
	adapter, err := approvaladapter.New(feishu, approver, &approvaladapter.EmailNotifier{
		SMTPHost: config.SMTPHost, SMTPPort: config.SMTPPort, SMTPUsername: config.SMTPUsername,
		SMTPPassword: config.SMTPPassword, From: config.SMTPFrom, Subject: config.SMTPSubject,
	}, approvaladapter.Config{
		ApprovalCode: config.FeishuApprovalCode, TargetIssuer: config.TargetIssuer,
		SubjectField: config.SubjectField, TTLField: config.TTLField, ReasonField: config.ReasonField,
		VerificationToken: config.FeishuVerificationToken, EncryptKey: config.FeishuEncryptKey,
	})
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr: config.ListenAddress, Handler: adapter.Handler(),
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
