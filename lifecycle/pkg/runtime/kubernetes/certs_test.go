package kubernetes

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/labring/sealos/pkg/constants"
	"github.com/labring/sealos/pkg/runtime"
)

func TestValidateRenewGroups(t *testing.T) {
	tests := []struct {
		name      string
		targets   []string
		renewAll  bool
		groups    []string
		expectErr bool
	}{
		{
			name:     "allow nil groups",
			targets:  []string{ControllerConf},
			renewAll: false,
			groups:   nil,
		},
		{
			name:     "allow all target",
			targets:  []string{"all"},
			renewAll: true,
			groups:   []string{"custom:group"},
		},
		{
			name:     "allow admin target",
			targets:  []string{AdminConf},
			renewAll: false,
			groups:   []string{"custom:group"},
		},
		{
			name:      "reject non admin targets",
			targets:   []string{ControllerConf, SchedulerConf},
			renewAll:  false,
			groups:    []string{"custom:group"},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRenewGroups(tt.targets, tt.renewAll, tt.groups)
			if (err != nil) != tt.expectErr {
				t.Fatalf("validateRenewGroups() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestDefaultLocalKubeConfigFiles(t *testing.T) {
	tests := []struct {
		version string
		want    []string
	}{
		{
			version: "v1.28.9",
			want:    []string{AdminConf, ControllerConf, SchedulerConf, KubeletConf},
		},
		{
			version: "v1.29.0",
			want:    []string{AdminConf, ControllerConf, SchedulerConf, KubeletConf, SuperAdminConf},
		},
	}

	for _, tt := range tests {
		got := defaultLocalKubeConfigFiles(tt.version)
		if len(got) != len(tt.want) {
			t.Fatalf("defaultLocalKubeConfigFiles(%q) = %v, want %v", tt.version, got, tt.want)
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Fatalf("defaultLocalKubeConfigFiles(%q) = %v, want %v", tt.version, got, tt.want)
			}
		}
	}
}

func TestNormalizeRenewTargetsAllowsSuperAdmin(t *testing.T) {
	targets, renewAll, err := normalizeRenewTargets([]string{SuperAdminConf})
	if err != nil {
		t.Fatalf("normalizeRenewTargets() error = %v", err)
	}
	if renewAll {
		t.Fatal("expected super-admin target not to imply renewAll")
	}
	if len(targets) != 1 || targets[0] != SuperAdminConf {
		t.Fatalf("normalizeRenewTargets() = %v, want [%s]", targets, SuperAdminConf)
	}
}

func TestNormalizeRenewTargetsRejectsSystemCertificates(t *testing.T) {
	if _, _, err := normalizeRenewTargets([]string{"apiserver-kubelet-client"}); err == nil {
		t.Fatal("expected system certificate targets to be rejected")
	}
}

func TestEffectiveLocalKubeConfigRenewTargets(t *testing.T) {
	tests := []struct {
		name    string
		targets []string
		version string
		want    []string
	}{
		{
			name:    "pre v129 keeps admin target only",
			targets: []string{AdminConf},
			version: "v1.28.9",
			want:    []string{AdminConf},
		},
		{
			name:    "v129 adds super admin alongside admin",
			targets: []string{AdminConf},
			version: "v1.29.0",
			want:    []string{AdminConf, SuperAdminConf},
		},
		{
			name:    "v129 does not duplicate explicit super admin target",
			targets: []string{AdminConf, SuperAdminConf},
			version: "v1.29.0",
			want:    []string{AdminConf, SuperAdminConf},
		},
	}

	for _, tt := range tests {
		got := effectiveLocalKubeConfigRenewTargets(tt.targets, tt.version)
		if len(got) != len(tt.want) {
			t.Fatalf("effectiveLocalKubeConfigRenewTargets(%v, %q) = %v, want %v", tt.targets, tt.version, got, tt.want)
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Fatalf("effectiveLocalKubeConfigRenewTargets(%v, %q) = %v, want %v", tt.targets, tt.version, got, tt.want)
			}
		}
	}
}

func TestRemoteControlPlaneKubeConfigFilesExcludeSuperAdmin(t *testing.T) {
	tests := []struct {
		includeKubelet bool
		want           []string
	}{
		{
			includeKubelet: false,
			want:           []string{AdminConf, ControllerConf, SchedulerConf},
		},
		{
			includeKubelet: true,
			want:           []string{AdminConf, ControllerConf, SchedulerConf, KubeletConf},
		},
	}

	for _, tt := range tests {
		got := remoteControlPlaneKubeConfigFiles(tt.includeKubelet)
		if len(got) != len(tt.want) {
			t.Fatalf("remoteControlPlaneKubeConfigFiles(%v) = %v, want %v", tt.includeKubelet, got, tt.want)
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Fatalf("remoteControlPlaneKubeConfigFiles(%v) = %v, want %v", tt.includeKubelet, got, tt.want)
			}
		}
	}
}

func TestValidateRenewGroupsAllowsAll(t *testing.T) {
	if err := validateRenewGroups([]string{"all"}, true, []string{"custom:group"}); err != nil {
		t.Fatalf("validateRenewGroups(all) error = %v", err)
	}
}

func TestBuildSyncCorePKICommandIncludesOnlyCoreFiles(t *testing.T) {
	cmd := buildSyncCorePKICommand("/var/lib/sealos/default/pki")
	for _, want := range []string{
		"/etc/kubernetes/pki/ca.crt",
		"/etc/kubernetes/pki/ca.key",
		"/etc/kubernetes/pki/front-proxy-ca.crt",
		"/etc/kubernetes/pki/front-proxy-ca.key",
		"/etc/kubernetes/pki/etcd/ca.crt",
		"/etc/kubernetes/pki/etcd/ca.key",
		"/etc/kubernetes/pki/sa.key",
		"/etc/kubernetes/pki/sa.pub",
		"/var/lib/sealos/default/pki/etcd",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("buildSyncCorePKICommand() missing %q in %q", want, cmd)
		}
	}
	for _, unwanted := range []string{
		"apiserver.crt",
		"apiserver.key",
		"apiserver-kubelet-client.crt",
		"etcd/peer.crt",
		"etcd/server.crt",
	} {
		if strings.Contains(cmd, unwanted) {
			t.Fatalf("buildSyncCorePKICommand() contains leaf cert %q in %q", unwanted, cmd)
		}
	}
}

