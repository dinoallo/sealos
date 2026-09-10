# Internal User Credentials Acceptance Test Plan

Status: acceptance checklist. This document is derived from
[`internal-user-credentials-design.md`](internal-user-credentials-design.md).
It is not a production approval by itself.

The default run validates the happy path, Kubernetes resource behavior, and
the Broker's own security boundary. Higress/WAF integration, audit coverage,
and information-disclosure coverage are explicitly excluded from the default
run and are listed as additional release gates below. A default test passes
only when its API result and Kubernetes resources match the expected result.
Credential-bearing responses must still be handled as restricted test data;
an accidental exposure is an incident and release blocker, but is not scored
as a default audit or information-disclosure test.

## 1. Acceptance rules

- Every P0 item must pass. A P0 failure blocks deployment or external route
  enablement.
- P1 items must pass before production rollout. A P1 exception requires an
  explicit security review and written risk acceptance.
- Tests run in an isolated cluster with non-production identities and
  credentials.
- Test evidence must include the cluster version, image digests, OIDC issuer,
  configured audiences, source CIDRs, NetworkPolicies, and profile role rules.
  The audit policy revision is required when running the additional audit
  tests below.
- The default run must discard credential-bearing responses and retain only
  redacted status/metadata evidence. An accidentally exposed bearer token,
  kubeconfig, refresh token, private key, or Secret data must not be shared
  and blocks release; the dedicated information-disclosure test is an
  additional gate below.

## 2. Test environment and fixtures

The environment must contain:

- The `InternalUser` and `CredentialLease` CRDs, the independently deployed
  Controller, the Broker, and the three dedicated namespaces.
- The repository's disposable OIDC issuer from `test/oidc`, running over HTTPS
  with a test CA trusted only by the acceptance clients. It supports Discovery,
  JWKS, signed JWTs, expiration and not-before claims, and Authorization Code
  + PKCE. No external OIDC provider is used for the end-to-end test.
- Test identities for an ordinary user, a second ordinary user, an
  administrator in the configured admin group, and an authenticated user who
  is not in that group.
- An allowed source CIDR, a disallowed source CIDR, a trusted-proxy test
  fixture that can send a normalized client-IP header to the Broker, and a
  direct Broker service path. Higress/WAF is not required for the default
  Broker acceptance run; its integration tests are listed separately below.
- A TokenRequest-capable test cluster. A protected audit/logging sink is
  required when running the additional audit and information-disclosure tests
  below.
- A controllable approval adapter representing an external approval system chat or the approved
  offline process. The adapter must distinguish an approval reference from a
  valid administrator authentication.

Unless a test says otherwise, create one active user for `alice`, one active
user for `bob`, and one administrator identity. Use a separate issuer or
subject for each test that checks duplicate or takeover behavior.

## 3. Test matrix

### 3.1 Identity and lifecycle

| ID | Priority | Action | Expected result and evidence |
| --- | --- | --- | --- |
| ID-001 | P0 | Create an `InternalUser` with an allowlisted HTTPS issuer, an opaque subject, and `base-readonly.v1`. | Creation succeeds. The name equals `iu-` plus the first 24 hexadecimal characters of `sha256(issuer + NUL + subject)`. The derived name is present in status and the identity is not replaced by a caller-supplied username. |
| ID-002 | P0 | Create users with an HTTP issuer, an HTTPS issuer outside the allowlist, a malformed issuer, or an empty subject. | Every request is rejected. No ServiceAccount, binding, or credential resource is created. |
| ID-003 | P0 | Create the same issuer/subject twice, then try the same identity under a different object name. | The deterministic object name prevents a second identity and the mismatched name is rejected. The original user's resources remain unchanged. |
| ID-004 | P0 | Update issuer, subject, or role profile on an existing `InternalUser`; attempt a metadata/name takeover. | Immutable fields and identity takeover are rejected. The existing stable ServiceAccount and binding are not changed. |
| ID-005 | P1 | Reconcile an active user. | Exactly one stable ServiceAccount exists in `internal-user-system`; automount is disabled, no legacy token reference exists, and only the reviewed base binding is attached. No Sealos namespace or standard `User` is created. |
| ID-006 | P0 | Set `spec.suspend=true` while the user has a stable binding and active leases. | New issuance is rejected, the stable base binding is removed, active leases become revoked/terminal, and their owned objects are cleaned. The InternalUser and stable ServiceAccount remain. |
| ID-007 | P0 | Resume a suspended user. | The base binding is recreated only after normal reconciliation and the user becomes active. No old elevated binding or old token is restored. |
| ID-008 | P0 | Delete an active user with prepared, issued, and temporary lease resources present. | The finalizer keeps the identity until all owned bindings, Secrets, temporary ServiceAccounts, and the stable ServiceAccount are gone. The identity is removed only after cleanup. |

