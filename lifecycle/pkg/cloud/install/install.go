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

	"github.com/labring/sealos/pkg/distribution"
)

const (
	DefaultDistribution = "cloud@v5.1.0"
	DefaultRegistry     = "sealos.hub:5000"
	DefaultRegistryPass = "passw0rd"
	DefaultCloudPort    = 443
	DefaultSSHPort      = 22
	DefaultWaitTimeout  = 30 * time.Minute
)

// Config contains the inputs required by the Sealos Cloud installer.
type Config struct {
	Distribution string
	PackageMode  distribution.ResolveMode
	SourceRoot   string
	SourceCache  string
	Masters      string
	Nodes        string

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
	CiliumMaskSize       string

	CertPath    string
	KeyPath     string
	EnableACME  bool
	Proxy       bool
	DryRun      bool
	ConfigDir   string
	WaitTimeout time.Duration
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
		CloudPort:            DefaultCloudPort,
		MaxPods:              120,
		OpenEBSStorage:       "/var/openebs",
		ContainerdStorage:    "/var/lib/containerd",
		PodCIDR:              "100.64.0.0/10",
		ServiceCIDR:          "10.96.0.0/22",
		ServiceNodePortRange: "30000-50000",
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
}

func ResolveImages(manifest *distribution.Manifest) (Images, error) {
	images, _, err := ResolveImagesWithOptions(manifest, distribution.ResolveOptions{Mode: distribution.ResolveRemote})
	return images, err
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
	for index, item := range resolved {
		name := item.Package.Name
		if len(manifest.Packages) == 0 {
			if index >= len(assign) {
				return Images{}, nil, fmt.Errorf("distribution %s contains %d images; cloud installer requires %d ordered packages", manifest.Ref(), len(resolved), len(assign))
			}
			name = assign[index].name
		}
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
	Name string
	Args []string
	Dir  string
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
	Config Config
	Runner Runner
	Stdout io.Writer
	Log    io.Writer
}

func (i *Installer) Install(ctx context.Context, manifest *distribution.Manifest) error {
	if err := i.Config.Validate(); err != nil {
		return err
	}
	workDir := i.Config.ConfigDir
	if _, err := os.Stat(workDir); errors.Is(err, os.ErrNotExist) {
		workDir = os.TempDir()
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
	if i.Runner == nil {
		return errors.New("installer command runner is nil")
	}
	if i.Stdout == nil {
		i.Stdout = io.Discard
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
	i.info("Starting Sealos Cloud installation for %s", manifest.Ref())
	i.info("Kubernetes installed=%t ready=%t", installed, ready)

	for _, image := range []string{images.Kubernetes, images.Cilium, images.CertManager, images.Helm, images.OpenEBS, images.Higress, images.KubeBlocks, images.Cockroach, images.MetricsServer, images.VictoriaMetricsKubernetesStack, images.Cloud, images.Finish, images.Certs} {
		if err := i.run(ctx, Command{Name: "sealos", Args: []string{"pull", "-q", image}}); err != nil {
			return err
		}
	}

	if !installed {
		if err := i.run(ctx, i.kubernetesCommand(images.Kubernetes)); err != nil {
			return err
		}
	}
	if err := i.run(ctx, Command{Name: "sealos", Args: []string{"run", images.Helm}}); err != nil {
		return err
	}
	if !ready {
		if err := i.run(ctx, i.ciliumCommand(images.Cilium)); err != nil {
			return err
		}
		if !i.Config.DryRun {
			if err := i.waitClusterReady(ctx); err != nil {
				return err
			}
		}
	}
	for _, command := range []Command{
		{Name: "sealos", Args: []string{"run", images.CertManager}},
		i.imageWithEnv(images.OpenEBS, "OPENEBS_STORAGE_PREFIX", i.Config.OpenEBSStorage),
		{Name: "sealos", Args: []string{"run", images.MetricsServer}},
		{Name: "sealos", Args: []string{"run", images.Cockroach}},
		{Name: "sealos", Args: []string{"run", images.VictoriaMetricsKubernetesStack}},
		i.imageWithEnvs(images.Higress, map[string]string{"SEALOS_CLOUD_PORT": strconv.Itoa(int(i.Config.CloudPort)), "SEALOS_CLOUD_DOMAIN": i.Config.CloudDomain}),
		{Name: "sealos", Args: []string{"run", images.KubeBlocks}},
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
	return i.run(ctx, Command{Name: "sealos", Args: []string{"run", images.Finish}})
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

func (i *Installer) info(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(i.Stdout, "[sealos install] "+format+"\n", args...)
}

func (i *Installer) clusterStatus(ctx context.Context) (bool, bool) {
	if i.Config.DryRun {
		return false, false
	}
	output, err := i.Runner.Output(ctx, Command{Name: "kubectl", Args: []string{"get", "nodes", "--no-headers"}})
	if err != nil {
		return false, false
	}
	return true, !strings.Contains(string(output), "NotReady")
}

func (i *Installer) waitClusterReady(ctx context.Context) error {
	deadline := time.NewTimer(i.Config.WaitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		output, err := i.Runner.Output(ctx, Command{Name: "kubectl", Args: []string{"get", "nodes", "--no-headers"}})
		if err == nil && !strings.Contains(string(output), "NotReady") {
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

func (i *Installer) kubernetesCommand(image string) Command {
	args := []string{"run", image}
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

func (i *Installer) ciliumCommand(image string) Command {
	return i.imageWithEnvs(image, map[string]string{
		"KUBEADM_POD_SUBNET":    i.Config.PodCIDR,
		"KUBEADM_SERVICE_RANGE": i.Config.ServiceNodePortRange,
		"CILIUM_MASKSIZE":       i.Config.CiliumMaskSize,
	})
}

func (i *Installer) imageWithEnv(image, key, value string) Command {
	return i.imageWithEnvs(image, map[string]string{key: value})
}

func (i *Installer) imageWithEnvs(image string, env map[string]string) Command {
	args := []string{"run", image}
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
		value, err := i.kubectlValue(ctx, "configmap", "sealos-config", "-n", "sealos-system", "-o", "jsonpath={.data."+key+"}")
		if err != nil {
			return cloudConfig{}, fmt.Errorf("read sealos-config %s: %w", key, err)
		}
		*target = value
	}
	value, err := i.kubectlValue(ctx, "configmap", "cert-config", "-n", "sealos-system", "-o", "jsonpath={.data.CERT_MODE}")
	if err != nil {
		return cloudConfig{}, fmt.Errorf("read cert-config: %w", err)
	}
	config.TLSRejectUnauthorized = value == "self-signed"
	return config, nil
}

func (i *Installer) kubectlValue(ctx context.Context, args ...string) (string, error) {
	output, err := i.Runner.Output(ctx, Command{Name: "kubectl", Args: args})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (i *Installer) runCloud(ctx context.Context, images Images, config cloudConfig) error {
	if err := i.run(ctx, Command{Name: "sealos", Args: []string{"login", "-u", "admin", "-p", i.Config.RegistryPass, DefaultRegistry}}); err != nil {
		return err
	}
	for _, image := range []string{images.CloudDesktopFrontend, images.CloudUserController, images.CloudTerminalController, images.CloudAppController, images.CloudResourcesController, images.CloudAccountController, images.CloudAccountService, images.CloudLicenseController, images.CloudJobInitController, images.CloudJobHeartbeatController, images.CloudApplaunchpadFrontend, images.CloudTerminalFrontend, images.CloudDBProviderFrontend, images.CloudCostCenterFrontend, images.CloudTemplateFrontend, images.CloudLicenseFrontend, images.CloudDatabaseService, images.CloudLaunchpadService} {
		if err := i.run(ctx, Command{Name: "sealos", Args: []string{"pull", "--policy=always", "-q", image}}); err != nil {
			return err
		}
	}

	if err := i.run(ctx, i.imageWithEnvs(images.CloudDesktopFrontend, map[string]string{
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
		{Name: "sealos", Args: []string{"run", images.CloudAppController}},
		i.imageWithEnvs(images.CloudResourcesController, map[string]string{"MONGO_URI": config.DatabaseMongo, "DEFAULT_NAMESPACE": "resources-system"}),
		i.imageWithEnvs(images.CloudAccountController, map[string]string{"MONGO_URI": config.DatabaseMongo, "TRAFFIC_MONGO_URI": config.DatabaseMongo, "cloudDomain": config.CloudDomain, "cloudPort": config.CloudPort, "DEFAULT_NAMESPACE": "account-system", "GLOBAL_COCKROACH_URI": config.DatabaseGlobalCockroach, "LOCAL_COCKROACH_URI": config.DatabaseLocalCockroach, "LOCAL_REGION": config.RegionUID, "ACCOUNT_API_JWT_SECRET": config.JWTInternal}),
		i.imageWithEnvs(images.CloudAccountService, map[string]string{"cloudDomain": config.CloudDomain, "cloudPort": config.CloudPort}),
		{Name: "sealos", Args: []string{"run", images.CloudLicenseController}},
		i.imageWithEnv(images.CloudJobInitController, "PASSWORD_SALT", config.PasswordSalt),
		{Name: "sealos", Args: []string{"run", images.CloudJobHeartbeatController}},
	}
	for _, command := range commands {
		if err := i.run(ctx, command); err != nil {
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
		{Name: "sealos", Args: []string{"run", images.CloudDatabaseService}},
		{Name: "sealos", Args: []string{"run", images.CloudLaunchpadService}},
	}
	for _, command := range frontendCommands {
		if err := i.run(ctx, command); err != nil {
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
		output, err := i.Runner.Output(ctx, Command{Name: "kubectl", Args: []string{"get", "pods", "-n", "sealos", "--no-headers"}})
		if err != nil {
			return false, err
		}
		for _, line := range strings.Split(string(output), "\n") {
			if strings.Contains(line, "desktop-frontend") && !strings.Contains(line, "Running") {
				return false, nil
			}
		}
		return true, nil
	})
}

func (i *Installer) waitForNamespace(ctx context.Context) error {
	if i.Config.DryRun {
		return nil
	}
	return i.waitUntil(ctx, func() (bool, error) {
		_, err := i.Runner.Output(ctx, Command{Name: "kubectl", Args: []string{"get", "ns", "ns-admin"}})
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
