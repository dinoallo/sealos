# 可选的 Package 清理 Hook

## 状态

Proposal

## 概要

Sealos 的 distribution 会记录目标环境中已安装的 package，但目前没有
package 级别的清理生命周期。现有 reset 流程可以清理 Kubernetes 和
Sealos runtime，却无法处理独立 package 自己创建的资源。

本提案为 package 镜像增加可选的清理 hook。package 可以在镜像 metadata
中声明 `sealos.io/package-clean` label，label 的值是镜像内清理脚本的路径。
执行 `distribution reset` 时，Sealos 会执行声明了该 label 的 package 的
清理流程。没有该 label 的 package 不执行 package 级别的 reset 操作。

package 可以在 distribution manifest 中声明依赖。缺少 `dependsOn` 时表示
该 package 没有依赖。清理顺序使用考虑依赖关系后的安装顺序逆序执行。

## 背景与动机

package 模型已经从只包含镜像的 distribution manifest 演进为支持独立安装
的包管理模型。有些 package 创建的资源并不是 Kubernetes 资源，单独执行
Kubernetes reset 无法清理。若由 package manager 了解所有 package 的文件和
服务，又会让 Sealos 与每个 package 的实现细节强耦合。

更合适的方式是由 package 自己描述清理行为，由 package manager 负责生命周期
编排、顺序控制和错误汇总。

## 目标

- 允许 package 在镜像 metadata 中声明可选清理 hook。
- 按安装顺序逆序执行 cleanup hook。
- 在所有已记录的 master 和 worker 节点执行 hook。
- 同时支持本地源码构建和远程镜像拉取的 package。
- 没有 cleanup hook 的 package 不执行 package 级别的 reset 操作。
- 一个 hook 失败后仍继续清理其他 package。
- 清理未完成时保留 package state，支持重试。

## 非目标

- 不根据 package 名称或镜像 label 推断依赖图。
- 不为没有 hook 的 package 定义 fallback 清理流程。
- 不允许 distribution manifest 直接提供任意远程 shell 命令。

对于新的 distribution，package repository 而不是 Sealos 二进制或 `runtime`
repository 是 package 定义的 source of truth。`runtime` repository 可以作为
构建 package 镜像时的实现参考，但不属于 package manager 的契约。

## Package 依赖

distribution 只选择精确的 package reference，package manifest 自己维护依赖
和镜像定义：

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

`dependsOn` 是可选字段。缺失或为空时表示没有依赖。每个依赖指定一个 package
slot 和一个 SemVer 约束，约束只在当前 distribution 选择的 package 中解析，
并且必须恰好匹配一个 package。manifest 校验会拒绝不存在的 slot、零个或多个
匹配、重复 slot、自依赖和循环依赖。没有依赖关系的 package 保持 manifest 中
的顺序，依赖边优先于原始顺序。

package manager 会把解析后的精确 package ref 保存到 package state。这样即使
package repository 后续发布了新的版本，reset 仍然使用安装时确定的依赖关系。

package manager 按稳定拓扑顺序安装，并按逆序清理。这样 Kubernetes 可以在
containerd 之前清理，而不需要在 reset 代码中硬编码 containerd。registry
是否依赖其他 package，由 package 定义根据实际实现声明。

## Hook 约定

package 镜像可以定义以下 OCI label：

```text
sealos.io/package-clean=/opt/sealos/package-clean.sh
```

label 值必须是镜像内的绝对路径，并且对应一个可执行的普通 shell 脚本。
Sealos 必须拒绝空路径、相对路径以及包含 `..` 路径组件的值。

脚本执行时使用临时 package 目录作为工作上下文，并获得以下环境变量：

```text
SEALOS_PACKAGE_NAME
SEALOS_PACKAGE_VERSION
SEALOS_CLUSTER_NAME
```

hook 应当设计为幂等操作，并在成功时返回零退出码。由于 hook 在普通集群
reset 之后执行，因此不能假设 Kubernetes、containerd 或本地 registry 仍在
运行。

使用 namespaced label 是有意的设计。现有的 `clean`、`clean-cri` 和
`clean-registry` label 会从多个挂载镜像中合并，并属于 legacy rootfs/bootstrap
生命周期。将它们用于 package cleanup 会导致不同 package 的清理行为互相覆盖。

## 生命周期

### 安装阶段

