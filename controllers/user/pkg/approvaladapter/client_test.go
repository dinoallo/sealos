package approvaladapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	userv1 "github.com/labring/sealos/controllers/user/api/v1"
	"github.com/labring/sealos/controllers/user/pkg/broker"
)

func TestFeishuClientFetchesAndCachesTenantToken(t *testing.T) {
	var tokenCalls, instanceCalls, userCalls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			tokenCalls++
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["app_id"] != "app-id" || body["app_secret"] != "app-secret" {
				t.Fatalf("unexpected token request: %#v", body)
			}
			_, _ = io.WriteString(w, `{"code":0,"msg":"success","tenant_access_token":"tenant-token","expire":7200}`)
		case "/open-apis/approval/v4/instances/instance-1":
			instanceCalls++
			if r.Header.Get("Authorization") != "Bearer tenant-token" || r.URL.Query().Get("user_id_type") != "open_id" {
				t.Fatalf("unexpected instance request: authorization=%q query=%v", r.Header.Get("Authorization"), r.URL.Query())
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"approval_code":"approval-definition-1","instance_code":"instance-1","status":"APPROVED","user_id":"ou_alice","form":"[]"}}`)
		case "/open-apis/contact/v3/users/ou_alice":
			userCalls++
			if r.Header.Get("Authorization") != "Bearer tenant-token" {
				t.Fatalf("unexpected user authorization: %q", r.Header.Get("Authorization"))
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"user":{"email":"alice@example.internal"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewFeishuClient(FeishuClientConfig{BaseURL: server.URL, AppID: "app-id", AppSecret: "app-secret", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := client.GetInstance(context.Background(), "instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if instance.InstanceCode != "instance-1" || instance.ApprovalCode != "approval-definition-1" || instance.InitiatorID != "ou_alice" {
		t.Fatalf("instance = %#v", instance)
	}
	email, err := client.GetUserEmail(context.Background(), "ou_alice")
	if err != nil || email != "alice@example.internal" {
		t.Fatalf("email = %q, err = %v", email, err)
	}
	if _, err := client.GetInstance(context.Background(), "instance-1"); err != nil {
		t.Fatal(err)
	}
	if tokenCalls != 1 || instanceCalls != 2 || userCalls != 1 {
		t.Fatalf("calls = token %d, instance %d, user %d", tokenCalls, instanceCalls, userCalls)
	}
}

func TestBrokerHTTPClientSubmitsOnlyApprovalRecord(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/internal/credentials/approve" || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var record broker.ApprovalRecord
		if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
			t.Fatal(err)
		}
		if record.Target.Subject != "alice" || record.RequestedTTLSeconds != 1800 || record.ApprovalReason != "incident" {
			t.Fatalf("record = %#v", record)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"approvalID":"instance-1","approvalReference":"r1.reference","profile":"cluster-ops-write.v1","requestedTTLSeconds":1800,"approvalExpiresAt":"2026-09-10T00:00:00Z"}`)
	}))
	defer server.Close()
	client, err := NewBrokerHTTPClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Approve(context.Background(), broker.ApprovalRecord{
		ApprovalID: "instance-1", Target: userv1.Identity{Issuer: "https://issuer.example", Subject: "alice"},
		RequestedTTLSeconds: 1800, ApprovalReason: "incident",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.ApprovalReference != "r1.reference" || response.Profile != userv1.ClusterOpsWriteProfile {
		t.Fatalf("response = %#v", response)
	}
}

func TestBrokerHTTPClientRejectsInsecureEndpoint(t *testing.T) {
	if _, err := NewBrokerHTTPClient("http://broker.example", nil); err == nil {
		t.Fatal("insecure Broker endpoint was accepted")
	}
	if _, err := NewBrokerHTTPClient("https://broker.example/path?x=1", nil); err == nil {
		t.Fatal("Broker endpoint with query was accepted")
	}
}
