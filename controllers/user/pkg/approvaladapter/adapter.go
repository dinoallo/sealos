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

// Package approvaladapter translates approved Feishu events into Broker
// approval records. It deliberately has no event-consumption store: retries
// are safe because the Broker uses the Feishu instance code as an idempotency
// key and returns the same reference.
package approvaladapter

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	userv1 "github.com/labring/sealos/controllers/user/api/v1"
	"github.com/labring/sealos/controllers/user/pkg/broker"
	"github.com/labring/sealos/controllers/user/pkg/internalcredentials"
)

const (
	DefaultEventType       = "approval_instance"
	DefaultSubjectField    = "subject"
	DefaultTTLField        = "requested_ttl_seconds"
	DefaultReasonField     = "reason"
	DefaultMaxBodyBytes    = int64(64 * 1024)
	DefaultSignatureWindow = 5 * time.Minute
)

// ApprovalInstance is the authoritative data fetched from Feishu after an
// approved event. Form is the JSON-encoded Feishu form value.
type ApprovalInstance struct {
	InstanceCode string
	ApprovalCode string
	Status       string
	InitiatorID  string
	Form         string
}

// ApprovalSource fetches authoritative approval data. Implementations must
// not return data from the callback event when the Feishu API can be queried.
type ApprovalSource interface {
	GetInstance(ctx context.Context, instanceCode string) (ApprovalInstance, error)
	GetUserEmail(ctx context.Context, userID string) (string, error)
}

// Approver submits a validated external approval to the Broker.
type Approver interface {
	Approve(ctx context.Context, record broker.ApprovalRecord) (broker.ApprovalResponse, error)
}

// Notification is non-Kubernetes delivery metadata. It contains the one-time
// approval reference because the target user needs it for redemption, but it
// never contains a token or kubeconfig.
type Notification struct {
	Recipient         string
	ApprovalID        string
	ApprovalReference string
	Profile           string
	ExpiresAt         time.Time
}

// Notifier delivers a Broker-generated reference to the approved target.
type Notifier interface {
	Notify(ctx context.Context, notification Notification) error
}

// Config contains adapter-owned validation policy. The target issuer and
// Feishu form field names are fixed by deployment configuration, not by the
// callback payload.
type Config struct {
	ApprovalCode      string
	TargetIssuer      string
	EventType         string
	VerificationToken string
	EncryptKey        string
	SubjectField      string
	TTLField          string
	ReasonField       string
	MaxBodyBytes      int64
	SignatureWindow   time.Duration
	Clock             func() time.Time
}

type Adapter struct {
	Source   ApprovalSource
	Approver Approver
	Notifier Notifier
	Config   Config
}

type callbackEnvelope struct {
	Challenge string `json:"challenge"`
	Type      string `json:"type"`
	Token     string `json:"token"`
	Header    struct {
		EventType string `json:"event_type"`
		Token     string `json:"token"`
	} `json:"header"`
	Event struct {
		ApprovalCode string `json:"approval_code"`
		InstanceCode string `json:"instance_code"`
		Status       string `json:"status"`
		InitiatorID  string `json:"user_id"`
		OpenID       string `json:"open_id"`
	} `json:"event"`
}

