# Package Repository 示例

[English](README.md)

此目录仅用于展示 Sealos 包仓库的目录结构，不会被编译进 Sealos 二进制，也不会作为运行时 fallback 使用。

```text
package-repository/
├── distributions/
│   └── demo/
│       └── v0.1.0.yaml
└── packages/
    └── demo-web/
        └── v0.1.0.yaml
```

`distributions` 中的清单是自包含锁清单。`packages/` 下的 package 清单可以独立复用；distribution 会保存自己的
package 定义，使其在包仓库发生变化后仍然可复现。

可以将此目录作为本地仓库缓存使用：

```shell
sealos distribution list \
  --repo-cache ./examples/package-repository \
  --offline

sealos distribution show demo@v0.1.0 \
  --repo-cache ./examples/package-repository \
  --offline
```

Sealos Pro rc6 的 SOTW 清单引用为 `cloud-pro@v5.1.2-rc6`，也是原生安装器的默认发行版：

```shell
sealos distribution validate cloud-pro@v5.1.2-rc6 \
  --repo-cache ./examples/package-repository \
  --offline
```

rc6 清单使用 Sealos Pro v5.1.2-rc6 bundle 中的官方镜像引用。Cloud 应用包还指向 `labring/sealos` 的
`v5.1.2-rc6` Git tag 中对应的源码目录；基础设施包因为 bundle 没有发布源码目录，仍然只使用远程镜像。使用
`--package-mode hybrid` 时，会构建有源码的 Cloud 包，并使用其余包的 bundle 镜像。

集成环境如果使用本地镜像仓库，可以在私有的 repository cache 中修改 `remote.image`，或使用单独的本地
repository；canonical fixture 本身保持可移植。

其中的镜像地址和 Git 地址都是占位符。在安装或从源码构建前，请替换为目标环境中实际存在的镜像和源码仓库。