### 3.2 API boundary and RBAC

| ID | Priority | Action | Expected result and evidence |
| --- | --- | --- | --- |
| RBAC-001 | P0 | As an ordinary user's Kubernetes identity, run `get`, `list`, `watch`, `create`, `update`, `patch`, and `delete` against `credentialleases.user.sealos.io`. | Every direct Kubernetes API operation is denied, including access outside `internal-credential-broker`. Capture `kubectl auth can-i` and API responses. |
| RBAC-002 | P0 | Repeat RBAC-001 as an administrator identity. | Direct Lease mutation remains denied. Administrator authorization exists only at the administrator Broker endpoint or the dedicated InternalUser administration path. |
| RBAC-003 | P0 | Use the ordinary Broker API to request a credential while sending a target belonging to another user. | The submitted target is ignored. The verified OIDC subject determines both requester and target, and no other user's Lease or credential is returned. |
| RBAC-004 | P0 | Use the Broker ServiceAccount to read Secret data, manage RBAC, or create an identity; inspect the Controller's permissions and generated-resource behavior. | Broker operations are denied. Controller has no TokenRequest, Secret-data delivery, Role/ClusterRole management, or identity-creation permission; its required namespaced Secret/ServiceAccount access and platform-generated ClusterRoleBinding write are separately reviewed. |
| RBAC-005 | P1 | Verify the Controller's ServiceAccount can list/watch ServiceAccounts and Secrets in `internal-user-system` but not cluster-wide. Start the Controller with the namespace-scoped cache configuration. | Namespaced operations succeed, `--all-namespaces` access is denied, and all Controller caches become ready without cluster-wide Secret permissions. |
| RBAC-006 | P1 | Access Lease status through the Broker for the owner and for a different ordinary user. | The owner receives only the allowed non-secret status. The other user receives a not-found or forbidden response without information disclosure. |
| RBAC-007 | P0 | Use the dedicated administrator OIDC group or administrator mTLS path to create, suspend, resume, and delete an `InternalUser`, then attempt Secret reads, arbitrary RBAC writes, and TokenRequest calls with the same administrator identity. | Identity lifecycle operations succeed only through the approved administrator path. Secret reads, arbitrary RBAC changes, and TokenRequest remain denied. |

### 3.3 Permission profiles

| ID | Priority | Action | Expected result and evidence |
| --- | --- | --- | --- |
| PROF-001 | P0 | Request `base-readonly.v1` with a TTL below, at, and above 24 hours. | Values at or below the configured maximum are accepted subject to the API server expiration; values above the maximum are rejected by both Broker and Controller. |
| PROF-002 | P0 | Use a base credential to access every allowed and disallowed resource in the reviewed allowlist. | Allowed `get/list/watch` operations work. Secrets, RBAC, identity resources, node credentials, webhook configuration, generic view permissions, and every unlisted resource are denied. |
| PROF-003 | P0 | Attempt to issue or redeem `cluster-ops-write.v1` without an administrator-approved external record, without an approval reference, or with only a text/message URL and no fresh user authentication. | The issuance is rejected. A self-service request may create a pending external approval record, but an approval reference alone never authorizes elevation. |
| PROF-004 | P0 | Request `cluster-ops-write.v1` with TTL above one hour. | The request is rejected by both Broker and Controller. A successful elevated credential never outlives the API Server's actual expiration. |
| PROF-005 | P0 | Inspect the stable ServiceAccount and its ClusterRoleBinding before and after an elevated issuance. | The stable ServiceAccount never receives elevated permissions. Elevated access is attached only to one lease-specific temporary ServiceAccount and binding. |
| PROF-006 | P0 | Submit an unknown profile, a profile with custom RBAC rules, or a caller-selected ClusterRole. | The request is rejected; callers cannot broaden or replace a platform-maintained profile. |
| PROF-007 | P1 | Review the concrete `cluster-ops-write.v1` rules with admission policy enabled, including Pod/workload creation paths. | The resource list is explicitly approved and no workload write permission can be used to select another ServiceAccount and escalate privileges. |

