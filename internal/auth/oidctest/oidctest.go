// Package oidctest is an OpenID Connect provider for tests: discovery, keys, an authorization
// endpoint that answers at once for the configured user, and a token endpoint that checks the
// client and the PKCE verifier.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// Identity is who the provider signs in next.
type Identity struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

// Provider is a running fake provider.
type Provider struct {
	*httptest.Server
	ClientID, ClientSecret string

	mu       sync.Mutex
	next     Identity
	key      *rsa.PrivateKey
	pending  map[string]grant
	nonceOff bool
}

type grant struct {
	identity  Identity
	nonce     string
	challenge string
	redirect  string
}

// New starts a provider for the client.
func New(clientID, clientSecret string) *Provider {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	p := &Provider{ClientID: clientID, ClientSecret: clientSecret, key: key, pending: map[string]grant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /jwks", p.jwks)
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	p.Server = httptest.NewServer(mux)
	return p
}

// SignIn sets the identity of the next authorization.
func (p *Provider) SignIn(id Identity) {
	p.mu.Lock()
	p.next = id
	p.mu.Unlock()
}

// WrongNonce makes the next ID tokens carry a nonce other than the one requested.
func (p *Provider) WrongNonce(on bool) {
	p.mu.Lock()
	p.nonceOff = on
	p.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"issuer": p.URL, "authorization_endpoint": p.URL + "/authorize", "token_endpoint": p.URL + "/token",
		"jwks_uri": p.URL + "/jwks", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"}, "code_challenge_methods_supported": []string{"S256"},
	})
}

func (p *Provider) jwks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
}

func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != p.ClientID || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	code := rand.Text()
	p.mu.Lock()
	p.pending[code] = grant{identity: p.next, nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri")}
	p.mu.Unlock()
	back, _ := url.Parse(q.Get("redirect_uri"))
	v := back.Query()
	v.Set("code", code)
	v.Set("state", q.Get("state"))
	back.RawQuery = v.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound) //nolint:gosec // a test provider sends the browser back to the client that asked
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	p.mu.Lock()
	g, found := p.pending[r.PostForm.Get("code")]
	delete(p.pending, r.PostForm.Get("code"))
	wrongNonce := p.nonceOff
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	switch {
	case id != p.ClientID || secret != p.ClientSecret:
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]string{"error": "invalid_client"})
		return
	case !found || r.PostForm.Get("redirect_uri") != g.redirect || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge:
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "invalid_grant"})
		return
	}
	nonce := g.nonce
	if wrongNonce {
		nonce = "not-the-nonce"
	}
	now := time.Now()
	claims, _ := json.Marshal(map[string]any{
		"iss": p.URL, "sub": g.identity.Subject, "aud": p.ClientID, "exp": now.Add(5 * time.Minute).Unix(), "iat": now.Unix(),
		"nonce": nonce, "email": g.identity.Email, "email_verified": g.identity.EmailVerified, "name": g.identity.Name,
	})
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jws, err := signer.Sign(claims)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	raw, _ := jws.CompactSerialize()
	writeJSON(w, map[string]any{"access_token": rand.Text(), "token_type": "Bearer", "expires_in": 300, "id_token": raw})
}
