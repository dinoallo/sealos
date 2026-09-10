package broker

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	userv1 "github.com/labring/sealos/controllers/user/api/v1"
	"github.com/labring/sealos/controllers/user/pkg/internalcredentials"
	"golang.org/x/time/rate"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestBaseIssueUsesCASAndReturnsOneTimeKubeconfig(t *testing.T) {
	now := time.Date(2026, 9, 7, 0, 0, 0, 123456789, time.UTC)
	identity := userv1.Identity{Issuer: "https://issuer.example", Subject: "alice"}
	user := &userv1.InternalUser{
		ObjectMeta: metav1.ObjectMeta{Name: internalcredentials.DeriveInternalUserName(identity.Issuer, identity.Subject)},
		Spec:       userv1.InternalUserSpec{Identity: identity, RoleProfile: userv1.BaseReadonlyProfile},
		Status:     userv1.InternalUserStatus{Phase: userv1.InternalUserActive},
	}
	scheme := runtime.NewScheme()
	if err := userv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	leaseClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&userv1.CredentialLease{}).WithObjects(user).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			lease, ok := obj.(*userv1.CredentialLease)
			if !ok {
				return c.Create(ctx, obj, opts...)
			}
			lease.Name = "lease-1"
			lease.GenerateName = ""
			lease.UID = "lease-uid"
			lease.Status.Phase = userv1.CredentialLeasePrepared
			lease.Status.ServiceAccountRef = &corev1.ObjectReference{APIVersion: "v1", Kind: "ServiceAccount", Namespace: userv1.InternalUserSystemNamespace, Name: user.Name}
			lease.Status.BoundSecretRef = &corev1.ObjectReference{APIVersion: "v1", Kind: "Secret", Namespace: userv1.InternalUserSystemNamespace, Name: "secret-1", UID: "secret-uid"}
			return c.Create(ctx, obj, opts...)
		},
	}).Build()
	authenticator := staticAuthenticator{principal: Principal{Issuer: identity.Issuer, Subject: identity.Subject}}
	tokenIssuer := recordingTokenIssuer{result: TokenResult{Token: "token-value", Expiration: now.Add(20 * time.Minute), Audiences: []string{"kubernetes.default.svc"}}}
	server := newTestServer(t, leaseClient, authenticator, &tokenIssuer, now)
	audit := &recordingAuditSink{}
	server.Audit = audit
	request := httptest.NewRequest(http.MethodPost, "/v1/credentials/base", bytes.NewBufferString(`{"requestedTTLSeconds":1800}`))
	request.RemoteAddr = "127.0.0.1:443"
	request.Header.Set("X-Trusted-Client-IP", "10.1.2.3")
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result issueResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.LeaseID != "lease-1" || result.Kubeconfig == "" || !strings.Contains(result.Kubeconfig, "token-value") {
		t.Fatalf("unexpected issue response: %#v", result)
	}
	if tokenIssuer.spec.BoundObjectRef == nil || tokenIssuer.spec.BoundObjectRef.UID != "secret-uid" {
		t.Fatalf("TokenRequest was not bound to the Lease Secret: %#v", tokenIssuer.spec)
	}
	lease := &userv1.CredentialLease{}
	if err := leaseClient.Get(context.Background(), types.NamespacedName{Namespace: userv1.CredentialBrokerNamespace, Name: "lease-1"}, lease); err != nil {
		t.Fatal(err)
	}
	if lease.Status.Phase != userv1.CredentialLeaseIssued || lease.Status.ConsumedAt == nil || lease.Status.ExpirationTimestamp == nil {
		t.Fatalf("Lease status was not recorded: %#v", lease.Status)
	}
	if !lease.Status.ExpirationTimestamp.Time.Equal(tokenIssuer.result.Expiration.Truncate(time.Second)) || !result.ExpirationTimestamp.Equal(tokenIssuer.result.Expiration) {
		t.Fatalf("actual TokenRequest expiration was not preserved: status=%v response=%v want=%v", lease.Status.ExpirationTimestamp, result.ExpirationTimestamp, tokenIssuer.result.Expiration)
	}
	if len(audit.events) != 1 {
		t.Fatalf("audit event count = %d, want 1", len(audit.events))
	}
	event := audit.events[0]
	if event.Operation != "issue_base" || event.Result != "success" || event.StatusCode != http.StatusOK || event.LeaseID != "lease-1" || event.SourceIP != "10.1.2.3" {
		t.Fatalf("unexpected audit event: %#v", event)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "token-value") || strings.Contains(string(encoded), "kubeconfig") || strings.Contains(string(encoded), "secret-uid") {
		t.Fatalf("audit event contains credential material: %s", encoded)
	}
}

