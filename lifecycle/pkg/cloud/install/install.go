// Copyright 2026 sealos.
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

package install

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	osExec "os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labring/sealos/pkg/constants"
	"github.com/labring/sealos/pkg/distribution"
	"sigs.k8s.io/yaml"
)

const (
	DefaultDistribution         = "cloud-pro@v5.1.2-rc6"
	DefaultRegistry             = "sealos.hub:5000"
	DefaultRegistryPass         = "passw0rd"
	DefaultCloudPort            = 443
	DefaultSSHPort              = 22
	DefaultServiceNodePortRange = "30000-50000"
	DefaultCiliumVersion        = "v1.16.9"
	DefaultWaitTimeout          = 30 * time.Minute
	cloudRuntimeRoot            = "/root/.sealos/cloud"
)

const cloudRuntimeTools = `#!/usr/bin/env bash

GLOBAL_VALUES_FILE="${SEALOS_GLOBAL_VALUES_FILE:-/root/.sealos/cloud/values/global.yaml}"

read_yaml_file_path() {
  local path="${1:-}"
  if command -v yq >/dev/null 2>&1 && [ -f "${GLOBAL_VALUES_FILE}" ]; then
    yq e -r "${path} // \"\"" "${GLOBAL_VALUES_FILE}" 2>/dev/null || true
    return 0
  fi

  case "${path}" in
    .global.cluster.podCIDR) awk -F: '/podCIDR:/ {gsub(/[ "\\047]/, "", $2); print $2; exit}' "${GLOBAL_VALUES_FILE}" ;;
    .global.cluster.serviceNodePortRange) awk -F: '/serviceNodePortRange:/ {gsub(/[ "\\047]/, "", $2); print $2; exit}' "${GLOBAL_VALUES_FILE}" ;;
    .global.network.cilium.maskSize) awk -F: '/maskSize:/ {gsub(/[ "\\047]/, "", $2); print $2; exit}' "${GLOBAL_VALUES_FILE}" ;;
    .global.network.cilium.native) awk -F: '/native:/ {gsub(/[ "\\047]/, "", $2); print $2; exit}' "${GLOBAL_VALUES_FILE}" ;;
    .global.os.isKylinV10) awk -F: '/isKylinV10:/ {gsub(/[ "\\047]/, "", $2); print $2; exit}' "${GLOBAL_VALUES_FILE}" ;;
    *) printf '' ;;
  esac
}

ensure_global_values_ready_for_component() {
  if [ ! -s "${GLOBAL_VALUES_FILE}" ]; then
    echo "global values file is missing: ${GLOBAL_VALUES_FILE}" >&2
    return 1
  fi
}

get_global_value() {
  read_yaml_file_path "$@"
}

helm_default_values() {
  return 0
}

bool_is_true() {
  case "${1:-}" in
    true|TRUE|yes|YES|y|Y|1|on) return 0 ;;
    *) return 1 ;;
  esac
}

global_http_disable_https() {
  bool_is_true "$(read_yaml_file_path '.global.http.disableHttps')"
}

global_http_scheme() {
  if global_http_disable_https; then
    printf 'http'
  else
    printf 'https'
  fi
}

global_http_effective_port() {
  if global_http_disable_https; then
    read_yaml_file_path '.global.http.httpPort'
  else
    read_yaml_file_path '.global.http.httpsPort'
  fi
}

global_http_port_suffix() {
  local scheme="${1:-$(global_http_scheme)}"
  local port="${2:-$(global_http_effective_port)}"
  if [ "${scheme}" = "http" ] && [ "${port}" = "80" ]; then
    return 0
  fi
  if [ "${scheme}" = "https" ] && [ "${port}" = "443" ]; then
    return 0
  fi
  printf ':%s' "${port}"
}

global_http_external_url() {
  local host="${1:-}"
  local path="${2:-}"
  local scheme="$(global_http_scheme)"
  local port="$(global_http_effective_port)"
  printf '%s://%s%s%s' "${scheme}" "${host}" "$(global_http_port_suffix "${scheme}" "${port}")" "${path}"
}
`

// Config contains the inputs required by the Sealos Cloud installer.
type Config struct {
	Distribution     string
	PackageMode      distribution.ResolveMode
	SourceRoot       string
	SourceCache      string
	StateDir         string
	ClusterName      string
	TargetID         string
	RepositoryCommit string
	Masters          string
	Nodes            string

	User         string
	SSHPassword  string
	SSHKey       string
	SSHKeyPasswd string
	SSHPort      uint16
	RegistryPass string

	CloudDomain          string
	CloudPort            uint16
	MaxPods              int
	OpenEBSStorage       string
	ContainerdStorage    string
	PodCIDR              string
	ServiceCIDR          string
	ServiceNodePortRange string
	CiliumVersion        string
	CiliumMaskSize       string

	CertPath    string
	KeyPath     string
	EnableACME  bool
	Proxy       bool
	DryRun      bool
	ConfigDir   string
	WaitTimeout time.Duration
}

// configDuration accepts the human-readable duration format used by the CLI
// while retaining JSON/YAML decoding errors for malformed values.
type configDuration time.Duration

func (d *configDuration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err == nil {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", value, err)
		}
		*d = configDuration(parsed)
		return nil
	}

	var nanos int64
	if err := json.Unmarshal(data, &nanos); err != nil {
		return errors.New("duration must be a string such as 30m or an integer number of nanoseconds")
	}
	*d = configDuration(nanos)
	return nil
}

// ConfigFile is the optional YAML configuration file consumed by the native
// distribution installer. Pointer fields distinguish omitted values from
// explicit false, zero, and empty values.
type ConfigFile struct {
	Distribution         *string         `json:"distribution,omitempty" yaml:"distribution,omitempty"`
	PackageMode          *string         `json:"packageMode,omitempty" yaml:"packageMode,omitempty"`
	SourceRoot           *string         `json:"sourceRoot,omitempty" yaml:"sourceRoot,omitempty"`
	SourceCache          *string         `json:"sourceCache,omitempty" yaml:"sourceCache,omitempty"`
	StateDir             *string         `json:"stateDir,omitempty" yaml:"stateDir,omitempty"`
	ClusterName          *string         `json:"cluster,omitempty" yaml:"cluster,omitempty"`
	TargetID             *string         `json:"targetID,omitempty" yaml:"targetID,omitempty"`
	Masters              *string         `json:"masters,omitempty" yaml:"masters,omitempty"`
	Nodes                *string         `json:"nodes,omitempty" yaml:"nodes,omitempty"`
	User                 *string         `json:"user,omitempty" yaml:"user,omitempty"`
	SSHPassword          *string         `json:"sshPassword,omitempty" yaml:"sshPassword,omitempty"`
	SSHKey               *string         `json:"sshKey,omitempty" yaml:"sshKey,omitempty"`
	SSHKeyPasswd         *string         `json:"sshKeyPassphrase,omitempty" yaml:"sshKeyPassphrase,omitempty"`
	SSHPort              *uint16         `json:"sshPort,omitempty" yaml:"sshPort,omitempty"`
	RegistryPass         *string         `json:"registryPassword,omitempty" yaml:"registryPassword,omitempty"`
	CloudDomain          *string         `json:"cloudDomain,omitempty" yaml:"cloudDomain,omitempty"`
	CloudPort            *uint16         `json:"cloudPort,omitempty" yaml:"cloudPort,omitempty"`
	MaxPods              *int            `json:"maxPods,omitempty" yaml:"maxPods,omitempty"`
	OpenEBSStorage       *string         `json:"openebsStorage,omitempty" yaml:"openebsStorage,omitempty"`
	ContainerdStorage    *string         `json:"containerdStorage,omitempty" yaml:"containerdStorage,omitempty"`
	PodCIDR              *string         `json:"podCIDR,omitempty" yaml:"podCIDR,omitempty"`
	ServiceCIDR          *string         `json:"serviceCIDR,omitempty" yaml:"serviceCIDR,omitempty"`
	ServiceNodePortRange *string         `json:"serviceNodePortRange,omitempty" yaml:"serviceNodePortRange,omitempty"`
	CiliumVersion        *string         `json:"ciliumVersion,omitempty" yaml:"ciliumVersion,omitempty"`
	CiliumMaskSize       *string         `json:"ciliumMaskSize,omitempty" yaml:"ciliumMaskSize,omitempty"`
	CertPath             *string         `json:"certPath,omitempty" yaml:"certPath,omitempty"`
	KeyPath              *string         `json:"keyPath,omitempty" yaml:"keyPath,omitempty"`
	EnableACME           *bool           `json:"enableACME,omitempty" yaml:"enableACME,omitempty"`
	Proxy                *bool           `json:"proxy,omitempty" yaml:"proxy,omitempty"`
	DryRun               *bool           `json:"dryRun,omitempty" yaml:"dryRun,omitempty"`
	ConfigDir            *string         `json:"configDir,omitempty" yaml:"configDir,omitempty"`
	WaitTimeout          *configDuration `json:"waitTimeout,omitempty" yaml:"waitTimeout,omitempty"`
}

