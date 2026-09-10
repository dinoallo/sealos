# 内部用户凭证 TODO

状态：进行中。本文件用于持续跟踪当前测试集群验收和 RBAC 加固工作，补充
`internal-user-credentials-acceptance-status.md`，不以缺少证据为通过。

更新时间：2026-09-09。

## 当前重点：发布门禁跟进（`LEASE-009` 已完成）

### 已完成的实现工作

- [x] 确认 Controller 当前仍拥有动态
  `ClusterRoleBinding` 写权限。
- [x] 增加独立的 `ValidatingAdmissionWebhook`，并限制校验对象为 Controller
  ServiceAccount 发起的 RBAC 变更。
- [x] Admission 组件与 Controller 使用独立的 Pod、ServiceAccount、RBAC 和证书。
- [x] 校验 `roleRef`、subject、binding 名称、ownership label、资源 UID、profile
  映射和 Controller ServiceAccount 身份。
- [x] 配置 webhook 故障时 `failurePolicy: Fail`。
- [x] 为允许和拒绝的 binding 变更增加单元测试。
- [x] 已生成 Admission Deployment、Service、证书、RBAC 和
  `ValidatingWebhookConfiguration`，并完成 kustomize 渲染检查。
- [x] 已将 Admission 资源应用到测试集群，证书已 Ready，CA bundle 已注入。
- [x] 实现无状态外部审批适配器、Broker mTLS 客户端、权威外部审批实例查询和受保护的
  邮件 reference 交付。
- [x] 增加适配器部署清单：无 Kubernetes RBAC、关闭 ServiceAccount token automount、独立
  Broker 客户端证书和 default-deny NetworkPolicy。

### 集群运行验证已完成

- [x] 修复 registry manifest 的重复压缩 layer，重新发布 Admission 镜像；Deployment
  已达到 `1/1`，Service 已有可用 endpoint。
- [x] Controller ServiceAccount 创建任意 binding 被拒绝；使用 Controller 已持有的
  role 进行隔离测试时，独立 Admission Webhook 返回拒绝。
- [x] 修改现有受管 binding 的 subject 被 Admission Webhook 拒绝；错误 ownership
  的 binding 删除也被拒绝。
- [x] 删除真实自有 base binding 被允许，Controller 随后正常重建该 binding。
- [x] Admission 缩容至零时，Controller RBAC 写请求因 webhook 无 endpoint 而失败；
  恢复一副本后 Deployment 再次 Ready，证明 `failurePolicy: Fail` 生效。
- [x] `RBAC-004` 的实现、部署和集群运行验证已完成。

## 尚未完成的验收项

### P0 或缺少安全证据

- [x] `RBAC-004`：独立 Admission 控制、镜像发布、部署和集群运行验证均已完成。
- [x] `LEASE-002`：自动化清理失败覆盖验证 finalizer 保留并请求重试。
- [x] `LEASE-005`：自动化 ServiceAccount、Secret 和 binding ownership 不匹配覆盖验证
  fail-closed，并产生可处理的 Warning Event。
- [x] `LEASE-007`：显式 acceptance-tag 测试执行了真实 `serviceaccounts/token` 请求，丢弃成功
  响应且不读取、不持久化 token 数据，并验证 consumed Lease 不能再次消费；临时测试资源已清理。
- [x] `LEASE-008`：自动化覆盖验证保存 TokenRequest 实际过期时间，并以此作为清理事实来源。
- [x] `CLI-003`：拒绝 symlink 输出，普通文件输出权限为 `0600`。
- [ ] `BROKER-010`：Broker 审批持久化、适配器提交和用户 redeem 已有定向测试；仍需真实
  真实外部审批系统审批、同用户兑换和重放证据。
- [ ] `CLI-004`：高权限 redeem 以 `0600` 写出且不记录凭据；仍需真实 reference 交付和
  端到端证据。
- [ ] `ADAPTER-006`：使用真实外部审批系统回调/API 凭据和批准的 SMTP relay 部署适配器，并记录
  重试、幂等和交付证据。

### P1 或部分覆盖

- [x] `PROF-007`：已启用 admission 控制；临时 profile 集群检查拒绝 workload 创建、
  deployment 更新、Secret 读取、ServiceAccount 创建和 impersonation。
- [x] `LEASE-009`：周期扫描器和 missed-watch 重扫已有单元覆盖；真实集群测试在 Controller
  重启后恢复了未处理的创建和删除事件。准备耗时约 18 秒，清理耗时约 19 秒，低于一分钟
  目标。
- [x] `BROKER-008`：OIDC JWKS 和审批校验故障会 fail-closed。第一版由受控管理员 CLI/管理员
  流程维护 InternalUser 生命周期；自动人员目录同步不属于本验收范围。
- [x] `NET-005`：Broker 侧进程内限流、method、body 和请求大小校验已实现并测试；健康
  检查仍可用。
- [x] `NET-006`：Broker 重启前后的 NetworkPolicy 检查和运行时 ingress/egress 证据均通过；
  已移除宽泛的 `10.0.0.0/8:443` egress 规则，并为 `internal-user-system` 增加 default-deny。
