# 内部用户凭证测试验收文档

状态：验收清单。本文件依据
[`internal-user-credentials-design.md`](internal-user-credentials-design.md) 制定，
本身不代表生产上线批准。

默认验收检查正常功能、Kubernetes 资源行为和 Broker 自身的安全边界。Higress/WAF
集成、审计和信息泄露覆盖明确不属于默认验收，作为下方额外上线门禁。默认测试只有
测试项的 API 返回结果和 Kubernetes 资源状态符合预期才算通过。凭证响应仍必须按受限
测试数据处理；意外暴露属于安全事件和发布阻断项，但不作为默认审计或信息泄露测试计分。

## 1. 验收规则

- 所有 P0 项必须通过；任意 P0 失败都阻断部署或公网入口开放。
- 所有 P1 项必须在生产上线前通过；例外必须经过安全评审并形成书面风险接受。
- 测试必须使用隔离集群、非生产身份和非生产凭证。
- 测试证据必须记录集群版本、镜像 digest、OIDC issuer、audience、源 IP CIDR、
  NetworkPolicy 和 profile 对应的 RBAC 规则。执行下方额外的审计测试时，才要求
  记录审计策略版本。
- 默认验收必须丢弃包含凭证的响应，只保留脱敏后的 status/metadata 证据。若意外暴露
  bearer token、kubeconfig、refresh token、私钥或 Secret 数据，不得分享并阻断上线；
  专门的信息泄露测试列为下方额外门禁。

## 2. 测试环境和测试夹具

测试环境必须包含：

- `InternalUser` 和 `CredentialLease` CRD、独立部署的 Controller、Broker 以及三个
  专用 namespace。
- 仓库内 `test/oidc` 提供的一次性 OIDC issuer，使用仅供验收客户端信任的 HTTPS 测试证书。
  它支持 Discovery、JWKS、签名 JWT、`exp`/`nbf` 和 Authorization Code + PKCE。
  端到端测试不使用外部 OIDC Provider。
- 普通用户、第二个普通用户、属于配置 admin group 的管理员，以及已认证但不属于
  admin group 的用户。
- 允许的源 CIDR、禁止的源 CIDR、可以向 Broker 发送规范化客户端 IP header 的可信
  代理测试夹具，以及 Broker 直连路径。默认 Broker 验收不要求部署 Higress/WAF；
  Higress/WAF 集成测试单独列在下文。
- 支持 TokenRequest 的测试集群。执行下方额外的审计和信息泄露测试时，才要求接入
  受保护的集中审计/日志 sink。
- 可控的审批适配器，模拟外部审批系统或批准的线下流程。适配器必须能够区分审批引用
  和有效的管理员重新认证。

除非测试项另有说明，默认创建 `alice`、`bob` 两个 Active 用户和一个管理员身份。
涉及重复身份或接管攻击时，应为每个测试使用独立 issuer 或 subject。

## 3. 测试验收矩阵

### 3.1 身份和生命周期