func TestAuditFailureSuppressesOneTimeCredentialResponse(t *testing.T) {
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	identity := userv1.Identity{Issuer: "https://issuer.example", Subject: "alice"}
	user := &userv1.InternalUser{
		ObjectMeta: metav1.ObjectMeta{Name: internalcredentials.DeriveInternalUserName(identity.Issuer, identity.Subject)},
		Spec:       userv1.InternalUserSpec{Identity: identity, RoleProfile: userv1.BaseReadonlyProfile},
		Status:     userv1.InternalUserStatus{Phase: userv1.InternalUserActive},
	}
	leaseClient := fake.NewClientBuilder().WithScheme(testScheme()).WithStatusSubresource(&userv1.CredentialLease{}).WithObjects(user).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			lease, ok := obj.(*userv1.CredentialLease)
			if !ok {
				return c.Create(ctx, obj, opts...)
			}
			lease.Name = "lease-audit-failure"
			lease.GenerateName = ""
			lease.UID = "lease-uid"
			lease.Status.Phase = userv1.CredentialLeasePrepared
			lease.Status.ServiceAccountRef = &corev1.ObjectReference{APIVersion: "v1", Kind: "ServiceAccount", Namespace: userv1.InternalUserSystemNamespace, Name: user.Name}
			lease.Status.BoundSecretRef = &corev1.ObjectReference{APIVersion: "v1", Kind: "Secret", Namespace: userv1.InternalUserSystemNamespace, Name: "secret-audit-failure", UID: "secret-uid"}
			return c.Create(ctx, obj, opts...)
		},
	}).Build()
	authenticator := staticAuthenticator{principal: Principal{Issuer: identity.Issuer, Subject: identity.Subject}}
	tokenIssuer := recordingTokenIssuer{result: TokenResult{Token: "token-must-not-be-delivered", Expiration: now.Add(20 * time.Minute), Audiences: []string{"kubernetes.default.svc"}}}
	server := newTestServer(t, leaseClient, authenticator, &tokenIssuer, now)
	server.Audit = &recordingAuditSink{err: errors.New("audit backend unavailable")}
	request := httptest.NewRequest(http.MethodPost, "/v1/credentials/base", bytes.NewBufferString(`{"requestedTTLSeconds":1800}`))
	request.RemoteAddr = "127.0.0.1:443"
	request.Header.Set("X-Trusted-Client-IP", "10.1.2.3")
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(response.Body.String(), "token-must-not-be-delivered") {
		t.Fatal("credential was delivered after audit failure")
	}
	if !tokenIssuer.called {
		t.Fatal("expected issuance flow to reach TokenRequest before final audit")
	}
}