1. 按选择的 package mode 解析或构建 package 镜像。
2. 检查镜像 metadata 中是否存在 `sealos.io/package-clean`。
3. 没有 label 时按普通 package 记录状态。
4. 有 label 时校验路径，并在镜像从本地存储删除前提取脚本。
5. 将提取出的脚本作为 package-state artifact 与已安装 package 关联保存。

保存脚本是支持本地源码 package 的必要条件。本地构建的镜像在安装完成后
可能会被删除，也可能无法在 reset 时从远程 registry 获取。

### Reset 阶段

`distribution reset` 读取 package state，并按考虑依赖关系后的安装顺序逆序
遍历 package。对存在 cleanup artifact 的 package，将脚本复制到所有已记录
的 master 和 worker，并远程执行。只有所有 cleanup hook 都成功后，CLI 才会
删除 package state。

没有 cleanup artifact 的 package 会被跳过。package cleanup executor 不能使用
合并后的 cluster label map，因为该 map 无法表达每个 package 独立的 cleanup
hook。`distribution reset` 不会调用旧的 `sealos reset`，也不会调用旧的
`clean`、`clean-cri` 和 `clean-registry` 流程。

### State

package state schema 增加每个已安装 package 的可选 cleanup metadata。metadata
只标识 cleanup artifact 和脚本路径，不保存凭据。

缺少 cleanup metadata 表示该 package 没有 package cleanup hook。如果 reset
失败，state 文件和未执行的 cleanup artifact 都必须保留，以便重试。

## 错误处理

cleanup 对多个 package 是 best-effort，但最终结果必须严格：

- 记录失败 hook 的 package reference 和错误。
- 继续按逆序执行剩余 package 的 cleanup。
- 所有 hook 尝试完成后，只要有失败就返回聚合错误。
- 返回聚合错误时保留 package state。
- 下一次 reset 可以重试未完成的清理状态。

每个失败都应包含 package reference 和目标节点，但不能在日志中暴露 SSH
密码或其他凭据。

## 兼容性

新的 distribution 只包含 package manifest，不再支持 image-only 的 `images:`
distribution。没有 `sealos.io/package-clean` 的镜像没有 package 级别的 reset
操作。普通 `sealos reset` 仍是独立的 legacy 命令，不会被
`distribution reset` 调用。

`clean`、`clean-cri` 和 `clean-registry` 是 legacy label，不会被
`distribution reset` 使用。

新的 containerd、registry 和 Kubernetes package 镜像自己维护 lifecycle
hook。现有 label 不作为新 package 的 lifecycle 契约。

## 安全考虑

package cleanup script 使用与现有 reset 操作相同的远程权限，因此：

- 提取和执行前必须校验脚本路径。
- 脚本必须以严格权限复制到远程临时路径。
- 必须正确转义 shell 参数，不能将 package metadata 直接拼接进 shell 源码。
- 执行完成后必须删除临时脚本。
- 日志必须隐藏连接凭据。

cleanup hook 属于 package 代码，应当按照与 package 安装镜像相同的信任模型
进行审核。

## 测试与验收标准

实现满足以下条件即可验收：

- 没有 label 的 package 不产生 package cleanup 命令。
- 有效 label 会在安装阶段被提取并保存。
- 本地构建的 package 在原始镜像被删除后仍可 reset。
- 多个 hook 按安装顺序逆序执行。
- hook 在所有已记录的 master 和 worker 执行。
- 一个 hook 失败不会阻止后续 hook 执行。
- 失败时返回聚合错误并保留 state。
- 成功 reset 后删除 state 和临时 cleanup artifact。
- 没有 cleanup hook 的 package 会被 `distribution reset` 跳过。
- dry-run 可以展示清理顺序，但不修改目标机或本地 package state。

## 已考虑的替代方案

### 复用现有 `clean` label

不采用。现有 label 会从多个挂载镜像中合并，主要用于 rootfs/bootstrap
清理，无法安全表示每个 package 独立的 cleanup hook。

### 只保存镜像引用

不采用。本地源码构建的镜像可能只存在于本机，并且通常会在安装后删除。
reset 不应依赖重新构建或重新发布 package。

### 继续将 containerd、registry 和 Kubernetes 放在 runtime repository

不作为新 distribution 的方案。package repository 应当拥有 package 定义和
发布镜像，runtime repository 只作为实现参考。