| 编号 | 优先级 | 操作 | 预期结果和证据 |
| --- | --- | --- | --- |
| ID-001 | P0 | 使用白名单 HTTPS issuer、不透明 subject 和 `base-readonly.v1` 创建 `InternalUser`。 | 创建成功。名称必须等于 `iu-` 加上 `sha256(issuer + NUL + subject)` 的前 24 位十六进制字符；status 暴露派生名称，不能使用调用方提交的人类 username。 |
| ID-002 | P0 | 使用 HTTP issuer、白名单外 HTTPS issuer、格式错误 issuer 或空 subject 创建对象。 | 所有请求都被拒绝，不创建 ServiceAccount、binding 或凭证资源。 |
| ID-003 | P0 | 重复创建相同 issuer/subject，再使用不同对象名提交相同身份。 | 确定性名称阻止重复身份，不匹配的名称被拒绝，原用户资源不变。 |
| ID-004 | P0 | 修改已有 `InternalUser` 的 issuer、subject、profile，或尝试通过 metadata/name 接管。 | 修改和接管均被拒绝，已有稳定 ServiceAccount 和 binding 不变。 |
| ID-005 | P1 | Reconcile 一个 Active 用户。 | `internal-user-system` 中只有一个稳定 ServiceAccount；关闭 automount、没有旧 token 引用，只附着评审过的基础 binding；不创建 Sealos namespace 或标准 `User`。 |
| ID-006 | P0 | 在存在稳定 binding 和活动 Lease 时设置 `spec.suspend=true`。 | 新签发被拒绝，稳定基础 binding 被删除，活动 Lease 变为 Revoked/终态，其拥有的对象被清理；InternalUser 和稳定 ServiceAccount 保留。 |
| ID-007 | P0 | 恢复一个被暂停的用户。 | 正常 reconcile 后恢复基础 binding 并变为 Active，不恢复旧高权限 binding 或旧 token。 |
| ID-008 | P0 | 删除一个仍有 Prepared、Issued 和临时 Lease 资源的用户。 | finalizer 在所有 binding、Secret、临时 ServiceAccount 和稳定 ServiceAccount 清理前保持存在；全部清理后才删除身份对象。 |

### 3.2 API 边界和 RBAC

| 编号 | 优先级 | 操作 | 预期结果和证据 |
| --- | --- | --- | --- |
| RBAC-001 | P0 | 以普通用户 Kubernetes 身份对 `credentialleases.user.sealos.io` 执行 get/list/watch/create/update/patch/delete。 | 所有直接 Kubernetes API 操作都被拒绝，包括 `internal-credential-broker` 之外的 namespace；保留 `kubectl auth can-i` 和 API 响应。 |
| RBAC-002 | P0 | 以管理员身份重复 RBAC-001。 | 直接修改 Lease 仍被拒绝。管理员授权只存在于管理员 Broker endpoint 或专用 InternalUser 管理路径。 |
| RBAC-003 | P0 | 调用普通 Broker API，同时在请求中提交属于其他用户的 target。 | 提交的 target 被忽略；verified OIDC subject 决定 requester 和 target，不能返回其他用户的 Lease 或凭证。 |
| RBAC-004 | P0 | 使用 Broker ServiceAccount 读取 Secret 数据、管理 RBAC 或创建身份；检查 Controller 权限和平台生成对象的行为。 | Broker 的这些操作被拒绝。Controller 没有 TokenRequest、Secret 数据交付、Role/ClusterRole 管理或身份创建权限；其必要的 namespaced Secret/ServiceAccount 访问以及平台生成 ClusterRoleBinding 写权限单独评审。 |
| RBAC-005 | P1 | 验证 Controller ServiceAccount 可在 `internal-user-system` list/watch ServiceAccount 和 Secret，但不能集群级访问；启动 namespace-scoped cache。 | namespace 内操作成功，`--all-namespaces` 被拒绝，Controller 在没有集群级 Secret 权限的情况下所有 cache 正常 ready。 |
| RBAC-006 | P1 | 通过 Broker 查询 Lease status，分别使用拥有者和其他普通用户。 | 拥有者只能收到允许的非敏感 status；其他用户得到 not-found 或 forbidden，且不能推断资源信息。 |
| RBAC-007 | P0 | 使用专用管理员 OIDC group 或管理员 mTLS 路径创建、暂停、恢复和删除 `InternalUser`，再用同一管理员身份尝试读取 Secret、修改任意 RBAC 和调用 TokenRequest。 | 只有批准的管理员路径可以管理身份生命周期；Secret 读取、任意 RBAC 修改和 TokenRequest 仍被拒绝。 |

### 3.3 权限档位