### 3.4 Lease state machine and Controller behavior

| ID | Priority | Action | Expected result and evidence |
| --- | --- | --- | --- |
| LEASE-001 | P0 | Exercise normal issuance from Pending through Prepared to Issued. | Only `Pending -> Prepared -> Issued` occurs. |
| LEASE-002 | P0 | Force invalid input, target suspension, and cleanup failures at each non-terminal phase. | Invalid input becomes Failed; suspension becomes Revoked; cleanup errors retain the finalizer. Failed, Revoked, and Expired leases cannot become signable again. |
| LEASE-004 | P0 | Inspect elevated lease resources and record deletion events. | A temporary ServiceAccount and binding are used. Cleanup order is binding removal, bound Secret deletion, temporary ServiceAccount deletion, then Lease finalizer removal. |
| LEASE-005 | P0 | Replace or label an owned ServiceAccount, Secret, or binding with an owner/lease mismatch before reconciliation. | The Controller refuses adoption, modification, or deletion, keeps the finalizer, and emits an actionable alert/event. Unrelated resources are untouched. |
| LEASE-006 | P0 | Issue a token, then retry redemption, replay the request, or change the Lease resourceVersion. | The consumed marker is written with a resourceVersion compare-and-swap before TokenRequest. Only one irreversible TokenRequest is allowed and a terminal/consumed Lease cannot be reissued. |
| LEASE-007 | P0 | Make the TokenRequest response unavailable to the Broker after the API Server accepts it. | The Lease remains consumed; the token is never read from Kubernetes or returned later. The client must create a new Lease. |
| LEASE-008 | P0 | Make the API Server return a shorter or otherwise different actual expiration than requested. | The actual TokenRequest expirationTimestamp is stored in status and drives cleanup; requested TTL is never treated as the source of truth. |
| LEASE-009 | P1 | Restart Controller and Broker during preparation, issuance, and cleanup; separately drop watch events. | Reconciliation and periodic scanning converge without duplicate elevated bindings, lost finalizers, or leaked resources. Cleanup delay remains below one minute under the stated test conditions. |
| LEASE-010 | P1 | Let an issued credential expire, then inspect Lease and owned objects. | The Lease becomes Expired and all owned temporary objects are removed. No expired credential can be redeemed. |

### 3.5 Broker authentication and authorization

