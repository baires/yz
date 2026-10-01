// Package fakes provides in-process stand-ins for the Cloudflare services
// yz talks to, so the E2E suite drives the real binary with no network.
package fakes

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/baires/yz/internal/auth"
)

// TokenRequest is one recorded POST to /oauth2/token.
type TokenRequest struct {
	GrantType    string
	Code         string
	RedirectURI  string
	RefreshToken string
}

type issuedCode struct {
	challenge   string
	redirectURI string
}

// OAuthServer fakes dash.cloudflare.com's /oauth2/auth and /oauth2/token.
// It enforces the same rules the real server does (exact redirect-URI match,
// state length, PKCE S256) and exposes modes and observations for tests.
type OAuthServer struct {
	srv *httptest.Server

	mu               sync.Mutex
	deny             bool
	wrongState       bool
	noCode           bool
	tokenStatus      int // 0 = behave; otherwise force this status
	expiresIn        int
	clientID         string // the only client_id the fake accepts
	codes            map[string]issuedCode
	refresh          map[string]bool // token -> revoked
	authorizeCount   int
	lastRedirectURI  string
	tokenRequests    []TokenRequest
	lastAccessToken  string
	lastRefreshToken string
}

// NewOAuthServer starts the fake and registers its cleanup with t.
func NewOAuthServer(t *testing.T) *OAuthServer {
	t.Helper()
	o := &OAuthServer{
		expiresIn: 3600,
		clientID:  auth.DefaultClientID,
		codes:     map[string]issuedCode{},
		refresh:   map[string]bool{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/auth", o.handleAuth)
	mux.HandleFunc("/oauth2/token", o.handleToken)
	o.srv = httptest.NewServer(mux)
	t.Cleanup(o.srv.Close)
	return o
}

// URL is the base URL to point YZ_OAUTH_BASE_URL at.
func (o *OAuthServer) URL() string { return o.srv.URL }

// Close shuts the fake down mid-scenario (network-failure simulation).
func (o *OAuthServer) Close() { o.srv.Close() }

// Modes.

// Deny makes the authorize handler redirect with error=access_denied.
func (o *OAuthServer) Deny() { o.mu.Lock(); o.deny = true; o.mu.Unlock() }

// WrongState makes the authorize handler redirect with a tampered state.
func (o *OAuthServer) WrongState() { o.mu.Lock(); o.wrongState = true; o.mu.Unlock() }

// NoCode makes the authorize handler redirect with state but no code.
func (o *OAuthServer) NoCode() { o.mu.Lock(); o.noCode = true; o.mu.Unlock() }

// FailToken makes the token endpoint respond with the given status.
func (o *OAuthServer) FailToken(status int) {
	o.mu.Lock()
	o.tokenStatus = status
	o.mu.Unlock()
}

// SetExpiresIn controls the expires_in value of issued token pairs.
func (o *OAuthServer) SetExpiresIn(n int) { o.mu.Lock(); o.expiresIn = n; o.mu.Unlock() }

// ExpectClientID makes the fake accept only the given OAuth client ID, so
// tests can prove the binary honors YZ_OAUTH_CLIENT_ID.
func (o *OAuthServer) ExpectClientID(id string) { o.mu.Lock(); o.clientID = id; o.mu.Unlock() }

// SeedRefreshToken registers a live refresh token without a login round,
// so tests can write a config that the refresh grant will accept.
func (o *OAuthServer) SeedRefreshToken(token string) {
	o.mu.Lock()
	o.refresh[token] = false
	o.mu.Unlock()
}

// Observations.

// AuthorizeCount reports how many codes the authorize handler issued.
func (o *OAuthServer) AuthorizeCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.authorizeCount
}

// LastRedirectURI reports the redirect_uri of the last authorize request.
func (o *OAuthServer) LastRedirectURI() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastRedirectURI
}

// TokenRequests returns a copy of every recorded token request.
func (o *OAuthServer) TokenRequests() []TokenRequest {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]TokenRequest(nil), o.tokenRequests...)
}

// LastTokens reports the most recently issued token pair.
func (o *OAuthServer) LastTokens() (access, refresh string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastAccessToken, o.lastRefreshToken
}