| 编号 | 优先级 | 操作 | 预期结果和证据 |
| --- | --- | --- | --- |
| PROF-001 | P0 | 使用低于、等于和高于 24 小时的 TTL 请求 `base-readonly.v1`。 | 不超过最大值的请求在 API Server 实际过期时间约束下接受；超过最大值的请求被 Broker 和 Controller 同时拒绝。 |
| PROF-002 | P0 | 使用 base 凭证访问 allowlist 内外的全部资源。 | allowlist 中的 get/list/watch 成功；Secret、RBAC、身份资源、节点凭证、webhook 配置、通用 view 权限及所有未列资源均拒绝。 |
| PROF-003 | P0 | 没有管理员批准的外部记录、没有审批引用，或只有文本/消息 URL 而没有用户重新认证时签发或兑换 `cluster-ops-write.v1`。 | 签发被拒绝。自助 request 可以创建外部待审批记录，但审批引用本身永远不能授权提权。 |
| PROF-004 | P0 | 请求 TTL 超过 1 小时的 `cluster-ops-write.v1`。 | Broker 和 Controller 都拒绝请求；成功的高权限凭证不能超过 API Server 的实际过期时间。 |
| PROF-005 | P0 | 在高权限签发前后检查稳定 ServiceAccount 和 ClusterRoleBinding。 | 稳定 ServiceAccount 从未获得高权限；高权限只绑定到本 Lease 独有的临时 ServiceAccount 和 binding。 |
| PROF-006 | P0 | 提交未知 profile、自定义 RBAC 规则或调用方指定的 ClusterRole。 | 请求被拒绝；调用方不能扩展或替换平台维护的 profile。 |
| PROF-007 | P1 | 在 admission policy 开启时评审 `cluster-ops-write.v1` 的具体规则，包括 Pod/workload 创建路径。 | 资源清单有明确批准记录；不能通过选择其他 ServiceAccount 的 workload 写权限实现提权。 |

### 3.4 Lease 状态机和 Controller 行为

| 编号 | 优先级 | 操作 | 预期结果和证据 |
| --- | --- | --- | --- |
| LEASE-001 | P0 | 执行 Pending 到 Prepared 再到 Issued 的正常签发流程。 | 只发生 `Pending -> Prepared -> Issued`。 |
| LEASE-002 | P0 | 在各非终态注入非法输入、目标暂停和清理失败。 | 非法输入变为 Failed，暂停变为 Revoked，清理失败时保留 finalizer；Failed、Revoked、Expired 不能重新变为可签发状态。 |
| LEASE-004 | P0 | 检查高权限 Lease 资源并记录删除事件。 | 使用临时 ServiceAccount 和 binding；清理顺序为删除 binding、删除 bound Secret、删除临时 ServiceAccount、最后移除 Lease finalizer。 |
| LEASE-005 | P0 | 在 reconcile 前替换或修改所属 ServiceAccount、Secret 或 binding 的 owner/lease label。 | Controller 拒绝接管、修改和删除，保留 finalizer，并产生可处理的告警/event；不影响无关资源。 |
| LEASE-006 | P0 | 发起签发后重试兑换、并发兑换或修改 Lease resourceVersion。 | TokenRequest 前使用 resourceVersion CAS 写入 consumed；只允许一次不可逆 TokenRequest，已 consumed/终态 Lease 不能重新签发。 |
| LEASE-007 | P0 | 让 API Server 接受 TokenRequest 后使 Broker 无法收到响应。 | Lease 保持 consumed；不能从 Kubernetes 读取或之后返回 token；客户端必须创建新 Lease。 |
| LEASE-008 | P0 | 让 API Server 返回比请求更短或不同的实际过期时间。 | status 保存 TokenRequest 的实际 expirationTimestamp，并以它驱动清理；不能把请求 TTL 当作事实来源。 |
| LEASE-009 | P1 | 在准备、签发和清理期间重启 Controller/Broker，并单独丢弃 watch 事件。 | reconcile 和周期扫描最终收敛，不产生重复高权限 binding、丢失 finalizer 或资源泄漏；在规定测试条件下清理延迟小于一分钟。 |
| LEASE-010 | P1 | 等待已签发凭证过期后检查 Lease 和所属对象。 | Lease 变为 Expired，所有临时对象被删除，过期凭证不能兑换。 |

