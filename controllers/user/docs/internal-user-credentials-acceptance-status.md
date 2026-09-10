# Internal User Credentials Acceptance Status

Status: in progress; not approved for production credentials or an external
route. `[x]` below means the stated evidence scope passed; a partial item
remains incomplete for the release gate.

Last updated: 2026-09-09. This document records the current execution status
of [`internal-user-credentials-acceptance.md`](internal-user-credentials-acceptance.md).
It does not replace the acceptance matrix and does not turn missing evidence
into a pass.

## Verified Evidence

- [x] `ID-001` through `ID-008`: identity validation, suspension/resume, user
  deletion cleanup, and user recreation.
- [x] Core ordinary/admin Broker authorization, self-only targeting, approval
  reference validation, fixed audiences, TTL limits, and profile allowlists.
- [x] Base credentials use the stable ServiceAccount; elevated credentials use
  a lease-specific temporary ServiceAccount and binding.
- [x] Lease issuance, revocation, expiry, cleanup, finalizer removal, and
  restart convergence cases exercised in the acceptance cluster.
- [x] Broker and Controller ServiceAccount boundary checks, including
  namespace-scoped Controller Secret/ServiceAccount access.
- [x] CLI Authorization Code + PKCE flow and `0600` kubeconfig output check.
- [x] Broker source-IP and trusted-proxy checks `NET-001` through `NET-004`.
- [x] Final cluster check: 19 CredentialLeases are terminal, none retain a
  finalizer, and no temporary lease ServiceAccount, Secret, or
  ClusterRoleBinding remains.
- [x] Targeted Go tests, Broker race test, vet, and builds passed. The exact
  targeted test command is recorded in the acceptance document.
- [x] `RBAC-004`: the independent Admission Webhook is deployed and Ready. It
  constrains binding create, update, and delete requests from the Controller
  ServiceAccount. Arbitrary or wrongly owned bindings were rejected, an owned
  base binding was deleted and recreated successfully, and webhook outage
  caused RBAC writes to fail closed.
- [x] `LEASE-002` automated coverage: cleanup failure retains the Lease
  finalizer and returns a positive retry delay.
- [x] `LEASE-005` automated coverage: ServiceAccount, Secret, and binding
  ownership mismatches are rejected without adoption and emit an actionable
  Warning Event.
- [x] `LEASE-008` automated coverage: the actual TokenRequest expiration is
  stored and used instead of the requested TTL.
- [x] `LEASE-007`: the explicit acceptance-tag test received a successful real
  `serviceaccounts/token` response from the API Server, discarded it without
  reading or persisting token data, and returned a simulated response-loss
  error. The Lease retained `ConsumedAt`/Prepared and a second consume attempt
  was rejected; temporary test resources were cleaned up.
- [x] `CLI-003`: the symlink output path is rejected and the regular output file
  is written with mode `0600`.
- [x] `CLI-006`: CLI tests verify that a base request contains only
  `requestedTTLSeconds`, and that target/profile/approval-reference injection
  through flags, configuration, or request mutation is rejected.
- [x] `CLI-010`: CLI tests reject untrusted certificates and wrong TLS server
  names for both OIDC and Broker endpoints. The full CLI run also confirms an
  OIDC TLS failure does not reach Broker or create an output file.
- [x] `CLI-005`: explicit and standard-path YAML config loading, command-line
  precedence, strict fields, and multi-document rejection are covered by CLI
  tests. The config format excludes tokens, kubeconfigs, client secrets, and
  one-time approval references.
- [x] Automated `BROKER-010`/`CLI-004` coverage: Broker internal approval,
  persisted `AwaitingRedemption` leases, fixed self target, replay rejection,
  reference-only redeem requests, and `0600` kubeconfig output are covered by
  targeted unit tests. The user-side elevated request flow has been removed.
- [x] Automated `ADAPTER-001` through `ADAPTER-005` coverage: callback
  authentication, authoritative external approval system lookup, fixed issuer/form mapping,
  Broker idempotent retry, notification retry behavior, and no Kubernetes
  credentials are covered by `pkg/approvaladapter` tests.
- [x] `PROF-007`: the deployed admission control and live temporary profile
  checks showed the reviewed profile permissions; workload creation,
  deployment updates, Secret reads, ServiceAccount creation, and impersonation
  were denied.
- [x] `NET-005`: Broker in-process request rate limiting, invalid methods,
  malformed JSON, and oversized request bodies are rejected without issuing
  credentials; health probes remain available.