func (o *OAuthServer) handleAuth(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	o.mu.Lock()
	expectedClientID := o.clientID
	o.mu.Unlock()
	if q.Get("response_type") != "code" {
		http.Error(w, "unsupported response_type", http.StatusBadRequest)
		return
	}
	if q.Get("client_id") != expectedClientID {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	if q.Get("scope") != "memberships.read workers-r2.read workers-r2.write offline_access" {
		http.Error(w, "required R2 and refresh scopes missing", http.StatusBadRequest)
		return
	}
	redirectURI := q.Get("redirect_uri")
	if !registeredRedirect(redirectURI) {
		http.Error(w, "redirect_uri not registered", http.StatusBadRequest)
		return
	}
	state := q.Get("state")
	if len(state) < 8 {
		http.Error(w, "state missing or too short", http.StatusBadRequest)
		return
	}
	challenge := q.Get("code_challenge")
	if challenge == "" || q.Get("code_challenge_method") != "S256" {
		http.Error(w, "PKCE S256 required", http.StatusBadRequest)
		return
	}

	o.mu.Lock()
	o.authorizeCount++
	o.lastRedirectURI = redirectURI
	deny, wrongState, noCode := o.deny, o.wrongState, o.noCode
	o.mu.Unlock()

	target, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	v := target.Query()
	switch {
	case deny:
		v.Set("error", "access_denied")
		v.Set("error_description", "the user denied the request")
		v.Set("state", state)
	case wrongState:
		v.Set("code", newToken("code"))
		v.Set("state", "tampered-state-value")
	case noCode:
		v.Set("state", state)
	default:
		code := newToken("code")
		o.mu.Lock()
		o.codes[code] = issuedCode{challenge: challenge, redirectURI: redirectURI}
		o.mu.Unlock()
		v.Set("code", code)
		v.Set("state", state)
	}
	target.RawQuery = v.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (o *OAuthServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	rec := TokenRequest{
		GrantType:    r.Form.Get("grant_type"),
		Code:         r.Form.Get("code"),
		RedirectURI:  r.Form.Get("redirect_uri"),
		RefreshToken: r.Form.Get("refresh_token"),
	}
	o.mu.Lock()
	o.tokenRequests = append(o.tokenRequests, rec)
	status := o.tokenStatus
	expectedClientID := o.clientID
	o.mu.Unlock()
	if status != 0 {
		http.Error(w, "forced token failure", status)
		return
	}
	if r.Form.Get("client_id") != expectedClientID {
		jsonError(w, http.StatusBadRequest, "invalid_client")
		return
	}
	switch rec.GrantType {
	case "authorization_code":
		o.mu.Lock()
		ic, ok := o.codes[rec.Code]
		if ok {
			delete(o.codes, rec.Code)
		}
		o.mu.Unlock()
		if !ok {
			jsonError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != ic.challenge {
			jsonError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		if rec.RedirectURI != ic.redirectURI {
			jsonError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		o.issue(w)
	case "refresh_token":
		o.mu.Lock()
		revoked, ok := o.refresh[rec.RefreshToken]
		if ok {
			delete(o.refresh, rec.RefreshToken)
		}
		o.mu.Unlock()
		if !ok || revoked {
			jsonError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		o.issue(w)
	default:
		jsonError(w, http.StatusBadRequest, "unsupported_grant_type")
	}
}

// issue rotates in a fresh token pair and writes the token response.
func (o *OAuthServer) issue(w http.ResponseWriter) {
	access, refresh := newToken("access"), newToken("refresh")
	o.mu.Lock()
	o.refresh[refresh] = false
	o.lastAccessToken, o.lastRefreshToken = access, refresh
	expiresIn := o.expiresIn
	o.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"expires_in":    expiresIn,
		"token_type":    "bearer",
	})
}

func registeredRedirect(uri string) bool {
	switch uri {
	case "http://127.0.0.1:8974/callback",
		"http://127.0.0.1:8975/callback",
		"http://127.0.0.1:8976/callback":
		return true
	}
	return false
}

func jsonError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func newToken(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + "-" + hex.EncodeToString(b)
}
