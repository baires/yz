package fakes

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	AccountID  = "11111111111111111111111111111111"
	AccessKey  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	SecretKey  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	SetupInput = "1\n" + AccessKey + "\n" + SecretKey + "\ny\n"
)

// CFAPI serves documented wire shapes, independently of production types.
// Options are set before starting the CLI; Calls and mutations are synchronized.
type CFAPI struct {
	srv                *httptest.Server
	mu                 sync.Mutex
	calls              []string
	BucketNames        []string
	CustomDomain       string
	CustomEnabled      bool
	CustomActive       bool
	ManagedEnabled     bool
	ManagedDomain      string
	FailPath           string
	Status             int
	Unsuccessful       bool
	Malformed          bool
	MalformedResult    bool
	EnableIneffective  bool
	Paged              bool
	RepeatCursor       bool
	Accounts           []map[string]string
	AccountsPaged      bool
	AccountScopeNeeded bool
	Delay              time.Duration
	// Workers deployment fixtures.
	ZoneName     string
	ScriptBodies [][]byte
	Routes       []map[string]string
}

// ZoneID is the fake zone that owns ZoneName.
const ZoneID = "22222222222222222222222222222222"

func NewCFAPI(t *testing.T) *CFAPI {
	t.Helper()
	f := &CFAPI{
		BucketNames: []string{"photos"}, ManagedDomain: "pub-test.r2.dev", calls: []string{},
		Accounts: []map[string]string{{"id": AccountID, "name": "Personal"}},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *CFAPI) URL() string { return f.srv.URL }
func (f *CFAPI) Close()      { f.srv.Close() }
func (f *CFAPI) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

// Configure synchronizes fixture changes with HTTP handlers.
func (f *CFAPI) Configure(change func(*CFAPI)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *CFAPI) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	delay := f.Delay
	f.mu.Unlock()
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-r.Context().Done():
			timer.Stop()
			return
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	call := r.Method + " " + r.URL.RequestURI()
	f.calls = append(f.calls, call)
	w.Header().Set("Content-Type", "application/json")
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if bearer != "seed-access" && !strings.HasPrefix(bearer, "access-") {
		http.Error(w, "missing OAuth bearer", http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/accounts" && f.AccountScopeNeeded && bearer == "seed-access" {
		http.Error(w, "memberships.read required", http.StatusForbidden)
		return
	}
	if call == f.FailPath {
		if f.Status != 0 {
			http.Error(w, "forced failure", f.Status)
			return
		}
		if f.Malformed {
			_, _ = w.Write([]byte("{bad json"))
			return
		}
		if f.Unsuccessful {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false})
			return
		}
		if f.MalformedResult {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{}})
			return
		}
	}
	base := "/accounts/" + AccountID + "/r2/buckets"
	if r.Method == "GET" && r.URL.Path == "/accounts" {
		accounts := f.Accounts
		page := 1
		if f.AccountsPaged {
			if r.URL.Query().Get("page") == "2" {
				accounts = accounts[1:]
				page = 2
			} else {
				accounts = accounts[:1]
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "result": accounts,
			"result_info": map[string]int{"page": page, "per_page": 50, "total_count": len(f.Accounts)},
		})
		return
	}
	var result any
	cursor := ""
	switch {
	case r.Method == "PUT" && r.URL.Path == "/accounts/"+AccountID+"/workers/scripts/yz-sites":
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			http.Error(w, "expected multipart", http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.ScriptBodies = append(f.ScriptBodies, body)
		result = map[string]string{"id": "yz-sites"}
	case r.Method == "GET" && r.URL.Path == "/zones":
		zones := []map[string]string{}
		if f.ZoneName != "" && r.URL.Query().Get("name") == f.ZoneName {
			zones = append(zones, map[string]string{"id": ZoneID, "name": f.ZoneName})
		}
		result = zones
	case r.URL.Path == "/zones/"+ZoneID+"/workers/routes" && r.Method == "GET":
		routes := f.Routes
		if routes == nil {
			routes = []map[string]string{}
		}
		result = routes
	case r.URL.Path == "/zones/"+ZoneID+"/workers/routes" && r.Method == "POST":
		var route map[string]string
		if json.NewDecoder(r.Body).Decode(&route) != nil {
			http.Error(w, "bad route", http.StatusBadRequest)
			return
		}
		route["id"] = "route1"
		f.Routes = append(f.Routes, route)
		result = route
	case r.Method == "GET" && r.URL.Path == base:
		buckets := []map[string]string{}
		names := f.BucketNames
		if f.Paged {
			if r.URL.Query().Get("cursor") == "" {
				names = names[:1]
				cursor = "next"
			} else {
				names = names[1:]
			}
			if f.RepeatCursor {
				cursor = "next"
			}
		}
		for _, name := range names {
			buckets = append(buckets, map[string]string{"name": name, "jurisdiction": "default"})
		}
		result = map[string]any{"buckets": buckets}
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/domains/custom") && strings.HasPrefix(r.URL.Path, base+"/"):
		domains := []map[string]any{}
		if f.CustomDomain != "" {
			status := "pending"
			if f.CustomActive {
				status = "active"
			}
			domains = append(domains, map[string]any{
				"domain": f.CustomDomain, "enabled": f.CustomEnabled,
				"status": map[string]string{"ownership": status, "ssl": status},
			})
		}
		result = map[string]any{"domains": domains}
	case (r.Method == "GET" || r.Method == "PUT") && strings.HasSuffix(r.URL.Path, "/domains/managed") && strings.HasPrefix(r.URL.Path, base+"/"):
		if r.Method == "PUT" {
			var body struct {
				Enabled bool `json:"enabled"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || !body.Enabled || r.Header.Get("Content-Type") != "application/json" {
				http.Error(w, "expected enabled=true JSON", http.StatusBadRequest)
				return
			}
			if !f.EnableIneffective {
				f.ManagedEnabled = true
			}
		}
		result = map[string]any{"domain": f.ManagedDomain, "enabled": f.ManagedEnabled, "bucketId": "fake-bucket"}
	default:
		http.Error(w, "unexpected API call", http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result, "result_info": map[string]string{"cursor": cursor}})
}