// LoadConfigFile reads a strict YAML configuration file. An empty path means
// that no file was requested and returns an empty configuration.
func LoadConfigFile(path string) (ConfigFile, error) {
	if strings.TrimSpace(path) == "" {
		return ConfigFile{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ConfigFile{}, fmt.Errorf("read installation config %q: %w", path, err)
	}
	var config ConfigFile
	if err := yaml.UnmarshalStrict(data, &config); err != nil {
		return ConfigFile{}, fmt.Errorf("decode installation config %q: %w", path, err)
	}
	return config, nil
}

// Apply overlays the file values on cfg unless the corresponding key is
// already marked as overridden. The returned map also marks values supplied by
// the file, so interactive prompts can avoid asking for them again.
func (f ConfigFile) Apply(cfg *Config, overridden map[string]bool) map[string]bool {
	provided := make(map[string]bool, len(overridden))
	for key, value := range overridden {
		provided[key] = value
	}
	setString := func(key string, value *string, target *string) {
		if value != nil && !provided[key] {
			*target = *value
			provided[key] = true
		}
	}
	setUint16 := func(key string, value *uint16, target *uint16) {
		if value != nil && !provided[key] {
			*target = *value
			provided[key] = true
		}
	}
	setInt := func(key string, value *int, target *int) {
		if value != nil && !provided[key] {
			*target = *value
			provided[key] = true
		}
	}
	setBool := func(key string, value *bool, target *bool) {
		if value != nil && !provided[key] {
			*target = *value
			provided[key] = true
		}
	}

	setString("distribution", f.Distribution, &cfg.Distribution)
	setString("packageMode", f.PackageMode, (*string)(&cfg.PackageMode))
	setString("sourceRoot", f.SourceRoot, &cfg.SourceRoot)
	setString("sourceCache", f.SourceCache, &cfg.SourceCache)
	setString("stateDir", f.StateDir, &cfg.StateDir)
	setString("cluster", f.ClusterName, &cfg.ClusterName)
	setString("targetID", f.TargetID, &cfg.TargetID)
	setString("masters", f.Masters, &cfg.Masters)
	setString("nodes", f.Nodes, &cfg.Nodes)
	setString("user", f.User, &cfg.User)
	setString("sshPassword", f.SSHPassword, &cfg.SSHPassword)
	setString("sshKey", f.SSHKey, &cfg.SSHKey)
	setString("sshKeyPassphrase", f.SSHKeyPasswd, &cfg.SSHKeyPasswd)
	setUint16("sshPort", f.SSHPort, &cfg.SSHPort)
	setString("registryPassword", f.RegistryPass, &cfg.RegistryPass)
	setString("cloudDomain", f.CloudDomain, &cfg.CloudDomain)
	setUint16("cloudPort", f.CloudPort, &cfg.CloudPort)
	setInt("maxPods", f.MaxPods, &cfg.MaxPods)
	setString("openebsStorage", f.OpenEBSStorage, &cfg.OpenEBSStorage)
	setString("containerdStorage", f.ContainerdStorage, &cfg.ContainerdStorage)
	setString("podCIDR", f.PodCIDR, &cfg.PodCIDR)
	setString("serviceCIDR", f.ServiceCIDR, &cfg.ServiceCIDR)
	setString("serviceNodePortRange", f.ServiceNodePortRange, &cfg.ServiceNodePortRange)
	setString("ciliumVersion", f.CiliumVersion, &cfg.CiliumVersion)
	setString("ciliumMaskSize", f.CiliumMaskSize, &cfg.CiliumMaskSize)
	setString("certPath", f.CertPath, &cfg.CertPath)
	setString("keyPath", f.KeyPath, &cfg.KeyPath)
	setBool("enableACME", f.EnableACME, &cfg.EnableACME)
	setBool("proxy", f.Proxy, &cfg.Proxy)
	setBool("dryRun", f.DryRun, &cfg.DryRun)
	setString("configDir", f.ConfigDir, &cfg.ConfigDir)
	if f.WaitTimeout != nil && !provided["waitTimeout"] {
		cfg.WaitTimeout = time.Duration(*f.WaitTimeout)
		provided["waitTimeout"] = true
	}
	return provided
}

func (c Config) ValidateReset() error {
	if strings.TrimSpace(c.Masters) == "" {
		return errors.New("masters are required")
	}
	if strings.TrimSpace(c.User) == "" {
		return errors.New("SSH user is required")
	}
	if c.SSHPort == 0 {
		return errors.New("SSH port must be greater than zero")
	}
	return nil
}

// DefaultConfig returns the defaults used by the native installer.
func DefaultConfig() Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/root"
	}
	return Config{
		Distribution: DefaultDistribution,
		User:         "root",
		SSHKey:       filepath.Join(home, ".ssh", "id_rsa"),
		SSHPort:      DefaultSSHPort,
		RegistryPass: DefaultRegistryPass,
		PackageMode:  distribution.ResolveRemote,
		// Absolute local source paths are owned by individual packages. This
		// remains a fallback for legacy manifests that use relative paths.
		SourceRoot:           currentDirectory(),
		SourceCache:          distribution.DefaultSourceCache(),
		StateDir:             distribution.DefaultPackageStateDir,
		ClusterName:          "default",
		CloudPort:            DefaultCloudPort,
		MaxPods:              120,
		OpenEBSStorage:       "/var/openebs",
		ContainerdStorage:    "/var/lib/containerd",
		PodCIDR:              "100.64.0.0/10",
		ServiceCIDR:          "10.96.0.0/22",
		ServiceNodePortRange: DefaultServiceNodePortRange,
		CiliumVersion:        DefaultCiliumVersion,
		CiliumMaskSize:       "24",
		ConfigDir:            filepath.Join(home, ".sealos", "cloud"),
		WaitTimeout:          DefaultWaitTimeout,
	}
}

func currentDirectory() string {
	directory, err := os.Getwd()
	if err != nil {
		return "."
	}
	return directory
}

// ConfigFromEnv keeps install-v2.sh users compatible while the CLI becomes the
// primary interface. Explicit CLI flags are applied after this function.
func ConfigFromEnv(lookup func(string) (string, bool)) Config {
	cfg := DefaultConfig()
	setString := func(key string, target *string) {
		if lookup == nil {
			return
		}
		if value, ok := lookup(key); ok {
			*target = value
		}
	}
	setBool := func(key string, target *bool) {
		if lookup == nil {
			return
		}
		if value, ok := lookup(key); ok {
			*target = strings.EqualFold(value, "true") || value == "1" || strings.EqualFold(value, "yes")
		}
	}
	setInt := func(key string, target *int) {
		if lookup == nil {
			return
		}
		if value, ok := lookup(key); ok {
			if parsed, err := strconv.Atoi(value); err == nil {
				*target = parsed
			}
		}
	}
	setUint16 := func(key string, target *uint16) {
		if lookup == nil {
			return
		}
		if value, ok := lookup(key); ok {
			if parsed, err := strconv.ParseUint(value, 10, 16); err == nil {
				*target = uint16(parsed)
			}
		}
	}

	setString("SEALOS_V2_DISTRIBUTION", &cfg.Distribution)
	setString("SEALOS_V2_PACKAGE_MODE", (*string)(&cfg.PackageMode))
	setString("SEALOS_V2_SOURCE_ROOT", &cfg.SourceRoot)
	setString("SEALOS_V2_SOURCE_CACHE", &cfg.SourceCache)
	setString("SEALOS_PACKAGE_STATE_DIR", &cfg.StateDir)
	setString("SEALOS_PACKAGE_CLUSTER", &cfg.ClusterName)
	setString("SEALOS_PACKAGE_TARGET_ID", &cfg.TargetID)
	setString("SEALOS_V2_MASTERS", &cfg.Masters)
	setString("SEALOS_V2_NODES", &cfg.Nodes)
	setString("SEALOS_V2_SSH_KEY", &cfg.SSHKey)
	setString("SEALOS_V2_SSH_PASSWORD", &cfg.SSHPassword)
	setString("SEALOS_V2_REGISTRY_PASSWORD", &cfg.RegistryPass)
	setString("SEALOS_V2_CLOUD_DOMAIN", &cfg.CloudDomain)
	setString("SEALOS_V2_OPENEBS_STORAGE", &cfg.OpenEBSStorage)
	setString("SEALOS_V2_CONTAINERD_STORAGE", &cfg.ContainerdStorage)
	setString("SEALOS_V2_POD_CIDR", &cfg.PodCIDR)
	setString("SEALOS_V2_SERVICE_CIDR", &cfg.ServiceCIDR)
	setString("SEALOS_V2_SERVICE_NODEPORT_RANGE", &cfg.ServiceNodePortRange)
	setString("SEALOS_V2_CILIUM_VERSION", &cfg.CiliumVersion)
	setString("SEALOS_V2_CILIUM_MASKSIZE", &cfg.CiliumMaskSize)
	setString("SEALOS_V2_CERT_PATH", &cfg.CertPath)
	setString("SEALOS_V2_KEY_PATH", &cfg.KeyPath)
	setUint16("SEALOS_V2_CLOUD_PORT", &cfg.CloudPort)
	setUint16("SEALOS_V2_SSH_PORT", &cfg.SSHPort)
	setInt("SEALOS_V2_MAX_POD", &cfg.MaxPods)
	setBool("SEALOS_V2_PROXY", &cfg.Proxy)
	setBool("SEALOS_V2_DRY_RUN", &cfg.DryRun)
	setBool("SEALOS_V2_ENABLE_ACME", &cfg.EnableACME)
	return cfg
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Distribution) == "" {
		return errors.New("distribution is required")
	}
	if c.PackageMode != distribution.ResolveRemote && c.PackageMode != distribution.ResolveSource && c.PackageMode != distribution.ResolveHybrid {
		return fmt.Errorf("unsupported package mode %q", c.PackageMode)
	}
	if strings.TrimSpace(c.Masters) == "" {
		return errors.New("masters are required")
	}
	if strings.TrimSpace(c.CloudDomain) == "" {
		return errors.New("cloud domain is required")
	}
	if err := validateServiceNodePortRange(c.ServiceNodePortRange); err != nil {
		return err
	}
	if c.SSHPort == 0 {
		return errors.New("ssh port must be greater than zero")
	}
	if c.CloudPort == 0 {
		return errors.New("cloud port must be greater than zero")
	}
	if c.MaxPods <= 0 {
		return errors.New("max pods must be greater than zero")
	}
	if c.RegistryPass == "" {
		return errors.New("registry password is required")
	}
	if c.WaitTimeout <= 0 {
		return errors.New("wait timeout must be greater than zero")
	}
	if strings.TrimSpace(c.ConfigDir) == "" {
		return errors.New("config directory is required")
	}
	if strings.TrimSpace(c.StateDir) == "" {
		return errors.New("package state directory is required")
	}
	if strings.TrimSpace(c.ClusterName) == "" || filepath.Base(c.ClusterName) != c.ClusterName || c.ClusterName == "." || c.ClusterName == ".." {
		return fmt.Errorf("cluster name or ID %q is invalid", c.ClusterName)
	}
	if (c.CertPath == "") != (c.KeyPath == "") {
		return errors.New("cert path and key path must be provided together")
	}
	if c.EnableACME && c.CertPath != "" {
		return errors.New("acme and custom certificates cannot be enabled together")
	}
	if c.CertPath != "" {
		for name, value := range map[string]string{"certificate": c.CertPath, "key": c.KeyPath} {
			if !filepath.IsAbs(value) {
				return fmt.Errorf("%s path must be absolute: %s", name, value)
			}
			info, err := os.Stat(value)
			if err != nil {
				return fmt.Errorf("%s file %s: %w", name, value, err)
			}
			if info.IsDir() {
				return fmt.Errorf("%s path is a directory: %s", name, value)
			}
		}
	}
	return nil
}

type Images struct {
	Kubernetes                     string
	Cilium                         string
	CertManager                    string
	Helm                           string
	OpenEBS                        string
	Higress                        string
	KubeBlocks                     string
	Cockroach                      string
	MetricsServer                  string
	VictoriaMetricsKubernetesStack string
	Cloud                          string
	Finish                         string
	Certs                          string
	CloudDesktopFrontend           string
	CloudUserController            string
	CloudTerminalController        string
	CloudAppController             string
	CloudResourcesController       string
	CloudAccountController         string
	CloudAccountService            string
	CloudLicenseController         string
	CloudJobInitController         string
	CloudJobHeartbeatController    string
	CloudApplaunchpadFrontend      string
	CloudTerminalFrontend          string
	CloudDBProviderFrontend        string
	CloudCostCenterFrontend        string
	CloudTemplateFrontend          string
	CloudLicenseFrontend           string
	CloudDatabaseService           string
	CloudLaunchpadService          string
	CloudAdmissionWebhook          string
	CloudNodeController            string
	CloudVlogsService              string
}

func ResolveImages(manifest *distribution.Manifest) (Images, error) {
	images, _, err := ResolveImagesWithOptions(manifest, distribution.ResolveOptions{Mode: distribution.ResolveRemote})
	return images, err
}

// ValidateManifest checks whether the installer can consume a package-based
// distribution.
func ValidateManifest(manifest *distribution.Manifest, options distribution.ResolveOptions) error {
	if manifest == nil {
		return errors.New("distribution manifest is nil")
	}
	if manifest.Name == "cloud" {
		_, _, err := ResolveImagesWithOptions(manifest, options)
		return err
	}
	_, err := distribution.ResolvePackages(manifest, options)
	return err
}