| ID | Priority | Action | Expected result and evidence |
| --- | --- | --- | --- |
| BROKER-001 | P0 | Send tokens with an untrusted issuer, wrong audience, invalid signature, missing subject, expired `exp`, or future `nbf`. | Authentication fails closed and no Lease or TokenRequest is created. |
| BROKER-002 | P0 | Authenticate as an ordinary user and request a daily credential. | Only `base-readonly.v1` for the verified caller's own identity can be issued. The response contains one kubeconfig. |
| BROKER-003 | P0 | Authenticate as an ordinary user and attempt direct elevated issuance, redeem without an approved reference, or target another identity. | Direct issuance and unapproved redemption are rejected. Caller-supplied target/profile fields are rejected; an ordinary user cannot obtain elevation by changing request JSON. |
| BROKER-004 | P0 | Authenticate at the admin endpoint with a valid admin-group claim and request temporary elevation for an active target with a valid approval reference. | The request succeeds only after the target, requester, profile, TTL, approval, status, and suspension state are rechecked. |
| BROKER-005 | P0 | Call the admin endpoint without the admin group, with a forged group claim, with a suspended/nonexistent target, or with a missing/changed approval reference. | The request is rejected and no elevated Lease or TokenRequest is created. |
| BROKER-006 | P0 | Change the OIDC Broker audience and the Kubernetes TokenRequest audience independently. | OIDC validation uses the server-configured Broker audience; TokenRequest uses the separately configured fixed Kubernetes audience. Caller input cannot select either audience. |
| BROKER-007 | P0 | Reuse an issued response, call redeem twice concurrently, and call redeem after revocation. | Only one response is delivered. Concurrent or subsequent redemption fails without a second TokenRequest or token disclosure. |
| BROKER-008 | P0 | Make OIDC or approval validation unavailable. | New issuance and elevation stop fail closed. Already issued tokens remain valid only until their original expiration; revocation and cleanup remain available. Personnel-directory synchronization is outside the version-one design and is not part of this acceptance item. |
| BROKER-009 | P1 | Verify Broker Kubernetes permissions and inspect its projected ServiceAccount token. | Broker cannot create identities, read Secret data, list all identities for ordinary lookup, or modify arbitrary RBAC. Its token is projected, short-lived, audience-bound, and default automount/long-lived Broker Secrets are absent. |
| BROKER-010 | P0 | Deliver an authenticated external approval system `APPROVED` event for an active InternalUser, let the adapter submit the record, redeem with the same identity, then replay the event/reference or add target/profile fields. | The adapter fetches the authoritative external approval system instance and calls internal approve. Broker persists one `AwaitingRedemption` Lease, creates no temporary resources before redemption, fixes requester/target/profile and persisted TTL, and allows one redemption only. The repeated event receives the same approval result through Broker idempotency. |

### 3.6 External approval adapter

| ID | Priority | Action | Expected result and evidence |
| --- | --- | --- | --- |
| ADAPTER-001 | P0 | Send an external approval system URL-verification challenge and an `APPROVED` event with a valid callback token. | The challenge is echoed without any approval data. The approved event is accepted only for the configured event type and approval definition. |
| ADAPTER-002 | P0 | Change the event's target, issuer, TTL, reason, or user fields after the callback, while the external approval system API returns an approved instance. | The adapter ignores event-supplied approval details, fetches the complete instance, uses the fixed issuer and configured form fields, and rejects malformed or policy-violating values. |
| ADAPTER-003 | P0 | Deliver a pending/rejected event, an invalid token/signature, an unconfigured approval code, or an instance that is not `APPROVED`. | The adapter does not call Broker or the notifier. Invalid authentication fails closed; non-approved events are safely ignored. |
| ADAPTER-004 | P0 | Deliver the same approved event twice and make notification fail after the first Broker success. | The adapter has no event-consumption persistence. Broker `approvalID` idempotency returns the same reference and the adapter retries notification without creating another Lease. |
| ADAPTER-005 | P1 | Inspect the adapter ServiceAccount, volumes, outbound TLS, SMTP message, and callback response. | The adapter has no Kubernetes RBAC or ServiceAccount token, uses a dedicated Broker mTLS client identity, stores only short-lived approval access tokens in memory, and never returns or handles a Kubernetes token/kubeconfig. |

### 3.7 CLI and file handling

