# Package Slot Dependencies

## Status

Proposal

## Summary

Package manifests should describe dependencies by package slot and a
SemVer-compatible version constraint instead of referring to one exact version
of another package. A distribution still selects exact package references, but
the package manager resolves each dependency against that selected set.

```yaml
# distributions/platform/v0.1.0.yaml
packages:
  - containerd@v1
  - kubernetes@v1.28.15
```

```yaml
# packages/kubernetes/v1.28.15.yaml
name: kubernetes
version: v1.28.15
dependsOn:
  - slot: containerd
    version: ">=v1 <v2"
remote:
  image: ghcr.io/example/kubernetes:v1.28.15
```

## Motivation

An exact dependency such as `containerd@v1` couples a package definition to one
specific catalog entry. That makes package updates unnecessarily invasive and
prevents a package from declaring compatibility with a range of versions.
Package repositories should be able to publish compatible package versions
without requiring every dependent manifest to be rewritten.

## Contract

`dependsOn` is optional. An omitted or empty list means that the package has no
dependencies. Each item has two required fields:

- `slot`: the `name` of another package manifest.
- `version`: a version constraint parsed by `github.com/Masterminds/semver/v3`.

Short versions such as `v1` and `v1.28`, complete versions, prerelease
versions, comparison ranges, and OR expressions supported by the SemVer
library are accepted. New manifests use the structured form only; the old
scalar form such as `containerd@v1` is not supported for `dependsOn`.

The distribution package list remains an exact selection. Dependency
resolution only considers packages in that list:

1. A dependency with no package in the requested slot fails validation.
2. A dependency whose constraint matches zero selected packages fails
   validation.
3. A dependency whose constraint matches more than one selected package fails
   validation. The package manager does not choose a catalog or latest version.
4. A dependency that matches exactly one selected package becomes an edge to
   that package's exact `name@version` reference.

Duplicate dependency slots, self-dependencies, and dependency cycles are
invalid. Packages without dependency edges retain their manifest order. The
complete graph is installed in stable topological order.

## State And Reset

Manifest constraints are resolution input, not package state. After resolution,
the exact dependency refs are stored with every installed package. For example,
`>=v1 <v2` may be persisted as `containerd@v1.28.15`.

`distribution reset` uses those persisted refs and therefore does not depend on
the current package repository or a later catalog update. Existing state files
that already contain exact `dependsOn` refs remain readable and continue to be
ordered using those refs.

## Scope

Resolution is performed after the distribution and package manifests have been
loaded from the configured package repository. It does not search other
repositories, infer dependencies from image metadata, or perform implicit
version selection. Source builds and remote image pulls use the same resolved
dependency graph.

## Testing And Acceptance

The implementation should cover:

- structured YAML parsing and invalid constraints;
- short versions, complete versions, prereleases, and ranges;
- missing slots, zero matches, multiple matches, duplicates, self-dependencies,
  and cycles;
- stable topological ordering;
- exact resolved refs in installed state;
- reset ordering from persisted state refs; and
- install/update dependency traversal from the resolved graph.