func TestConsumedLeaseCannotBeRedeemedAfterTokenRequestFailure(t *testing.T) {
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	identity := userv1.Identity{Issuer: "https://issuer.example", Subject: "alice"}
	user := &userv1.InternalUser{
		ObjectMeta: metav1.ObjectMeta{Name: internalcredentials.DeriveInternalUserName(identity.Issuer, identity.Subject)},
		Spec:       userv1.InternalUserSpec{Identity: identity, RoleProfile: userv1.BaseReadonlyProfile},
		Status:     userv1.InternalUserStatus{Phase: userv1.InternalUserActive},
	}
	leaseClient := fake.NewClientBuilder().WithScheme(testScheme()).WithStatusSubresource(&userv1.CredentialLease{}).WithObjects(user).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			lease, ok := obj.(*userv1.CredentialLease)
			if !ok {
				return c.Create(ctx, obj, opts...)
			}
			lease.Name = "lease-token-response-lost"
			lease.GenerateName = ""
			lease.UID = "lease-uid"
			lease.Status.Phase = userv1.CredentialLeasePrepared
			lease.Status.ServiceAccountRef = &corev1.ObjectReference{APIVersion: "v1", Kind: "ServiceAccount", Namespace: userv1.InternalUserSystemNamespace, Name: user.Name}
			lease.Status.BoundSecretRef = &corev1.ObjectReference{APIVersion: "v1", Kind: "Secret", Namespace: userv1.InternalUserSystemNamespace, Name: "secret-token-response-lost", UID: "secret-uid"}
			return c.Create(ctx, obj, opts...)
		},
	}).Build()
	tokenIssuer := recordingTokenIssuer{err: errors.New("TokenRequest response unavailable")}
	server := newTestServer(t, leaseClient, staticAuthenticator{principal: Principal{Issuer: identity.Issuer, Subject: identity.Subject}}, &tokenIssuer, now)
	_, err := server.issue(context.Background(), identity, identity, userv1.BaseReadonlyProfile, 30*60, "")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Code != http.StatusServiceUnavailable {
		t.Fatalf("issue() error = %v, want service unavailable", err)
	}
	if tokenIssuer.calls != 1 {
		t.Fatalf("TokenRequest calls = %d, want 1", tokenIssuer.calls)
	}
	lease := &userv1.CredentialLease{}
	if err := leaseClient.Get(context.Background(), types.NamespacedName{Namespace: userv1.CredentialBrokerNamespace, Name: "lease-token-response-lost"}, lease); err != nil {
		t.Fatal(err)
	}
	if lease.Status.ConsumedAt == nil || lease.Status.Phase != userv1.CredentialLeasePrepared {
		t.Fatalf("consumed lease status = %#v, want consumed Prepared", lease.Status)
	}
	if _, err := server.markConsumed(context.Background(), lease); err == nil {
		t.Fatal("consumed lease was redeemable after TokenRequest failure")
	}
}

func TestAdminEndpointRequiresGroupAndTrustedProxy(t *testing.T) {
	t.Skip("administrator OIDC issuance endpoint was replaced by the mTLS approval endpoint")
	client := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	authenticator := staticAuthenticator{principal: Principal{Issuer: "https://issuer.example", Subject: "admin"}}
	tokenIssuer := recordingTokenIssuer{}
	server := newTestServer(t, client, authenticator, &tokenIssuer, time.Now())
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/credentials", strings.NewReader(`{}`))
	request.RemoteAddr = "127.0.0.1:443"
	request.Header.Set("X-Trusted-Client-IP", "10.1.2.3")
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	if tokenIssuer.called {
		t.Fatal("non-admin request reached TokenRequest")
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/admin/credentials", strings.NewReader(`{}`))
	request.RemoteAddr = "10.99.0.1:443"
	request.Header.Set("X-Trusted-Client-IP", "10.1.2.3")
	request.Header.Set("X-Forwarded-For", "10.1.2.3")
	request.Header.Set("Authorization", "Bearer valid")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("direct proxy-bypass status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestBrokerRejectsInvalidMethodsBodiesAndOversizedRequests(t *testing.T) {
	identity := userv1.Identity{Issuer: "https://issuer.example", Subject: "alice"}
	server := newTestServer(t, fake.NewClientBuilder().WithScheme(testScheme()).Build(), staticAuthenticator{principal: Principal{Issuer: identity.Issuer, Subject: identity.Subject}}, &recordingTokenIssuer{}, time.Now())
	tests := []struct {
		name       string
		method     string
		body       string
		maxBody    int64
		wantStatus int
	}{
		{name: "invalid method", method: http.MethodGet, body: "", wantStatus: http.StatusMethodNotAllowed},
		{name: "malformed body", method: http.MethodPost, body: `{`, wantStatus: http.StatusBadRequest},
		{name: "oversized body", method: http.MethodPost, body: `{"requestedTTLSeconds":1800,"padding":"0123456789"}`, maxBody: 8, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server.Config.MaxBodyBytes = 16 * 1024
			if tt.maxBody != 0 {
				server.Config.MaxBodyBytes = tt.maxBody
			}
			request := httptest.NewRequest(tt.method, "/v1/credentials/base", strings.NewReader(tt.body))
			request.RemoteAddr = "127.0.0.1:443"
			request.Header.Set("X-Trusted-Client-IP", "10.1.2.3")
			request.Header.Set("Authorization", "Bearer valid")
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, body = %s, want %d", response.Code, response.Body.String(), tt.wantStatus)
			}
		})
	}
}