### 3.5 Broker 认证和授权

| 编号 | 优先级 | 操作 | 预期结果和证据 |
| --- | --- | --- | --- |
| BROKER-001 | P0 | 使用不可信 issuer、错误 audience、错误签名、缺 subject、过期 `exp` 或未来 `nbf` 的 token 请求。 | 认证失败关闭，不创建 Lease 或 TokenRequest。 |
| BROKER-002 | P0 | 以普通用户身份请求日常凭证。 | 只能为 verified caller 自己签发 `base-readonly.v1`；响应包含一个 kubeconfig。 |
| BROKER-003 | P0 | 以普通用户身份直接签发高权限、没有批准 reference 就兑换，或指定其他 target。 | 直接签发和未批准兑换都被拒绝；请求中的 target/profile 字段被拒绝，普通用户不能通过修改 JSON 获得高权限。 |
| BROKER-004 | P0 | 使用有效 admin group 身份，通过管理员 endpoint 为 Active target 请求带有效审批引用的临时高权限。 | 只有在重新检查 target、requester、profile、TTL、审批、状态和暂停状态后才成功。 |
| BROKER-005 | P0 | 管理员 endpoint 缺少 admin group、伪造 group、target 被暂停/不存在，或审批引用缺失/已改变。 | 请求被拒绝，不创建高权限 Lease 或 TokenRequest。 |
| BROKER-006 | P0 | 分别修改 OIDC Broker audience 和 Kubernetes TokenRequest audience。 | OIDC 使用服务端配置的 Broker audience；TokenRequest 使用独立配置的固定 Kubernetes audience；调用方不能选择任一 audience。 |
| BROKER-007 | P0 | 重用已返回响应、并发调用两次 redeem，或撤销后 redeem。 | 只交付一个响应；并发或后续兑换失败，不产生第二次 TokenRequest，也不泄露 token。 |
| BROKER-008 | P0 | 令 OIDC 或审批校验不可用。 | 新签发和提权失败关闭；已签发 token 只存活至原过期时间；撤销和清理仍可用。第一版不定义人员目录同步，因此不在本验收项中验证。 |
| BROKER-009 | P1 | 检查 Broker Kubernetes 权限和 projected ServiceAccount token。 | Broker 不能创建身份、读取 Secret 数据、为普通查询 list 全部身份或修改任意 RBAC；token 是短时、固定 audience 的 projected token，不存在默认 automount 或长期 Broker Secret。 |
| BROKER-010 | P0 | 投递经过认证的外部审批系统 `APPROVED` 事件，让适配器提交记录，再以同一身份 redeem，随后重放事件/reference 或增加 target/profile 字段。 | 适配器重新获取权威外部审批系统实例并调用 internal approve；Broker 持久化一条 `AwaitingRedemption` Lease，兑换前不创建临时资源，固定 requester/target/profile 并使用持久化 TTL，且只允许兑换一次。重复事件通过 Broker 幂等返回相同审批结果。 |

### 3.6 外部审批适配器

| 编号 | 优先级 | 操作 | 预期结果和证据 |
| --- | --- | --- | --- |
| ADAPTER-001 | P0 | 发送外部审批系统 URL verification challenge 和带正确回调 token 的 `APPROVED` event。 | challenge 原样响应且不含审批数据；已批准事件只在 event type 和审批定义均为配置值时接受。 |
| ADAPTER-002 | P0 | 修改回调 event 中的 target、issuer、TTL、reason 或 user 字段，同时让外部审批系统 API 返回已批准实例。 | 适配器忽略 event 提供的审批详情并重新获取完整实例，使用固定 issuer 和配置的表单字段；格式或策略不合法时拒绝。 |
| ADAPTER-003 | P0 | 发送 pending/rejected event、错误 token/signature、未配置的审批 code 或非 `APPROVED` 实例。 | 适配器不调用 Broker 或 notifier；认证错误失败关闭，非批准事件安全忽略。 |
| ADAPTER-004 | P0 | 重复投递同一批准事件，并在第一次 Broker 成功后让通知失败。 | 适配器不持久化事件消费状态；Broker 通过 `approvalID` 幂等返回同一 reference，适配器重试通知且不创建第二条 Lease。 |
| ADAPTER-005 | P1 | 检查适配器 ServiceAccount、volume、出站 TLS、SMTP 内容和回调响应。 | 适配器没有 Kubernetes RBAC 或 ServiceAccount token，使用独立 Broker mTLS 客户端身份，只在内存保存短期审批系统 token，绝不返回或处理 Kubernetes token/kubeconfig。 |

