# Kubernetes Lifecycle Management

Sealos provides a powerful set of tools that allow users to easily manage the entire lifecycle of a cluster.

## Features

With Sealos, you can install a bare Kubernetes cluster without any components. Additionally, Sealos can assemble various
upper-layer distributed applications on top of Kubernetes using cluster image capabilities, such as databases, message
queues, and more.

Sealos not only allows you to install a single-node Kubernetes development environment but also enables you to build
production-grade highly available clusters with thousands of nodes.

Sealos offers features like cluster scaling, backup and recovery, and cluster release. It provides an excellent
Kubernetes runtime experience even in offline environments.

## Key Features

- ARM support. Offline packages v1.20 and above support integration with both containerd and Docker.
- Provides 99-year certificates and supports cluster backup and upgrade.
- Does not rely on Ansible, HAProxy, or Keepalived. It is a standalone binary tool with zero dependencies.
- Provides offline installation. Different versions of Kubernetes only require different cluster images.
- High availability is achieved through localLB based on IPVS, which consumes fewer resources and provides stability and
  reliability, similar to kube-proxy implementation.
- Automatically recognizes image names using image-cri-shim, making offline delivery more convenient.
- Almost compatible with all x86_64 architectures that support systemd.
- Easy addition/deletion of cluster nodes.
- Trusted by tens of thousands of users in production environments, stable and reliable.
- Supports cluster images, allowing you to customize and combine the cluster components you need, such as OpenEBS
  storage + database + MinIO object storage.
- Uses the SDK of Buildah to standardize the image format, fully compatible with OCI standards.

## Running a Kubernetes Cluster with Sealos

Running a Kubernetes cluster with Sealos is straightforward. Just follow these steps:

```bash
$ curl -sfL  https://raw.githubusercontent.com/labring/sealos/v4.3.0/scripts/install.sh \
    | sh -s v4.3.0 labring/sealos
# Create a cluster
$ sealos run labring/kubernetes:v1.25.0-4.2.0 labring/helm:v3.8.2 labring/calico:v3.24.1 \
     --masters 192.168.64.2,192.168.64.22,192.168.64.20 \
     --nodes 192.168.64.21,192.168.64.19 -p [your-ssh-passwd]
```

