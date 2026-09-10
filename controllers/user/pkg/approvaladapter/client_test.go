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
