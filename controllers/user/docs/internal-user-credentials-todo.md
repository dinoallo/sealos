# Internal User Credentials TODO

Status: in progress. This is a working checklist for the current test-cluster
acceptance and RBAC hardening work. It complements
`internal-user-credentials-acceptance-status.md` and does not mark an item as
accepted without recorded evidence.

Last updated: 2026-09-09.

## Current Focus: Release-gate follow-up (`LEASE-009` complete)

### Implementation completed

- [x] Confirm that the Controller currently has dynamic
  `ClusterRoleBinding` write permissions.
- [x] Add an independent `ValidatingAdmissionWebhook` that only constrains RBAC
  mutations made by the Controller ServiceAccount.
- [x] Keep the admission component in a separate Pod, ServiceAccount, RBAC
  configuration, and certificate from the Controller.
- [x] Validate `roleRef`, subjects, binding names, ownership labels, resource
  UIDs, profile mapping, and Controller ServiceAccount identity.
- [x] Configure fail-closed behavior with `failurePolicy: Fail`.
- [x] Add unit tests for allowed and rejected binding mutations.
- [x] Generate the Admission Deployment, Service, certificate, RBAC, and
  `ValidatingWebhookConfiguration`; kustomize rendering passed.
- [x] Apply the Admission resources to the test cluster; the certificate is
  Ready and the CA bundle is injected.
- [x] Implement the stateless external approval adapter, Broker mTLS client,
  authoritative external approval system instance lookup, and protected email reference delivery.
- [x] Add adapter deployment manifests with no Kubernetes RBAC, disabled
  ServiceAccount token automount, dedicated Broker client certificate, and
  default-deny NetworkPolicy.

### Cluster runtime verification completed

- [x] Correct the duplicate-compressed layer in the registry manifest and
  republish the Admission image; the Deployment reached `1/1` and the Service
  has a ready endpoint.
- [x] A Controller ServiceAccount attempt to create an arbitrary binding was
  rejected. With a role already held by the Controller to isolate admission
  behavior, the independent webhook also rejected the binding.
- [x] Mutating the subject of an existing managed binding was rejected by the
  webhook, and deleting a binding with mismatched ownership was rejected.
- [x] Deleting a genuinely owned base binding was allowed and the Controller
  recreated it normally.
- [x] Scaling the Admission deployment to zero caused a Controller RBAC write
  to fail because the webhook had no endpoint; restoring one replica made the
  Deployment Ready again, proving `failurePolicy: Fail` is active.
- [x] `RBAC-004` implementation, deployment, and cluster runtime verification
  are complete.

## Acceptance Items Still Outstanding

### P0 or Missing Security Evidence

- [x] `RBAC-004`: independent admission control, image publication, deployment,
  and cluster runtime verification are complete.
- [x] `LEASE-002`: automated cleanup-failure coverage retains the finalizer and
  requests a retry.
- [x] `LEASE-005`: automated ServiceAccount, Secret, and binding ownership
  mismatch coverage fails closed and emits an actionable Warning Event.
- [x] `LEASE-007`: the explicit acceptance-tag test performed a real
  `serviceaccounts/token` request, discarded the successful response without
  reading or persisting token data, and verified that the consumed Lease could
  not be consumed again. Temporary test resources were cleaned up.
- [x] `LEASE-008`: automated coverage stores the actual TokenRequest expiration
  and uses it as the cleanup source of truth.
- [x] `CLI-003`: symlink output is rejected and regular-file output uses mode
  `0600`.
- [ ] `BROKER-010`: Broker approval persistence, adapter submission, and user
  redeem code have targeted coverage; live external approval system approval, same-user
  redemption, and replay evidence are still required.
- [ ] `CLI-004`: elevated redeem writes `0600` output without credential
  logging; live reference delivery and end-to-end evidence are still required.
- [ ] `ADAPTER-006`: deploy the adapter with real external approval system callback/API
  credentials and the approved SMTP relay, then record retry/idempotency and
  delivery evidence.

### P1 or Partial Coverage

- [x] `PROF-007`: admission control was enabled and live temporary profile
  checks denied workload creation, deployment updates, Secret reads,
  ServiceAccount creation, and impersonation.
- [x] `LEASE-009`: the periodic scanner and missed-watch rescan have unit
  coverage. Live cluster coverage recovered missed create and delete events
  after Controller restart; preparation took about 18 seconds and cleanup took
  about 19 seconds, below the one-minute target.
- [x] `BROKER-008`: OIDC JWKS and approval-validation outages are fail-closed.
  The controlled administrator CLI/administrator workflow owns InternalUser
  lifecycle state in version one; automatic personnel-directory synchronization
  is outside this acceptance scope.
- [x] `NET-005`: Broker-side in-process rate limiting, method, body, and
  request-size validation are implemented and tested; health probes remain
  available.