[![asciicast](https://asciinema.org/a/519263.svg)](https://asciinema.org/a/519263?speed=3)

## Running Distributed Applications on the Cluster

With the `sealos run` command, you can run various distributed applications on the cluster, such as databases, message
queues, AI capabilities, and even enterprise-level SaaS software. For example:

```shell
# MySQL cluster
$ sealos run labring/mysql-operator:8.0.23-14.1

# Clickhouse cluster
$ sealos run labring/clickhouse:0.18.4

# Redis cluster
$ sealos run labring/redis-operator:3.1.4
```

## Installing Sealos Cloud from a Distribution

Versioned Sealos Cloud installations are tracked by distribution manifests in the external Sealos package repository.
The CLI caches that Git repository locally and updates it at most once per day:

```shell
sealos repo sync
sealos repo status
sealos distribution list
sealos distribution install cloud-pro@v5.1.2-rc6
```

After installing a distribution, the CLI records its package state on the control machine under
`/var/lib/sealos/package-state/`. Repository synchronization only updates the local catalog; it does not change a
cluster. To inspect and apply a newer SOTW manifest:

```shell
sealos distribution diff cloud-pro@v5.1.2-rc7 --cluster default
sealos distribution update cloud-pro@v5.1.2-rc7 --cluster default
sealos distribution status --cluster default
```

To remove a distribution-installed cluster, use the distribution-specific reset command. It validates the recorded
target, removes the distribution runtime, reuses the existing cluster reset engine, and deletes the local package
state only after the remote reset succeeds:

```shell
sealos distribution reset --cluster default
```

`--cluster` is the stable cluster name or ID used by distribution state. Master and worker addresses are connection
metadata, not cluster identity, so changing a master address does not create a new distribution target. Use
`--masters`, `--nodes`, `--user`, `--ssh-key`, or `--ssh-port` only for the initial install/adopt or to override
recorded connection metadata. `--target-id` remains a compatibility alias for older state directories. Add `--force`
only when the destructive confirmation should be skipped. This command is separate from `sealos reset`, which
continues to operate on a named legacy Clusterfile.

New packages are installed in SOTW order. Changed standalone packages are applied after confirmation. Kubernetes,
Cilium, and Cloud aggregate package changes are reported as blocked, while packages removed from a newer SOTW are
reported as orphaned and are not automatically uninstalled. Use `--yes` for non-interactive updates. An existing
cluster created before package-state tracking must be adopted explicitly:

```shell
sealos distribution adopt cloud-pro@v5.1.2-rc6 --cluster default --masters 192.0.2.10
```

Use a different `--cluster` value when the same control machine manages multiple clusters, and `--state-dir` to
select a different state root for an isolated environment or test.

Use `--refresh` to force a repository update or `--offline` to use the existing cache. The repository can be
overridden with `--repo`, `--repo-ref`, and `--repo-cache`, or with `SEALOS_PACKAGE_REPO`,
`SEALOS_PACKAGE_REPO_REF`, and `SEALOS_PACKAGE_REPO_CACHE`. The default repository is
`https://github.com/labring-sigs/sealos-package-repository.git`.

The persistent configuration file is `$XDG_CONFIG_HOME/sealos/config.yaml` or
`~/.config/sealos/config.yaml`:

```yaml
repository:
  url: https://github.com/labring-sigs/sealos-package-repository.git
  ref: main
```

Configuration precedence is CLI flags, environment variables, this file, then built-in defaults. The cache path is
derived from the repository URL and ref when `cacheDir` is omitted.

A minimal repository layout is available in [`examples/package-repository`](../examples/package-repository/README.md).
It is documentation-only and can be used as a local repository with `--repo-cache` and `--offline`.

Use `--interactive=false` with `--masters` and `--cloud-domain` for automation. The legacy
`scripts/cloud/install-v2.sh` entrypoint remains as a compatibility wrapper and forwards to this command.

Each distribution is a self-contained lock manifest. Every package records its remote OCI image, optional digest, and
ordered local/Git build sources. Updating the package repository therefore does not require updating the Sealos binary,
while an existing distribution remains reproducible:

```yaml
name: cloud
version: v5.1.0
packages:
  - name: sealos-cloud-user-controller
    version: v5.1.0
    remote:
      image: ghcr.io/labring/sealos-cloud-user-controller:v5.1.0
      digest: sha256:...
    sources:
      - type: local
        path: /workspace/sealos-cloud-user-controller
        context: deploy
        file: Kubefile
      - type: git
        url: https://github.com/labring/sealos-cloud-user-controller.git
        ref: v5.1.0
        context: deploy
        file: Kubefile
```

Remote images are the default resolution mode; source builds are explicit:

```shell
sealos distribution install cloud-pro@v5.1.2-rc6 --package-mode remote
sealos distribution install <distribution@version> --package-mode source
sealos distribution install <distribution@version> --package-mode hybrid
```

When a distribution contains multiple versions of the same bootstrap package, select the Cilium version explicitly
with `--cilium-version` or `SEALOS_V2_CILIUM_VERSION`. The rc6 default is `v1.16.9`; set the option explicitly when
using a distribution with a different available Cilium version.

`source` remains supported for legacy packages. New packages should use ordered `sources`: the first available local
checkout is preferred, and a Git source is cloned automatically when the local checkout is unavailable. `source.path`
is the package's own source checkout; use an absolute path when packages live in different repositories. Each Git
source is cached persistently and can be overridden with `--source-cache` or `SEALOS_V2_SOURCE_CACHE`.

```yaml
sources:
  - type: local
    path: /path/to/sealos-cloud-user-controller
    context: deploy
    file: Kubefile
  - type: git
    url: https://github.com/labring/sealos-cloud-user-controller.git
    ref: v5.1.0
    context: deploy
    file: Kubefile
```

`--source-root` is only a compatibility base for legacy manifests with relative local paths. `source` mode requires
every package to resolve to source. `hybrid` mode builds packages with available source and uses their remote image
for packages without source metadata. Core images whose build sources are not published remain remote-only.

## Customizing the Cluster

For cluster images not available in the Sealos ecosystem, users can easily build and customize their own cluster images.
For example:

Sealfile:

```shell
FROM kubernetes:v1.25.0
COPY flannel-chart .
COPY mysql-chart .
CMD ["helm install flannel flannel-chart", "helm install mysql mysql-chart"]
```
Build and run the Cluster Image
```shell
sealos build -t my-kubernetes:v1.25.0 .
sealos run my-kubernetes:v1.25.0 ...
```

## Frequently Asked Questions

**Is Sealos a Kubernetes installation tool?**

Installation and deployment are basic functions of Sealos, similar to the boot module in a single-node operating system.
Sealos' boot module effectively manages the lifecycle of Kubernetes in any scenario.

**What are the differences between Sealos, Rancher, and KubeSphere?**

Sealos is designed with the philosophy of "simplifying complexity, freely assembling, and simplicity as the ultimate
goal." Sealos leverages the capabilities of Kubernetes to provide users with exactly what they need in a simple way.
Users may not necessarily need Kubernetes; what they need is specific functionality.

Sealos is highly flexible and does not impose additional burdens on users. Its form depends on user requirements and the
applications being installed. The core of Sealos is distributed applications, and all applications are treated equally.
