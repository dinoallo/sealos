# Optional Package Cleanup Hooks

## Status

Proposal

## Summary

Sealos distributions record the packages installed on a target, but packages
currently do not have a package-specific cleanup lifecycle. The existing reset
flow cleans Kubernetes and the Sealos runtime, but it cannot clean resources
owned by an independent package when that package needs additional cleanup.

This proposal adds an optional cleanup hook to package images. A package may
publish an image label named `sealos.io/package-clean` whose value is the path
to a cleanup script inside the package image. During `distribution reset`,
Sealos executes hooks for packages that provide them. Packages without the
label have no package-level reset action.

Packages may declare dependencies in the distribution manifest. An omitted
`dependsOn` list means that the package has no dependencies. Cleanup order is
the reverse of the dependency-aware installation order.

## Motivation

The package model has evolved from a distribution manifest containing images to
a package manager model with independently installable packages. Some package
resources are not Kubernetes resources and cannot be removed by Kubernetes
reset alone. Requiring the package manager to know every package's files and
services would tightly couple Sealos to every package implementation.

The package should be able to describe its own cleanup behavior while the
package manager remains responsible for lifecycle orchestration, ordering, and
error reporting.

## Goals

- Allow a package to declare an optional cleanup hook in its image metadata.
- Execute cleanup hooks in reverse installation order.
- Run hooks on every recorded master and worker node.
- Support packages built from local source as well as packages pulled from a
  remote registry.
- Treat packages without a cleanup hook as packages with no package-level reset
  action.
- Continue cleanup of remaining packages when one hook fails.
- Keep package state available when cleanup is incomplete so reset can be
  retried.

## Non-goals

- Inferring a dependency graph from package names or image labels.
- Defining a fallback cleanup flow for packages that do not publish a hook.
- Running arbitrary cleanup commands supplied directly by a distribution
  manifest.

The package repository, rather than the Sealos binary or the `runtime`
repository, is the source of truth for the package definitions used by a new
distribution. The `runtime` repository may be used as an implementation
reference when building the package images, but it is not part of the package
manager contract.

## Package Dependencies

Distributions select exact package references, while each package manifest owns
its dependencies and image definition:

```yaml
# distributions/platform/v0.1.0.yaml
packages:
  - containerd@v1
  - kubernetes@v1
```

```yaml
# packages/kubernetes/v1.yaml
name: kubernetes
version: v1
dependsOn:
  - slot: containerd
    version: ">=v1 <v2"
remote:
  image: ghcr.io/example/kubernetes:v1
```

`dependsOn` is optional. When it is absent or empty, the package has no
dependencies. Each dependency names a package slot and a SemVer constraint;
the constraint is resolved against packages selected by the current
distribution. It must match exactly one selected package. Manifest validation
rejects missing slots, zero or multiple matches, duplicate slots,
self-dependencies, and dependency cycles. Independent packages retain their
manifest order; dependency edges take precedence.

The package manager records the resolved exact dependency refs in package state.
This keeps reset deterministic even if the package repository later publishes
new package versions.

The package manager installs packages in stable topological order and cleans
them in reverse order. This allows Kubernetes to be cleaned before containerd
without hardcoding containerd into the reset implementation. Registry
dependencies are declared by the package definitions when required by their
implementation.

## Hook Contract

The package image may define the following OCI label:

```text
sealos.io/package-clean=/opt/sealos/package-clean.sh
```

The label value is an absolute path inside the package image. The referenced
file must be a regular executable shell script. Sealos must reject empty paths,
relative paths, and paths containing `..` components.

The script is executed from its temporary package directory and receives these
environment variables:

```text
SEALOS_PACKAGE_NAME
SEALOS_PACKAGE_VERSION
SEALOS_CLUSTER_NAME
```

The hook is expected to be idempotent. A successful hook should return exit
status zero. A hook must not assume that Kubernetes, containerd, or the local
registry is still running, because hooks run after the normal cluster reset.