### 3.7 CLI 和文件处理

| 编号 | 优先级 | 操作 | 预期结果和证据 |
| --- | --- | --- | --- |
| CLI-001 | P0 | 使用 Authorization Code + PKCE 完成登录，再重放 state 或修改 verifier/challenge。 | 正确的 state 和 S256 PKCE 成功；重放或不匹配失败；不需要 client secret。 |
| CLI-003 | P0 | 分别写入新文件、已有文件和 symlink 输出路径。 | 必须显式选择输出路径；普通文件权限为 `0600`；symlink 路径被拒绝。 |
| CLI-004 | P0 | 从外部审批适配器收到 reference 后执行 `elevated redeem`，检查请求字段和 stdout/stderr。 | CLI 在新的 OIDC 登录后只发送 reference，以 `0600` 写入 kubeconfig，且任何阶段都不打印凭据内容；不存在用户侧 elevated request 命令。 |
| CLI-005 | P1 | 使用标准或显式 YAML 配置运行 base 和 elevated 命令，再用命令行参数覆盖部分值。 | 配置默认值会被加载，命令行参数优先；未知字段和多个 YAML 文档会被拒绝；配置格式不接受 token、kubeconfig、client secret 或审批 reference 字段。 |
| CLI-006 | P0 | 对接可记录请求的 Broker 执行 base 命令，再通过命令行参数、配置文件或篡改请求尝试加入 target、profile 或 approval-reference 字段。 | base 请求中只能包含 requested TTL。target 和 profile 由服务端推导或固定；不支持的字段和参数会被拒绝，CLI 不能选择其他身份。 |
| CLI-007 | P0 | 使用缺失、格式错误、含空白或超长 reference 执行 `elevated redeem`，并尝试已删除的 `elevated request` 命令。 | 每个非法操作都在交付凭据前被 CLI 拒绝，不调用 Broker，也不创建或覆盖输出文件。用户侧不存在 elevated request 操作。 |
| CLI-008 | P0 | 以用户 A 登录后兑换用户 B 的 reference，再在第一次成功兑换后重放有效 reference。 | Broker 拒绝身份不匹配和重放；CLI 不会收到第二份 kubeconfig，失败操作不会留下新的输出文件。 |
| CLI-009 | P1 | 让 OIDC discovery 返回不匹配的 issuer 或非 HTTPS endpoint，让 token endpoint 返回无效 bearer 响应或错误 nonce，并让 Broker 返回不完整凭据响应。 | CLI 使用安全错误失败关闭，不写入凭据文件，也不显示响应 body 或凭据内容。 |
| CLI-010 | P1 | 使用不可信 CA、错误 TLS server name 或无效证书连接 OIDC 和 Broker 测试 endpoint。 | TLS 校验失败；CLI 没有 insecure fallback，不继续 OIDC 登录、Broker 签发或 kubeconfig 输出。 |

### 3.8 CLI 和 Broker 端到端流程

这是一个真实进程验收测试。必须运行编译后的 `internal-user-cli` 二进制，
连接已部署的测试 Broker 和 Controller，并使用仓库内的 OIDC Provider 作为唯一身份
Provider。不要求真实外部审批系统租户、Higress/WAF 或额外的审计 sink。