| ID | Priority | Action | Expected result and evidence |
| --- | --- | --- | --- |
| CLI-001 | P0 | Complete login with Authorization Code + PKCE, then replay state or alter the verifier/challenge. | Valid state and S256 PKCE succeed; replayed or mismatched values fail. No client secret is required. |
| CLI-003 | P0 | Write to a new file, an existing file, and a symlink output path. | The output path must be explicitly selected, regular files are written with mode `0600`, and symlink paths are refused. |
| CLI-004 | P0 | Receive the reference from the external approval adapter, run `elevated redeem`, and inspect request fields and stdout/stderr. | The CLI sends only the reference after fresh OIDC login, writes the kubeconfig with mode `0600`, and never prints credential material. There is no user-side elevated request command. |
| CLI-005 | P1 | Run base and elevated commands with a standard or explicit YAML config, then override selected values with command-line flags. | Configured defaults are loaded, command-line flags take precedence, unknown fields and multiple YAML documents are rejected, and the config format accepts no token, kubeconfig, client secret, or approval reference field. |
| CLI-006 | P0 | Run the base command against a recording Broker, then try to add target, profile, or approval-reference fields through command-line arguments, configuration, or a modified request. | The base request contains only the requested TTL. Target and profile are derived or fixed by the service; unsupported fields and flags are rejected, and no alternate identity can be selected by the CLI. |
| CLI-007 | P0 | Run `elevated redeem` with a missing, malformed, whitespace-containing, or oversized reference; also try the removed `elevated request` command. | The CLI rejects each invalid operation before credential delivery. It does not call the Broker or create/overwrite the output file. There is no user-side elevated request operation. |
| CLI-008 | P0 | Log in as user A and redeem user B's reference, then replay a valid reference after the first successful redemption. | The Broker rejects the identity mismatch and replay. The CLI receives no second kubeconfig and does not leave a new output file for the rejected operation. |
| CLI-009 | P1 | Make OIDC discovery return a mismatched issuer or non-HTTPS endpoint, make the token endpoint return an invalid bearer response or nonce, and make the Broker return an incomplete credential response. | The CLI fails closed with a safe error, performs no credential-file write, and does not display the response body or credential material. |
| CLI-010 | P1 | Connect to OIDC and Broker test endpoints with an untrusted CA, wrong TLS server name, or invalid certificate. | TLS verification fails. The CLI has no insecure fallback and does not continue to OIDC login, Broker issuance, or kubeconfig output. |

### 3.8 End-to-end CLI and Broker flow

This is a real-process acceptance test. It must run the compiled
`internal-user-cli` binary against the deployed test Broker and Controller, with
the in-repository OIDC provider as the only identity provider. It does not
require a real external approval system tenant, Higress/WAF, or the additional audit sink.

| ID | Priority | Action | Expected result and evidence |
| --- | --- | --- | --- |
| E2E-001 | P0 | Start the repository OIDC provider with an HTTPS test certificate, the `internal-kc` public client, and test subjects `alice` and `bob`. Configure the Broker and CLI with that issuer, its CA, the fixed Broker audience, and a loopback callback. Build and execute the actual `internal-user-cli` binary with `--no-browser` and `--login-hint alice`; a test harness follows the printed authorization URL and its redirect to the CLI loopback callback. Run both `base` and `elevated redeem` after pre-creating an approved reference through the administrator CLI/internal approval test path. | The real process completes Discovery, S256 PKCE authorization, token exchange, Broker authentication, Lease processing, and kubeconfig write. The OIDC server observes the expected state, nonce, loopback redirect, one-time code, and PKCE challenge. Base uses the stable ServiceAccount; elevated redemption uses the approved reference and a lease-specific temporary ServiceAccount. The binary exits successfully, writes exactly the selected output file with mode `0600`, and prints only approved metadata. |
| E2E-002 | P0 | Re-run the compiled CLI with the same elevated reference and `--login-hint alice`, then create a separate reference for `bob` and run the CLI with `--login-hint bob` while redeeming Alice's reference. Keep the first output file and process output for comparison. | Replay and identity mismatch are rejected by the Broker; no second credential is returned, no second TokenRequest occurs, and a rejected run does not create or overwrite an output file. |

For this test, the harness may retain the kubeconfig only as restricted test data
for the duration of an authorized API check. It must not print, log, upload, or
put the kubeconfig, bearer token, refresh token, private key, or Secret data into
test evidence. Remove the temporary output files after the assertions.

### 3.9 Broker source IP enforcement (default)