- [x] `NET-007`：Broker 重启后 endpoint Ready、可信 peer 源 IP 响应（允许返回 `401`、拒绝
  返回 `403`）以及普通 Pod 直连阻断均已在不使用凭据的情况下验证。该结果不替代真实
  Higress 代理链测试。
- [ ] `go test ./...`：修复现有 envtest/etcd、default namespace 和历史 fixture 问题后，
  重新运行完整测试套件。

### NET-006 集群证据

- [x] Broker ingress 只允许 Higress namespace 中带有要求 Gateway label 的来源访问 TCP 8443。
  真实 Higress Gateway 在 Broker 重启前后访问 `/healthz` 均返回 HTTP 200。
- [x] Broker egress 只允许 DNS、Kubernetes API Service IP、配置的 issuer 地址和集群内
  OIDC Pod selector。Broker 标签探针访问 OIDC discovery 和 `/jwks.json` 均返回 HTTP 200。
  审批状态现在由 Broker 持久化，因此不再存在独立的 OIDC approval endpoint。
- [x] Broker 访问 `internal-user-controller` webhook Service 和 Pod IP 被连接超时阻断。普通
  业务 Pod 访问 Broker Service、Pod IP 以及 `internal-user-system` 中的临时 workload 均被阻断。
- [x] 策略变更和重启后 Broker rollout 恢复为 2/2 Ready。所有临时探针均设置
  `automountServiceAccountToken: false`，未读取 Secret 或 token 数据，测试后已删除。

### LEASE-009 集群证据

- [x] 在 `2026-09-08T08:30:35Z`、Controller 为 0 副本时创建一次性高权限 Lease
  `lease-le009-20260908083033`。Lease 保持 `Pending`，没有生成 ServiceAccount、Secret
  或 ClusterRoleBinding。
- [x] 将两个 CredentialLease webhook 策略恢复为 `Fail` 后重启 Controller，在
  `2026-09-08T08:30:53Z` 观察到 `Prepared`；每种预期受管资源恰好存在 1 个。
- [x] 在 Prepared 阶段重启两副本 Broker，于 `2026-09-08T08:31:25Z` 恢复 Ready，且没有
  兑换该 Lease。
- [x] 再次停止 Controller，于 `2026-09-08T08:31:26Z` 请求删除 Lease，恢复两个 webhook
  的 `Fail` 策略并重启 Controller；在 `2026-09-08T08:31:45Z` Lease 和全部受管对象均已
  删除，耗时 19 秒。
- [x] 测试全程没有读取 Secret 数据，也没有创建 TokenRequest。为在 Controller endpoint
  停止期间允许测试 API 变更，临时 `Ignore` 只作用于两个 CredentialLease webhook，且在
  恢复前已重新设置为 `Fail`。

## 已验证

- [x] 身份校验、暂停/恢复、用户删除清理和用户重建（`ID-001` 至 `ID-008`）。
- [x] Broker 授权、自身 target 限制、审批引用、固定 audience、TTL 限制和 profile
  allowlist。
- [x] 稳定 base ServiceAccount 凭证和 Lease 专属临时 ServiceAccount 凭证。
- [x] Lease 签发、撤销、过期、清理、finalizer 移除和重启收敛。
- [x] Broker 与 Controller ServiceAccount 权限边界检查。
- [x] CLI Authorization Code + PKCE 流程和 `0600` kubeconfig 输出。
- [x] CLI 高权限 redeem 请求构造和校验已有单元覆盖；这不替代现场 `CLI-004` 验收。
- [x] Broker 源 IP 和可信代理检查（`NET-001` 至 `NET-004`）。
- [x] 最终集群清理：19 条 CredentialLease 均为终态、没有 Lease 保留 finalizer，且没有
  临时 Lease ServiceAccount、Secret 或 ClusterRoleBinding 残留。
- [x] Targeted Go test、Broker race test、`go vet` 和 build。完整 `go test ./...` release
  gate 仍受已有 envtest 和历史 fixture 失败阻断，详见下方。

## 默认验收之外

以下项目按要求作为额外上线门禁，不计入默认验收结论：

- [ ] `GW-001` 至 `GW-004`：Higress/WAF 集成和真实代理链 header 转换。
- [ ] `AUD-001` 至 `AUD-006`：集中审计和信息泄露测试。

## 工作约束

- [x] 验收完成前不创建 commit。
- [x] Registry 验证遵守既定流程：只读取 pull Secret 中目标 registry 字段，通过 stdin
  传递凭据，不输出、不持久化凭据。
- [x] 修复镜像后再次发布并验证镜像；继续遵守上述凭据处理约束。
- [x] 每个项目有可复现证据后，更新
  `internal-user-credentials-acceptance-status.md` 和本 TODO 文档；部分证据明确标注为
  部分完成。

## 发布决策

- [ ] 其他必需安全项未完成时，不批准生产凭证或公网入口；`RBAC-004` 已不再是当前
  阻断项。
- [ ] 最终发布前记录完整证据、剩余风险和明确接受的例外。