func ResolveImagesWithOptions(manifest *distribution.Manifest, options distribution.ResolveOptions) (Images, []distribution.BuildPlan, error) {
	if manifest == nil {
		return Images{}, nil, errors.New("distribution manifest is nil")
	}
	if manifest.Name != "cloud" {
		return Images{}, nil, fmt.Errorf("distribution %s is not supported by the cloud installer", manifest.Ref())
	}
	resolved, err := distribution.ResolvePackages(manifest, options)
	if err != nil {
		return Images{}, nil, err
	}

	var images Images
	assign := []struct {
		name   string
		target *string
	}{
		{"kubernetes", &images.Kubernetes}, {"cilium", &images.Cilium}, {"cert-manager", &images.CertManager},
		{"helm", &images.Helm}, {"openebs", &images.OpenEBS}, {"higress", &images.Higress},
		{"kubeblocks", &images.KubeBlocks}, {"cockroach", &images.Cockroach}, {"metrics-server", &images.MetricsServer},
		{"victoria-metrics-k8s-stack", &images.VictoriaMetricsKubernetesStack}, {"sealos-cloud", &images.Cloud},
		{"sealos-finish", &images.Finish}, {"sealos-certs", &images.Certs},
		{"sealos-cloud-desktop-frontend", &images.CloudDesktopFrontend}, {"sealos-cloud-user-controller", &images.CloudUserController},
		{"sealos-cloud-terminal-controller", &images.CloudTerminalController}, {"sealos-cloud-app-controller", &images.CloudAppController},
		{"sealos-cloud-resources-controller", &images.CloudResourcesController}, {"sealos-cloud-account-controller", &images.CloudAccountController},
		{"sealos-cloud-account-service", &images.CloudAccountService}, {"sealos-cloud-license-controller", &images.CloudLicenseController},
		{"sealos-cloud-job-init-controller", &images.CloudJobInitController}, {"sealos-cloud-job-heartbeat-controller", &images.CloudJobHeartbeatController},
		{"sealos-cloud-applaunchpad-frontend", &images.CloudApplaunchpadFrontend}, {"sealos-cloud-terminal-frontend", &images.CloudTerminalFrontend},
		{"sealos-cloud-dbprovider-frontend", &images.CloudDBProviderFrontend}, {"sealos-cloud-costcenter-frontend", &images.CloudCostCenterFrontend},
		{"sealos-cloud-template-frontend", &images.CloudTemplateFrontend}, {"sealos-cloud-license-frontend", &images.CloudLicenseFrontend},
		{"sealos-cloud-database-service", &images.CloudDatabaseService}, {"sealos-cloud-launchpad-service", &images.CloudLaunchpadService},
	}
	byName := make(map[string]distribution.ResolvedPackage, len(resolved))
	for _, item := range resolved {
		name := item.Package.Name
		if _, exists := byName[name]; exists {
			return Images{}, nil, fmt.Errorf("distribution %s contains duplicate package name %q", manifest.Ref(), name)
		}
		byName[name] = item
	}
	get := func(name string) (string, error) {
		item, ok := byName[name]
		if !ok {
			return "", fmt.Errorf("distribution %s is missing package %q", manifest.Ref(), name)
		}
		return item.Image, nil
	}
	for _, item := range assign {
		image, err := get(item.name)
		if err != nil {
			return Images{}, nil, err
		}
		*item.target = image
	}
	plans := make([]distribution.BuildPlan, 0)
	for _, item := range resolved {
		if item.Build != nil {
			plans = append(plans, *item.Build)
		}
	}
	return images, plans, nil
}

func (i Images) RewriteProxy() Images {
	rewrite := func(image string) string {
		if strings.HasPrefix(image, "ghcr.io/") {
			return "ghcr.dockerproxy.net/" + strings.TrimPrefix(image, "ghcr.io/")
		}
		return image
	}
	return Images{
		Kubernetes: rewrite(i.Kubernetes), Cilium: rewrite(i.Cilium), CertManager: rewrite(i.CertManager),
		Helm: rewrite(i.Helm), OpenEBS: rewrite(i.OpenEBS), Higress: rewrite(i.Higress),
		KubeBlocks: rewrite(i.KubeBlocks), Cockroach: rewrite(i.Cockroach), MetricsServer: rewrite(i.MetricsServer),
		VictoriaMetricsKubernetesStack: rewrite(i.VictoriaMetricsKubernetesStack), Cloud: rewrite(i.Cloud),
		Finish: rewrite(i.Finish), Certs: rewrite(i.Certs), CloudDesktopFrontend: rewrite(i.CloudDesktopFrontend),
		CloudUserController: rewrite(i.CloudUserController), CloudTerminalController: rewrite(i.CloudTerminalController),
		CloudAppController: rewrite(i.CloudAppController), CloudResourcesController: rewrite(i.CloudResourcesController),
		CloudAccountController: rewrite(i.CloudAccountController), CloudAccountService: rewrite(i.CloudAccountService),
		CloudLicenseController: rewrite(i.CloudLicenseController), CloudJobInitController: rewrite(i.CloudJobInitController),
		CloudJobHeartbeatController: rewrite(i.CloudJobHeartbeatController), CloudApplaunchpadFrontend: rewrite(i.CloudApplaunchpadFrontend),
		CloudTerminalFrontend: rewrite(i.CloudTerminalFrontend), CloudDBProviderFrontend: rewrite(i.CloudDBProviderFrontend),
		CloudCostCenterFrontend: rewrite(i.CloudCostCenterFrontend), CloudTemplateFrontend: rewrite(i.CloudTemplateFrontend),
		CloudLicenseFrontend: rewrite(i.CloudLicenseFrontend), CloudDatabaseService: rewrite(i.CloudDatabaseService),
		CloudLaunchpadService: rewrite(i.CloudLaunchpadService),
	}
}

type Command struct {
	Name          string
	Args          []string
	Dir           string
	SensitiveArgs bool
}

func (c Command) String() string {
	parts := make([]string, 0, len(c.Args)+1)
	parts = append(parts, c.Name)
	for _, arg := range c.Args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func (c Command) RedactedString() string {
	args := append([]string(nil), c.Args...)
	if c.SensitiveArgs && len(args) > 0 {
		args[len(args)-1] = "<redacted-package-cleanup-script>"
	}
	for index := range args {
		if args[index] == "--passwd" || args[index] == "--pk-passwd" || args[index] == "-p" || args[index] == "--password" {
			if index+1 < len(args) {
				args[index+1] = "<redacted>"
			}
			continue
		}
		if args[index] == "--env" && index+1 < len(args) {
			args[index+1] = redactEnv(args[index+1])
		}
		if args[index] == "--build-arg" && index+1 < len(args) {
			args[index+1] = redactEnv(args[index+1])
		}
	}
	return (Command{Name: c.Name, Args: args}).String()
}

func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return strings.ContainsRune(" \t\n'\"\\$;&|<>()[{}]", r)
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func redactEnv(value string) string {
	key, _, _ := strings.Cut(value, "=")
	upper := strings.ToUpper(key)
	if strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "JWT") || strings.Contains(upper, "SALT") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "URI") || strings.HasSuffix(upper, "_SECRET") || strings.Contains(upper, "PRIVATE_KEY") {
		return key + "=<redacted>"
	}
	return value
}

type Runner interface {
	Run(context.Context, Command) error
	Output(context.Context, Command) ([]byte, error)
}

type CommandRunner struct {
	SealosPath string
	Stdout     io.Writer
	Stderr     io.Writer
}

func NewCommandRunner(stdout, stderr io.Writer) (*CommandRunner, error) {
	path, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve current sealos executable: %w", err)
	}
	return &CommandRunner{SealosPath: path, Stdout: stdout, Stderr: stderr}, nil
}

func (r *CommandRunner) command(ctx context.Context, command Command) *osExec.Cmd {
	name := command.Name
	if name == "sealos" {
		name = r.SealosPath
	}
	cmd := osExec.CommandContext(ctx, name, command.Args...)
	cmd.Dir = command.Dir
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	cmd.Stdin = os.Stdin
	return cmd
}

func (r *CommandRunner) Run(ctx context.Context, command Command) error {
	return r.command(ctx, command).Run()
}

func (r *CommandRunner) Output(ctx context.Context, command Command) ([]byte, error) {
	cmd := r.command(ctx, command)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.CombinedOutput()
}

type Installer struct {
	Config       Config
	Runner       Runner
	Stdout       io.Writer
	Log          io.Writer
	cleanupHooks map[string]cleanupHookArtifact
}

type cleanupHookArtifact struct {
	Path   string
	Script []byte
}

func (i *Installer) capturePackageCleanupHook(ctx context.Context, image string) error {
	if i.Config.DryRun || strings.TrimSpace(image) == "" {
		return nil
	}
	output, err := i.Runner.Output(ctx, Command{Name: "sealos", Args: []string{"inspect", "--type", "image", image}})
	if err != nil {
		return fmt.Errorf("inspect package image %s: %w", image, err)
	}
	if len(strings.TrimSpace(string(output))) == 0 {
		return nil
	}
	var inspected struct {
		OCIv1 *struct {
			Config struct {
				Labels map[string]string `json:"Labels"`
			} `json:"Config"`
		} `json:"OCIv1"`
	}
	if err := json.Unmarshal(output, &inspected); err != nil {
		return fmt.Errorf("decode package image %s metadata: %w", image, err)
	}
	if inspected.OCIv1 == nil {
		return nil
	}
	hookPath := strings.TrimSpace(inspected.OCIv1.Config.Labels[distribution.PackageCleanupLabel])
	if hookPath == "" {
		return nil
	}
	if err := distribution.ValidateCleanupPath(hookPath); err != nil {
		return fmt.Errorf("package image %s cleanup hook: %w", image, err)
	}

	digest := sha256.Sum256([]byte(image))
	container := "sealos-package-clean-" + hex.EncodeToString(digest[:8])
	if err := i.run(ctx, Command{Name: "sealos", Args: []string{"create", "--cluster", container, image}}); err != nil {
		return fmt.Errorf("create package image %s for cleanup extraction: %w", image, err)
	}
	defer func() {
		_ = i.run(ctx, Command{Name: "sealos", Args: []string{"umount", container}})
		_ = i.run(ctx, Command{Name: "sealos", Args: []string{"rm", container}})
	}()
	mountOutput, err := i.Runner.Output(ctx, Command{Name: "sealos", Args: []string{"mount", "--json", container}})
	if err != nil {
		return fmt.Errorf("mount package image %s for cleanup extraction: %w", image, err)
	}
	var mounts []struct {
		MountPoint string `json:"mountPoint"`
	}
	if err := json.Unmarshal(mountOutput, &mounts); err != nil {
		return fmt.Errorf("decode package image %s mount point: %w", image, err)
	}
	if len(mounts) != 1 || strings.TrimSpace(mounts[0].MountPoint) == "" {
		return fmt.Errorf("package image %s did not produce one mount point", image)
	}
	root := filepath.Clean(mounts[0].MountPoint)
	scriptPath := filepath.Join(root, strings.TrimPrefix(hookPath, string(filepath.Separator)))
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve package image %s mount point: %w", image, err)
	}
	resolvedScript, err := filepath.EvalSymlinks(scriptPath)
	if err != nil {
		return fmt.Errorf("resolve package cleanup hook %s: %w", hookPath, err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedScript)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("package cleanup hook %s escapes the image rootfs", hookPath)
	}
	info, err := os.Stat(scriptPath)
	if err != nil {
		return fmt.Errorf("stat package cleanup hook %s: %w", hookPath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("package cleanup hook %s is not a regular file", hookPath)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("package cleanup hook %s is not executable", hookPath)
	}
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("read package cleanup hook %s: %w", hookPath, err)
	}
	if i.cleanupHooks == nil {
		i.cleanupHooks = make(map[string]cleanupHookArtifact)
	}
	i.cleanupHooks[image] = cleanupHookArtifact{Path: hookPath, Script: append([]byte(nil), script...)}
	return nil
}

