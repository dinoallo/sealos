# 内部用户凭证验收状态

状态：验收进行中，尚未批准生产凭证或公网入口。以下 `[x]` 仅表示所注明范围的
证据已通过；标记为部分完成的项目仍未满足发布门禁。

更新时间：2026-09-09。本文件记录
[`internal-user-credentials-acceptance.md`](internal-user-credentials-acceptance.md)
当前的执行状态，不替代验收矩阵，也不把缺少证据的项目视为通过。

## 已验证证据

- [x] `ID-001` 至 `ID-008`：身份校验、暂停/恢复、用户删除清理和用户重建。
- [x] 普通用户/管理员 Broker 授权、自身 target 限制、审批引用校验、固定 audience、
  TTL 限制和 profile allowlist。
- [x] base 凭证使用稳定 ServiceAccount；高权限凭证只使用 Lease 专属临时
  ServiceAccount 和 binding。
- [x] Lease 签发、撤销、过期、清理、finalizer 移除及重启收敛场景。
- [x] Broker 和 Controller ServiceAccount 权限边界，包括 Controller 的
  namespace-scoped Secret/ServiceAccount 访问。
- [x] CLI Authorization Code + PKCE 流程和 `0600` kubeconfig 文件权限。
- [x] Broker 源 IP 和可信代理校验 `NET-001` 至 `NET-004`。
- [x] 最终集群检查：19 条 CredentialLease 均为终态，均无 finalizer，且没有临时
  Lease ServiceAccount、Secret 或 ClusterRoleBinding 残留。
- [x] Targeted Go test、Broker race test、vet 和 build 均通过。具体命令见原验收文档。
- [x] `RBAC-004`：独立 Admission Webhook 已部署并 Ready；其校验了 Controller
  ServiceAccount 的 binding 创建、更新和删除。任意/错误归属 binding 被拒绝，自有
  base binding 清理可成功并由 Controller 重建；Webhook 不可用时 RBAC 写请求 fail-closed。
- [x] `LEASE-002` 自动化覆盖：清理失败时 Lease finalizer 保留，并返回正数重试延迟。
- [x] `LEASE-005` 自动化覆盖：ServiceAccount、Secret 和 binding 的 ownership 不匹配
  会被拒绝接管，并产生可处理的 Warning Event。
- [x] `LEASE-008` 自动化覆盖：保存并使用 TokenRequest 的实际过期时间，而不是请求 TTL。
- [x] `LEASE-007`：显式 acceptance-tag 测试在真实 API Server 上成功执行了
  `serviceaccounts/token`，随后不读取、不持久化 token 数据而丢弃响应并返回模拟的响应丢失
  错误。Lease 保留 `ConsumedAt`/Prepared，第二次消费被拒绝；临时测试资源已清理。
- [x] `CLI-003`：拒绝 symlink 输出路径，普通输出文件权限为 `0600`。
- [x] `CLI-006`：CLI 测试验证 base 请求只包含 `requestedTTLSeconds`，并拒绝通过参数、
  配置或篡改请求注入 target/profile/approval-reference。
- [x] `CLI-010`：CLI 测试验证 OIDC 和 Broker endpoint 在不可信证书或错误 TLS server name
  时均被拒绝；完整 CLI 运行还确认 OIDC TLS 失败不会访问 Broker 或创建输出文件。
- [x] `CLI-005`：CLI 测试覆盖显式和标准路径 YAML 配置加载、命令行优先级、严格字段校验
  和多文档拒绝；配置格式排除 token、kubeconfig、client secret 和一次性审批 reference。
- [x] `BROKER-010`/`CLI-004` 自动化覆盖：Broker internal approve、持久化
  `AwaitingRedemption` Lease、固定 self target、重放拒绝、只带 reference 的 redeem 请求和
  `0600` kubeconfig 输出均已有定向单元测试；用户侧 elevated request 流程已移除。
- [x] `ADAPTER-001` 至 `ADAPTER-005` 自动化覆盖：回调认证、权威外部审批系统查询、固定 issuer/
  表单映射、Broker 幂等重试、通知重试和无 Kubernetes 凭据均有 `pkg/approvaladapter` 测试。
- [x] `PROF-007`：已启用部署中的 admission 控制并完成临时 profile 的集群验证；workload
  创建、deployment 更新、Secret 读取、ServiceAccount 创建和 impersonation 均被拒绝。
- [x] `NET-005`：Broker 进程内请求限流、非法 method、错误 JSON 和超大 request body
  会被拒绝，且不会签发凭证；健康检查仍可用。
