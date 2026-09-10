package approvaladapter

import (
	"context"
	"net/smtp"
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

func TestValidReference(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"valid reference", "r1.approval-test.reference", true},
		{"empty", "", false},
		{"has whitespace", "r1 ref", false},
		{"too long", strings.Repeat("a", 513), false},
		{"leading space", " ref", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validReference(tt.value); got != tt.want {
				t.Errorf("validReference(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestValidEmail(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"valid email", "alice@example.internal", true},
		{"empty", "", false},
		{"no domain", "alice", false},
		{"leading space", " alice@example.internal", false},
		{"contains newline", "alice@example\n.internal", false},
		{"too long", strings.Repeat("a", 305) + "@example.internal", false},
		{"contains angle bracket", "alice@example<internal", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validEmail(tt.value); got != tt.want {
				t.Errorf("validEmail(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