func (i *Installer) imageHasPackageInit(ctx context.Context, image string) (bool, error) {
	if i.Config.DryRun {
		return false, nil
	}
	output, err := i.Runner.Output(ctx, Command{Name: "sealos", Args: []string{"inspect", "--type", "image", image}})
	if err != nil {
		return false, fmt.Errorf("inspect package image %s: %w", image, err)
	}
	var inspected struct {
		OCIv1 *struct {
			Config struct {
				Labels map[string]string `json:"Labels"`
			} `json:"Config"`
		} `json:"OCIv1"`
	}
	if err := json.Unmarshal(output, &inspected); err != nil {
		return false, fmt.Errorf("decode package image %s metadata: %w", image, err)
	}
	return inspected.OCIv1 != nil && strings.TrimSpace(inspected.OCIv1.Config.Labels[distribution.PackageInitLabel]) != "", nil
}

func (i *Installer) cleanupArtifactForImage(image string) (cleanupHookArtifact, bool) {
	if i == nil || len(i.cleanupHooks) == 0 {
		return cleanupHookArtifact{}, false
	}
	if artifact, ok := i.cleanupHooks[image]; ok {
		return artifact, true
	}
	if strings.HasPrefix(image, "ghcr.io/") {
		artifact, ok := i.cleanupHooks["ghcr.dockerproxy.net/"+strings.TrimPrefix(image, "ghcr.io/")]
		return artifact, ok
	}
	return cleanupHookArtifact{}, false
}

func (i *Installer) Install(ctx context.Context, manifest *distribution.Manifest) error {
	if err := i.Config.Validate(); err != nil {
		return err
	}
	if i.Runner == nil {
		return errors.New("installer command runner is nil")
	}
	if i.Stdout == nil {
		i.Stdout = io.Discard
	}
	workDir := i.Config.ConfigDir
	if _, err := os.Stat(workDir); errors.Is(err, os.ErrNotExist) {
		workDir = os.TempDir()
	}
	if manifest != nil && manifest.Name != "cloud" {
		if err := i.installBootstrap(ctx, manifest, workDir); err != nil {
			return err
		}
		return i.recordInstalledState(ctx, manifest)
	}
	images, buildPlans, err := ResolveImagesWithOptions(manifest, distribution.ResolveOptions{
		Mode:        i.Config.PackageMode,
		SourceRoot:  i.Config.SourceRoot,
		SourceCache: i.Config.SourceCache,
		WorkDir:     workDir,
	})
	if err != nil {
		return err
	}
	if !i.Config.DryRun {
		if err := os.MkdirAll(i.Config.ConfigDir, 0o700); err != nil {
			return fmt.Errorf("create installer config directory: %w", err)
		}
	}
	for _, plan := range buildPlans {
		if plan.CleanupDir != "" {
			defer os.RemoveAll(plan.CleanupDir)
		}
		for _, command := range plan.Commands {
			if err := i.run(ctx, Command{Name: command.Name, Args: command.Args, Dir: command.Dir}); err != nil {
				return err
			}
		}
	}

	if i.Config.Proxy && i.Config.PackageMode == distribution.ResolveRemote {
		images = images.RewriteProxy()
	}
	installed, ready := i.clusterStatus(ctx)
	ciliumReady := installed && i.ciliumStatus(ctx)
	i.info("Starting Sealos Cloud installation for %s", manifest.Ref())
	i.info("Kubernetes installed=%t ready=%t Cilium ready=%t", installed, ready, ciliumReady)

	for _, image := range []string{images.Kubernetes, images.Cilium, images.CertManager, images.Helm, images.OpenEBS, images.Higress, images.KubeBlocks, images.Cockroach, images.MetricsServer, images.VictoriaMetricsKubernetesStack, images.Cloud, images.Finish, images.Certs} {
		if err := i.run(ctx, Command{Name: "sealos", Args: []string{"pull", "-q", image}}); err != nil {
			return err
		}
		if err := i.capturePackageCleanupHook(ctx, image); err != nil {
			return err
		}
	}

	if !installed {
		if err := i.prepareClusterBootstrap(ctx); err != nil {
			return err
		}
		if err := i.run(ctx, i.kubernetesCommand(images.Kubernetes)); err != nil {
			return err
		}
	}
	if err := i.ensureServiceNodePortRange(ctx); err != nil {
		return err
	}
	if err := i.run(ctx, i.runImage(images.Helm)); err != nil {
		return err
	}
	if err := i.prepareCloudRuntimeConfig(ctx); err != nil {
		return err
	}
	if !ciliumReady {
		if err := i.run(ctx, i.ciliumCommand(images.Cilium)); err != nil {
			return err
		}
		if !i.Config.DryRun {
			if err := i.waitCiliumReady(ctx); err != nil {
				return err
			}
		}
	}
	if !ready && !i.Config.DryRun {
		if err := i.waitClusterReady(ctx); err != nil {
			return err
		}
	}
	for _, command := range []Command{
		i.runImage(images.CertManager),
		i.imageWithEnv(images.OpenEBS, "OPENEBS_STORAGE_PREFIX", i.Config.OpenEBSStorage),
		i.runImage(images.MetricsServer),
		i.runImage(images.Cockroach),
		i.runImage(images.VictoriaMetricsKubernetesStack),
		i.imageWithEnvs(images.Higress, map[string]string{"SEALOS_CLOUD_PORT": strconv.Itoa(int(i.Config.CloudPort)), "SEALOS_CLOUD_DOMAIN": i.Config.CloudDomain}),
		i.runImage(images.KubeBlocks),
	} {
		if err := i.run(ctx, command); err != nil {
			return err
		}
	}
	if err := i.run(ctx, i.imageWithEnvs(images.Cloud, map[string]string{
		"SEALOS_CLOUD_DIR":    i.Config.ConfigDir,
		"SEALOS_CLOUD_DOMAIN": i.Config.CloudDomain,
		"SEALOS_CLOUD_PORT":   strconv.Itoa(int(i.Config.CloudPort)),
	})); err != nil {
		return err
	}
	certEnv := map[string]string{}
	switch {
	case i.Config.EnableACME:
		certEnv["SEALOS_CLOUD_CERT_MODE"] = "acmedns"
	case i.Config.CertPath != "":
		certEnv["SEALOS_CLOUD_CERT_MODE"] = "https"
		certEnv["SEALOS_CLOUD_CERT_CRT_PATH"] = i.Config.CertPath
		certEnv["SEALOS_CLOUD_CERT_KEY_PATH"] = i.Config.KeyPath
	default:
		certEnv["SEALOS_CLOUD_CERT_MODE"] = "self-signed"
	}
	if err := i.run(ctx, i.imageWithEnvs(images.Certs, certEnv)); err != nil {
		return err
	}

	cloudConfig, err := i.cloudConfig(ctx)
	if err != nil {
		return err
	}
	if err := i.runCloud(ctx, images, cloudConfig); err != nil {
		return err
	}
	if err := i.run(ctx, i.runImage(images.Finish)); err != nil {
		return err
	}
	return i.recordInstalledState(ctx, manifest)
}

// Reset executes package-owned cleanup hooks in reverse dependency order. A
// distribution reset deliberately does not delegate to the legacy reset
// command: containerd, registry, Kubernetes, and application packages own
// their cleanup behavior through their package images.
func (i *Installer) Reset(ctx context.Context, clusterName string) error {
	if err := i.Config.ValidateReset(); err != nil {
		return err
	}
	if i.Runner == nil {
		return errors.New("installer command runner is nil")
	}
	if i.Stdout == nil {
		i.Stdout = io.Discard
	}
	store, err := i.stateStore()
	if err != nil {
		return err
	}
	state, err := store.Load()
	if err != nil {
		return fmt.Errorf("load package state for reset: %w", err)
	}
	ordered, err := distribution.OrderInstalledPackages(state.Packages)
	if err != nil {
		return err
	}
	if i.Config.DryRun {
		for index := len(ordered) - 1; index >= 0; index-- {
			pkg := ordered[index]
			if pkg.Cleanup != nil && !pkg.CleanupDone {
				i.info("Would clean package %s on masters and workers", pkg.Ref())
			}
		}
		return nil
	}
	unlock, err := store.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var cleanupErrors []error
	for index := len(ordered) - 1; index >= 0; index-- {
		pkg := ordered[index]
		if pkg.Cleanup == nil || pkg.CleanupDone {
			continue
		}
		script, readErr := store.ReadCleanupArtifact(*pkg.Cleanup)
		if readErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup package %s: %w", pkg.Ref(), readErr))
			continue
		}
		if err := i.run(ctx, i.packageCleanupCommand(clusterName, pkg, script)); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup package %s: %w", pkg.Ref(), err))
			continue
		}
		for stateIndex := range state.Packages {
			if state.Packages[stateIndex].Ref() == pkg.Ref() {
				state.Packages[stateIndex].CleanupDone = true
				break
			}
		}
		if err := store.Save(state); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("save package cleanup state after %s: %w", pkg.Ref(), err))
		}
	}
	if len(cleanupErrors) > 0 {
		return errors.Join(cleanupErrors...)
	}
	return nil
}

