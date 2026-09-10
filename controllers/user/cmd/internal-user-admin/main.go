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
	"encoding/json"
	stderrors "errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	userv1 "github.com/labring/sealos/controllers/user/api/v1"
	"github.com/labring/sealos/controllers/user/pkg/internalcredentials"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type adminOperation string

const (
	operationCreate  adminOperation = "create"
	operationApprove adminOperation = "approve"
)

type adminConfig struct {
	Operation        adminOperation
	ConfigFile       string
	Kubeconfig       string
	Issuer           string
	Subject          string
	BrokerURL        string
	BrokerCAFile     string
	BrokerServerName string
	ClientCertFile   string
	ClientKeyFile    string
	ApprovalID       string
	ApprovalReason   string
	RequestedTTL     int64
	Client           client.Client
	ConfigLoader     func(string) (*rest.Config, error)
	HTTPClient       *http.Client
	Stdout           io.Writer
}

func main() {
	config, err := parseAdminCLIArgs(os.Args[1:])
	if err != nil {
		if stderrors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	config.Stdout = os.Stdout
	if err := run(context.Background(), config); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseAdminCLIArgs(args []string) (adminConfig, error) {
	config := adminConfig{Operation: operationCreate}
	if len(args) == 0 {
		return adminConfig{}, stderrors.New("a create subcommand is required")
	}
	if args[0] != string(operationCreate) && args[0] != string(operationApprove) {
		flags := newAdminFlagSet(&config)
		if err := flags.Parse(args); err != nil {
			return adminConfig{}, err
		}
		if flags.NArg() > 0 && flags.Arg(0) != string(operationCreate) && flags.Arg(0) != string(operationApprove) {
			return adminConfig{}, fmt.Errorf("unknown subcommand %q", flags.Arg(0))
		}
		return adminConfig{}, fmt.Errorf("unknown subcommand %q", args[0])
	}
	config.Operation = adminOperation(args[0])
	flagArgs := args[1:]
	configPath, _, err := extractAdminConfigPath(flagArgs)
	if err != nil {
		return adminConfig{}, err
	}
	if !adminArgsRequestHelp(flagArgs) {
		fileConfig, discoveredPath, err := loadAdminConfig(flagArgs)
		if err != nil {
			return adminConfig{}, err
		}
		if discoveredPath != "" {
			configPath = discoveredPath
		}
		fileConfig.apply(&config)
	}
	config.ConfigFile = configPath
	flags := newAdminFlagSet(&config)
	if err := flags.Parse(flagArgs); err != nil {
		return adminConfig{}, err
	}
	if flags.NArg() != 0 {
		return adminConfig{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	return config, nil
}

func newAdminFlagSet(config *adminConfig) *flag.FlagSet {
	flags := flag.NewFlagSet("internal-user-admin", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&config.ConfigFile, "config", config.ConfigFile, "YAML config file; defaults to the standard admin config path when present.")
	flags.StringVar(&config.Kubeconfig, "kubeconfig", config.Kubeconfig, "Path to the administrator kubeconfig; defaults to the standard client-go loading rules.")
	flags.StringVar(&config.Issuer, "issuer", config.Issuer, "Allowlisted HTTPS OIDC issuer for the InternalUser.")
	flags.StringVar(&config.Subject, "subject", config.Subject, "Opaque immutable OIDC subject for the InternalUser.")
	flags.StringVar(&config.BrokerURL, "broker-url", config.BrokerURL, "HTTPS URL of the internal credential Broker.")
	flags.StringVar(&config.BrokerCAFile, "broker-ca-file", config.BrokerCAFile, "CA file used to verify the Broker certificate.")
	flags.StringVar(&config.BrokerServerName, "broker-server-name", config.BrokerServerName, "TLS server name used for the Broker connection.")
	flags.StringVar(&config.ClientCertFile, "client-cert-file", config.ClientCertFile, "mTLS client certificate used for approval submission.")
	flags.StringVar(&config.ClientKeyFile, "client-key-file", config.ClientKeyFile, "mTLS client private key used for approval submission.")
	flags.StringVar(&config.ApprovalID, "approval-id", config.ApprovalID, "Immutable external approval instance ID.")
	flags.StringVar(&config.ApprovalReason, "approval-reason", config.ApprovalReason, "Short operator reason recorded with the approval.")
	flags.Int64Var(&config.RequestedTTL, "requested-ttl-seconds", config.RequestedTTL, "Approved elevated credential lifetime in seconds.")
	return flags
}

func run(ctx context.Context, config adminConfig) error {
	if err := validateAdminConfig(config); err != nil {
		return err
	}
	if config.Stdout == nil {
		config.Stdout = io.Discard
	}

	if config.Operation == operationApprove {
		return approveElevated(ctx, config)
	}

	kubeClient := config.Client
	if kubeClient == nil {
		loadConfig := config.ConfigLoader
		if loadConfig == nil {
			loadConfig = loadKubernetesConfig
		}
		kubeConfig, err := loadConfig(config.Kubeconfig)
		if err != nil {
			return fmt.Errorf("load administrator kubeconfig: %w", err)
		}
		kubeClient, err = newInternalUserClient(kubeConfig)
		if err != nil {
			return fmt.Errorf("create Kubernetes client: %w", err)
		}
	}

	user := newInternalUser(config.Issuer, config.Subject)
	if err := kubeClient.Create(ctx, user); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("InternalUser %q already exists", user.Name)
		}
		return fmt.Errorf("create InternalUser %q: %w", user.Name, err)
	}
	_, err := fmt.Fprintf(config.Stdout, "created InternalUser %q with profile %q\n", user.Name, user.Spec.RoleProfile)
	return err
}

func validateAdminConfig(config adminConfig) error {
	if config.Operation != operationCreate && config.Operation != operationApprove {
		return stderrors.New("unsupported admin CLI operation")
	}
	if strings.TrimSpace(config.Issuer) == "" {
		return stderrors.New("issuer is required")
	}
	if strings.TrimSpace(config.Subject) == "" {
		return stderrors.New("subject is required")
	}
	if err := internalcredentials.ValidateIdentity(config.Issuer, config.Subject); err != nil {
		return fmt.Errorf("invalid identity: %w", err)
	}
	if config.Operation == operationApprove {
		if strings.TrimSpace(config.BrokerURL) == "" {
			return stderrors.New("broker URL is required for approve")
		}
		if err := validateHTTPSURL(config.BrokerURL); err != nil {
			return fmt.Errorf("invalid Broker URL: %w", err)
		}
		if strings.TrimSpace(config.ClientCertFile) == "" || strings.TrimSpace(config.ClientKeyFile) == "" {
			return stderrors.New("client certificate and key are required for approve")
		}
		if strings.TrimSpace(config.ApprovalID) == "" || strings.IndexFunc(config.ApprovalID, unicode.IsSpace) >= 0 || len(config.ApprovalID) > 512 {
			return stderrors.New("approval ID is required and must not contain whitespace")
		}
		if strings.TrimSpace(config.ApprovalReason) == "" || len(config.ApprovalReason) > 2048 {
			return stderrors.New("approval reason is required and must be at most 2048 bytes")
		}
		if _, err := internalcredentials.ValidateRequestedTTL(userv1.ClusterOpsWriteProfile, config.RequestedTTL); err != nil {
			return fmt.Errorf("invalid requested TTL: %w", err)
		}
	}
	return nil
}

func approveElevated(ctx context.Context, config adminConfig) error {
	httpClient := config.HTTPClient
	if httpClient == nil {
		var err error
		httpClient, err = newAdminHTTPClient(config.BrokerCAFile, config.BrokerServerName, config.ClientCertFile, config.ClientKeyFile)
		if err != nil {
			return fmt.Errorf("create Broker HTTP client: %w", err)
		}
	}
	body, err := json.Marshal(struct {
		ApprovalID          string          `json:"approvalID"`
		Target              userv1.Identity `json:"target"`
		RequestedTTLSeconds int64           `json:"requestedTTLSeconds"`
		ApprovalReason      string          `json:"approvalReason"`
	}{config.ApprovalID, userv1.Identity{Issuer: config.Issuer, Subject: config.Subject}, config.RequestedTTL, config.ApprovalReason})
	if err != nil {
		return fmt.Errorf("encode approval record: %w", err)
	}
	endpoint := strings.TrimRight(config.BrokerURL, "/") + "/v1/internal/credentials/approve"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create Broker request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("submit approval record: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return fmt.Errorf("Broker returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64*1024))
	decoder.DisallowUnknownFields()
	var result struct {
		ApprovalID          string    `json:"approvalID"`
		ApprovalReference   string    `json:"approvalReference"`
		Profile             string    `json:"profile"`
		RequestedTTLSeconds int64     `json:"requestedTTLSeconds"`
		ApprovalExpiresAt   time.Time `json:"approvalExpiresAt"`
	}
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("decode Broker approval response: %w", err)
	}
	if result.ApprovalID != config.ApprovalID || result.Profile != userv1.ClusterOpsWriteProfile || result.RequestedTTLSeconds != config.RequestedTTL || !validAdminReference(result.ApprovalReference) || result.ApprovalExpiresAt.IsZero() {
		return stderrors.New("Broker returned an incomplete approval response")
	}
	_, err = fmt.Fprintf(config.Stdout, "approval ID: %s\napproval reference: %s\nprofile: %s\nrequested TTL: %ds\nexpires: %s\n", result.ApprovalID, result.ApprovalReference, result.Profile, result.RequestedTTLSeconds, result.ApprovalExpiresAt.UTC().Format(time.RFC3339))
	return err
}