| ID | Priority | Action | Expected result and evidence |
| --- | --- | --- | --- |
| NET-001 | P0 | From the trusted-proxy test fixture, send an admin request with an allowed normalized client-IP header. | Broker accepts the source-IP check only when the peer is the configured trusted proxy identity and the normalized IP is in the allowlist. The test does not exercise Higress IP derivation. |
| NET-002 | P0 | From the same fixture, send an admin request with a normalized client IP outside the allowlist. | Broker rejects the request before creating a Lease or TokenRequest. |
| NET-003 | P0 | Send an admin request with a missing, malformed, client-controlled, or untrusted normalized client-IP header. | Broker rejects it fail closed even when OIDC authentication and admin group checks pass. |
| NET-004 | P0 | Connect directly to the Broker Service, bypassing the trusted proxy. | NetworkPolicy, service exposure, or peer authentication blocks the request. Direct access cannot bypass source-IP enforcement. |
| NET-005 | P1 | Verify the Broker's own rate limits, request-size limits, invalid HTTP methods, and malformed body handling without Higress. | Broker rejects invalid or excessive requests without creating a Lease. |
| NET-006 | P1 | Inspect namespace policies and egress traffic for the default Broker deployment. | Broker ingress is limited to the trusted proxy and approval-adapter identities; egress is limited to Kubernetes API and configured OIDC discovery/JWKS endpoints. Ordinary application Pods cannot reach `internal-user-system`. |
| NET-007 | P1 | Restart the Broker and repeat NET-001 through NET-004. | Source-IP and trusted-peer checks remain fail closed after restart; no stale trusted header or bypass state is accepted. |

### 3.10 Additional Higress/WAF integration tests

These tests are excluded from the default Broker-only acceptance run because
they require a deployed Higress/WAF and its real trusted proxy-chain
configuration. They are mandatory before enabling an external/public route.

| ID | Gate | Action | Expected result and evidence |
| --- | --- | --- | --- |
| GW-001 | External route | Send an allowed request through Higress/WAF with the configured trusted proxy chain. | Higress derives the real client IP, applies its source CIDR allowlist, removes client-supplied `Forwarded`, `X-Forwarded-For`, `X-Real-IP`, and trusted-IP headers, and writes the normalized internal header received by the Broker. |
| GW-002 | External route | Send forged forwarding and trusted-IP headers from allowed and disallowed networks. | Client-supplied headers never override the real source. Higress and Broker make the expected defense-in-depth decision. |
| GW-003 | External route | Send a request from outside the configured CIDR and verify both the WAF response and Broker access logs. | Higress/WAF rejects the request before forwarding. If a request reaches the Broker through a test bypass, Broker rejects it as well. |
| GW-004 | External route | Exercise Higress/WAF rate limits, request-size limits, invalid methods, and malformed bodies. | Gateway limits are enforced before forwarding; Broker validation remains active as a second boundary and no Lease is created. |

### 3.11 Additional audit and information-disclosure tests

These tests are excluded from the default functional acceptance run. They are
mandatory before issuing production credentials or enabling an external/public
route, and require the protected centralized audit/logging sink.

| ID | Gate | Action | Expected result and evidence |
| --- | --- | --- | --- |
| AUD-001 | Production gate | Perform successful, rejected, revoked, expired, and failed issuance attempts. | Central records contain subject, requester, profile, Lease ID, approval reference where applicable, source IP, user-agent, result, and timestamp. |
| AUD-002 | Production gate | Search Broker logs, Controller logs, Lease objects, Secret metadata, API responses, and centralized audit records for sensitive values. | No token, kubeconfig, refresh token, private key, or Secret data appears anywhere. |
| AUD-003 | Production gate | Inspect the bound Secret and Lease status for base and elevated leases. | Each Lease has one unique empty Opaque Secret; status and Secret contain no token, kubeconfig, private key, or Secret value. |
| AUD-004 | Production gate | Run the CLI and inspect stdout, stderr, process arguments, local storage, and success display. | No token, kubeconfig, refresh token, or private key is printed or persisted outside the explicitly selected `0600` output file; only approved metadata is displayed. |
| AUD-005 | Production gate | Inspect Kubernetes audit policy and a `serviceaccounts/token` event. | The event is recorded at Metadata level only; request and response bodies are absent. |
| AUD-006 | Production gate | Force audit write failure immediately before delivery. | The Broker does not return the one-time kubeconfig and records a safe failure. No retry can retrieve the lost credential from Kubernetes. |