func (i *Installer) packageCleanupCommand(clusterName string, pkg distribution.InstalledPackage, script []byte) Command {
	encoded := base64.StdEncoding.EncodeToString(script)
	temporary := "$(mktemp -d /tmp/sealos-package-clean.XXXXXX)"
	remoteScript := fmt.Sprintf("set -eu; tmpdir=%s; tmp=\"$tmpdir/hook.sh\"; trap 'rm -rf -- \"$tmpdir\"' EXIT; printf '%%s' '%s' | base64 -d > \"$tmp\"; chmod 700 \"$tmp\"; (cd \"$tmpdir\" && env SEALOS_PACKAGE_NAME=%s SEALOS_PACKAGE_VERSION=%s SEALOS_CLUSTER_NAME=%s \"$tmp\")", temporary, encoded, shellQuote(pkg.Name), shellQuote(pkg.Version), shellQuote(clusterName))
	args := []string{"exec", "--cluster", clusterName}
	appendArg := func(name, value string) {
		if strings.TrimSpace(value) != "" {
			args = append(args, name, value)
		}
	}
	appendArg("--user", i.Config.User)
	appendArg("--passwd", i.Config.SSHPassword)
	appendArg("--pk", i.Config.SSHKey)
	appendArg("--pk-passwd", i.Config.SSHKeyPasswd)
	if i.Config.SSHPort != 0 {
		args = append(args, "--port", strconv.Itoa(int(i.Config.SSHPort)))
	}
	targets := make([]string, 0, 2)
	for _, value := range []string{i.Config.Masters, i.Config.Nodes} {
		for _, target := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t' || r == '\n'
		}) {
			target = strings.TrimSpace(target)
			if target != "" && !containsString(targets, target) {
				targets = append(targets, target)
			}
		}
	}
	args = append(args, "--ips", strings.Join(targets, ","), "--capture-output", remoteScript)
	return Command{Name: "sealos", Args: args, SensitiveArgs: true}
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func (i *Installer) installBootstrap(ctx context.Context, manifest *distribution.Manifest, workDir string) error {
	resolved, err := distribution.ResolvePackages(manifest, distribution.ResolveOptions{
		Mode:        i.Config.PackageMode,
		SourceRoot:  i.Config.SourceRoot,
		SourceCache: i.Config.SourceCache,
		WorkDir:     workDir,
	})
	if err != nil {
		return err
	}
	buildPlans := make([]distribution.BuildPlan, 0, len(resolved))
	images := make(map[string]string, len(resolved))
	for _, item := range resolved {
		if item.Package.Name == "cilium" {
			continue
		}
		if item.Build != nil {
			buildPlans = append(buildPlans, *item.Build)
		}
		if _, exists := images[item.Package.Name]; exists {
			return fmt.Errorf("distribution %s contains duplicate package name %q", manifest.Ref(), item.Package.Name)
		}
		images[item.Package.Name] = item.Image
	}
	kubernetesImage := images["kubernetes"]
	cilium, err := selectCiliumPackage(resolved, i.Config.CiliumVersion)
	if kubernetesImage == "" || err != nil {
		if err != nil {
			return fmt.Errorf("resolve Cilium package for distribution %s: %w", manifest.Ref(), err)
		}
		return fmt.Errorf("distribution %s could not resolve Kubernetes and Cilium images", manifest.Ref())
	}
	ciliumImage := cilium.Image
	if cilium.Build != nil {
		buildPlans = append(buildPlans, *cilium.Build)
	}
	for _, plan := range buildPlans {
		if plan.CleanupDir != "" {
			defer os.RemoveAll(plan.CleanupDir)
		}
		for _, command := range plan.Commands {
			if err := i.run(ctx, Command{Name: command.Name, Args: command.Args, Dir: command.Dir}); err != nil {
				return err
			}
		}
	}
	preinstalled, err := i.installPackageDependencies(ctx, resolved, "kubernetes")
	if err != nil {
		return err
	}

	installed, ready := i.clusterStatus(ctx)
	ciliumReady := installed && i.ciliumStatus(ctx)
	i.info("Starting distribution bootstrap for %s", manifest.Ref())
	i.info("Kubernetes installed=%t ready=%t Cilium ready=%t", installed, ready, ciliumReady)
	if !installed {
		if err := i.prepareClusterBootstrap(ctx); err != nil {
			return err
		}
		if err := i.pullImage(ctx, kubernetesImage, ""); err != nil {
			return err
		}
		if err := i.capturePackageCleanupHook(ctx, kubernetesImage); err != nil {
			return err
		}
		packageImage, err := i.imageHasPackageInit(ctx, kubernetesImage)
		if err != nil {
			return err
		}
		if packageImage {
			if err := i.run(ctx, i.kubernetesPackageCommand(kubernetesImage)); err != nil {
				return fmt.Errorf("initialize Kubernetes package %s: %w", kubernetesImage, err)
			}
		}
		if err := i.run(ctx, i.kubernetesCommand(kubernetesImage)); err != nil {
			return err
		}
		i.cleanupImage(ctx, kubernetesImage)
	}
	if err := i.ensureServiceNodePortRange(ctx); err != nil {
		return err
	}
	if err := i.prepareCloudRuntimeConfig(ctx); err != nil {
		return err
	}
	if !ciliumReady {
		if err := i.pullImage(ctx, ciliumImage, ""); err != nil {
			return err
		}
		if err := i.capturePackageCleanupHook(ctx, ciliumImage); err != nil {
			return err
		}
		packageImage, err := i.imageHasPackageInit(ctx, ciliumImage)
		if err != nil {
			return err
		}
		command := i.ciliumCommand(ciliumImage)
		if packageImage {
			command = i.ciliumPackageCommand(ciliumImage)
		}
		if err := i.run(ctx, command); err != nil {
			return err
		}
		i.cleanupImage(ctx, ciliumImage)
		if !i.Config.DryRun {
			if err := i.waitCiliumReady(ctx); err != nil {
				return fmt.Errorf("wait for Cilium package readiness: %w", err)
			}
		}
	}
	if !ready && !i.Config.DryRun {
		if err := i.waitClusterReady(ctx); err != nil {
			return err
		}
	}
	for _, item := range resolved {
		if item.Package.Name != "cilium" || item.Image == ciliumImage {
			continue
		}
		if err := i.pullImage(ctx, item.Image, ""); err != nil {
			return err
		}
		if err := i.capturePackageCleanupHook(ctx, item.Image); err != nil {
			return err
		}
		i.cleanupImage(ctx, item.Image)
	}
	if err := i.ensurePlatformNamespaces(ctx); err != nil {
		return err
	}
	if err := i.installDistributionPackages(ctx, resolved, preinstalled); err != nil {
		return err
	}
	i.info("Distribution installation completed with all packages processed")
	return nil
}

func (i *Installer) installPackageDependencies(ctx context.Context, resolved []distribution.ResolvedPackage, targetName string) (map[string]bool, error) {
	byRef := make(map[string]distribution.ResolvedPackage, len(resolved))
	target := ""
	for _, item := range resolved {
		byRef[item.Package.Ref()] = item
		if item.Package.Name == targetName {
			if target != "" {
				return nil, fmt.Errorf("distribution contains multiple %s packages", targetName)
			}
			target = item.Package.Ref()
		}
	}
	if target == "" {
		return nil, fmt.Errorf("distribution is missing package %q", targetName)
	}

	needed := make(map[string]bool)
	visiting := make(map[string]bool)
	var visit func(string) error
	visit = func(ref string) error {
		if visiting[ref] {
			return fmt.Errorf("package dependency cycle detected while installing %s", ref)
		}
		item, ok := byRef[ref]
		if !ok {
			return fmt.Errorf("package %s depends on missing package %q", target, ref)
		}
		if needed[ref] {
			return nil
		}
		visiting[ref] = true
		if len(item.Dependencies) == 0 && len(item.Package.DependsOn) > 0 {
			return fmt.Errorf("package %s has unresolved dependencies", item.Package.Ref())
		}
		for _, dependency := range item.Dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
			needed[dependency] = true
		}
		delete(visiting, ref)
		return nil
	}
	if err := visit(target); err != nil {
		return nil, err
	}
	delete(needed, target)
	for _, item := range resolved {
		if !needed[item.Package.Ref()] {
			continue
		}
		if item.Package.Name == "cilium" {
			return nil, fmt.Errorf("package %s cannot be a Kubernetes prerequisite", item.Package.Ref())
		}
		if err := i.runPackage(ctx, i.systemPackageCommand(item.Image)); err != nil {
			return nil, fmt.Errorf("install package dependency %s: %w", item.Package.Ref(), err)
		}
	}
	return needed, nil
}

func (i *Installer) prepareCloudRuntimeConfig(ctx context.Context) error {
	if i.Config.DryRun {
		return nil
	}
	if err := i.run(ctx, i.cloudRuntimeConfigCommand()); err != nil {
		return fmt.Errorf("prepare cloud runtime configuration: %w", err)
	}
	return nil
}

func (i *Installer) cloudRuntimeConfigCommand() Command {
	tools := base64.StdEncoding.EncodeToString(i.cloudRuntimeToolsContent())
	values := base64.StdEncoding.EncodeToString([]byte(i.cloudRuntimeValues()))
	script := fmt.Sprintf(`set -eu
root=%s
tools_tmp=$(mktemp)
values_tmp=$(mktemp)
trap 'rm -f "$tools_tmp" "$values_tmp"' EXIT
mkdir -p "$root/scripts" "$root/values" "$root/bin"
printf '%%s' '%s' | base64 -d > "$tools_tmp"
printf '%%s' '%s' | base64 -d > "$values_tmp"
chmod 0755 "$tools_tmp"
chmod 0644 "$values_tmp"
mv "$tools_tmp" "$root/scripts/tools.sh"
mv "$values_tmp" "$root/values/global.yaml"
if [ ! -x "$root/bin/yq" ] && command -v yq >/dev/null 2>&1; then
  ln -s "$(command -v yq)" "$root/bin/yq"
fi`, shellQuote(cloudRuntimeRoot), tools, values)
	args := i.withCluster([]string{"exec"})
	args = append(args, "--roles", "master", script)
	return Command{Name: "sealos", Args: args}
}

func (i *Installer) cloudRuntimeToolsContent() []byte {
	candidates := make([]string, 0, 2)
	if strings.TrimSpace(i.Config.ConfigDir) != "" {
		candidates = append(candidates, filepath.Join(i.Config.ConfigDir, "scripts", "tools.sh"))
	}
	candidates = append(candidates, filepath.Join(cloudRuntimeRoot, "scripts", "tools.sh"))
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return data
		}
	}
	return []byte(cloudRuntimeTools)
}

func (i *Installer) cloudRuntimeValues() string {
	return fmt.Sprintf(`global:
  http:
    domain: %s
    httpsPort: %d
    httpPort: 80
    disableHttps: false
  cluster:
    podCIDR: %s
    serviceCIDR: %s
    serviceNodePortRange: %s
    maxPods: %d
  network:
    cilium:
      version: %s
      maskSize: %s
      native: false
  os:
    isKylinV10: false
`, yamlString(i.Config.CloudDomain), i.Config.CloudPort, yamlString(i.Config.PodCIDR), yamlString(i.Config.ServiceCIDR), yamlString(i.Config.ServiceNodePortRange), i.Config.MaxPods, yamlString(i.Config.CiliumVersion), yamlString(i.Config.CiliumMaskSize))
}

func yamlString(value string) string {
	return strconv.Quote(value)
}

func selectCiliumImage(resolved []distribution.ResolvedPackage, version string) (string, error) {
	item, err := selectCiliumPackage(resolved, version)
	if err != nil {
		return "", err
	}
	return item.Image, nil
}

func selectCiliumPackage(resolved []distribution.ResolvedPackage, version string) (distribution.ResolvedPackage, error) {
	version = strings.TrimSpace(version)
	available := make([]string, 0)
	for _, item := range resolved {
		if item.Package.Name != "cilium" {
			continue
		}
		available = append(available, item.Package.Version)
		if version == "" || item.Package.Version == version {
			return item, nil
		}
	}
	if version == "" {
		return distribution.ResolvedPackage{}, errors.New("package is missing")
	}
	if len(available) == 0 {
		return distribution.ResolvedPackage{}, fmt.Errorf("package is missing (requested version %q)", version)
	}
	return distribution.ResolvedPackage{}, fmt.Errorf("version %q is unavailable (available: %s)", version, strings.Join(available, ", "))
}