func New(source ApprovalSource, approver Approver, notifier Notifier, config Config) (*Adapter, error) {
	if source == nil || approver == nil || notifier == nil {
		return nil, errors.New("approval adapter requires a source, approver, and notifier")
	}
	if strings.TrimSpace(config.ApprovalCode) == "" || strings.ContainsAny(config.ApprovalCode, "\r\n") {
		return nil, errors.New("Feishu approval code is required")
	}
	if err := internalcredentials.ValidateIssuer(config.TargetIssuer); err != nil {
		return nil, fmt.Errorf("invalid target issuer: %w", err)
	}
	if strings.TrimSpace(config.VerificationToken) == "" {
		return nil, errors.New("Feishu verification token is required")
	}
	if config.EventType == "" {
		config.EventType = DefaultEventType
	}
	if config.SubjectField == "" {
		config.SubjectField = DefaultSubjectField
	}
	if config.TTLField == "" {
		config.TTLField = DefaultTTLField
	}
	if config.ReasonField == "" {
		config.ReasonField = DefaultReasonField
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if config.SignatureWindow <= 0 {
		config.SignatureWindow = DefaultSignatureWindow
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &Adapter{Source: source, Approver: approver, Notifier: notifier, Config: config}, nil
}

// Handler returns the Feishu callback endpoint handler.
func (a *Adapter) Handler() http.Handler {
	return http.HandlerFunc(a.ServeHTTP)
}

func (a *Adapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if r.URL.Path != "/v1/feishu/events" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	body, err := readBody(r, a.Config.MaxBodyBytes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid event body"})
		return
	}
	if err := a.verifyCallback(r, body); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "event authentication failed"})
		return
	}
	var envelope callbackEnvelope
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(&envelope); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid event body"})
		return
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "event body must contain one JSON value"})
		return
	}
	if envelope.Type == "url_verification" || envelope.Challenge != "" {
		if envelope.Challenge == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "challenge is missing"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"challenge": envelope.Challenge})
		return
	}

	if envelope.Header.EventType != a.Config.EventType {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	if !strings.EqualFold(strings.TrimSpace(envelope.Event.Status), "APPROVED") {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	if envelope.Event.InstanceCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "approved event has no instance code"})
		return
	}
	instance, err := a.Source.GetInstance(r.Context(), envelope.Event.InstanceCode)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Feishu approval instance is unavailable"})
		return
	}
	if instance.InstanceCode != envelope.Event.InstanceCode ||
		instance.ApprovalCode != a.Config.ApprovalCode ||
		!strings.EqualFold(strings.TrimSpace(instance.Status), "APPROVED") {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Feishu approval instance is not an approved configured request"})
		return
	}
	initiatorID := strings.TrimSpace(instance.InitiatorID)
	if initiatorID == "" {
		initiatorID = strings.TrimSpace(envelope.Event.InitiatorID)
	}
	if initiatorID == "" {
		initiatorID = strings.TrimSpace(envelope.Event.OpenID)
	}
	if initiatorID == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Feishu approval initiator is missing"})
		return
	}
	subject, err := formValue(instance.Form, a.Config.SubjectField)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "approved request subject is invalid"})
		return
	}
	if err := internalcredentials.ValidateIdentity(a.Config.TargetIssuer, subject); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "approved request subject is invalid"})
		return
	}
	ttlText, err := formValue(instance.Form, a.Config.TTLField)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "approved request TTL is invalid"})
		return
	}
	ttl, err := strconv.ParseInt(strings.TrimSpace(ttlText), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "approved request TTL is invalid"})
		return
	}
	if _, err := internalcredentials.ValidateRequestedTTL(userv1.ClusterOpsWriteProfile, ttl); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "approved request TTL is outside policy"})
		return
	}
	reason, err := formValue(instance.Form, a.Config.ReasonField)
	if err != nil || strings.TrimSpace(reason) == "" || len(reason) > 2048 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "approved request reason is invalid"})
		return
	}
	recipient, err := a.Source.GetUserEmail(r.Context(), initiatorID)
	if err != nil || !validEmail(recipient) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "approved request recipient is unavailable"})
		return
	}
	response, err := a.Approver.Approve(r.Context(), broker.ApprovalRecord{
		ApprovalID:          envelope.Event.InstanceCode,
		Target:              userv1.Identity{Issuer: a.Config.TargetIssuer, Subject: subject},
		RequestedTTLSeconds: ttl,
		ApprovalReason:      reason,
	})
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Broker approval submission failed"})
		return
	}
	if response.ApprovalID != envelope.Event.InstanceCode || response.Profile != userv1.ClusterOpsWriteProfile ||
		response.RequestedTTLSeconds != ttl || !validReference(response.ApprovalReference) || response.ApprovalExpiresAt.IsZero() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Broker returned an incomplete approval response"})
		return
	}
	if err := a.Notifier.Notify(r.Context(), Notification{
		Recipient:         recipient,
		ApprovalID:        response.ApprovalID,
		ApprovalReference: response.ApprovalReference,
		Profile:           response.Profile,
		ExpiresAt:         response.ApprovalExpiresAt,
	}); err != nil {
		// Returning a retryable response is safe: the Broker is idempotent by
		// ApprovalID, so the next event delivery obtains the same reference.
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "approval reference notification failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "processed"})
}