## 4. Test execution and evidence

Run the focused automated checks from `controllers/user`:

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

For `E2E-001` and `E2E-002`, build and run the real CLI process. Start
`test/oidc/cmd/test-oidc` with a disposable HTTPS certificate and a test JSON
configuration containing only the `internal-kc` client and the `alice`/`bob`
test users; pass its CA
and server name to both the Broker and CLI. The test harness must consume the
CLI's `--no-browser` authorization URL, follow the test provider's redirect to
the loopback callback, and wait for the process to exit. Use the deployed
Broker/Controller and an isolated test cluster for the resource and
`kubectl --kubeconfig` assertions. Build the binary with
`go build -o <temporary-test-path>/internal-user-cli ./cmd/internal-user-cli`.
Use the administrator CLI or internal approval test path to seed the elevated
reference; do not call the removed
user-side elevated request operation.

The E2E evidence consists only of exit codes, redacted OIDC/Broker request
metadata, Lease and ServiceAccount metadata, output-file mode, and authorized
API result summaries. Do not save or display any credential-bearing response.

For the disposable-cluster `LEASE-007` response-loss test, explicitly opt in
to the acceptance build tag and keep all token-bearing responses inside the
test process:

```bash
KUBECONFIG=<disposable-test-cluster-kubeconfig> INTERNAL_USER_ACCEPTANCE_CLUSTER=true \
  go test -tags acceptance ./pkg/broker -run '^TestTokenRequestResponseLossAfterAPIServerAcceptance$' -count=1
```

Run the full package suite when the envtest binaries and other repository test
fixtures are installed:

```bash
go test ./...
```

The full suite is a release gate. Missing test infrastructure or unrelated
legacy fixture failures must be recorded separately and cannot be reported as
an acceptance pass.

RBAC-004 cannot be considered complete for production from Kubernetes RBAC
alone: the Controller's dynamic ClusterRoleBinding create/delete permission
requires an admission policy or isolated signer that constrains role refs,
subjects, names, and ownership labels. The default run records this as an
explicit security review item when that control is not installed.

For cluster acceptance, retain at least:

- `kubectl get internalusers,credentialleases --all-namespaces -o yaml` with
  sensitive fields reviewed and redacted;
- `kubectl auth can-i` results for ordinary, administrator, Broker, and
  Controller identities;
- ServiceAccount, Role, ClusterRole, RoleBinding, ClusterRoleBinding,
  and NetworkPolicy evidence;
- Broker HTTP status/body summaries with all credentials removed;
- TokenRequest results and resource deletion timestamps;
- restart, watch-loss, approval-outage, and OIDC-outage results.

When running the additional gateway and audit/information-disclosure tests,
also retain the Higress/WAF header transformation evidence, centralized audit
records, Kubernetes audit events, and the sensitive-value search result.

## 5. Release gates

The feature is not ready for an external route or production credentials until
all P0/P1 cases pass and the following are explicitly reviewed:

- The exact profile resource allowlists and admission policies.
- OIDC issuer, audience, admin-group, and key rotation configuration.
- Higress/WAF trusted proxy chain and source CIDRs.
- Broker and namespace NetworkPolicies.
- Kubernetes audit policy and protected retention destination.
- Administrator bindings and approval workflow.
- Cleanup and recovery behavior under Controller/Broker restarts.
- Higress/WAF integration cases `GW-001` through `GW-004` before enabling any
  external/public route.
- Audit and information-disclosure cases `AUD-001` through `AUD-006` before
  issuing production credentials or enabling any external/public route.