func (i *Installer) installDistributionPackages(ctx context.Context, resolved []distribution.ResolvedPackage, preinstalled map[string]bool) error {
	images := make(map[string]string, len(resolved))
	for _, item := range resolved {
		if item.Package.Name == "kubernetes" || item.Package.Name == "cilium" {
			continue
		}
		if _, exists := images[item.Package.Name]; exists {
			return fmt.Errorf("distribution contains duplicate package name %q", item.Package.Name)
		}
		images[item.Package.Name] = item.Image
	}
	if images["sealos-cloud-desktop-frontend"] != "" {
		for _, name := range []string{
			"sealos-cloud-desktop-frontend",
			"sealos-cloud-user-controller",
			"sealos-cloud-terminal-controller",
			"sealos-cloud-app-controller",
			"sealos-cloud-resources-controller",
			"sealos-cloud-account-controller",
			"sealos-cloud-account-service",
			"sealos-cloud-license-controller",
			"sealos-cloud-job-init-controller",
			"sealos-cloud-job-heartbeat-controller",
			"sealos-cloud-applaunchpad-frontend",
			"sealos-cloud-terminal-frontend",
			"sealos-cloud-dbprovider-frontend",
			"sealos-cloud-costcenter-frontend",
			"sealos-cloud-template-frontend",
			"sealos-cloud-license-frontend",
			"sealos-cloud-database-service",
			"sealos-cloud-launchpad-service",
			"sealos-cloud-admission-webhook",
			"sealos-cloud-node-controller",
			"sealos-cloud-vlogs-service",
		} {
			if strings.TrimSpace(images[name]) == "" {
				return fmt.Errorf("distribution is missing Cloud package %q", name)
			}
		}
	}

	for _, item := range resolved {
		name := item.Package.Name
		if name == "kubernetes" || name == "cilium" || isCloudPackage(name) {
			continue
		}
		if preinstalled[item.Package.Ref()] {
			continue
		}
		command := i.runImage(item.Image)
		if name == "sealos-oss" {
			command = i.imageWithEnv(item.Image, "SEALOS_V2_SERVICE_NODEPORT_RANGE", i.Config.ServiceNodePortRange)
		}
		if err := i.runPackage(ctx, command); err != nil {
			return fmt.Errorf("install package %s: %w", item.Package.Ref(), err)
		}
		if name == "sealos-oss" {
			if err := i.waitForConfigMap(ctx, "sealos-config", "sealos-system"); err != nil {
				return err
			}
		}
	}

	if images["sealos-cloud-desktop-frontend"] == "" {
		return nil
	}
	cloudImages := Images{
		CloudDesktopFrontend:        images["sealos-cloud-desktop-frontend"],
		CloudUserController:         images["sealos-cloud-user-controller"],
		CloudTerminalController:     images["sealos-cloud-terminal-controller"],
		CloudAppController:          images["sealos-cloud-app-controller"],
		CloudResourcesController:    images["sealos-cloud-resources-controller"],
		CloudAccountController:      images["sealos-cloud-account-controller"],
		CloudAccountService:         images["sealos-cloud-account-service"],
		CloudLicenseController:      images["sealos-cloud-license-controller"],
		CloudJobInitController:      images["sealos-cloud-job-init-controller"],
		CloudJobHeartbeatController: images["sealos-cloud-job-heartbeat-controller"],
		CloudApplaunchpadFrontend:   images["sealos-cloud-applaunchpad-frontend"],
		CloudTerminalFrontend:       images["sealos-cloud-terminal-frontend"],
		CloudDBProviderFrontend:     images["sealos-cloud-dbprovider-frontend"],
		CloudCostCenterFrontend:     images["sealos-cloud-costcenter-frontend"],
		CloudTemplateFrontend:       images["sealos-cloud-template-frontend"],
		CloudLicenseFrontend:        images["sealos-cloud-license-frontend"],
		CloudDatabaseService:        images["sealos-cloud-database-service"],
		CloudLaunchpadService:       images["sealos-cloud-launchpad-service"],
		CloudAdmissionWebhook:       images["sealos-cloud-admission-webhook"],
		CloudNodeController:         images["sealos-cloud-node-controller"],
		CloudVlogsService:           images["sealos-cloud-vlogs-service"],
	}
	config, err := i.cloudConfig(ctx)
	if err != nil {
		return err
	}
	if err := i.runCloudWithOptions(ctx, cloudImages, config, false); err != nil {
		return err
	}

	commands := []Command{}
	if cloudImages.CloudResourcesController != "" {
		resourceEnv := map[string]string{
			"MONGO_URI":         config.DatabaseMongo,
			"DEFAULT_NAMESPACE": "resources-system",
		}
		if !i.Config.DryRun {
			accessKey, secretKey, err := i.objectStorageCredentials(ctx)
			if err != nil {
				return err
			}
			resourceEnv["MINIO_AK"] = accessKey
			resourceEnv["MINIO_SK"] = secretKey
		}
		commands = append(commands, i.imageWithEnvs(cloudImages.CloudResourcesController, resourceEnv))
	}
	commands = append(commands,
		i.imageWithEnvs(cloudImages.CloudAdmissionWebhook, map[string]string{"cnameDomains": i.Config.CloudDomain, "cnameCheck": "false"}),
		i.runImage(cloudImages.CloudNodeController),
		i.imageWithEnvs(cloudImages.CloudAccountController, map[string]string{
			"MONGO_URI":              config.DatabaseMongo,
			"TRAFFIC_MONGO_URI":      config.DatabaseMongo,
			"cloudDomain":            config.CloudDomain,
			"cloudPort":              config.CloudPort,
			"DEFAULT_NAMESPACE":      "account-system",
			"GLOBAL_COCKROACH_URI":   config.DatabaseGlobalCockroach,
			"LOCAL_COCKROACH_URI":    config.DatabaseLocalCockroach,
			"LOCAL_REGION":           config.RegionUID,
			"ACCOUNT_API_JWT_SECRET": config.JWTInternal,
		}),
		i.runImage(cloudImages.CloudVlogsService),
	)
	for _, command := range commands {
		if commandImage(command) == "" {
			continue
		}
		if err := i.runPackage(ctx, command); err != nil {
			return err
		}
	}
	return nil
}

func isCloudPackage(name string) bool {
	return strings.HasPrefix(name, "sealos-cloud-")
}

func (i *Installer) run(ctx context.Context, command Command) error {
	line := command.RedactedString()
	i.info("Running: %s", line)
	if i.Log != nil {
		_, _ = fmt.Fprintln(i.Log, line)
	}
	if i.Config.DryRun {
		return nil
	}
	if err := i.Runner.Run(ctx, command); err != nil {
		return fmt.Errorf("command failed (%s): %w", line, err)
	}
	return nil
}

func (i *Installer) pullImage(ctx context.Context, image, policy string) error {
	if strings.TrimSpace(image) == "" {
		return errors.New("image reference is required")
	}
	args := []string{"pull", "-q", image}
	if policy != "" {
		args = []string{"pull", "--policy=" + policy, "-q", image}
	}
	return i.run(ctx, Command{Name: "sealos", Args: args})
}

func commandImage(command Command) string {
	if command.Name != "sealos" || len(command.Args) < 2 || command.Args[0] != "run" {
		return ""
	}
	for index := 1; index < len(command.Args); index++ {
		arg := strings.TrimSpace(command.Args[index])
		if arg == "" || arg == "--force" || arg == "--package" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if !strings.Contains(arg, "=") && index+1 < len(command.Args) {
				index++
			}
			continue
		}
		return arg
	}
	return ""
}

func (i *Installer) runPackage(ctx context.Context, command Command) error {
	image := commandImage(command)
	if image == "" {
		return nil
	}
	if err := i.pullImage(ctx, image, ""); err != nil {
		return err
	}
	if err := i.capturePackageCleanupHook(ctx, image); err != nil {
		return err
	}
	if err := i.run(ctx, command); err != nil {
		return err
	}
	i.cleanupImage(ctx, image)
	return nil
}

func (i *Installer) cleanupImage(ctx context.Context, image string) {
	if i.Config.DryRun || strings.TrimSpace(image) == "" {
		return
	}
	if err := i.run(ctx, Command{Name: "sealos", Args: []string{"rmi", "--force", image}}); err != nil {
		i.info("Unable to remove local package image %s: %v", image, err)
	}
}

func (i *Installer) info(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(i.Stdout, "[sealos install] "+format+"\n", args...)
}

func (i *Installer) clusterStatus(ctx context.Context) (bool, bool) {
	if i.Config.DryRun {
		return false, false
	}
	output, err := i.Runner.Output(ctx, i.clusterStatusCommand())
	if err != nil {
		return false, false
	}
	return true, nodesReady(output)
}

// prepareClusterBootstrap prevents a failed distribution installation from
// reusing a local Clusterfile after the target was reset. Sealos decides
// between create and install from that local file, while distribution install
// determines the real state from the target's Kubernetes files.
func (i *Installer) prepareClusterBootstrap(ctx context.Context) error {
	if i.Config.DryRun || strings.TrimSpace(i.Config.Masters) == "" {
		return nil
	}
	clusterDir := constants.ClusterDir(i.Config.ClusterName)
	if _, err := os.Stat(constants.Clusterfile(i.Config.ClusterName)); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect local cluster state: %w", err)
	}

	state, err := i.remoteKubernetesState(ctx)
	if err != nil {
		return fmt.Errorf("check remote Kubernetes state before bootstrap: %w", err)
	}
	switch state {
	case "initialized":
		return nil
	case "partial":
		return fmt.Errorf("remote Kubernetes state for cluster %q is incomplete; clean the target before retrying", i.Config.ClusterName)
	case "absent":
		backup := fmt.Sprintf("%s.stale-%d", clusterDir, time.Now().UnixNano())
		if err := os.Rename(clusterDir, backup); err != nil {
			return fmt.Errorf("archive stale local cluster state %s: %w", clusterDir, err)
		}
		i.info("Archived stale local cluster state at %s", backup)
		return nil
	default:
		return fmt.Errorf("remote Kubernetes state check returned unexpected result %q", state)
	}
}

func (i *Installer) remoteKubernetesState(ctx context.Context) (string, error) {
	script := `if [ -f /etc/kubernetes/admin.conf ] && [ -f /etc/kubernetes/manifests/kube-apiserver.yaml ]; then
  printf initialized
elif [ -e /etc/kubernetes/admin.conf ] || [ -e /etc/kubernetes/manifests/kube-apiserver.yaml ]; then
  printf partial
else
  printf absent
fi`
	args := i.withCluster([]string{"exec"})
	args = append(args, "--roles", "master", "--capture-output", script)
	output, err := i.Runner.Output(ctx, Command{Name: "sealos", Args: args})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (i *Installer) ciliumStatus(ctx context.Context) bool {
	if i.Config.DryRun {
		return false
	}
	output, err := i.Runner.Output(ctx, i.kubectlCommand(
		"get", "daemonset", "cilium", "--namespace", "kube-system", "--output", "json",
	))
	return err == nil && daemonSetReady(output)
}

func (i *Installer) waitCiliumReady(ctx context.Context) error {
	return i.waitUntil(ctx, func() (bool, error) {
		output, err := i.Runner.Output(ctx, i.kubectlCommand(
			"get", "daemonset", "cilium", "--namespace", "kube-system", "--output", "json",
		))
		if err != nil {
			return false, nil
		}
		return daemonSetReady(output), nil
	})
}

func daemonSetReady(output []byte) bool {
	var status struct {
		Status struct {
			DesiredNumberScheduled int `json:"desiredNumberScheduled"`
			NumberReady            int `json:"numberReady"`
		} `json:"status"`
	}
	if err := json.Unmarshal(output, &status); err != nil {
		return false
	}
	return status.Status.DesiredNumberScheduled > 0 &&
		status.Status.NumberReady >= status.Status.DesiredNumberScheduled
}

func (i *Installer) waitClusterReady(ctx context.Context) error {
	deadline := time.NewTimer(i.Config.WaitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		output, err := i.Runner.Output(ctx, i.clusterStatusCommand())
		if err == nil && nodesReady(output) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for Kubernetes nodes to become ready")
		case <-ticker.C:
		}
	}
}

