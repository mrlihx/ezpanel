package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
)

func TestSSOEndpointAndIdentityBinding(t *testing.T) {
	const issuer = "https://id.example.test/tenant"
	for _, bad := range []string{"http://id.example.test/token", "https://other.example.test/token", "https://username@id.example.test/token"} {
		if err := validateOIDCEndpoint(issuer, bad); err == nil {
			t.Errorf("invalid OIDC endpoint accepted: %s", bad)
		}
	}
	if err := validateOIDCEndpoint(issuer, "https://id.example.test/tenant/token"); err != nil {
		t.Fatal(err)
	}
	token := &oidc.IDToken{Subject: "verified-user", Nonce: "browser-nonce", Audience: []string{"panel-client"}}
	settings := ssoConfig{Subject: "verified-user", ClientID: "panel-client"}
	if !acceptsSSOIdentity(settings, "browser-nonce", token, "") {
		t.Fatal("explicitly bound identity was rejected")
	}
	if acceptsSSOIdentity(settings, "other-nonce", token, "") {
		t.Fatal("wrong nonce accepted")
	}
	token.Subject = "different-user"
	if acceptsSSOIdentity(settings, "browser-nonce", token, "") {
		t.Fatal("unbound identity accepted")
	}
	token.Subject = "verified-user"
	token.Audience = append(token.Audience, "other-client")
	if acceptsSSOIdentity(settings, "browser-nonce", token, "") || acceptsSSOIdentity(settings, "browser-nonce", token, "other-client") {
		t.Fatal("multiple-audience token with incorrect authorized party accepted")
	}
}

func TestSSOVerifiesProviderAndPKCE(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	routes := http.NewServeMux()
	server := httptest.NewTLSServer(routes)
	defer server.Close()
	cfg := ssoConfig{Issuer: server.URL, ClientID: "panel-client", ClientSecret: "demo-secret", Subject: "verified-user"}
	flow := ssoFlow{Nonce: "browser-nonce", Verifier: "test-pkce-verifier"}
	routes.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": server.URL, "authorization_endpoint": server.URL + "/authorize",
			"token_endpoint": server.URL + "/token", "jwks_uri": server.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	routes.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		encode := func(n *big.Int) string { return base64.RawURLEncoding.EncodeToString(n.Bytes()) }
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "test-key", "n": encode(key.N), "e": encode(big.NewInt(int64(key.E))),
		}}})
	})
	routes.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("code") != "test-code" || r.Form.Get("code_verifier") != flow.Verifier {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": cfg.Issuer, "aud": cfg.ClientID, "sub": cfg.Subject,
			"nonce": flow.Nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix()})
		token.Header["kid"] = "test-key"
		signed, err := token.SignedString(key)
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "test-access-token", "token_type": "Bearer", "id_token": signed})
	})
	ctx := oidc.ClientContext(t.Context(), server.Client())
	callback := "https://panel.example.test" + ssoCallbackPath
	if err := verifySSO(ctx, cfg, callback, "test-code", flow); err != nil {
		t.Fatal(err)
	}
	if err := verifySSO(ctx, cfg, callback, "test-code", ssoFlow{Nonce: "other", Verifier: flow.Verifier}); err == nil {
		t.Fatal("token issued for another browser nonce was accepted")
	}
}