- [x] `NET-007`：Broker 重启后两个副本和 Service endpoints 保持 Ready；无凭据可信 peer
  重放中，允许客户端 IP 返回 `401`，不允许、缺失和格式错误 header 返回 `403`；普通 Pod
  直连 bypass 超时。
- [x] `NET-006`：已将 Broker 宽泛 egress 规则收紧为 Kubernetes API、配置的 issuer 和集群内
  OIDC discovery/JWKS endpoint，并为 `internal-user-system` 增加 ingress/egress default-deny。
  真实 Higress Gateway 在重启前后访问 Broker health 均返回 HTTP 200。Broker 标签探针访问
  Kubernetes API、OIDC discovery 和 JWKS 均返回 HTTP 200；访问 Controller webhook 超时。
  审批状态由 Broker 持久化，不再存在独立的 OIDC endpoint。普通 Pod 访问 Broker Service、
  Broker Pod IP 和临时的 `internal-user-system` Pod 均超时。Broker rollout 恢复为 2/2 Ready；
  所有临时探针均已删除，测试没有读取凭据或 ServiceAccount token。
- [x] `BROKER-008`：OIDC JWKS 故障以及 外部审批系统查询/提交故障会 fail-closed。第一版由受控管理员
  CLI/管理员流程维护 InternalUser 生命周期；自动人员目录同步不属于本验收项。
- [x] `LEASE-009`：使用一次性 Lease `lease-le009-20260908083033` 完成真实集群验证。
  Controller 停止期间创建事件未被处理；恢复后约 18 秒进入 `Prepared`，且只生成了 1 个
  Lease ServiceAccount、1 个 bound Secret 和 1 个 ClusterRoleBinding。Prepared 阶段重启
  Broker 后恢复为 `2/2 Ready`。随后 Controller 停止期间删除事件未被处理；恢复后约 19 秒
  Lease 及全部受管资源均已清理。全程没有发起 TokenRequest，也没有读取 Secret 数据。
  Controller endpoint 停止期间，仅为允许 API 变更而临时将两个 CredentialLease webhook 设为
  `Ignore`，每次 Controller 恢复前均已恢复为 `Fail`。
- [x] `E2E-001` 和 `E2E-002`：2026-09-09 使用编译后的 `internal-user-cli` 和
  `internal-user-admin`，在隔离验收集群中对接仓库 HTTPS OIDC Provider、已部署 Broker
  和 Controller 完成真实流程。Alice 通过真实 OIDC Discovery、S256 PKCE、loopback 回调、
  token exchange、Broker 认证和 Lease 处理完成 base 及高权限兑换。高权限 reference
  全程只保存在测试进程内存中。重放 reference 以及 Bob 使用 Alice reference 均以非零退出，
  没有生成输出文件，也没有再次签发凭证。成功输出在 tmpfs 中以 `0600` 创建并在断言后删除；
  临时测试镜像、审批记录和 mTLS 材料已移除，Broker/Controller 原 Deployment 已恢复。

## TODO：P0 阻断项或缺少证据

- [ ] `BROKER-010`：真实外部审批系统 `APPROVED` event、适配器提交、同用户兑换和一次性重放证据
  尚未记录。
- [ ] `CLI-004`：现场端到端 reference 交付和高权限 redeem 输出证据尚未记录。
- [ ] `ADAPTER-006`：尚未使用真实外部审批系统回调、审批实例 API 和批准的 SMTP relay 验证已部署
  适配器。

## TODO：P1 或部分完成的场景

- [x] `LEASE-009`：周期扫描器和 missed-watch 重扫已有单元覆盖；真实集群测试在 Controller
  重启后恢复了未处理的创建和删除事件。准备收敛约 18 秒，清理收敛约 19 秒，低于一分钟
  目标。
- [x] `NET-006`：NetworkPolicy 检查和 Broker 重启前后的运行时 ingress/egress 测试均通过，
  具体证据见上文。
- [ ] `go test ./...`：修复仓库已有 envtest 二进制和历史 fixture 问题后，重新运行完整
  release-gate 测试。此前被 etcd/default namespace 和历史 kubeconfig/license fixture 阻断。

## 默认验收之外

以下项目按要求作为额外上线门禁，不计入默认验收失败：

- [ ] `GW-001` 至 `GW-004`：Higress/WAF 集成和真实代理链 header 转换。
- [ ] `AUD-001` 至 `AUD-006`：集中审计和信息泄露测试。

## 发布状态

`RBAC-004` 已完成测试集群验收，但整体尚未获得生产批准；其余 P0/P1 TODO 需要按
优先级完成或经过明确的风险接受。按要求当前不创建 commit。
