package broker

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

func TestOIDCVerifierValidatesSignatureAndClaims(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	verifier, err := NewOIDCVerifier(OIDCConfig{Issuer: "https://issuer.example", Audience: "broker", JWKSURL: "https://issuer.example/jwks", Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	verifier.cache.mu.Lock()
	verifier.cache.keys = []oidcJWK{{
		KeyType: "RSA", Kid: "key-1", Use: "sig", Alg: "RS256",
		N: base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), E: "AQAB",
	}}
	verifier.cache.expiresAt = now.Add(time.Hour)
	verifier.cache.mu.Unlock()
	signedToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "https://issuer.example", "sub": "alice", "aud": "broker", "exp": now.Add(time.Minute).Unix(), "nbf": now.Add(-time.Minute).Unix(), "groups": []string{"sealos.internal.user"},
	})
	signedToken.Header["kid"] = "key-1"
	token, err := signedToken.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := verifier.Authenticate(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Subject != "alice" || len(principal.Groups) != 1 || principal.Groups[0] != "sealos.internal.user" {
		t.Fatalf("unexpected principal: %#v", principal)
	}

	wrongAudienceToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "https://issuer.example", "sub": "alice", "aud": "other", "exp": now.Add(time.Minute).Unix(),
	})
	wrongAudienceToken.Header["kid"] = "key-1"
	wrongAudience, err := wrongAudienceToken.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Authenticate(t.Context(), wrongAudience); err == nil {
		t.Fatal("wrong OIDC audience was accepted")
	}
}

func TestOIDCVerifierRejectsUnexpectedAlgorithm(t *testing.T) {
	if _, err := NewOIDCVerifier(OIDCConfig{Issuer: "https://issuer.example", Audience: "broker", JWKSURL: "https://issuer.example/jwks", AllowedAlgorithms: []string{"none"}}); err == nil {
		t.Fatal("unexpected OIDC algorithm was accepted in configuration")
	}
}

func TestOIDCVerifierFailsClosedWhenJWKSIsUnavailable(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	verifier, err := NewOIDCVerifier(OIDCConfig{
		Issuer:     "https://issuer.example",
		Audience:   "broker",
		JWKSURL:    "https://issuer.example/jwks",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("OIDC provider unavailable") })},
		Clock:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "https://issuer.example", "sub": "alice", "aud": "broker", "exp": now.Add(time.Minute).Unix(),
	})
	token.Header["kid"] = "key-unavailable"
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Authenticate(t.Context(), signed); err == nil {
		t.Fatal("OIDC authentication succeeded while JWKS was unavailable")
	}
}
