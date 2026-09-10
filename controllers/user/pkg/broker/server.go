/*
Copyright 2026 labring.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package broker

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	userv1 "github.com/labring/sealos/controllers/user/api/v1"
	"github.com/labring/sealos/controllers/user/pkg/internalcredentials"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewServer(leaseClient client.Client, authenticator Authenticator, tokenIssuer TokenIssuer, config Config) (*Server, error) {
	server := &Server{
		LeaseClient:   leaseClient,
		Authenticator: authenticator,
		TokenIssuer:   tokenIssuer,
		Config:        config,
	}
	if err := server.normalizeConfig(); err != nil {
		return nil, err
	}
	return server, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/v1/credentials/base", s.handleBaseIssue)
	// Elevated requests are created by the external approval system. Keep the
	// former user-facing endpoint explicitly unavailable instead of allowing
	// it to fall through to the lease status handler.
	mux.HandleFunc("/v1/credentials/elevated/request", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "elevated requests are created by the approval system"})
	})
	mux.HandleFunc("/v1/credentials/elevated/redeem", s.handleElevatedRedeem)
	mux.HandleFunc("/v1/internal/credentials/approve", s.handleInternalApprove)
	mux.HandleFunc("/v1/credentials/", s.handleLease)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health probes are intentionally unauthenticated and are not part of
		// the credential audit stream.
		if r.URL.Path == "/healthz" {
			mux.ServeHTTP(w, r)
			return
		}

		event := &AuditEvent{
			Timestamp: s.now(),
			Operation: "http_request",
			UserAgent: truncateAuditValue(r.UserAgent(), 512),
		}
		request := r.WithContext(withAuditEvent(r.Context(), event))
		buffered := newBufferedResponseWriter()
		if s.requestLimiter != nil && !s.requestLimiter.Allow() {
			event.Operation = "rate_limited"
			buffered.Header().Set("Retry-After", "1")
			writeJSON(buffered, http.StatusTooManyRequests, map[string]string{"error": "broker request rate limit exceeded"})
			event.StatusCode = buffered.statusCode()
			event.Result = auditResult(event.StatusCode)
			if s.Audit != nil {
				if err := s.Audit.Record(request.Context(), *event); err != nil {
					writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "broker audit is unavailable"})
					return
				}
			}
			buffered.commit(w)
			return
		}
		mux.ServeHTTP(buffered, request)
		event.StatusCode = buffered.statusCode()
		event.Result = auditResult(event.StatusCode)
		if s.Audit != nil {
			if err := s.Audit.Record(request.Context(), *event); err != nil {
				// Do not deliver a one-time credential if the corresponding
				// audit event could not be accepted.
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "broker audit is unavailable"})
				return
			}
		}
		buffered.commit(w)
	})
}

func (s *Server) handleBaseIssue(w http.ResponseWriter, r *http.Request) {
	setAuditOperation(r.Context(), "issue_base")
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	principal, ok := s.authenticateHTTP(w, r)
	if !ok {
		return
	}
	var request issueRequest
	if err := decodeRequest(r, s.Config.MaxBodyBytes, &request); err != nil {
		writeError(w, newHTTPError(http.StatusBadRequest, "invalid base credential request", err))
		return
	}
	response, err := s.issue(r.Context(), principal.Identity(), principal.Identity(), userv1.BaseReadonlyProfile, request.RequestedTTLSeconds, "")
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleElevatedRedeem(w http.ResponseWriter, r *http.Request) {
	setAuditOperation(r.Context(), "redeem_elevated")
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	principal, ok := s.authenticateHTTP(w, r)
	if !ok {
		return
	}
	var request elevatedRedeemRequest
	if err := decodeRequest(r, s.Config.MaxBodyBytes, &request); err != nil {
		writeError(w, newHTTPError(http.StatusBadRequest, "invalid elevated redemption request", err))
		return
	}
	if err := validateApprovalReference(request.ApprovalReference); err != nil {
		writeError(w, newHTTPError(http.StatusBadRequest, "approval reference is invalid", err))
		return
	}
	setAuditApprovalReference(r.Context(), request.ApprovalReference)
	leaseName, err := leaseNameFromApprovalReference(request.ApprovalReference)
	if err != nil {
		writeError(w, newHTTPError(http.StatusBadRequest, "approval reference is invalid", err))
		return
	}
	lease := &userv1.CredentialLease{}
	if err := s.LeaseClient.Get(r.Context(), namespaced(leaseName, s.Config.BrokerNamespace), lease); err != nil {
		if apierrors.IsNotFound(err) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "approval reference is not valid"})
			return
		}
		writeError(w, newHTTPError(http.StatusServiceUnavailable, "approval record is unavailable", err))
		return
	}
	setAuditLeaseID(r.Context(), lease.Name)
	setAuditTarget(r.Context(), lease.Spec.Target)
	setAuditProfile(r.Context(), lease.Spec.Profile)
	if lease.Spec.Profile != userv1.ClusterOpsWriteProfile || lease.Spec.ApprovalID == "" || lease.Spec.ApprovalReference != request.ApprovalReference || lease.Spec.Target != principal.Identity() || lease.Spec.Requester != principal.Identity() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "approval reference is not valid for this identity"})
		return
	}
	if lease.Status.ApprovalExpirationTimestamp != nil && !lease.Status.ApprovalExpirationTimestamp.After(s.now()) {
		if err := s.expireApproval(r.Context(), lease); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": "approval reference has expired"})
		return
	}
	if _, err := s.activeTarget(r.Context(), principal.Identity()); err != nil {
		writeError(w, err)
		return
	}
	if lease.Status.Phase != userv1.CredentialLeaseAwaitingRedemption && !(lease.Status.Phase == "" && lease.Spec.ApprovalID != "") {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "approval reference has already been redeemed"})
		return
	}
	now := metav1.NewTime(s.now().UTC().Truncate(time.Second))
	lease.Status.Phase = userv1.CredentialLeasePending
	lease.Status.RedeemedAt = &now
	meta.SetStatusCondition(&lease.Status.Conditions, metav1.Condition{Type: "Redeemed", Status: metav1.ConditionTrue, Reason: "UserAuthenticated", Message: "approval reference redeemed by the target identity"})
	if err := s.LeaseClient.Status().Update(r.Context(), lease); err != nil {
		if apierrors.IsConflict(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "approval reference was redeemed concurrently"})
			return
		}
		writeError(w, newHTTPError(http.StatusServiceUnavailable, "approval record could not be redeemed", err))
		return
	}
	profile, _ := internalcredentials.ProfileFor(userv1.ClusterOpsWriteProfile)
	response, err := s.issueLease(r.Context(), lease, profile)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleInternalApprove(w http.ResponseWriter, r *http.Request) {
	setAuditOperation(r.Context(), "approve_elevated")
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.authenticateInternalHTTP(w, r) {
		return
	}
	var request internalApprovalRequest
	if err := decodeRequest(r, s.Config.MaxBodyBytes, &request); err != nil {
		writeError(w, newHTTPError(http.StatusBadRequest, "invalid approval record", err))
		return
	}
	if err := validateApprovalID(request.ApprovalID); err != nil {
		writeError(w, newHTTPError(http.StatusBadRequest, "approval ID is invalid", err))
		return
	}
	if err := validateApprovalReason(request.ApprovalReason); err != nil {
		writeError(w, newHTTPError(http.StatusBadRequest, "approval reason is invalid", err))
		return
	}
	_, err := internalcredentials.ValidateRequestedTTL(userv1.ClusterOpsWriteProfile, request.RequestedTTLSeconds)
	if err != nil {
		writeError(w, newHTTPError(http.StatusBadRequest, "requested TTL is invalid", err))
		return
	}
	if err := internalcredentials.ValidateIdentity(request.Target.Issuer, request.Target.Subject); err != nil || !s.issuerAllowed(request.Target.Issuer) {
		writeError(w, newHTTPError(http.StatusBadRequest, "target identity is invalid", err))
		return
	}
	if _, err := s.activeTarget(r.Context(), request.Target); err != nil {
		writeError(w, err)
		return
	}
	setAuditTarget(r.Context(), request.Target)
	setAuditProfile(r.Context(), userv1.ClusterOpsWriteProfile)
	leaseName := approvalLeaseName(request.ApprovalID)
	lease := &userv1.CredentialLease{}
	if err := s.LeaseClient.Get(r.Context(), namespaced(leaseName, s.Config.BrokerNamespace), lease); err == nil {
		response, err := approvalResponseForLease(lease)
		if err != nil || lease.Spec.Target != request.Target || lease.Spec.RequestedTTLSeconds != request.RequestedTTLSeconds || lease.Spec.ApprovalReason != request.ApprovalReason {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "approval ID is already bound to another request"})
			return
		}
		setAuditApprovalReference(r.Context(), lease.Spec.ApprovalReference)
		writeJSON(w, http.StatusOK, response)
		return
	} else if !apierrors.IsNotFound(err) {
		writeError(w, newHTTPError(http.StatusServiceUnavailable, "approval record is unavailable", err))
		return
	}
	reference, err := newApprovalReference(leaseName)
	if err != nil {
		writeError(w, newHTTPError(http.StatusServiceUnavailable, "approval reference could not be generated", err))
		return
	}
	expiresAt := metav1.NewTime(s.now().Add(s.Config.ApprovalWaitTimeout).UTC().Truncate(time.Second))
	lease = &userv1.CredentialLease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: s.Config.BrokerNamespace,
			Labels:    map[string]string{"user.sealos.io/broker-created": "true"},
		},
		Spec: userv1.CredentialLeaseSpec{
			Target:              request.Target,
			Requester:           request.Target,
			Profile:             userv1.ClusterOpsWriteProfile,
			RequestedTTLSeconds: request.RequestedTTLSeconds,
			ApprovalReference:   reference,
			ApprovalID:          request.ApprovalID,
			ApprovalReason:      request.ApprovalReason,
		},
		Status: userv1.CredentialLeaseStatus{
			Phase:                       userv1.CredentialLeaseAwaitingRedemption,
			ApprovalExpirationTimestamp: &expiresAt,
		},
	}
	if err := s.LeaseClient.Create(r.Context(), lease); err != nil {
		if apierrors.IsAlreadyExists(err) {
			existing := &userv1.CredentialLease{}
			if getErr := s.LeaseClient.Get(r.Context(), namespaced(leaseName, s.Config.BrokerNamespace), existing); getErr == nil {
				response, responseErr := approvalResponseForLease(existing)
				if responseErr == nil && existing.Spec.Target == request.Target && existing.Spec.RequestedTTLSeconds == request.RequestedTTLSeconds && existing.Spec.ApprovalReason == request.ApprovalReason {
					setAuditApprovalReference(r.Context(), existing.Spec.ApprovalReference)
					writeJSON(w, http.StatusOK, response)
					return
				}
			}
		}
		writeError(w, newHTTPError(http.StatusServiceUnavailable, "approval record could not be persisted", err))
		return
	}
	if err := s.LeaseClient.Status().Update(r.Context(), lease); err != nil {
		writeError(w, newHTTPError(http.StatusServiceUnavailable, "approval record status could not be persisted", err))
		return
	}
	setAuditLeaseID(r.Context(), lease.Name)
	setAuditApprovalReference(r.Context(), reference)
	writeJSON(w, http.StatusCreated, approvalResponse{
		ApprovalID:          request.ApprovalID,
		ApprovalReference:   reference,
		Profile:             userv1.ClusterOpsWriteProfile,
		RequestedTTLSeconds: request.RequestedTTLSeconds,
		ApprovalExpiresAt:   expiresAt.Time,
	})
}

func (s *Server) authenticateInternalHTTP(w http.ResponseWriter, r *http.Request) bool {
	if s.Config.InternalClientCA == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "internal approval endpoint is unavailable"})
		return false
	}
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "mTLS client authentication is required"})
		return false
	}
	leaf := r.TLS.PeerCertificates[0]
	intermediates := x509.NewCertPool()
	for _, certificate := range r.TLS.PeerCertificates[1:] {
		intermediates.AddCert(certificate)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: s.Config.InternalClientCA, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, CurrentTime: s.now()}); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "mTLS client certificate is invalid"})
		return false
	}
	return true
}

func approvalResponseForLease(lease *userv1.CredentialLease) (approvalResponse, error) {
	if lease == nil || lease.Spec.Profile != userv1.ClusterOpsWriteProfile || lease.Spec.ApprovalID == "" || validateApprovalReference(lease.Spec.ApprovalReference) != nil || lease.Status.ApprovalExpirationTimestamp == nil {
		return approvalResponse{}, errors.New("approval record is incomplete")
	}
	return approvalResponse{
		ApprovalID:          lease.Spec.ApprovalID,
		ApprovalReference:   lease.Spec.ApprovalReference,
		Profile:             lease.Spec.Profile,
		RequestedTTLSeconds: lease.Spec.RequestedTTLSeconds,
		ApprovalExpiresAt:   lease.Status.ApprovalExpirationTimestamp.Time,
	}, nil
}

func (s *Server) expireApproval(ctx context.Context, lease *userv1.CredentialLease) error {
	if lease.Status.Phase == userv1.CredentialLeaseExpired {
		return nil
	}
	lease.Status.Phase = userv1.CredentialLeaseExpired
	meta.SetStatusCondition(&lease.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: "ApprovalExpired", Message: "approval reference expired before redemption"})
	if err := s.LeaseClient.Status().Update(ctx, lease); err != nil {
		if apierrors.IsConflict(err) {
			return newHTTPError(http.StatusConflict, "approval record changed concurrently", err)
		}
		return newHTTPError(http.StatusServiceUnavailable, "approval record could not be expired", err)
	}
	return nil
}

func (s *Server) handleLease(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateHTTP(w, r)
	if !ok {
		return
	}
	name, action, valid := leasePath(r.URL.Path)
	if !valid {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "credential lease not found"})
		return
	}
	if action == "revoke" {
		setAuditOperation(r.Context(), "revoke_lease")
	} else {
		setAuditOperation(r.Context(), "get_lease")
	}
	setAuditLeaseID(r.Context(), name)
	lease := &userv1.CredentialLease{}
	if err := s.LeaseClient.Get(r.Context(), namespaced(name, s.Config.BrokerNamespace), lease); err != nil {
		if apierrors.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "credential lease not found"})
			return
		}
		writeError(w, newHTTPError(http.StatusServiceUnavailable, "credential lease is unavailable", err))
		return
	}
	setAuditTarget(r.Context(), lease.Spec.Target)
	setAuditProfile(r.Context(), lease.Spec.Profile)
	setAuditApprovalReference(r.Context(), lease.Spec.ApprovalReference)
	if lease.Spec.Requester != principal.Identity() && !isAdmin(principal, s.Config.AdminGroup) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "credential lease access is forbidden"})
		return
	}
	if action == "revoke" {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		if err := s.revoke(r.Context(), lease); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, leaseStatus(lease))
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, leaseStatus(lease))
}

func (s *Server) authenticateHTTP(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	sourceIP, err := s.Config.SourceIP.Authorize(r)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "source address is not allowed"})
		return Principal{}, false
	}
	if event := auditEventFromContext(r.Context()); event != nil {
		event.SourceIP = sourceIP.String()
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Bearer authentication is required"})
		return Principal{}, false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Bearer authentication is required"})
		return Principal{}, false
	}
	principal, err := s.Authenticator.Authenticate(r.Context(), parts[1])
	if err != nil || validatePrincipal(principal) != nil || !s.issuerAllowed(principal.Issuer) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Bearer token is invalid"})
		return Principal{}, false
	}
	setAuditRequester(r.Context(), principal.Identity())
	return principal, true
}

func (s *Server) issue(ctx context.Context, target, requester userv1.Identity, profileName string, requestedSeconds int64, approvalReference string) (issueResponse, error) {
	profile, ok := internalcredentials.ProfileFor(profileName)
	if !ok {
		return issueResponse{}, newHTTPError(http.StatusBadRequest, "credential profile is not allowed", nil)
	}
	if profile.Elevated {
		return issueResponse{}, newHTTPError(http.StatusForbidden, "elevated credentials require an approved redemption", nil)
	}
	if approvalReference != "" {
		return issueResponse{}, newHTTPError(http.StatusBadRequest, "approval reference is not valid for base credentials", nil)
	}
	setAuditProfile(ctx, profile.Name)
	_, err := internalcredentials.ValidateRequestedTTL(profileName, requestedSeconds)
	if err != nil {
		return issueResponse{}, newHTTPError(http.StatusBadRequest, "requested TTL is invalid", err)
	}
	if err := internalcredentials.ValidateIdentity(target.Issuer, target.Subject); err != nil || !s.issuerAllowed(target.Issuer) {
		return issueResponse{}, newHTTPError(http.StatusForbidden, "target identity is not allowed", nil)
	}
	if err := internalcredentials.ValidateIdentity(requester.Issuer, requester.Subject); err != nil || !s.issuerAllowed(requester.Issuer) {
		return issueResponse{}, newHTTPError(http.StatusUnauthorized, "requester identity is invalid", nil)
	}
	setAuditTarget(ctx, target)
	if _, err := s.activeTarget(ctx, target); err != nil {
		return issueResponse{}, err
	}
	lease := &userv1.CredentialLease{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    s.Config.BrokerNamespace,
			GenerateName: "lease-",
			Labels:       map[string]string{"user.sealos.io/broker-created": "true"},
		},
		Spec: userv1.CredentialLeaseSpec{
			Target:              target,
			Requester:           requester,
			Profile:             profile.Name,
			RequestedTTLSeconds: requestedSeconds,
		},
	}
	if err := s.LeaseClient.Create(ctx, lease); err != nil {
		return issueResponse{}, newHTTPError(http.StatusServiceUnavailable, "credential lease could not be created", err)
	}
	return s.issueLease(ctx, lease, profile)
}

func (s *Server) issueLease(ctx context.Context, lease *userv1.CredentialLease, profile internalcredentials.Profile) (issueResponse, error) {
	if lease == nil || lease.Namespace != s.Config.BrokerNamespace || lease.Spec.Profile != profile.Name {
		return issueResponse{}, newHTTPError(http.StatusConflict, "credential lease is not valid for issuance", nil)
	}
	ttl, err := internalcredentials.ValidateRequestedTTL(profile.Name, lease.Spec.RequestedTTLSeconds)
	if err != nil {
		return issueResponse{}, newHTTPError(http.StatusBadRequest, "requested TTL is invalid", err)
	}
	if _, err := s.activeTarget(ctx, lease.Spec.Target); err != nil {
		return issueResponse{}, err
	}
	setAuditLeaseID(ctx, lease.Name)
	prepared, err := s.waitPrepared(ctx, lease.Name)
	if err != nil {
		return issueResponse{}, err
	}
	if err := s.validatePrepared(prepared, profile); err != nil {
		return issueResponse{}, err
	}
	consumedAt, err := s.markConsumed(ctx, prepared)
	if err != nil {
		return issueResponse{}, err
	}
	result, err := s.TokenIssuer.Issue(ctx, prepared.Status.ServiceAccountRef.Namespace, prepared.Status.ServiceAccountRef.Name, tokenRequestSpec(ttl, s.Config.KubernetesAudience, prepared.Status.BoundSecretRef))
	if err != nil {
		return issueResponse{}, newHTTPError(http.StatusServiceUnavailable, "credential token could not be issued", err)
	}
	if err := s.validateTokenResult(result, consumedAt, ttl); err != nil {
		return issueResponse{}, err
	}
	if err := s.markIssued(ctx, prepared.Name, consumedAt, result.Expiration); err != nil {
		return issueResponse{}, err
	}
	kubeconfig, err := buildKubeconfig(s.Config, prepared.Status.ServiceAccountRef.Name, result.Token)
	if err != nil {
		return issueResponse{}, newHTTPError(http.StatusServiceUnavailable, "credential response could not be built", err)
	}
	return issueResponse{
		LeaseID:             prepared.Name,
		Profile:             prepared.Spec.Profile,
		Username:            prepared.Status.ServiceAccountRef.Name,
		ExpirationTimestamp: result.Expiration,
		Kubeconfig:          string(kubeconfig),
	}, nil
}

func (s *Server) activeTarget(ctx context.Context, target userv1.Identity) (*userv1.InternalUser, error) {
	name := internalcredentials.DeriveInternalUserName(target.Issuer, target.Subject)
	user := &userv1.InternalUser{}
	if err := s.LeaseClient.Get(ctx, types.NamespacedName{Name: name}, user); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, newHTTPError(http.StatusNotFound, "target identity is not provisioned", nil)
		}
		return nil, newHTTPError(http.StatusServiceUnavailable, "target identity is unavailable", err)
	}
	if user.Spec.Identity != target {
		return nil, newHTTPError(http.StatusForbidden, "target identity does not match", nil)
	}
	if user.Spec.Suspend || user.Status.Phase != userv1.InternalUserActive {
		return nil, newHTTPError(http.StatusForbidden, "target identity is not active", nil)
	}
	return user, nil
}

func (s *Server) waitPrepared(ctx context.Context, name string) (*userv1.CredentialLease, error) {
	waitCtx, cancel := context.WithTimeout(ctx, s.Config.PrepareTimeout)
	defer cancel()
	ticker := time.NewTicker(s.Config.PollInterval)
	defer ticker.Stop()
	for {
		lease := &userv1.CredentialLease{}
		err := s.LeaseClient.Get(waitCtx, namespaced(name, s.Config.BrokerNamespace), lease)
		if err == nil {
			switch lease.Status.Phase {
			case userv1.CredentialLeasePrepared:
				return lease, nil
			case userv1.CredentialLeaseFailed, userv1.CredentialLeaseRevoked, userv1.CredentialLeaseExpired:
				return nil, newHTTPError(http.StatusConflict, "credential lease cannot be issued", nil)
			}
		} else if !apierrors.IsNotFound(err) {
			return nil, newHTTPError(http.StatusServiceUnavailable, "credential lease status is unavailable", err)
		}
		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return nil, newHTTPError(http.StatusGatewayTimeout, "credential lease preparation timed out", nil)
			}
			return nil, newHTTPError(http.StatusServiceUnavailable, "credential lease preparation was cancelled", nil)
		case <-ticker.C:
		}
	}
}

func (s *Server) validatePrepared(lease *userv1.CredentialLease, profile internalcredentials.Profile) error {
	if lease.Namespace != s.Config.BrokerNamespace || lease.Status.Phase != userv1.CredentialLeasePrepared || lease.Status.ServiceAccountRef == nil || lease.Status.BoundSecretRef == nil {
		return newHTTPError(http.StatusConflict, "credential lease is not prepared safely", nil)
	}
	serviceAccount := lease.Status.ServiceAccountRef
	if serviceAccount.Namespace != s.Config.UserNamespace || serviceAccount.Kind != "ServiceAccount" || serviceAccount.APIVersion != "v1" || serviceAccount.Name == "" {
		return newHTTPError(http.StatusConflict, "credential lease ServiceAccount reference is invalid", nil)
	}
	secret := lease.Status.BoundSecretRef
	if secret.Namespace != s.Config.UserNamespace || secret.Kind != "Secret" || secret.APIVersion != "v1" || secret.Name == "" || secret.UID == "" {
		return newHTTPError(http.StatusConflict, "credential lease bound Secret reference is invalid", nil)
	}
	if profile.UsesStableAccount {
		if serviceAccount.Name != internalcredentials.DeriveInternalUserName(lease.Spec.Target.Issuer, lease.Spec.Target.Subject) || lease.Status.BindingRef != nil {
			return newHTTPError(http.StatusConflict, "stable credential lease references are invalid", nil)
		}
	} else {
		if lease.Status.BindingRef == nil || lease.Status.BindingRef.Kind != "ClusterRoleBinding" || lease.Status.BindingRef.APIVersion != "rbac.authorization.k8s.io/v1" || lease.Status.BindingRef.Namespace != "" || lease.Status.BindingRef.Name != internalcredentials.ResourceName("internal-lease-binding", lease.Namespace, lease.Name) || serviceAccount.Name != internalcredentials.ResourceName("internal-lease", lease.Namespace, lease.Name) {
			return newHTTPError(http.StatusConflict, "temporary credential lease references are invalid", nil)
		}
	}
	return nil
}

func (s *Server) markConsumed(ctx context.Context, lease *userv1.CredentialLease) (time.Time, error) {
	if lease.Status.ConsumedAt != nil || lease.Status.Phase != userv1.CredentialLeasePrepared {
		return time.Time{}, newHTTPError(http.StatusConflict, "credential lease has already been consumed", errLeaseBusy)
	}
	// metav1.Time serializes with second precision, so keep the CAS value
	// stable across the status update and the subsequent API read.
	now := s.now().UTC().Truncate(time.Second)
	lease.Status.ConsumedAt = ptr.To(metav1.NewTime(now))
	meta.SetStatusCondition(&lease.Status.Conditions, metav1.Condition{Type: "Consumed", Status: metav1.ConditionTrue, Reason: "CAS", Message: "lease consumed before TokenRequest"})
	if err := s.LeaseClient.Status().Update(ctx, lease); err != nil {
		if apierrors.IsConflict(err) {
			return time.Time{}, newHTTPError(http.StatusConflict, "credential lease was consumed concurrently", err)
		}
		return time.Time{}, newHTTPError(http.StatusServiceUnavailable, "credential lease could not be consumed", err)
	}
	return now, nil
}

func (s *Server) validateTokenResult(result TokenResult, consumedAt time.Time, ttl time.Duration) error {
	if result.Token == "" || !result.Expiration.After(s.now().Add(-s.Config.ClockSkew)) || result.Expiration.After(consumedAt.Add(ttl).Add(s.Config.ClockSkew)) {
		return newHTTPError(http.StatusServiceUnavailable, "TokenRequest returned an invalid expiration", nil)
	}
	for _, audience := range result.Audiences {
		if audience == s.Config.KubernetesAudience {
			return nil
		}
	}
	return newHTTPError(http.StatusServiceUnavailable, "TokenRequest audience validation failed", nil)
}

func (s *Server) markIssued(ctx context.Context, name string, consumedAt time.Time, expiration time.Time) error {
	lease := &userv1.CredentialLease{}
	if err := s.LeaseClient.Get(ctx, namespaced(name, s.Config.BrokerNamespace), lease); err != nil {
		return newHTTPError(http.StatusServiceUnavailable, "credential lease could not be reloaded", err)
	}
	if lease.Status.ConsumedAt == nil || !lease.Status.ConsumedAt.Time.Equal(consumedAt) {
		return newHTTPError(http.StatusConflict, "credential lease consumption changed", nil)
	}
	if lease.Status.Phase == userv1.CredentialLeaseRevoked || lease.Status.Phase == userv1.CredentialLeaseExpired {
		return newHTTPError(http.StatusConflict, "credential lease was revoked during issuance", nil)
	}
	lease.Status.Phase = userv1.CredentialLeaseIssued
	lease.Status.ExpirationTimestamp = ptr.To(metav1.NewTime(expiration))
	meta.SetStatusCondition(&lease.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Issued", Message: "credential issued once"})
	if err := s.LeaseClient.Status().Update(ctx, lease); err != nil {
		return newHTTPError(http.StatusServiceUnavailable, "credential lease status could not be recorded", err)
	}
	return nil
}

func (s *Server) revoke(ctx context.Context, lease *userv1.CredentialLease) error {
	if lease.Status.Phase == userv1.CredentialLeaseFailed || lease.Status.Phase == userv1.CredentialLeaseRevoked || lease.Status.Phase == userv1.CredentialLeaseExpired {
		return nil
	}
	lease.Status.Phase = userv1.CredentialLeaseRevoked
	meta.SetStatusCondition(&lease.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: "Revoked", Message: "credential revoked by authorized caller"})
	if err := s.LeaseClient.Status().Update(ctx, lease); err != nil {
		if apierrors.IsConflict(err) {
			return newHTTPError(http.StatusConflict, "credential lease changed concurrently", err)
		}
		return newHTTPError(http.StatusServiceUnavailable, "credential lease could not be revoked", err)
	}
	return nil
}

func leaseStatus(lease *userv1.CredentialLease) leaseResponse {
	return leaseResponse{
		LeaseID:             lease.Name,
		Profile:             lease.Spec.Profile,
		Phase:               lease.Status.Phase,
		ExpirationTimestamp: lease.Status.ExpirationTimestamp,
		ConsumedAt:          lease.Status.ConsumedAt,
	}
}

func decodeRequest(request *http.Request, maxBytes int64, target interface{}) error {
	body, err := io.ReadAll(io.LimitReader(request.Body, maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > maxBytes {
		return errors.New("request body is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("request contains more than one JSON value")
		}
		return err
	}
	return nil
}

func leasePath(value string) (name, action string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(value, "/v1/credentials/"), "/")
	if len(parts) == 1 && parts[0] != "" {
		return parts[0], "", true
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "revoke" {
		return parts[0], "revoke", true
	}
	return "", "", false
}

func writeError(w http.ResponseWriter, err error) {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		httpErr = newHTTPError(http.StatusInternalServerError, "internal broker error", err)
	}
	writeJSON(w, httpErr.Code, map[string]string{"error": httpErr.Message})
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
