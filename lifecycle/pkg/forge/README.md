Sealos Forge: 下一代集群镜像与特征管理架构方案
1. 设计哲学与核心概念

本方案旨在将 Gentoo Linux 的 Portage (Overlay & USE flags) 思想引入 Sealos 集群镜像管理体系，结合云原生 OCI 标准，实现集群级组件的“源码级/配置级”动态组装，彻底解决高度定制化场景下的镜像管理难题。
1.1 核心概念映射表
传统/Gentoo 概念	Sealos Forge 概念	描述
Package	Cluster Image	最小部署单元（包含基础镜像、YAML 补丁、配置脚本等）。
ebuild	Forgefile	定义如何拉取、补丁合并和构建此集群镜像的声明式“配方”。
Main Tree	Official Registry	官方维护的基础镜像和主流应用组件库。
Overlay	Sealos Overlay	自定义镜像源，支持高优先级覆盖官方源中的同名组件。
USE Flags	Traits (特性标签)	运行时动态启停的配置开关（如 +ha, +ebpf, -prometheus）。
emerge	sealos forge	核心执行引擎，负责解析依赖、融合 Overlay、渲染特征并部署。
2. Overlay 机制与存储介质

Overlay 采用优先级寻址机制，支持两种互补的存储形态：

    Source Overlay (Git 驱动): 存放纯文本的 Forgefile 和配置补丁（YAML/TOML/Scripts）。引擎拉取后在本地进行 JIT（Just-In-Time）即时渲染和构建。

    Binary Overlay (OCI 驱动): 作为全局缓存中心，存放已经按照特定 Traits 组合预先“锻造”好的 OCI 镜像，提供秒级分发能力。

3. 标准化目录树与 Forgefile 设计
3.1 Source Overlay 目录规范
Plaintext

my-company-overlay/
├── profiles/
│   ├── traits.desc                # 全局 Traits 字典定义与说明
│   └── forge.yaml                 # 全局默认后备配置
└── apps/
    └── infrastructure/
        └── containerd/
            └── 1.7/
                ├── Forgefile      # 核心锻造剧本
                ├── patches/       # Kustomize 或 JSON/TOML 补丁
                └── scripts/       # 自定义初始化脚本

3.2 声明式配置覆盖 (Forgefile)

Forgefile 拒绝对核心文件的暴力替换，采用结构化补丁和安全注入策略保障系统稳定：

    toml_patch / json_patch: 针对 config.toml 等核心配置进行键值对级别的无损合并。

    kustomize_patch: 标准的 Kubernetes 资源 Strategic Merge。

    template: 基于传入参数的 Go Template 动态渲染。

    copy (附带 backup: true): 安全的文件替换，自动备份系统级原生脚本。

4. OCI 进阶存储与引擎执行流

为了解决多 Traits 带来的“组合爆炸”与 Tag 污染问题，方案深度利用了 OCI Image Index (Manifest List) 和 Annotations 技术。
4.1 存储结构

同一个 mysql:8.0 Tag 对应一个 OCI Index。Index 内部包含多个 Manifest，每个 Manifest 通过 Annotation 标记其特有的 Traits 组合：
JSON

"annotations": {
  "forge.sealos.io/traits": "+cgroupv2,+ha"
}

4.2 sealos forge 执行流

当用户执行 sealos forge run mysql:8.0 --traits="+ha,+cgroupv2" 时：

    特征归一化： 引擎对 Traits 按字母排序（+cgroupv2,+ha）。

    缓存查询： 拿着归一化后的 Traits，去高优先级 Binary Overlay (Registry) 请求 mysql:8.0 的 Image Index。

    精确命中 (Cache Hit)： 解析 Annotations，若匹配成功，直接拉取该 Digest 的镜像并运行（极速路径）。

    未命中回退 (Cache Miss/JIT)： 若未找到，降级到 Source Overlay (Git)，利用 Forgefile 在本地执行 Kustomize/文件合并 -> 构建临时镜像 -> 运行 -> (可选) 将新产物 Push 回 Binary Overlay 反哺团队。

5. 宏观编排：ClusterForge 清单

除了单组件管理，平台工程团队可以通过 ClusterForge.yaml 定义整个集群的拓扑和特性基线，实现基础设施即代码（IaC）。
YAML

apiVersion: forge.sealos.io/v1alpha1
kind: ClusterForge
metadata:
  name: prod-cluster-matrix
spec:
  base:
    image: labring/kubernetes:v1.28.0
  components:
    - name: networking/cilium
      version: 1.14.0
      traits: ["+ebpf", "+cgroupv2"]
      overlay: internal-infra-overlay
  strategy:
    allowJITBuild: true

执行方式： sealos forge apply -f ClusterForge.yaml
引擎将解析依赖 DAG 图，并行执行 OCI 寻址与 JIT 构建，最终将所有定制化组件的产物合并为一个巨型 Rootfs 进行交付。
