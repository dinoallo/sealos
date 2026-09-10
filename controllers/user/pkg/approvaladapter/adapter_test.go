package approvaladapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strconv"
	"strings"
	"testing"
	"time"

	userv1 "github.com/labring/sealos/controllers/user/api/v1"
	"github.com/labring/sealos/controllers/user/pkg/broker"
)

type fakeSource struct {
	instance     ApprovalInstance
	email        string
	instanceCall int
	emailCall    int
	err          error
}

func (s *fakeSource) GetInstance(context.Context, string) (ApprovalInstance, error) {
	s.instanceCall++
	if s.err != nil {
		return ApprovalInstance{}, s.err
	}
	return s.instance, nil
}

func (s *fakeSource) GetUserEmail(context.Context, string) (string, error) {
	s.emailCall++
	if s.err != nil {
		return "", s.err
	}
	return s.email, nil
}

type fakeApprover struct {
	records []broker.ApprovalRecord
	result  broker.ApprovalResponse
	err     error
}

func (a *fakeApprover) Approve(_ context.Context, record broker.ApprovalRecord) (broker.ApprovalResponse, error) {
	a.records = append(a.records, record)
	if a.err != nil {
		return broker.ApprovalResponse{}, a.err
	}
	return a.result, nil
}

type fakeNotifier struct {
	notifications []Notification
	err           error
}

func (n *fakeNotifier) Notify(_ context.Context, notification Notification) error {
	n.notifications = append(n.notifications, notification)
	return n.err
}