- [x] `NET-006`: NetworkPolicy inspection and runtime ingress/egress evidence
  passed before and after Broker restart; the broad `10.0.0.0/8:443` egress
  rule was removed, and `internal-user-system` now has default-deny policy.
- [x] `NET-007`: after Broker restart, endpoint readiness, trusted-peer source-IP
  responses (`401` allowed, `403` rejected), and ordinary-Pod direct bypass
  blocking were verified without credentials. This remains distinct from the
  real Higress proxy-chain tests.
- [ ] `go test ./...`: rerun the full suite after the existing envtest/etcd,
  default-namespace, and legacy fixture failures are fixed.

### NET-006 cluster evidence

- [x] Broker ingress allows the Higress namespace and the required Gateway
  labels on TCP 8443. A real Higress Gateway reached Broker `/healthz` with
  HTTP 200 before and after the Broker restart.
- [x] Broker egress allows DNS, the Kubernetes API Service IP, the configured
  issuer address, and the in-cluster OIDC Pod selector. A Broker-label probe
  reached OIDC discovery and `/jwks.json` with HTTP 200. Approval state is now
  persisted by Broker, so there is no separate OIDC approval endpoint.
- [x] Broker egress denied the internal-user-controller webhook Service and
  Pod IP probes with connection timeout. Ordinary application Pods were also
  denied access to the Broker Service and Pod IPs, and to a temporary workload
  in `internal-user-system`.
- [x] The Broker rollout returned to 2/2 Ready after the policy change and
  restart. All temporary probes used `automountServiceAccountToken: false`,
  read no Secret or token data, and were deleted after testing.

### LEASE-009 cluster evidence

- [x] Created disposable elevated Lease `lease-le009-20260908083033` at
  `2026-09-08T08:30:35Z` while the Controller had zero replicas. It remained
  `Pending` with zero owned ServiceAccounts, Secrets, or ClusterRoleBindings.
- [x] Restored both CredentialLease webhook policies to `Fail`, restarted the
  Controller, and observed `Prepared` at `2026-09-08T08:30:53Z`; exactly one
  object of each expected owned-resource type existed.
- [x] Restarted the two-replica Broker during the prepared phase; it returned
  Ready at `2026-09-08T08:31:25Z` without redeeming the Lease.
- [x] Stopped the Controller again, requested Lease deletion at
  `2026-09-08T08:31:26Z`, restored both webhook policies to `Fail`, and
  restarted the Controller. The Lease and all owned objects were gone at
  `2026-09-08T08:31:45Z` (19 seconds).
- [x] The test never read Secret data and never created a TokenRequest. The
  temporary `Ignore` policy was limited to the two CredentialLease webhooks
  while their Controller endpoint was intentionally stopped, and was restored
  before recovery.

## Already Verified

- [x] Identity validation, suspension/resume, deletion cleanup, and user
  recreation (`ID-001` through `ID-008`).
- [x] Broker authorization, self-only targeting, approval references, fixed
  audiences, TTL limits, and profile allowlists.
- [x] Stable base ServiceAccount credentials and lease-specific temporary
  ServiceAccount credentials.
- [x] Lease issuance, revocation, expiry, cleanup, finalizer removal, and
  restart convergence coverage.
- [x] Broker and Controller ServiceAccount boundary checks.
- [x] CLI Authorization Code + PKCE flow and `0600` kubeconfig output.
- [x] CLI elevated redeem request construction and validation unit coverage;
  this does not replace live `CLI-004` acceptance.
- [x] Broker source-IP and trusted-proxy checks (`NET-001` through `NET-004`).
- [x] Final cluster cleanup: 19 CredentialLeases are terminal, no Lease has a
  finalizer, and no temporary lease ServiceAccount, Secret, or
  ClusterRoleBinding remains.
- [x] Targeted Go tests, Broker race test, `go vet`, and builds. The full
  `go test ./...` release gate remains blocked by existing envtest and legacy
  fixture failures recorded below.

## Default-Run Exclusions

These remain additional release gates and are intentionally excluded from the
default acceptance decision:

- [ ] `GW-001` through `GW-004`: Higress/WAF integration and real proxy-chain
  header derivation.
- [ ] `AUD-001` through `AUD-006`: centralized audit and information-disclosure
  testing.

## Working Constraints

- [x] Do not create a commit until acceptance is complete.
- [x] Keep registry verification within the approved procedure: read only the
  target registry field from the pull Secret, pass credentials through stdin,
  and do not print or persist credentials.
- [x] Republish and verify the corrected image while continuing to follow the
  credential-handling procedure above.
- [x] Update `internal-user-credentials-acceptance-status.md` and this TODO
  document when each item gains reproducible evidence; partial evidence is
  explicitly labeled.

## Release Decision

- [ ] Do not approve production credentials or a public route while another
  required security item remains incomplete; `RBAC-004` is no longer the
  current blocker.
- [ ] Record explicit evidence, residual risk, and any accepted exceptions
  before the final release decision.