func TestBrokerRateLimitsRequestsAndLeavesHealthChecksAvailable(t *testing.T) {
	identity := userv1.Identity{Issuer: "https://issuer.example", Subject: "alice"}
	tokenIssuer := recordingTokenIssuer{}
	server := newTestServer(t, fake.NewClientBuilder().WithScheme(testScheme()).Build(), staticAuthenticator{principal: Principal{Issuer: identity.Issuer, Subject: identity.Subject}}, &tokenIssuer, time.Now())
	server.requestLimiter = rate.NewLimiter(rate.Limit(1), 1)

	request := func(path string) *httptest.ResponseRecorder {
		httpRequest := httptest.NewRequest(http.MethodGet, path, nil)
		httpRequest.RemoteAddr = "127.0.0.1:443"
		httpRequest.Header.Set("X-Trusted-Client-IP", "10.1.2.3")
		httpRequest.Header.Set("Authorization", "Bearer valid")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httpRequest)
		return response
	}

	first := request("/v1/credentials/base")
	if first.Code != http.StatusMethodNotAllowed {
		t.Fatalf("first request status = %d, want %d", first.Code, http.StatusMethodNotAllowed)
	}
	second := request("/v1/credentials/base")
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want %d", second.Code, http.StatusTooManyRequests)
	}
	if second.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q, want 1", second.Header().Get("Retry-After"))
	}
	if tokenIssuer.called {
		t.Fatal("rate-limited request reached TokenRequest")
	}
	for i := 0; i < 2; i++ {
		if response := request("/healthz"); response.Code != http.StatusOK {
			t.Fatalf("health check status = %d, want %d", response.Code, http.StatusOK)
		}
	}
}