func validAdminReference(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsSpace) < 0 && len(value) <= 512
}

func validateHTTPSURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return stderrors.New("must be an absolute HTTPS URL without query or fragment")
	}
	return nil
}

func newAdminHTTPClient(caFile, serverName, certFile, keyFile string) (*http.Client, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load client certificate: %w", err)
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		transport = &http.Transport{}
	}
	transport = transport.Clone()
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName, Certificates: []tls.Certificate{cert}}
	if caFile != "" {
		data, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read Broker CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, stderrors.New("Broker CA file contains no certificates")
		}
		tlsConfig.RootCAs = pool
	}
	transport.TLSClientConfig = tlsConfig
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}, nil
}

func newInternalUser(issuer, subject string) *userv1.InternalUser {
	return &userv1.InternalUser{
		ObjectMeta: metav1.ObjectMeta{
			Name: internalcredentials.DeriveInternalUserName(issuer, subject),
		},
		Spec: userv1.InternalUserSpec{
			Identity: userv1.Identity{
				Issuer:  issuer,
				Subject: subject,
			},
			RoleProfile: userv1.BaseReadonlyProfile,
		},
	}
}

func loadKubernetesConfig(kubeconfig string) (*rest.Config, error) {
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func newInternalUserClient(config *rest.Config) (client.Client, error) {
	scheme := runtime.NewScheme()
	if err := userv1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	return client.New(config, client.Options{Scheme: scheme})
}
