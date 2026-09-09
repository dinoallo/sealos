# Package Slot 依赖

## 状态

Proposal

## 概要

package manifest 应该通过 package slot 和 SemVer 版本约束描述依赖，而不是
绑定另一个 package 的某个精确版本。distribution 仍然选择精确的 package
ref，但 package manager 会在这个选择集合中解析每个依赖。

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

## 动机

`containerd@v1` 这样的精确依赖会把 package 定义绑定到 catalog 中的一个
版本。这会让兼容版本的发布变得不必要地复杂，也无法表达 package 支持的
版本范围。package repository 应该可以发布兼容的新版本，而不要求所有依赖
它的 manifest 同时修改。

## 契约

`dependsOn` 是可选字段。缺失或为空表示 package 没有依赖。每一项包含两个
必填字段：

- `slot`：另一个 package manifest 的 `name`。
- `version`：由 `github.com/Masterminds/semver/v3` 解析的版本约束。

支持 SemVer 库支持的短版本（例如 `v1`、`v1.28`）、完整版本、预发布版本、
比较范围和 OR 表达式。新 manifest 只使用结构化格式，不支持
`containerd@v1` 这样的旧字符串格式。

distribution 的 package 列表仍然是精确选择，依赖只在这个列表中解析：

1. slot 中不存在 package 时，校验失败。
2. 约束匹配零个已选择 package 时，校验失败。
3. 约束匹配多个已选择 package 时，校验失败，package manager 不会从 catalog
   中选择 latest 或其他隐式版本。
4. 约束恰好匹配一个 package 时，依赖边指向该 package 的精确
   `name@version` ref。

重复依赖 slot、自依赖和循环依赖都是非法的。没有依赖边的 package 保留
manifest 顺序，完整依赖图按稳定拓扑顺序安装。

## State 与 Reset

manifest 中的约束是解析输入，不是 package state。解析完成后，每个已安装
package 保存解析出的精确依赖 ref，例如把 `>=v1 <v2` 保存为
`containerd@v1.28.15`。

`distribution reset` 使用 state 中保存的精确 ref，因此不依赖当前 package
repository，也不受之后 catalog 更新影响。已经包含精确 `dependsOn` ref 的旧
state 文件仍然可以读取，并继续使用这些 ref 排序。

## 范围

依赖解析发生在从配置的 package repository 加载 distribution 和 package
manifest 之后。它不会搜索其他 repository，不会从镜像 metadata 推断依赖，
也不会进行隐式版本选择。源码构建和远程镜像拉取使用同一份解析后的依赖图。

## 测试与验收

实现应覆盖：

- 结构化 YAML 解析和非法约束；
- 短版本、完整版本、预发布版本和范围；
- 缺少 slot、零匹配、多匹配、重复依赖、自依赖和循环依赖；
- 稳定拓扑排序；
- installed state 中保存精确解析 ref；
- reset 使用 state 中的精确依赖排序；
- install/update 使用解析后的依赖图遍历。