func (a *Adapter) verifyCallback(r *http.Request, body []byte) error {
	token := r.Header.Get("X-Lark-Token")
	if token == "" {
		token = r.Header.Get("X-Feishu-Token")
	}
	var envelope callbackEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return err
	}
	if token == "" {
		token = envelope.Header.Token
	}
	if token == "" {
		token = envelope.Token
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.Config.VerificationToken)) != 1 {
		return errors.New("verification token mismatch")
	}
	if a.Config.EncryptKey == "" {
		return nil
	}
	timestamp := r.Header.Get("X-Lark-Request-Timestamp")
	nonce := r.Header.Get("X-Lark-Request-Nonce")
	signature := r.Header.Get("X-Lark-Signature")
	if timestamp == "" || nonce == "" || signature == "" {
		return errors.New("signed callback headers are required")
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || absDuration(a.Config.Clock().Sub(time.Unix(seconds, 0))) > a.Config.SignatureWindow {
		return errors.New("callback timestamp is outside the acceptance window")
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(timestamp))
	_, _ = hash.Write([]byte(nonce))
	_, _ = hash.Write([]byte(a.Config.EncryptKey))
	_, _ = hash.Write(body)
	expected := hex.EncodeToString(hash.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(signature)), []byte(expected)) != 1 {
		return errors.New("callback signature mismatch")
	}
	return nil
}

func readBody(r *http.Request, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil || int64(len(body)) > maxBytes {
		return nil, errors.New("event body is too large or unreadable")
	}
	return body, nil
}

func formValue(form, field string) (string, error) {
	if strings.TrimSpace(form) == "" || strings.TrimSpace(field) == "" {
		return "", errors.New("form field is missing")
	}
	var fields []struct {
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal([]byte(form), &fields); err != nil {
		return "", errors.New("Feishu form is invalid")
	}
	var result string
	matched := false
	for _, candidate := range fields {
		if candidate.ID != field && candidate.Name != field {
			continue
		}
		if matched {
			return "", errors.New("form field is ambiguous")
		}
		value, err := scalarValue(candidate.Value)
		if err != nil || strings.TrimSpace(value) == "" {
			return "", errors.New("form field value is invalid")
		}
		result = strings.TrimSpace(value)
		matched = true
	}
	if !matched {
		return "", errors.New("form field is missing")
	}
	return result, nil
}

func scalarValue(value json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text, nil
	}
	var number json.Number
	decoder := json.NewDecoder(strings.NewReader(string(value)))
	decoder.UseNumber()
	if decoder.Decode(&number) == nil {
		return number.String(), nil
	}
	var values []json.RawMessage
	if json.Unmarshal(value, &values) == nil && len(values) > 0 {
		return scalarValue(values[0])
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(value, &object) == nil {
		for _, key := range []string{"value", "text", "name", "id", "email"} {
			if candidate, ok := object[key]; ok {
				return scalarValue(candidate)
			}
		}
	}
	return "", errors.New("form value is not scalar")
}

func validReference(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsSpace) < 0 && len(value) <= 512
}

func validEmail(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n") || len(value) > 320 {
		return false
	}
	parts := strings.Split(value, "@")
	return len(parts) == 2 && parts[0] != "" && parts[1] != "" && !strings.ContainsAny(parts[0], " <>(),:;[]\\\"") && !strings.ContainsAny(parts[1], " <>(),:;[]\\\"")
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