func TestInternalApprovePersistsAwaitingLeaseAndIsIdempotent(t *testing.T) {
	now := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	identity := userv1.Identity{Issuer: "https://issuer.example", Subject: "alice"}
	user := &userv1.InternalUser{
		ObjectMeta: metav1.ObjectMeta{Name: internalcredentials.DeriveInternalUserName(identity.Issuer, identity.Subject)},
		Spec:       userv1.InternalUserSpec{Identity: identity, RoleProfile: userv1.BaseReadonlyProfile},
		Status:     userv1.InternalUserStatus{Phase: userv1.InternalUserActive},
	}
	server := newTestServer(t, fake.NewClientBuilder().WithScheme(testScheme()).WithStatusSubresource(&userv1.CredentialLease{}).WithObjects(user).Build(), staticAuthenticator{principal: Principal{Issuer: identity.Issuer, Subject: identity.Subject}}, &recordingTokenIssuer{}, now)
	clientCAPool, clientCertificate := testClientIdentity(t)
	server.Config.InternalClientCA = clientCAPool
	body := `{"approvalID":"approval-instance-1","target":{"issuer":"https://issuer.example","subject":"alice"},"requestedTTLSeconds":1800,"approvalReason":"deploy incident remediation"}`
	serve := func(value string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/internal/credentials/approve", strings.NewReader(value))
		req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{clientCertificate}}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, req)
		return response
	}
	first := serve(body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first approval status = %d, body = %s", first.Code, first.Body.String())
	}
	var approval approvalResponse
	if err := json.Unmarshal(first.Body.Bytes(), &approval); err != nil {
		t.Fatal(err)
	}
	if approval.ApprovalID != "approval-instance-1" || approval.Profile != userv1.ClusterOpsWriteProfile || approval.RequestedTTLSeconds != 1800 || approval.ApprovalReference == "" {
		t.Fatalf("approval response = %#v", approval)
	}
	lease := &userv1.CredentialLease{}
	if err := server.LeaseClient.Get(context.Background(), types.NamespacedName{Namespace: userv1.CredentialBrokerNamespace, Name: approvalLeaseName("approval-instance-1")}, lease); err != nil {
		t.Fatal(err)
	}
	if lease.Status.Phase != userv1.CredentialLeaseAwaitingRedemption || lease.Status.ApprovalExpirationTimestamp == nil || len(lease.Status.Conditions) != 0 {
		t.Fatalf("approval lease status = %#v", lease.Status)
	}
	serviceAccounts := &corev1.ServiceAccountList{}
	if err := server.LeaseClient.List(context.Background(), serviceAccounts, client.InNamespace(userv1.InternalUserSystemNamespace)); err != nil {
		t.Fatal(err)
	}
	if len(serviceAccounts.Items) != 0 {
		t.Fatalf("approval created %d ServiceAccounts before redemption", len(serviceAccounts.Items))
	}
	second := serve(body)
	if second.Code != http.StatusOK {
		t.Fatalf("idempotent approval status = %d, body = %s", second.Code, second.Body.String())
	}
	var retry approvalResponse
	if err := json.Unmarshal(second.Body.Bytes(), &retry); err != nil {
		t.Fatal(err)
	}
	if retry.ApprovalReference != approval.ApprovalReference || retry.ApprovalID != approval.ApprovalID {
		t.Fatalf("idempotent response = %#v, first = %#v", retry, approval)
	}
	conflict := serve(strings.Replace(body, "deploy incident remediation", "different target operation", 1))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("approval ID mismatch status = %d, want %d", conflict.Code, http.StatusConflict)
	}
}

