// Package auth implements Cloudflare authorization-code login with PKCE.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DefaultClientID identifies the registered public PKCE client; it is not a
// secret. Forks that register their own Cloudflare OAuth client can override
// it via YZ_OAUTH_CLIENT_ID.
const DefaultClientID = "551dee5d77aa970489cf0c5170dd2545"

// ErrInvalidGrant means the refresh token must be replaced through login.
var ErrInvalidGrant = errors.New("saved login is no longer valid")

// Tokens is a validated token pair with an absolute expiry.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
}

// Client uses the production endpoint unless BaseURL is overridden for E2E,
// and the registered public client unless ClientID is set.
type Client struct {
	BaseURL  string
	ClientID string
}

func (c Client) clientID() string {
	if c.ClientID != "" {
		return c.ClientID
	}
	return DefaultClientID
}

func (c Client) endpoint(path string) string {
	base := c.BaseURL
	if base == "" {
		base = "https://dash.cloudflare.com"
	}
	return strings.TrimRight(base, "/") + path
}

func randomValue() string {
	var b [32]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read cannot fail on supported Go runtimes.
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// Login prints the authorization URL after binding the registered callback.
func (c Client) Login(ctx context.Context, out io.Writer, timeout time.Duration) (Tokens, error) {
	var ln net.Listener
	for _, port := range []string{"8974", "8975", "8976"} {
		candidate, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err == nil {
			ln = candidate
			break
		}
	}
	if ln == nil {
		return Tokens{}, errors.New("callback ports 8974–8976 are busy; close the process holding a port and run yz setup")
	}
	defer func() { _ = ln.Close() }()
	state, verifier := randomValue(), randomValue()
	redirect := "http://" + ln.Addr().String() + "/callback"
	sum := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"client_id": {c.clientID()}, "response_type": {"code"}, "redirect_uri": {redirect},
		"state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"}, "scope": {"memberships.read workers-r2.read workers-r2.write offline_access"},
	}
	type callback struct {
		code string
		err  error
	}
	callbacks := make(chan callback, 1)
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("GET /callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		result := callback{code: q.Get("code")}
		switch {
		case subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1:
			result.err = errors.New("OAuth state mismatch; run yz setup again")
		case q.Get("error") == "access_denied":
			result.err = errors.New("login denied; run yz setup to retry")
		case q.Get("error") != "":
			result.err = errors.New("OAuth authorization failed; run yz setup again")
		case result.code == "":
			result.err = errors.New("OAuth callback has no code; run yz setup again")
		}
		w.Header().Set("Cache-Control", "no-store")
		if result.err != nil {
			http.Error(w, result.err.Error(), http.StatusBadRequest)
		} else {
			_, _ = fmt.Fprintln(w, "Login received. You can return to yz.")
		}
		once.Do(func() { callbacks <- result })
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.Serve(ln) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			_ = srv.Close()
		}
	}()
	_, _ = fmt.Fprintln(out, "Open this URL to log in:")
	authorizeURL := c.endpoint("/oauth2/auth") + "?" + query.Encode()
	_, _ = fmt.Fprintln(out, authorizeURL)
	if c.BaseURL == "" {
		openBrowser(authorizeURL)
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case <-waitCtx.Done():
		if ctx.Err() != nil {
			return Tokens{}, ctx.Err()
		}
		return Tokens{}, errors.New("login timed out; run yz setup again")
	case err := <-serverErr:
		return Tokens{}, fmt.Errorf("OAuth callback listener: %w", err)
	case result := <-callbacks:
		if result.err != nil {
			return Tokens{}, result.err
		}
		return c.token(ctx, url.Values{
			"grant_type": {"authorization_code"}, "code": {result.code},
			"redirect_uri": {redirect}, "code_verifier": {verifier},
		})
	}
}

// Browser launch is best effort; the printed URL also works on headless hosts.
func openBrowser(target string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "linux":
		cmd = exec.Command("xdg-open", target)
	default:
		return
	}
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}

// Refresh exchanges the saved refresh token and returns its rotated pair.
func (c Client) Refresh(ctx context.Context, refresh string) (Tokens, error) {
	return c.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
}

func (c Client) token(ctx context.Context, form url.Values) (Tokens, error) {
	form.Set("client_id", c.clientID())
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.endpoint("/oauth2/token"),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return Tokens{}, errors.New("invalid OAuth endpoint; run yz setup again")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Tokens{}, ctx.Err()
		}
		return Tokens{}, errors.New("reaching cloudflare failed; check your connection and run yz setup")
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int64  `json:"expires_in"`
		Type    string `json:"token_type"`
		Error   string `json:"error"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 400 && body.Error == "invalid_grant" {
			return Tokens{}, ErrInvalidGrant
		}
		return Tokens{}, fmt.Errorf("OAuth token endpoint returned HTTP %d; run yz setup again", resp.StatusCode)
	}
	if decodeErr != nil || body.Access == "" || body.Refresh == "" || body.Expires <= 0 ||
		body.Expires > int64((365*24*time.Hour)/time.Second) || !strings.EqualFold(body.Type, "bearer") {
		return Tokens{}, errors.New("invalid OAuth token response; run yz setup again")
	}
	return Tokens{
		AccessToken:  body.Access,
		RefreshToken: body.Refresh,
		Expiry:       time.Now().Add(time.Duration(body.Expires) * time.Second),
	}, nil
}
