# Package Repository Example

[简体中文](README.zh-CN.md)

This directory is a documentation-only example of the repository layout used by Sealos. It is not embedded in the
Sealos binary and is not used as a runtime fallback.

```text
package-repository/
├── distributions/
│   └── demo/
│       └── v0.1.0.yaml
└── packages/
    └── demo-web/
        └── v0.1.0.yaml
```

The distribution manifest is a self-contained lock. The package manifest under `packages/` is independently reusable;
the distribution keeps its own package definition so it remains reproducible when the package repository changes.

Use this directory as a local repository cache:

```shell
sealos distribution list \
  --repo-cache ./examples/package-repository \
  --offline

sealos distribution show demo@v0.1.0 \
  --repo-cache ./examples/package-repository \
  --offline
```

The Sealos Pro rc6 SOTW is available as `cloud-pro@v5.1.2-rc6` and is the default distribution for the native installer:

```shell
sealos distribution validate cloud-pro@v5.1.2-rc6 \
  --repo-cache ./examples/package-repository \
  --offline
```

The rc6 manifest follows the official image references from the Sealos Pro v5.1.2-rc6 bundle. Cloud application
packages also point to their source contexts in the `v5.1.2-rc6` Git tag of `labring/sealos`; the infrastructure
packages remain remote-only because the bundle does not publish their source trees. Use `--package-mode hybrid` to
build source-backed Cloud packages while using the bundle images for the remaining packages.

For an integration environment with a local registry mirror, edit the manifest's `remote.image` fields in a private
repository cache or use a separate local repository; the canonical fixture remains portable.

The image and Git URL are placeholders. Replace them with images and source repositories that exist in the target
environment before installing or building from this example.
