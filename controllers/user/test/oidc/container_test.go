package oidc_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/labring/sealos/controllers/user/test/oidc"
	oidccontainer "github.com/labring/sealos/controllers/user/test/oidc/container"
	"github.com/testcontainers/testcontainers-go"
)

func TestOIDCContainerRoundTrip(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	provider, err := oidccontainer.Start(ctx, oidc.Config{
		Users:   []oidc.User{{Subject: "alice", Email: "alice@example.invalid", Groups: []string{"ops"}}},
		Clients: []oidc.Client{{ID: "internal-kc", RedirectURIs: []string{"http://127.0.0.1/callback"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := provider.Terminate(context.Background()); err != nil {
			t.Errorf("terminate OIDC container: %v", err)
		}
	})

	response, err := http.Get(provider.Issuer() + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("discovery status = %d", response.StatusCode)
	}
	var discovery map[string]any
	if err := json.NewDecoder(response.Body).Decode(&discovery); err != nil {
		t.Fatal(err)
	}
	if discovery["issuer"] != provider.Issuer() {
		t.Fatalf("container issuer = %v, want %s", discovery["issuer"], provider.Issuer())
	}

	// The same authorization-code and PKCE exchange used by the CLI now runs
	// through a real mapped container port.
	verifyAuthorizationCodeFlow(t, provider.Issuer())
}

func verifyAuthorizationCodeFlow(t *testing.T, issuer string) {
	t.Helper()
	verifier := "container-verifier-012345678901234567890123456789012"
	challenge := pkceChallenge(verifier)
	callback := "http://127.0.0.1/callback"
	query := "client_id=internal-kc&redirect_uri=" + url.QueryEscape(callback) +
		"&response_type=code&scope=openid&login_hint=alice&code_challenge=" + url.QueryEscape(challenge) +
		"&code_challenge_method=S256"
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Get(issuer + "/authorize?" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("authorize status = %d", response.StatusCode)
	}
	location := response.Header.Get("Location")
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("code") == "" {
		t.Fatalf("authorization redirect = %s", location)
	}
	token := postToken(t, issuer+"/token", url.Values{
		"grant_type":    []string{"authorization_code"},
		"code":          []string{parsed.Query().Get("code")},
		"client_id":     []string{"internal-kc"},
		"redirect_uri":  []string{callback},
		"code_verifier": []string{verifier},
	})
	if token.AccessToken == "" || token.IDToken == "" {
		t.Fatal("container token response did not contain access and ID tokens")
	}
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
}

func pkceChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func postToken(t *testing.T, endpoint string, form url.Values) tokenResponse {
	t.Helper()
	response, err := http.PostForm(endpoint, form)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("POST %s status = %d body = %s", endpoint, response.StatusCode, body)
	}
	var value tokenResponse
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