func newTestAdapter(t *testing.T) (*Adapter, *fakeSource, *fakeApprover, *fakeNotifier) {
	t.Helper()
	source := &fakeSource{instance: ApprovalInstance{
		InstanceCode: "instance-1",
		ApprovalCode: "approval-definition-1",
		Status:       "APPROVED",
		InitiatorID:  "ou_alice",
		Form: `[{
  "id":"subject-field", "name":"OIDC subject", "value":"alice"
}, {
  "id":"ttl-field", "name":"TTL", "value":"1800"
}, {
  "id":"reason-field", "name":"Reason", "value":"incident remediation"
}]`,
	}, email: "alice@example.internal"}
	approver := &fakeApprover{result: broker.ApprovalResponse{
		ApprovalID: "instance-1", ApprovalReference: "r1.approval-test.reference",
		Profile: userv1.ClusterOpsWriteProfile, RequestedTTLSeconds: 1800,
		ApprovalExpiresAt: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC),
	}}
	notifier := &fakeNotifier{}
	adapter, err := New(source, approver, notifier, Config{
		ApprovalCode: "approval-definition-1", TargetIssuer: "https://issuer.example",
		VerificationToken: "verification-token", SubjectField: "subject-field",
		TTLField: "ttl-field", ReasonField: "reason-field",
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter, source, approver, notifier
}

func approvalEventBody(status string) string {
	return `{"header":{"event_type":"approval_instance","token":"verification-token"},"event":{"instance_code":"instance-1","status":"` + status + `"}}`
}

func serveEvent(adapter *Adapter, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/v1/feishu/events", strings.NewReader(body))
	response := httptest.NewRecorder()
	adapter.Handler().ServeHTTP(response, request)
	return response
}

func TestApprovedEventFetchesAuthoritativeDetailsSubmitsAndNotifies(t *testing.T) {
	adapter, source, approver, notifier := newTestAdapter(t)
	response := serveEvent(adapter, approvalEventBody("APPROVED"))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"processed"`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if source.instanceCall != 1 || source.emailCall != 1 || len(approver.records) != 1 || len(notifier.notifications) != 1 {
		t.Fatalf("calls = source instance %d, email %d, broker %d, notifier %d", source.instanceCall, source.emailCall, len(approver.records), len(notifier.notifications))
	}
	record := approver.records[0]
	if record.ApprovalID != "instance-1" || record.Target != (userv1.Identity{Issuer: "https://issuer.example", Subject: "alice"}) || record.RequestedTTLSeconds != 1800 || record.ApprovalReason != "incident remediation" {
		t.Fatalf("Broker record = %#v", record)
	}
	if notifier.notifications[0].ApprovalReference != approver.result.ApprovalReference || notifier.notifications[0].Recipient != "alice@example.internal" {
		t.Fatalf("notification = %#v", notifier.notifications[0])
	}

	// A retry does not consult an adapter-side event store. The Broker's
	// ApprovalID idempotency is what makes this safe and returns the same ref.
	response = serveEvent(adapter, approvalEventBody("APPROVED"))
	if response.Code != http.StatusOK || len(approver.records) != 2 || len(notifier.notifications) != 2 {
		t.Fatalf("retry status = %d, calls = broker %d, notifier %d", response.Code, len(approver.records), len(notifier.notifications))
	}
}

func TestAdapterDoesNotTrustEventFieldsOrProcessUnapprovedEvents(t *testing.T) {
	adapter, source, approver, notifier := newTestAdapter(t)
	ignored := serveEvent(adapter, approvalEventBody("PENDING"))
	if ignored.Code != http.StatusOK || source.instanceCall != 0 || len(approver.records) != 0 || len(notifier.notifications) != 0 {
		t.Fatalf("pending event was processed: status=%d source=%d broker=%d notifier=%d", ignored.Code, source.instanceCall, len(approver.records), len(notifier.notifications))
	}
	body := `{"header":{"event_type":"approval_instance","token":"verification-token"},"event":{"instance_code":"instance-1","status":"APPROVED","approval_code":"attacker-definition","user_id":"attacker"}}`
	response := serveEvent(adapter, body)
	if response.Code != http.StatusOK || len(approver.records) != 1 || approver.records[0].Target.Subject != "alice" {
		t.Fatalf("authoritative instance was not used: status=%d records=%#v", response.Code, approver.records)
	}
}

func TestAdapterRejectsUnauthenticatedAndMalformedApprovedEvents(t *testing.T) {
	adapter, source, approver, _ := newTestAdapter(t)
	unauthenticated := serveEvent(adapter, strings.Replace(approvalEventBody("APPROVED"), "verification-token", "wrong", 1))
	if unauthenticated.Code != http.StatusUnauthorized || source.instanceCall != 0 {
		t.Fatalf("unauthenticated status = %d, source calls = %d", unauthenticated.Code, source.instanceCall)
	}
	adapter.Source = &fakeSource{instance: ApprovalInstance{
		InstanceCode: "instance-1", ApprovalCode: "approval-definition-1", Status: "APPROVED",
		InitiatorID: "ou_alice", Form: "[]",
	}}
	malformed := serveEvent(adapter, approvalEventBody("APPROVED"))
	if malformed.Code != http.StatusUnprocessableEntity || len(approver.records) != 0 {
		t.Fatalf("malformed approval status = %d, records = %d", malformed.Code, len(approver.records))
	}
}

func TestAdapterVerifiesOptionalFeishuSignature(t *testing.T) {
	adapter, _, _, _ := newTestAdapter(t)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	adapter.Config.EncryptKey = "encrypt-key"
	adapter.Config.Clock = func() time.Time { return now }
	body := []byte(approvalEventBody("PENDING"))
	timestamp := strconv.FormatInt(now.Unix(), 10)
	nonce := "nonce-1"
	digest := sha256.Sum256(append(append(append([]byte(timestamp), []byte(nonce)...), []byte("encrypt-key")...), body...))
	request := httptest.NewRequest(http.MethodPost, "/v1/feishu/events", strings.NewReader(string(body)))
	request.Header.Set("X-Lark-Request-Timestamp", timestamp)
	request.Header.Set("X-Lark-Request-Nonce", nonce)
	request.Header.Set("X-Lark-Signature", hex.EncodeToString(digest[:]))
	response := httptest.NewRecorder()
	adapter.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("signed callback status = %d, body = %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/v1/feishu/events", strings.NewReader(string(body)))
	request.Header.Set("X-Lark-Request-Timestamp", timestamp)
	request.Header.Set("X-Lark-Request-Nonce", nonce)
	request.Header.Set("X-Lark-Signature", "bad")
	response = httptest.NewRecorder()
	adapter.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature status = %d", response.Code)
	}
}

func TestEmailNotifierDoesNotIncludeCredentialMaterial(t *testing.T) {
	var sent []byte
	notifier := &EmailNotifier{
		SMTPHost: "smtp.example", SMTPPort: 587, From: "broker@example.internal",
		Send: func(_ string, _ smtp.Auth, _ string, _ []string, message []byte) error {
			sent = append([]byte(nil), message...)
			return nil
		},
	}
	err := notifier.Notify(context.Background(), Notification{
		Recipient: "alice@example.internal", ApprovalID: "instance-1", ApprovalReference: "r1.reference",
		Profile: userv1.ClusterOpsWriteProfile, ExpiresAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sent), "r1.reference") || strings.Contains(string(sent), "kubeconfig") || strings.Contains(string(sent), "token-value") {
		t.Fatalf("unexpected email body: %s", sent)
	}
}

func TestNewRejectsMissingCallbackAuthentication(t *testing.T) {
	_, err := New(&fakeSource{}, &fakeApprover{}, &fakeNotifier{}, Config{ApprovalCode: "approval", TargetIssuer: "https://issuer.example"})
	if err == nil || !strings.Contains(err.Error(), "verification token") {
		t.Fatalf("New() error = %v", err)
	}
}