The namespaced label is deliberate. Existing labels such as `clean`,
`clean-cri`, and `clean-registry` are merged across mounted images and belong
to the legacy rootfs/bootstrap lifecycle. Reusing them for package cleanup
would allow one package to overwrite another package's cleanup behavior.

## Lifecycle

### Installation

1. Resolve or build the package image using the selected package mode.
2. Inspect the image metadata for `sealos.io/package-clean`.
3. If the label is absent, record the package normally.
4. If the label is present, validate the path and extract the script before the
   package image is removed from local storage.
5. Store the extracted script as a package-state artifact associated with the
   installed package.

Persisting the script is required for local-source packages. A locally built
   image may be removed after installation and may not be available from a
   remote registry when the cluster is reset.

### Reset

`distribution reset` loads the recorded package state and walks installed
packages in reverse dependency-aware installation order. For packages with a
cleanup artifact, it copies the script to every recorded master and worker and
executes it remotely. Package state is removed by the CLI only when all cleanup
hooks succeed.

Packages without a cleanup artifact are skipped. The package cleanup executor
must not use the merged cluster label map, because that map cannot represent
independent package hooks. `distribution reset` does not invoke the legacy
`sealos reset` or the legacy `clean`, `clean-cri`, and `clean-registry` flows.

### State

The package state schema gains optional cleanup metadata for each installed
package. The metadata identifies the cleanup artifact and its script path; it
does not store credentials.

Missing cleanup metadata means that the package has no package cleanup hook. If
reset fails, both the state file and unexecuted cleanup artifacts remain
available for a retry.

## Failure Handling

Cleanup is best-effort across packages but strict in its final result:

- A failed hook is recorded with its package reference and error.
- Cleanup continues for all remaining packages in reverse order.
- After all hooks have been attempted, reset returns an aggregate error if any
  hook failed.
- Package state is retained when an aggregate error is returned.
- A subsequent reset retries the remaining cleanup state.

The cleanup executor should report the package reference and target node for
each failure without exposing SSH passwords or other credentials.

## Compatibility

New distributions contain only package manifests; image-only `images:`
distributions are not part of the package manager contract. Images that do not
define `sealos.io/package-clean` have no package-level reset action. The
ordinary `sealos reset` command remains a separate legacy command; it is not
part of `distribution reset`.

`clean`, `clean-cri`, and `clean-registry` are legacy labels and are not
consumed by `distribution reset`.

New containerd, registry, and Kubernetes package images own their lifecycle
hooks. The existing labels are not used as the lifecycle contract for new
packages.

## Security Considerations

Package cleanup scripts run with the same remote privileges as the existing
reset operation. Therefore:

- The script path must be validated before extraction or execution.
- The script must be transferred to a temporary destination with restrictive
  permissions.
- The executor must use shell argument quoting and must not interpolate package
  metadata directly into shell source.
- Temporary scripts must be removed after execution.
- Logs must redact connection credentials.

Cleanup hooks are package code and should be reviewed with the same trust model
as package installation images.

## Testing and Acceptance Criteria

The implementation is acceptable when the following cases pass:

- A package without the label produces no package cleanup command.
- A package with a valid label is extracted and stored during installation.
- A locally built package can be reset after its image has been removed.
- Multiple hooks run in reverse installation order.
- Hooks run on all recorded masters and workers.
- One failed hook does not prevent later hooks from running.
- Reset returns an aggregate error and preserves state after a failure.
- A successful reset removes state and temporary cleanup artifacts.
- Packages without a cleanup hook are skipped by `distribution reset`.
- Dry-run output shows the cleanup order without modifying the target or local
  package state.

## Alternatives Considered

### Reuse the existing `clean` label

Rejected. Existing labels are merged from all mounted images and are intended
for rootfs/bootstrap cleanup. They cannot safely represent one cleanup hook per
package.

### Store only the image reference

Rejected. A source-built image may be local-only and is normally removed after
installation. Reset must remain possible without rebuilding or republishing
the package.

### Keep containerd, registry, and Kubernetes in the runtime repository

Rejected for new distributions. The package repository should own the package
definitions and published images. The runtime repository remains a reference
for implementation details only.