func TestInternalApproveRequiresDedicatedMTLS(t *testing.T) {
	identity := userv1.Identity{Issuer: "https://issuer.example", Subject: "alice"}
	user := &userv1.InternalUser{ObjectMeta: metav1.ObjectMeta{Name: internalcredentials.DeriveInternalUserName(identity.Issuer, identity.Subject)}, Spec: userv1.InternalUserSpec{Identity: identity}, Status: userv1.InternalUserStatus{Phase: userv1.InternalUserActive}}
	server := newTestServer(t, fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(user).Build(), staticAuthenticator{principal: Principal{Issuer: identity.Issuer, Subject: identity.Subject}}, &recordingTokenIssuer{}, time.Now())
	request := httptest.NewRequest(http.MethodPost, "/v1/internal/credentials/approve", strings.NewReader(`{"approvalID":"id","target":{"issuer":"https://issuer.example","subject":"alice"},"requestedTTLSeconds":1800,"approvalReason":"reason"}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unconfigured mTLS status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestElevatedRedeemUsesPersistedApprovalAndConsumesReferenceOnce(t *testing.T) {
	now := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	identity := userv1.Identity{Issuer: "https://issuer.example", Subject: "alice"}
	user := &userv1.InternalUser{ObjectMeta: metav1.ObjectMeta{Name: internalcredentials.DeriveInternalUserName(identity.Issuer, identity.Subject)}, Spec: userv1.InternalUserSpec{Identity: identity, RoleProfile: userv1.BaseReadonlyProfile}, Status: userv1.InternalUserStatus{Phase: userv1.InternalUserActive}}
	leaseName := approvalLeaseName("approval-instance-2")
	reference, err := newApprovalReference(leaseName)
	if err != nil {
		t.Fatal(err)
	}
	expires := metav1.NewTime(now.Add(time.Hour))
	lease := &userv1.CredentialLease{ObjectMeta: metav1.ObjectMeta{Name: leaseName, Namespace: userv1.CredentialBrokerNamespace}, Spec: userv1.CredentialLeaseSpec{Target: identity, Requester: identity, Profile: userv1.ClusterOpsWriteProfile, RequestedTTLSeconds: 1800, ApprovalReference: reference, ApprovalID: "approval-instance-2", ApprovalReason: "incident"}, Status: userv1.CredentialLeaseStatus{Phase: userv1.CredentialLeaseAwaitingRedemption, ApprovalExpirationTimestamp: &expires}}
	client := fake.NewClientBuilder().WithScheme(testScheme()).WithStatusSubresource(&userv1.CredentialLease{}).WithObjects(user, lease).WithInterceptorFuncs(interceptor.Funcs{SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
		if subResourceName == "status" {
			if candidate, ok := obj.(*userv1.CredentialLease); ok && candidate.Status.Phase == userv1.CredentialLeasePending {
				candidate.Status.Phase = userv1.CredentialLeasePrepared
				candidate.Status.ServiceAccountRef = &corev1.ObjectReference{APIVersion: "v1", Kind: "ServiceAccount", Namespace: userv1.InternalUserSystemNamespace, Name: internalcredentials.ResourceName("internal-lease", userv1.CredentialBrokerNamespace, leaseName)}
				candidate.Status.BoundSecretRef = &corev1.ObjectReference{APIVersion: "v1", Kind: "Secret", Namespace: userv1.InternalUserSystemNamespace, Name: "secret-elevated", UID: "secret-uid"}
				candidate.Status.BindingRef = &corev1.ObjectReference{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding", Name: internalcredentials.ResourceName("internal-lease-binding", userv1.CredentialBrokerNamespace, leaseName)}
			}
		}
		return c.Status().Update(ctx, obj, opts...)
	}}).Build()
	tokenIssuer := &recordingTokenIssuer{result: TokenResult{Token: "elevated-token", Expiration: now.Add(30 * time.Minute), Audiences: []string{"kubernetes.default.svc"}}}
	server := newTestServer(t, client, staticAuthenticator{principal: Principal{Issuer: identity.Issuer, Subject: identity.Subject}}, tokenIssuer, now)
	serve := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/credentials/elevated/redeem", strings.NewReader(fmt.Sprintf(`{"approvalReference":%q}`, reference)))
		req.RemoteAddr = "127.0.0.1:443"
		req.Header.Set("X-Trusted-Client-IP", "10.1.2.3")
		req.Header.Set("Authorization", "Bearer valid")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, req)
		return response
	}
	first := serve()
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "elevated-token") {
		t.Fatalf("redeem status = %d, body = %s", first.Code, first.Body.String())
	}
	if tokenIssuer.calls != 1 || tokenIssuer.spec.ExpirationSeconds == nil || *tokenIssuer.spec.ExpirationSeconds != 1800 {
		t.Fatalf("TokenRequest = %#v, calls = %d", tokenIssuer.spec, tokenIssuer.calls)
	}
	replay := serve()
	if replay.Code != http.StatusConflict || tokenIssuer.calls != 1 {
		t.Fatalf("replay status = %d, calls = %d, body = %s", replay.Code, tokenIssuer.calls, replay.Body.String())
	}
}

func TestSourceIPPolicyIgnoresForwardingHeaders(t *testing.T) {
	trusted, err := ParseCIDRs([]string{"127.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := ParseCIDRs([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	policy := SourceIPPolicy{TrustedProxyCIDRs: trusted, AllowedClientCIDRs: allowed, TrustedClientIPHeader: "X-Trusted-Client-IP"}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "127.0.0.1:443"
	request.Header.Set("X-Trusted-Client-IP", "10.1.2.3")
	request.Header.Set("X-Forwarded-For", "192.0.2.1")
	address, err := policy.Authorize(request)
	if err != nil || !address.Equal([]byte{10, 1, 2, 3}) {
		t.Fatalf("Authorize() = %v, %v", address, err)
	}
}

func TestSourceIPPolicyRejectsInvalidTrustedClientIPHeader(t *testing.T) {
	trusted, err := ParseCIDRs([]string{"127.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := ParseCIDRs([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	policy := SourceIPPolicy{TrustedProxyCIDRs: trusted, AllowedClientCIDRs: allowed, TrustedClientIPHeader: "X-Trusted-Client-IP"}

	tests := []struct {
		name   string
		values []string
	}{
		{name: "missing"},
		{name: "malformed", values: []string{"not-an-ip"}},
		{name: "leading whitespace", values: []string{" 10.1.2.3"}},
		{name: "trailing whitespace", values: []string{"10.1.2.3 "}},
		{name: "ambiguous", values: []string{"10.1.2.3", "10.1.2.4"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = "127.0.0.1:443"
			for _, value := range test.values {
				request.Header.Add("X-Trusted-Client-IP", value)
			}
			if address, err := policy.Authorize(request); err == nil {
				t.Fatalf("Authorize() accepted invalid header with address %v", address)
			}
		})
	}
}

type staticAuthenticator struct {
	principal Principal
}

func (a staticAuthenticator) Authenticate(context.Context, string) (Principal, error) {
	return a.principal, nil
}

type recordingTokenIssuer struct {
	result TokenResult
	err    error
	spec   authenticationv1.TokenRequestSpec
	called bool
	calls  int
}

type recordingAuditSink struct {
	events []AuditEvent
	err    error
}

func (s *recordingAuditSink) Record(_ context.Context, event AuditEvent) error {
	s.events = append(s.events, event)
	return s.err
}

func (i *recordingTokenIssuer) Issue(_ context.Context, _, _ string, spec authenticationv1.TokenRequestSpec) (TokenResult, error) {
	i.called = true
	i.calls++
	i.spec = spec
	return i.result, i.err
}

func newTestServer(t *testing.T, leaseClient client.Client, authenticator Authenticator, tokenIssuer TokenIssuer, now time.Time) *Server {
	t.Helper()
	trusted, err := ParseCIDRs([]string{"127.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := ParseCIDRs([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(leaseClient, authenticator, tokenIssuer, Config{
		AllowedIssuers:     map[string]struct{}{"https://issuer.example": {}},
		AdminGroup:         "sealos.internal.admin",
		KubernetesAudience: "kubernetes.default.svc",
		ClusterServer:      "https://kubernetes.example",
		SourceIP:           SourceIPPolicy{TrustedProxyCIDRs: trusted, AllowedClientCIDRs: allowed, TrustedClientIPHeader: "X-Trusted-Client-IP"},
		Clock:              func() time.Time { return now },
		PrepareTimeout:     time.Second,
		PollInterval:       time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func testScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = userv1.AddToScheme(scheme)
	return scheme
}

func testClientIdentity(t *testing.T) (*x509.CertPool, *x509.Certificate) {
	t.Helper()
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	expires := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test internal approval CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              expires,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	// The test only needs a certificate chain and not a production-grade key
	// profile, so use a small RSA key generated by the standard library.
	caRSAKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caDER, err := x509.CreateCertificate(cryptorand.Reader, caTemplate, caTemplate, &caRSAKey.PublicKey, caRSAKey)
	if err != nil {
		t.Fatal(err)
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test internal approval adapter"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     expires,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(cryptorand.Reader, clientTemplate, caCertificate, &clientKey.PublicKey, caRSAKey)
	if err != nil {
		t.Fatal(err)
	}
	clientCertificate, err := x509.ParseCertificate(clientDER)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCertificate)
	return pool, clientCertificate
}