func TestSyncPKISyncsMastersAndFetchesMaster0ToControlNode(t *testing.T) {
	rootDir := t.TempDir()
	prevRuntimeRoot := constants.DefaultRuntimeRootDir
	constants.DefaultRuntimeRootDir = rootDir
	t.Cleanup(func() {
		constants.DefaultRuntimeRootDir = prevRuntimeRoot
	})

	stub := &stubSSH{fetchContents: map[string]string{}}
	for _, pkiFile := range corePKIFiles {
		stub.fetchContents["master0|"+pathJoinPKI(pkiFile.path)] = "master0:" + pkiFile.path
	}

	rt := &KubeadmRuntime{
		execer:       stub,
		cluster:      testCluster([]string{"master0", "master1"}),
		pathResolver: constants.NewPathResolver("test-cluster"),
	}

	existingCA := filepath.Join(rootDir, "test-cluster", "pki", "ca.crt")
	if err := os.MkdirAll(filepath.Dir(existingCA), 0o755); err != nil {
		t.Fatalf("mkdir existing pki dir: %v", err)
	}
	if err := os.WriteFile(existingCA, []byte("old-ca"), 0o644); err != nil {
		t.Fatalf("write existing ca: %v", err)
	}

	if err := rt.SyncPKI(runtime.SyncPKIDirectionK8sToSealos); err != nil {
		t.Fatalf("SyncPKI() error = %v", err)
	}

	if len(stub.cmdAsyncCalls) != 2 {
		t.Fatalf("SyncPKI() cmdAsyncCalls = %v, want two master sync commands", stub.cmdAsyncCalls)
	}
	for _, master := range []string{"master0", "master1"} {
		found := false
		for _, call := range stub.cmdAsyncCalls {
			if strings.HasPrefix(call, master+"|") &&
				strings.Contains(call, "/etc/kubernetes/pki/ca.crt") &&
				strings.Contains(call, filepath.Join(rootDir, "test-cluster", "pki", "ca.crt")) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("SyncPKI() missing remote sync command for %s in %v", master, stub.cmdAsyncCalls)
		}
	}

	wantFetches := make([]string, 0, len(corePKIFiles))
	for _, pkiFile := range corePKIFiles {
		wantFetches = append(wantFetches, "master0|"+pathJoinPKI(pkiFile.path)+"|")
		gotPath := filepath.Join(rootDir, "test-cluster", "pki", filepath.FromSlash(pkiFile.path))
		got, err := os.ReadFile(gotPath)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", gotPath, err)
		}
		if string(got) != "master0:"+pkiFile.path {
			t.Fatalf("local synced file %s = %q, want %q", gotPath, string(got), "master0:"+pkiFile.path)
		}
	}
	gotFetches := append([]string{}, stub.fetchCalls...)
	sort.Strings(gotFetches)
	sort.Strings(wantFetches)
	if len(gotFetches) != len(wantFetches) {
		t.Fatalf("SyncPKI() fetchCalls = %v, want %v", gotFetches, wantFetches)
	}
	for i := range wantFetches {
		if !strings.HasPrefix(gotFetches[i], wantFetches[i]) {
			t.Fatalf("SyncPKI() fetchCalls = %v, want prefixes %v", gotFetches, wantFetches)
		}
	}
}