func (i *Installer) clusterStatusCommand() Command {
	if strings.TrimSpace(i.Config.Masters) != "" {
		args := i.withCluster([]string{"exec"})
		args = append(args, "--roles", "master", "--capture-output", "kubectl get nodes --no-headers")
		return Command{Name: "sealos", Args: args}
	}
	kubeconfig := constants.NewPathResolver(clusterName(i.Config)).AdminFile()
	return Command{Name: "kubectl", Args: []string{"--kubeconfig", kubeconfig, "get", "nodes", "--no-headers"}}
}

func (i *Installer) kubectlCommand(args ...string) Command {
	if strings.TrimSpace(i.Config.Masters) == "" {
		return Command{Name: "kubectl", Args: args}
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, "kubectl")
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	execArgs := i.withCluster([]string{"exec"})
	execArgs = append(execArgs, "--roles", "master", "--capture-output", strings.Join(parts, " "))
	return Command{Name: "sealos", Args: execArgs}
}

func nodesReady(output []byte) bool {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return false
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.Contains(line, "NotReady") {
			return false
		}
	}
	return true
}

func (i *Installer) kubernetesCommand(image string) Command {
	args := []string{"run", "--force", "--allow-existing-runtime", image}
	args = i.withCluster(args)
	appendArg := func(name, value string) {
		if value != "" {
			args = append(args, name, value)
		}
	}
	appendArg("--masters", i.Config.Masters)
	appendArg("--nodes", i.Config.Nodes)
	appendArg("--env", "KUBEADM_POD_SUBNET="+i.Config.PodCIDR)
	appendArg("--env", "KUBEADM_SERVICE_SUBNET="+i.Config.ServiceCIDR)
	appendArg("--env", "KUBEADM_MAX_PODS="+strconv.Itoa(i.Config.MaxPods))
	appendArg("--env", "KUBEADM_SERVICE_RANGE="+i.Config.ServiceNodePortRange)
	appendArg("--env", "criData="+i.Config.ContainerdStorage)
	appendArg("--env", "registryPassword="+i.Config.RegistryPass)
	appendArg("--pk", i.Config.SSHKey)
	appendArg("--pk-passwd", i.Config.SSHKeyPasswd)
	appendArg("--passwd", i.Config.SSHPassword)
	appendArg("--user", i.Config.User)
	appendArg("--port", strconv.Itoa(int(i.Config.SSHPort)))
	return Command{Name: "sealos", Args: args}
}

func (i *Installer) systemPackageCommand(image string) Command {
	return i.packageImageWithEnvs(image, map[string]string{
		"registryDomain":   "sealos.hub",
		"registryPort":     "5000",
		"registryUsername": "admin",
		"registryPassword": i.Config.RegistryPass,
	})
}

func (i *Installer) packageImageWithEnvs(image string, env map[string]string) Command {
	args := []string{"run", "--package", "--force", image}
	args = i.withCluster(args)
	appendArg := func(name, value string) {
		if strings.TrimSpace(value) != "" {
			args = append(args, name, value)
		}
	}
	appendArg("--masters", i.Config.Masters)
	appendArg("--nodes", i.Config.Nodes)
	appendArg("--pk", i.Config.SSHKey)
	appendArg("--pk-passwd", i.Config.SSHKeyPasswd)
	appendArg("--passwd", i.Config.SSHPassword)
	appendArg("--user", i.Config.User)
	if i.Config.SSHPort != 0 {
		appendArg("--port", strconv.Itoa(int(i.Config.SSHPort)))
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		appendArg("--env", key+"="+env[key])
	}
	return Command{Name: "sealos", Args: args}
}

func validateServiceNodePortRange(value string) error {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 2 {
		return fmt.Errorf("service NodePort range must be in the form START-END: %q", value)
	}
	start, err := strconv.Atoi(parts[0])
	if err != nil {
		return fmt.Errorf("service NodePort range has an invalid start port: %q", value)
	}
	end, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("service NodePort range has an invalid end port: %q", value)
	}
	if start < 1 || end > 65535 || start > end {
		return fmt.Errorf("service NodePort range must be within 1-65535 and ordered: %q", value)
	}
	return nil
}

func (i *Installer) ensureServiceNodePortRange(ctx context.Context) error {
	if i.Config.DryRun {
		return nil
	}
	if err := validateServiceNodePortRange(i.Config.ServiceNodePortRange); err != nil {
		return err
	}
	if err := i.run(ctx, i.serviceNodePortRangeCommand()); err != nil {
		return fmt.Errorf("configure Kubernetes service NodePort range: %w", err)
	}
	if err := i.waitForAPIServer(ctx); err != nil {
		return fmt.Errorf("wait for Kubernetes API Server after NodePort range change: %w", err)
	}
	return nil
}

func (i *Installer) serviceNodePortRangeCommand() Command {
	value := i.Config.ServiceNodePortRange
	script := fmt.Sprintf(`set -eu
manifest=/etc/kubernetes/manifests/kube-apiserver.yaml
node_port_range=%q
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
if [ ! -f "$manifest" ]; then
  echo "kube-apiserver manifest not found: $manifest" >&2
  exit 1
fi
annotation="sealos.io/service-node-port-range: \"${node_port_range}\""
if grep -Eq "^[[:space:]]*- --service-node-port-range=${node_port_range}$" "$manifest" && \
   grep -Fq "$annotation" "$manifest"; then
  exit 0
fi
if grep -q -- '--service-node-port-range=' "$manifest"; then
  awk -v node_port_range="$node_port_range" '
    $0 ~ /^[[:space:]]*- --service-node-port-range=/ {
      sub(/=.*/, "=" node_port_range)
    }
    { print }
  ' "$manifest" > "$tmp"
else
  awk -v node_port_range="$node_port_range" '
    { print }
    /--service-cluster-ip-range=/ {
      print "    - --service-node-port-range=" node_port_range
    }
  ' "$manifest" > "$tmp"
fi
cp "$tmp" "$manifest"
if ! grep -q '^  annotations:' "$manifest"; then
	awk '
	  { print }
	  /^metadata:/ { print "  annotations:" }
	' "$manifest" > "$tmp"
	cp "$tmp" "$manifest"
fi
if grep -q -- 'sealos.io/service-node-port-range:' "$manifest"; then
	awk -v node_port_range="$node_port_range" '
	  $0 ~ /^[[:space:]]*sealos.io\/service-node-port-range:/ {
	    sub(/:.*/, ": \"" node_port_range "\"")
	  }
	  { print }
	' "$manifest" > "$tmp"
else
	awk -v node_port_range="$node_port_range" '
	  { print }
	  /^  annotations:/ {
	    print "    sealos.io/service-node-port-range: \"" node_port_range "\""
	  }
	' "$manifest" > "$tmp"
fi
cp "$tmp" "$manifest"`, value)
	args := i.withCluster([]string{"exec"})
	args = append(args, "--roles", "master", script)
	return Command{Name: "sealos", Args: args}
}

func (i *Installer) waitForAPIServer(ctx context.Context) error {
	return i.waitUntil(ctx, func() (bool, error) {
		_, err := i.Runner.Output(ctx, i.kubectlCommand("get", "--raw=/readyz"))
		return err == nil, nil
	})
}

func (i *Installer) ciliumCommand(image string) Command {
	return i.imageWithEnvs(image, map[string]string{
		"KUBEADM_POD_SUBNET":    i.Config.PodCIDR,
		"KUBEADM_SERVICE_RANGE": i.Config.ServiceNodePortRange,
		"CILIUM_MASKSIZE":       i.Config.CiliumMaskSize,
	})
}

func (i *Installer) kubernetesPackageCommand(image string) Command {
	return i.packageImageWithEnvs(image, map[string]string{
		"KUBEADM_POD_SUBNET":     i.Config.PodCIDR,
		"KUBEADM_SERVICE_SUBNET": i.Config.ServiceCIDR,
		"KUBEADM_SERVICE_RANGE":  i.Config.ServiceNodePortRange,
		"KUBEADM_MAX_PODS":       strconv.Itoa(i.Config.MaxPods),
		"KUBEADM_CONTAINERD_DIR": i.Config.ContainerdStorage,
		"registryDomain":         "sealos.hub",
		"registryPort":           "5000",
		"registryUsername":       "admin",
		"registryPassword":       i.Config.RegistryPass,
	})
}

func (i *Installer) ciliumPackageCommand(image string) Command {
	return i.packageImageWithEnvs(image, map[string]string{
		"KUBEADM_POD_SUBNET":    i.Config.PodCIDR,
		"KUBEADM_SERVICE_RANGE": i.Config.ServiceNodePortRange,
		"CILIUM_MASKSIZE":       i.Config.CiliumMaskSize,
	})
}

func (i *Installer) imageWithEnv(image, key, value string) Command {
	return i.imageWithEnvs(image, map[string]string{key: value})
}

func (i *Installer) imageWithEnvs(image string, env map[string]string) Command {
	args := []string{"run", "--force", image}
	args = i.withCluster(args)
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--env", key+"="+env[key])
	}
	return Command{Name: "sealos", Args: args}
}

func (i *Installer) runImage(image string) Command {
	return Command{Name: "sealos", Args: i.withCluster([]string{"run", "--force", image})}
}

func (i *Installer) withCluster(args []string) []string {
	if i == nil {
		return args
	}
	name := strings.TrimSpace(i.Config.ClusterName)
	if name == "" || name == "default" {
		return args
	}
	return append(args, "--cluster", name)
}

type cloudConfig struct {
	CloudDomain             string
	CloudPort               string
	RegionUID               string
	DatabaseGlobalCockroach string
	DatabaseLocalCockroach  string
	DatabaseMongo           string
	PasswordSalt            string
	JWTInternal             string
	JWTRegional             string
	JWTGlobal               string
	TLSRejectUnauthorized   bool
}

