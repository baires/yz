package r2

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBucketListClassifiesCloudflareErrors(t *testing.T) {
	const enabled = `{"success":false,"errors":[{"code":10042,"message":"Please enable R2 through the Cloudflare Dashboard."}],"messages":[],"result":null}`
	cases := []struct {
		name, body, snippet string
		status              int
		want                error
	}{
		{name: "not_enabled", status: http.StatusForbidden, body: enabled, want: ErrR2NotEnabled},
		{name: "not_enabled_message", status: http.StatusForbidden, body: `{"success":false,"errors":[{"message":"Please enable R2 through the Cloudflare Dashboard."}],"messages":[],"result":null}`, want: ErrR2NotEnabled},
		{name: "access_denied", status: http.StatusForbidden, body: `{"success":false,"errors":[{"code":10003,"message":"Insufficient permissions for the requested operation."}],"messages":[],"result":null}`, want: ErrR2ScopeRequired},
		{name: "auth_error", status: http.StatusForbidden, body: `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}],"messages":[],"result":null}`, want: ErrR2ScopeRequired},
		{name: "wrong_account", status: http.StatusForbidden, body: `{"success":false,"errors":[{"code":9109,"message":"Unauthorized to access requested resource"}],"messages":[],"result":null}`, want: ErrAccountMismatch},
		{name: "unroutable_account", status: http.StatusForbidden, body: `{"success":false,"errors":[{"code":7003,"message":"Could not route to /accounts/x/r2/buckets, perhaps your object identifier is invalid?"}],"messages":[],"result":null}`, want: ErrAccountMismatch},
		{name: "plain_forbidden", status: http.StatusForbidden, body: "forced failure", snippet: "HTTP 403"},
		{name: "outage", status: http.StatusInternalServerError, body: `{"success":false,"errors":[{"code":10001,"message":"Internal error"}],"messages":[],"result":null}`, snippet: "HTTP 500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)
			_, err := (API{BaseURL: srv.URL, AccessToken: "token"}).Buckets(context.Background(), strings.Repeat("1", 32))
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.snippet) {
				t.Fatalf("err = %v, want text %q", err, tc.snippet)
			}
			if errors.Is(err, ErrR2NotEnabled) || errors.Is(err, ErrR2ScopeRequired) || errors.Is(err, ErrAccountMismatch) {
				t.Fatalf("unclassified error became %v", err)
			}
		})
	}
}

func TestAccountsForbiddenJSONStillNeedsMembership(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}],"messages":[],"result":null}`))
	}))
	t.Cleanup(srv.Close)
	_, err := (API{BaseURL: srv.URL, AccessToken: "token"}).Accounts(context.Background())
	if !errors.Is(err, ErrAccountReadRequired) {
		t.Fatalf("err = %v, want membership scope", err)
	}
}