- [x] `NET-007`: after a Broker restart, the two replicas and Service endpoints
  stayed Ready; a credential-free trusted-peer replay returned `401` for an
  allowed client IP and `403` for disallowed, missing, and malformed headers;
  an ordinary-Pod direct bypass timed out.
- [x] `NET-006`: replaced the broad Broker egress rule with exact Kubernetes API,
  configured issuer, and in-cluster OIDC discovery/JWKS destinations, and added
  `internal-user-system` default-deny ingress/egress. A real Higress Gateway
  returned HTTP 200 to the Broker health endpoint before and after restart.
  Broker-label probes reached the Kubernetes API, OIDC discovery, and JWKS with
  HTTP 200, while access to the Controller webhook timed out. Approval state is
  persisted by Broker and has no separate OIDC endpoint. Ordinary-Pod access to
  the Broker Service, Broker Pod IPs, and a temporary `internal-user-system`
  Pod all timed out.
  The Broker rollout returned to 2/2 Ready, and all temporary probes were
  deleted without reading credentials or ServiceAccount tokens.
- [x] `BROKER-008`: OIDC JWKS failure and external approval system lookup/submission
  failures are fail-closed. Version one assigns InternalUser lifecycle state to the
  controlled administrator CLI/administrator workflow; automatic
  personnel-directory synchronization is outside this acceptance item.
- [x] `LEASE-009`: live cluster coverage used the disposable Lease
  `lease-le009-20260908083033`. Its create event was missed while the Controller
  was stopped; after recovery it reached `Prepared` in about 18 seconds and
  produced exactly one lease ServiceAccount, one bound Secret, and one
  ClusterRoleBinding. The Broker was restarted during the prepared phase and
  returned to `2/2 Ready`. Its delete event was then missed while the Controller
  was stopped; after recovery all owned resources and the Lease were gone in
  about 19 seconds. No TokenRequest was made and no Secret data was read. The
  two CredentialLease webhooks were temporarily set to `Ignore` only to allow
  the API mutations while their Controller endpoint was stopped, and both were
  restored to `Fail` before each Controller recovery.
- [x] `E2E-001` and `E2E-002`: on 2026-09-09, the compiled `internal-user-cli`
  and `internal-user-admin` binaries ran against the repository HTTPS OIDC
  provider, the deployed Broker, and the deployed Controller in the isolated
  acceptance cluster. Alice completed base and elevated redemption through
  real OIDC Discovery, S256 PKCE, loopback callback, token exchange, Broker
  authentication, and Lease processing. The elevated reference was held only
  in the test process memory. Replaying the reference and redeeming it as Bob
  both exited non-zero, returned no output file, and did not issue a second
  credential. Successful outputs were created with mode `0600` in tmpfs and
  removed after assertions. Temporary test images, approvals, and mTLS
  material were removed and the original Broker/Controller deployments were
  restored.

## TODO: P0 Blockers Or Missing Evidence

- [ ] `BROKER-010`: live external approval system `APPROVED` event, adapter submission, same-user
  redemption, and one-time replay evidence are not recorded.
- [ ] `CLI-004`: live end-to-end reference delivery and elevated redeem output
  evidence are not recorded.
- [ ] `ADAPTER-006`: the deployed adapter has not yet been validated against a
  real external approval system callback, external approval system approval-instance API, and approved SMTP relay.

## TODO: P1 Or Partial Cases

- [x] `LEASE-009`: the periodic scanner and missed-watch rescan have unit
  coverage, and the live cluster test recovered missed create and delete events
  after Controller restart. Preparation converged in about 18 seconds and
  cleanup converged in about 19 seconds, below the one-minute target.
- [x] `NET-006`: NetworkPolicy inspection and runtime ingress/egress probes
  passed before and after Broker restart; the exact evidence is recorded above.
- [ ] `go test ./...`: rerun the full release-gate suite after the repository's
  envtest binaries and legacy fixtures are fixed. The previous full-suite
  attempts were blocked by existing etcd/default-namespace and historical
  kubeconfig/license fixtures.

## Excluded From Default Run

These remain additional release gates, as requested, and are not counted as
default acceptance failures:

- [ ] `GW-001` through `GW-004`: Higress/WAF integration and real proxy-chain
  header derivation.
- [ ] `AUD-001` through `AUD-006`: centralized audit and information-disclosure
  testing.

## Release State

`RBAC-004` has passed test-cluster acceptance, but the overall feature is not
production-approved. The remaining TODO items must be completed or explicitly
risk-accepted according to their priority. Changes remain uncommitted by
request.