| 编号 | 优先级 | 操作 | 预期结果和证据 |
| --- | --- | --- | --- |
| E2E-001 | P0 | 启动仓库 OIDC Provider，使用 HTTPS 测试证书、`internal-kc` 公共 client 和 `alice`、`bob` 两个测试 subject。让 Broker 和 CLI 使用相同 issuer、CA、固定 Broker audience 和 loopback callback。编译并运行真实 `internal-user-cli` 二进制，设置 `--no-browser --login-hint alice`；测试夹具访问打印出的 authorization URL，并跟随其重定向到 CLI 的 loopback callback。分别运行 `base` 和 `elevated redeem`，高权限流程的 approved reference 通过管理员 CLI/内部审批测试路径预先创建。 | 真实进程完成 Discovery、S256 PKCE 授权、token exchange、Broker 认证、Lease 处理和 kubeconfig 写入。OIDC server 观察到符合预期的 state、nonce、loopback redirect、一次性 code 和 PKCE challenge。base 使用稳定 ServiceAccount；高权限兑换使用 approved reference 和 Lease 专属临时 ServiceAccount。二进制成功退出，只写入明确选择且权限为 `0600` 的输出文件，并且只打印批准的 metadata。 |
| E2E-002 | P0 | 使用相同的高权限 reference 和 `--login-hint alice` 再次运行编译后的 CLI；然后为 `bob` 创建独立 reference，使用 `--login-hint bob` 兑换 Alice 的 reference。保留第一次输出文件和进程输出用于对比。 | Broker 拒绝重放和身份不匹配；不会返回第二份凭据，不会发生第二次 TokenRequest；失败运行不会创建或覆盖输出文件。 |

测试期间，夹具可以在授权 API 检查的持续时间内将 kubeconfig 作为受限测试数据保留，
但不得将 kubeconfig、bearer token、refresh token、私钥或 Secret 数据打印、记录、上传或
写入验收证据。断言完成后删除临时输出文件。

### 3.9 Broker 源 IP 校验（默认验收）

| 编号 | 优先级 | 操作 | 预期结果和证据 |
| --- | --- | --- | --- |
| NET-001 | P0 | 从可信代理测试夹具发送带允许源 IP 的规范化客户端 IP header 的管理员请求。 | 只有当 peer 是配置的可信代理身份且规范化 IP 在 allowlist 内时，Broker 才通过源 IP 校验。本测试不验证 Higress 的 IP 解析。 |
| NET-002 | P0 | 从同一测试夹具发送规范化客户端 IP 不在 allowlist 内的管理员请求。 | Broker 在创建 Lease 或 TokenRequest 前拒绝请求。 |
| NET-003 | P0 | 发送缺失、格式错误、客户端自行设置或来源不可信的规范化客户端 IP header。 | 即使 OIDC 认证和 admin group 正确，Broker 仍失败关闭。 |
| NET-004 | P0 | 绕过可信代理直接连接 Broker Service。 | NetworkPolicy、Service 暴露限制或 peer authentication 阻断请求，不能通过直连绕过源 IP 校验。 |
| NET-005 | P1 | 不经过 Higress 测试 Broker 自身的限流、请求大小限制、非法 HTTP method 和格式错误 body。 | Broker 拒绝非法或超限请求，不创建 Lease。 |
| NET-006 | P1 | 检查默认 Broker 部署的 namespace policy 和 egress 流量。 | Broker ingress 只允许可信代理和审批适配器身份；egress 只允许 Kubernetes API 和配置的 OIDC discovery/JWKS endpoint；普通业务 Pod 不能访问 `internal-user-system`。 |
| NET-007 | P1 | 重启 Broker 后重复 NET-001 至 NET-004。 | 重启后源 IP 和可信 peer 校验仍失败关闭，不接受旧的可信 header 或绕过状态。 |

### 3.10 额外的 Higress/WAF 集成测试

以下测试因需要部署 Higress/WAF 及其真实可信代理链配置，不属于默认的 Broker 单体验收，
但在开放公网/外部入口前必须执行。