func TestBuildSyncSealosPKIToK8sCommandIncludesOnlyCoreFiles(t *testing.T) {
	cmd := buildSyncSealosPKIToK8sCommand("/var/lib/sealos/default/pki")
	for _, want := range []string{
		"/var/lib/sealos/default/pki/ca.crt",
		"/var/lib/sealos/default/pki/ca.key",
		"/var/lib/sealos/default/pki/front-proxy-ca.crt",
		"/var/lib/sealos/default/pki/front-proxy-ca.key",
		"/var/lib/sealos/default/pki/etcd/ca.crt",
		"/var/lib/sealos/default/pki/etcd/ca.key",
		"/var/lib/sealos/default/pki/sa.key",
		"/var/lib/sealos/default/pki/sa.pub",
		"/etc/kubernetes/pki/etcd",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("buildSyncSealosPKIToK8sCommand() missing %q in %q", want, cmd)
		}
	}
	for _, unwanted := range []string{
		"apiserver.crt",
		"apiserver.key",
		"apiserver-kubelet-client.crt",
		"etcd/peer.crt",
		"etcd/server.crt",
	} {
		if strings.Contains(cmd, unwanted) {
			t.Fatalf("buildSyncSealosPKIToK8sCommand() contains leaf cert %q in %q", unwanted, cmd)
		}
	}
}

func TestSyncPKISealosToK8sPushesControlNodePKIToMastersAndSyncsToK8sPKI(t *testing.T) {
	rootDir := t.TempDir()
	prevRuntimeRoot := constants.DefaultRuntimeRootDir
	constants.DefaultRuntimeRootDir = rootDir
	t.Cleanup(func() {
		constants.DefaultRuntimeRootDir = prevRuntimeRoot
	})

	rt := &KubeadmRuntime{
		execer:       &stubSSH{},
		cluster:      testCluster([]string{"master0", "master1"}),
		pathResolver: constants.NewPathResolver("test-cluster"),
	}

	for _, pkiFile := range corePKIFiles {
		controlNodePath := filepath.Join(rootDir, "test-cluster", "pki", filepath.FromSlash(pkiFile.path))
		if err := os.MkdirAll(filepath.Dir(controlNodePath), 0o755); err != nil {
			t.Fatalf("mkdir control node pki dir: %v", err)
		}
		if err := os.WriteFile(controlNodePath, []byte("sealos:"+pkiFile.path), pkiFile.mode); err != nil {
			t.Fatalf("write control node pki file: %v", err)
		}
	}

	if err := rt.SyncPKI(runtime.SyncPKIDirectionSealosToK8s); err != nil {
		t.Fatalf("SyncPKI(sealos-to-k8s) error = %v", err)
	}

	stub := rt.execer.(*stubSSH)

	wantCopies := len(corePKIFiles) * 2
	if len(stub.copyCalls) != wantCopies {
		t.Fatalf("SyncPKI() copyCalls = %v, want %d", stub.copyCalls, wantCopies)
	}
	for _, master := range []string{"master0", "master1"} {
		found := 0
		for _, call := range stub.copyCalls {
			if strings.HasPrefix(call, master+"|") {
				found++
			}
		}
		if found != len(corePKIFiles) {
			t.Fatalf("SyncPKI() copyCalls for %s = %d, want %d", master, found, len(corePKIFiles))
		}
	}

	if len(stub.cmdAsyncCalls) != 2 {
		t.Fatalf("SyncPKI() cmdAsyncCalls = %v, want two master sync commands", stub.cmdAsyncCalls)
	}
	for _, master := range []string{"master0", "master1"} {
		found := false
		for _, call := range stub.cmdAsyncCalls {
			if strings.HasPrefix(call, master+"|") &&
				strings.Contains(call, filepath.Join(rootDir, "test-cluster", "pki", "ca.crt")) &&
				strings.Contains(call, "/etc/kubernetes/pki/ca.crt") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("SyncPKI() missing remote sync command for %s in %v", master, stub.cmdAsyncCalls)
		}
	}
}

func pathJoinPKI(p string) string {
	return "/etc/kubernetes/pki/" + p
}