func (i *Installer) cloudConfig(ctx context.Context) (cloudConfig, error) {
	config := cloudConfig{
		CloudDomain:           i.Config.CloudDomain,
		CloudPort:             strconv.Itoa(int(i.Config.CloudPort)),
		TLSRejectUnauthorized: true,
	}
	if i.Config.DryRun {
		return config, nil
	}
	data, err := i.requiredResourceData(ctx, "configmap", "sealos-config", "sealos-system", []string{
		"cloudDomain",
		"cloudPort",
		"regionUID",
		"databaseGlobalCockroachdbURI",
		"databaseLocalCockroachdbURI",
		"databaseMongodbURI",
		"passwordSalt",
		"jwtInternal",
		"jwtRegional",
		"jwtGlobal",
	})
	if err != nil {
		return cloudConfig{}, fmt.Errorf("read sealos-config: %w", err)
	}
	values := map[string]*string{
		"cloudDomain":                  &config.CloudDomain,
		"cloudPort":                    &config.CloudPort,
		"regionUID":                    &config.RegionUID,
		"databaseGlobalCockroachdbURI": &config.DatabaseGlobalCockroach,
		"databaseLocalCockroachdbURI":  &config.DatabaseLocalCockroach,
		"databaseMongodbURI":           &config.DatabaseMongo,
		"passwordSalt":                 &config.PasswordSalt,
		"jwtInternal":                  &config.JWTInternal,
		"jwtRegional":                  &config.JWTRegional,
		"jwtGlobal":                    &config.JWTGlobal,
	}
	for key, target := range values {
		*target = data[key]
	}
	value, err := i.kubectlValue(ctx, "get", "configmap", "cert-config", "-n", "sealos-system", "-o", "jsonpath={.data.CERT_MODE}")
	if err == nil {
		config.TLSRejectUnauthorized = value == "self-signed"
	}
	return config, nil
}

func (i *Installer) requiredResourceData(ctx context.Context, resource, name, namespace string, required []string) (map[string]string, error) {
	timeout := 2 * time.Minute
	if i.Config.WaitTimeout > 0 && i.Config.WaitTimeout < timeout {
		timeout = i.Config.WaitTimeout
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		output, err := i.kubectlValue(ctx, "get", resource, name, "-n", namespace, "-o", "json")
		if err == nil {
			var object struct {
				Data map[string]string `json:"data"`
			}
			if decodeErr := json.Unmarshal([]byte(output), &object); decodeErr != nil {
				lastErr = fmt.Errorf("decode %s: %w", resource, decodeErr)
			} else {
				missing := make([]string, 0)
				for _, key := range required {
					if strings.TrimSpace(object.Data[key]) == "" {
						missing = append(missing, key)
					}
				}
				if len(missing) == 0 {
					return object.Data, nil
				}
				lastErr = fmt.Errorf("missing fields: %s", strings.Join(missing, ", "))
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("timed out waiting for value: %w", lastErr)
		case <-ticker.C:
		}
	}
}

func (i *Installer) objectStorageCredentials(ctx context.Context) (string, string, error) {
	type credentialSource struct {
		name      string
		accessKey string
		secretKey string
	}
	sources := []credentialSource{
		{name: "object-storage-secret", accessKey: "accesskey", secretKey: "secretkey"},
		{name: "object-storage-user-0", accessKey: "CONSOLE_ACCESS_KEY", secretKey: "CONSOLE_SECRET_KEY"},
	}

	timeout := 2 * time.Minute
	if i.Config.WaitTimeout > 0 && i.Config.WaitTimeout < timeout {
		timeout = i.Config.WaitTimeout
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		for _, source := range sources {
			data, err := i.secretData(ctx, source.name, "objectstorage-system")
			if err != nil {
				lastErr = err
				continue
			}
			accessKey, err := base64.StdEncoding.DecodeString(data[source.accessKey])
			if err != nil {
				lastErr = fmt.Errorf("decode object storage access key from %s: %w", source.name, err)
				continue
			}
			if len(accessKey) == 0 {
				lastErr = fmt.Errorf("object storage access key from %s is empty", source.name)
				continue
			}
			secretKey, err := base64.StdEncoding.DecodeString(data[source.secretKey])
			if err != nil {
				lastErr = fmt.Errorf("decode object storage secret key from %s: %w", source.name, err)
				continue
			}
			if len(secretKey) == 0 {
				lastErr = fmt.Errorf("object storage secret key from %s is empty", source.name)
				continue
			}
			return string(accessKey), string(secretKey), nil
		}

		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-deadline.C:
			if lastErr == nil {
				lastErr = errors.New("no valid object storage credential source found")
			}
			return "", "", fmt.Errorf("read object storage credentials: %w", lastErr)
		case <-ticker.C:
		}
	}
}

func (i *Installer) secretData(ctx context.Context, name, namespace string) (map[string]string, error) {
	output, err := i.kubectlValue(ctx, "get", "secret", name, "-n", namespace, "-o", "json")
	if err != nil {
		return nil, err
	}
	var object struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &object); err != nil {
		return nil, fmt.Errorf("decode secret %s: %w", name, err)
	}
	return object.Data, nil
}

func (i *Installer) kubectlValue(ctx context.Context, args ...string) (string, error) {
	output, err := i.Runner.Output(ctx, i.kubectlCommand(args...))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (i *Installer) ensurePlatformNamespaces(ctx context.Context) error {
	if i.Config.DryRun {
		return nil
	}
	for _, namespace := range []string{"sealos", "sealos-system"} {
		if _, err := i.Runner.Output(ctx, i.kubectlCommand("get", "namespace", namespace)); err == nil {
			continue
		}
		if err := i.run(ctx, i.kubectlCommand("create", "namespace", namespace)); err != nil {
			return err
		}
	}
	return nil
}

func (i *Installer) waitForConfigMap(ctx context.Context, name, namespace string) error {
	if i.Config.DryRun {
		return nil
	}
	return i.waitUntil(ctx, func() (bool, error) {
		_, err := i.Runner.Output(ctx, i.kubectlCommand("get", "configmap", name, "-n", namespace))
		return err == nil, nil
	})
}

func (i *Installer) runCloud(ctx context.Context, images Images, config cloudConfig) error {
	return i.runCloudWithOptions(ctx, images, config, true)
}

func (i *Installer) runCloudWithOptions(ctx context.Context, images Images, config cloudConfig, includeInfrastructure bool) error {
	if err := i.runPackage(ctx, i.imageWithEnvs(images.CloudDesktopFrontend, map[string]string{
		"cloudDomain": config.CloudDomain, "cloudPort": config.CloudPort, "certSecretName": "wildcard-cert",
		"passwordEnabled": "true", "passwordSalt": config.PasswordSalt, "regionUID": config.RegionUID,
		"databaseMongodbURI":           config.DatabaseMongo + "/sealos-auth?authSource=admin",
		"databaseLocalCockroachdbURI":  config.DatabaseLocalCockroach,
		"databaseGlobalCockroachdbURI": config.DatabaseGlobalCockroach,
		"jwtInternal":                  config.JWTInternal, "jwtRegional": config.JWTRegional, "jwtGlobal": config.JWTGlobal,
	})); err != nil {
		return err
	}
	if err := i.waitForDesktop(ctx); err != nil {
		return err
	}

	commands := []Command{
		i.imageWithEnvs(images.CloudUserController, map[string]string{"cloudDomain": config.CloudDomain, "apiserverPort": "6443"}),
		i.imageWithEnvs(images.CloudTerminalController, map[string]string{"cloudDomain": config.CloudDomain, "cloudPort": config.CloudPort, "userNamespace": "user-system", "wildcardCertSecretName": "wildcard-cert", "wildcardCertSecretNamespace": "sealos-system"}),
		i.runImage(images.CloudAppController),
		i.imageWithEnvs(images.CloudAccountService, map[string]string{"cloudDomain": config.CloudDomain, "cloudPort": config.CloudPort}),
		i.runImage(images.CloudLicenseController),
		i.imageWithEnv(images.CloudJobInitController, "PASSWORD_SALT", config.PasswordSalt),
		i.runImage(images.CloudJobHeartbeatController),
	}
	if includeInfrastructure {
		commands = append(commands,
			i.imageWithEnvs(images.CloudResourcesController, map[string]string{"MONGO_URI": config.DatabaseMongo, "DEFAULT_NAMESPACE": "resources-system"}),
			i.imageWithEnvs(images.CloudAccountController, map[string]string{"MONGO_URI": config.DatabaseMongo, "TRAFFIC_MONGO_URI": config.DatabaseMongo, "cloudDomain": config.CloudDomain, "cloudPort": config.CloudPort, "DEFAULT_NAMESPACE": "account-system", "GLOBAL_COCKROACH_URI": config.DatabaseGlobalCockroach, "LOCAL_COCKROACH_URI": config.DatabaseLocalCockroach, "LOCAL_REGION": config.RegionUID, "ACCOUNT_API_JWT_SECRET": config.JWTInternal}),
		)
	}
	for _, command := range commands {
		if err := i.runPackage(ctx, command); err != nil {
			return err
		}
	}
	if err := i.waitForNamespace(ctx); err != nil {
		return err
	}

	tlsEnv := map[string]string{"cloudDomain": config.CloudDomain, "cloudPort": config.CloudPort, "certSecretName": "wildcard-cert"}
	if config.TLSRejectUnauthorized {
		tlsEnv["tlsRejectUnauthorized"] = "0"
	}
	frontendCommands := []Command{
		i.imageWithEnvs(images.CloudApplaunchpadFrontend, tlsEnv),
		i.imageWithEnvs(images.CloudTerminalFrontend, tlsEnv),
		i.imageWithEnvs(images.CloudDBProviderFrontend, tlsEnv),
		i.imageWithEnvs(images.CloudCostCenterFrontend, map[string]string{"cloudDomain": config.CloudDomain, "cloudPort": config.CloudPort, "certSecretName": "wildcard-cert", "transferEnabled": "true", "rechargeEnabled": "false", "jwtInternal": config.JWTInternal}),
		i.imageWithEnvs(images.CloudTemplateFrontend, tlsEnv),
		i.imageWithEnvs(images.CloudLicenseFrontend, map[string]string{"cloudDomain": config.CloudDomain, "cloudPort": config.CloudPort, "certSecretName": "wildcard-cert", "MONGODB_URI": config.DatabaseMongo + "/sealos-license?authSource=admin", "licensePurchaseDomain": "license.sealos.io"}),
		i.runImage(images.CloudDatabaseService),
		i.runImage(images.CloudLaunchpadService),
	}
	for _, command := range frontendCommands {
		if err := i.runPackage(ctx, command); err != nil {
			return err
		}
	}
	return nil
}

func (i *Installer) waitForDesktop(ctx context.Context) error {
	if i.Config.DryRun {
		return nil
	}
	return i.waitUntil(ctx, func() (bool, error) {
		output, err := i.Runner.Output(ctx, i.kubectlCommand("get", "pods", "-n", "sealos", "--no-headers"))
		if err != nil {
			return false, err
		}
		found := false
		for _, line := range strings.Split(string(output), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 0 && (strings.Contains(fields[0], "desktop-frontend") || strings.HasPrefix(fields[0], "sealos-desktop-")) {
				found = true
				if !strings.Contains(line, "Running") {
					return false, nil
				}
			}
		}
		return found, nil
	})
}

func (i *Installer) waitForNamespace(ctx context.Context) error {
	if i.Config.DryRun {
		return nil
	}
	return i.waitUntil(ctx, func() (bool, error) {
		_, err := i.Runner.Output(ctx, i.kubectlCommand("get", "ns", "ns-admin"))
		return err == nil, nil
	})
}

func (i *Installer) waitUntil(ctx context.Context, ready func() (bool, error)) error {
	deadline := time.NewTimer(i.Config.WaitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		ok, err := ready()
		if err == nil && ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("timed out waiting for Sealos Cloud resources")
		case <-ticker.C:
		}
	}
}