| 编号 | 门禁 | 操作 | 预期结果和证据 |
| --- | --- | --- | --- |
| GW-001 | 外部入口 | 通过配置好的可信代理链，经 Higress/WAF 发送允许源 IP 的请求。 | Higress 解析真实客户端 IP，执行源 CIDR allowlist，删除客户端提交的 `Forwarded`、`X-Forwarded-For`、`X-Real-IP` 和可信 IP header，并写入 Broker 实际收到的规范化内部 header。 |
| GW-002 | 外部入口 | 从允许和禁止网络发送伪造 forwarding header 和可信 IP header。 | 客户端 header 不能覆盖真实来源；Higress 和 Broker 做出预期的纵深防御判断。 |
| GW-003 | 外部入口 | 从配置 CIDR 外发送请求，同时检查 WAF 响应和 Broker access log。 | Higress/WAF 在转发前拒绝；若通过测试 bypass 到达 Broker，Broker 也必须拒绝。 |
| GW-004 | 外部入口 | 测试 Higress/WAF 的限流、请求大小限制、非法 method 和格式错误 body。 | 网关在转发前执行限制；Broker 仍执行第二层校验，且不创建 Lease。 |

### 3.11 额外的审计和信息泄露测试

以下测试不属于默认功能验收，但在签发生产凭证或开放公网/外部入口前必须执行，并且
需要接入受保护的集中审计/日志 sink。

| 编号 | 门禁 | 操作 | 预期结果和证据 |
| --- | --- | --- | --- |
| AUD-001 | 生产门禁 | 执行成功、拒绝、撤销、过期和失败的签发流程。 | 集中记录包含 subject、requester、profile、Lease ID、审批引用（如有）、源 IP、user-agent、结果和时间。 |
| AUD-002 | 生产门禁 | 在 Broker/Controller 日志、Lease、Secret metadata、API 响应和集中审计中搜索敏感值。 | 任何位置都不能出现 token、kubeconfig、refresh token、私钥或 Secret 数据。 |
| AUD-003 | 生产门禁 | 检查 base 和高权限 Lease 的 bound Secret 及 status。 | 每个 Lease 恰好一个唯一的空 Opaque Secret；status 和 Secret 不含 token、kubeconfig、私钥或 Secret 值。 |
| AUD-004 | 生产门禁 | 执行 CLI 并检查 stdout、stderr、进程参数、本地存储和成功提示。 | 除明确选择且权限为 `0600` 的输出文件外，不打印或持久化 token、kubeconfig、refresh token 或私钥；提示只显示批准的元数据。 |
| AUD-005 | 生产门禁 | 检查 Kubernetes audit policy 和 `serviceaccounts/token` 事件。 | 只记录 Metadata，不包含 request/response body。 |
| AUD-006 | 生产门禁 | 在一次性交付前强制审计写入失败。 | Broker 不返回 kubeconfig，并记录安全失败；不能从 Kubernetes 重试取回丢失的凭证。 |

## 4. 测试执行和证据

在 `controllers/user` 目录执行定向自动化检查：

```bash
go test ./pkg/broker -run '^TestElevated|^TestInternalApprove' -count=1
go test ./pkg/approvaladapter -count=1
go test ./cmd/internal-user-approval-adapter ./cmd/internal-user-admin ./cmd/internal-user-cli -count=1
go test ./cmd/internal-user-cli -run 'Config|Elevated|CLIArgs|CredentialResponse|WriteKubeconfig|IssueBaseCredential|CLIRejects|RunFailsClosed' -count=1
go test ./controllers -run '^TestCredentialLeaseScanner' -count=1
go test ./controllers/internalcredentialstest ./pkg/internalcredentials ./pkg/rbacadmission ./cmd/internal-user-admission
go test -race ./pkg/broker
go vet ./cmd/broker ./cmd/internal-user-controller ./cmd/internal-user-cli ./cmd/internal-user-admin ./cmd/internal-user-admission ./cmd/internal-user-approval-adapter ./controllers ./controllers/internalcredentialstest ./pkg/approvaladapter ./pkg/broker ./pkg/internalcredentials ./pkg/rbacadmission
```

执行 `E2E-001` 和 `E2E-002` 时，必须构建并运行真实 CLI 进程。启动
`test/oidc/cmd/test-oidc`，使用一次性 HTTPS 证书和只包含 `internal-kc` client、
`alice`/`bob` 测试用户的测试 JSON 配置；将其 CA 和 server name 同时配置给 Broker 和 CLI。
使用 `go build -o <temporary-test-path>/internal-user-cli ./cmd/internal-user-cli` 构建真实二进制。
测试夹具
消费 CLI `--no-browser` 输出的 authorization URL，访问测试 Provider，并跟随其重定向到
loopback callback，等待 CLI 进程退出。资源和 `kubectl --kubeconfig` 断言使用已部署的
Broker/Controller 和隔离测试集群。高权限 reference 通过管理员 CLI 或内部审批测试路径
预置，不调用已删除的用户侧 elevated request 操作。

端到端证据只保留退出码、脱敏后的 OIDC/Broker 请求 metadata、Lease 和 ServiceAccount
metadata、输出文件权限及授权 API 结果摘要。不得保存或展示任何包含凭据的响应。

执行 `LEASE-007` 的一次性测试集群响应丢失测试时，必须显式启用 acceptance build tag，
并让所有可能含 token 的响应只留在测试进程内：

```bash
KUBECONFIG=<一次性测试集群 kubeconfig> INTERNAL_USER_ACCEPTANCE_CLUSTER=true \
  go test -tags acceptance ./pkg/broker -run '^TestTokenRequestResponseLossAfterAPIServerAcceptance$' -count=1
```

在安装 envtest 二进制和其他仓库测试夹具后执行完整包测试：

```bash
go test ./...
```

完整测试套件是发布门禁。缺少测试基础设施或旧测试夹具失败必须单独记录，不能报告为
验收通过。

RBAC-004 不能仅凭 Kubernetes RBAC 视为生产完成：Controller 动态创建/删除
ClusterRoleBinding 的权限必须由 admission policy 或隔离的 signer 进一步限制 role ref、
subject、名称和 owner label。如果默认验收环境未安装该控制，必须将其作为明确的安全评审
事项记录。

集群验收至少保留以下证据：

- `kubectl get internalusers,credentialleases --all-namespaces -o yaml`，并完成敏感
  字段检查和脱敏；
- 普通用户、管理员、Broker、Controller 身份的 `kubectl auth can-i` 结果；
- ServiceAccount、Role、ClusterRole、RoleBinding、ClusterRoleBinding、
  NetworkPolicy 证据；
- 删除敏感信息后的 Broker HTTP 状态/响应摘要；
- TokenRequest 结果和资源删除时间；
- 重启、watch 丢失、审批不可用和 OIDC 不可用的结果。

执行额外的网关和审计/信息泄露测试时，还应保留 Higress/WAF header 转换证据、集中审计
记录、Kubernetes audit 事件和敏感值搜索结果。

## 5. 上线门禁

所有 P0/P1 测试通过，并完成以下明确评审前，不得开放公网入口或签发生产凭证：

- profile 的完整资源 allowlist 和 admission policy；
- OIDC issuer、audience、admin group 和密钥轮换配置；
- Higress/WAF 可信代理链和源 CIDR；
- Broker 及各 namespace 的 NetworkPolicy；
- Kubernetes audit policy 和受保护的留存目标；
- 管理员绑定和审批流程；
- Controller/Broker 重启期间的清理和恢复行为。
- 开放公网/外部入口前完成 Higress/WAF 集成测试 `GW-001` 至 `GW-004`。
- 签发生产凭证或开放公网/外部入口前完成审计和信息泄露测试 `AUD-001` 至 `AUD-006`。
