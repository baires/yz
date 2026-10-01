// Package r2 implements bucket management and S3 operations for Cloudflare R2.
package r2

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

// ValidHex checks account IDs and pasted R2 credential formats without echoing them.
func ValidHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// API carries the OAuth bearer and optional E2E endpoint override.
type API struct {
	BaseURL     string
	AccessToken string
}

func noRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// apiClient calls the Cloudflare REST API: small JSON payloads where a
// whole-request deadline is the right failure mode.
var apiClient = &http.Client{Timeout: 30 * time.Second, CheckRedirect: noRedirect}

// s3Client streams object payloads. A whole-request timeout would abort long
// uploads (the old shared 30s client capped any upload at 30s), so deadlines
// live on the transport stages instead. Idle connections are pooled deep
// enough for parallel multipart parts.
var s3Client = &http.Client{
	CheckRedirect: noRedirect,
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

type apiRequest struct {
	method string
	path   string
	body   []byte
}

type responseInfo struct {
	Cursor     string `json:"cursor"`
	TotalCount *int   `json:"total_count"`
}

var ErrAccountReadRequired = errors.New("memberships.read permission required")

type httpStatusError int

func (e httpStatusError) Error() string {
	return fmt.Sprintf("Cloudflare API returned HTTP %d; check account access and R2 permissions, then run yz setup", e)
}

func (c API) request(ctx context.Context, call apiRequest, target any) (responseInfo, error) {
	base := c.BaseURL
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	req, err := http.NewRequestWithContext(
		ctx,
		call.method,
		strings.TrimRight(base, "/")+call.path,
		bytes.NewReader(call.body),
	)
	if err != nil {
		return responseInfo{}, errors.New("invalid Cloudflare API endpoint; run yz setup")
	}
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	if call.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return responseInfo{}, ctx.Err()
		}
		return responseInfo{}, errors.New("connection to Cloudflare API failed; check your connection and run yz setup")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseInfo{}, httpStatusError(resp.StatusCode)
	}
	var envelope struct {
		Success *bool           `json:"success"`
		Result  json.RawMessage `json:"result"`
		Info    responseInfo    `json:"result_info"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(&envelope); err != nil {
		return responseInfo{}, errors.New("invalid Cloudflare API JSON; run yz setup")
	}
	if envelope.Success == nil || !*envelope.Success {
		return responseInfo{}, errors.New("unsuccessful Cloudflare API request; run yz setup")
	}
	if len(envelope.Result) == 0 || bytes.Equal(envelope.Result, []byte("null")) {
		return responseInfo{}, errors.New("invalid Cloudflare API result; run yz setup")
	}
	if err := json.Unmarshal(envelope.Result, target); err != nil {
		return responseInfo{}, errors.New("invalid Cloudflare API result; run yz setup")
	}
	return envelope.Info, nil
}

// Account identifies an accessible Cloudflare account.
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Accounts lists accessible accounts across numbered pages.
func (c API) Accounts(ctx context.Context) ([]Account, error) {
	accounts := []Account{}
	seen := map[string]bool{}
	for page := 1; page <= 100; page++ {
		path := "/accounts?per_page=50"
		if page > 1 {
			path += fmt.Sprintf("&page=%d", page)
		}
		var batch []Account
		info, err := c.request(ctx, apiRequest{method: http.MethodGet, path: path}, &batch)
		if err != nil {
			var status httpStatusError
			if errors.As(err, &status) && (status == 401 || status == 403) {
				return nil, ErrAccountReadRequired
			}
			return nil, err
		}
		for _, account := range batch {
			if !ValidHex(account.ID, 32) || len([]rune(account.Name)) == 0 || len([]rune(account.Name)) > 100 || strings.IndexFunc(account.Name, unicode.IsControl) >= 0 || seen[account.ID] {
				return nil, errors.New("invalid Cloudflare account result; run yz setup")
			}
			seen[account.ID] = true
			accounts = append(accounts, account)
		}
		if info.TotalCount != nil {
			if *info.TotalCount < len(accounts) {
				return nil, errors.New("invalid Cloudflare account pagination")
			}
			if *info.TotalCount == len(accounts) {
				return accounts, nil
			}
			if len(batch) == 0 {
				return nil, errors.New("invalid Cloudflare account pagination")
			}
		} else if len(batch) < 50 {
			return accounts, nil
		}
	}
	return nil, errors.New("pagination for Cloudflare accounts exceeded 100 pages")
}

func accountPath(account string) (string, error) {
	if !ValidHex(account, 32) {
		return "", errors.New("account ID must be 32 hexadecimal characters; run yz setup")
	}
	return "/accounts/" + account + "/r2/buckets", nil
}

func domainPath(account, bucket, kind string) (string, error) {
	path, err := accountPath(account)
	if err != nil {
		return "", err
	}
	if !bucketName.MatchString(bucket) {
		return "", errors.New("invalid Cloudflare bucket name; run yz setup")
	}
	return path + "/" + url.PathEscape(bucket) + "/domains/" + kind, nil
}

// Buckets lists all pages in the default jurisdiction, rejecting cursor loops.
func (c API) Buckets(ctx context.Context, account string) ([]string, error) {
	base, err := accountPath(account)
	if err != nil {
		return nil, err
	}
	buckets := []string{}
	seen := map[string]bool{}
	cursor := ""
	for range 100 {
		path := base
		if cursor != "" {
			path += "?cursor=" + url.QueryEscape(cursor)
		}
		var result struct {
			Buckets []struct {
				Name         string `json:"name"`
				Jurisdiction string `json:"jurisdiction"`
			} `json:"buckets"`
		}
		info, err := c.request(ctx, apiRequest{method: http.MethodGet, path: path}, &result)
		if err != nil {
			return nil, err
		}
		if result.Buckets == nil {
			return nil, errors.New("invalid Cloudflare bucket result; run yz setup")
		}
		for _, bucket := range result.Buckets {
			if !bucketName.MatchString(bucket.Name) || (bucket.Jurisdiction != "" && bucket.Jurisdiction != "default") {
				return nil, errors.New("invalid Cloudflare bucket result or unsupported jurisdiction; run yz setup")
			}
			buckets = append(buckets, bucket.Name)
		}
		next := info.Cursor
		if next == "" {
			return buckets, nil
		}
		if seen[next] {
			return nil, errors.New("pagination for Cloudflare buckets repeated a cursor; run yz setup")
		}
		seen[next], cursor = true, next
	}
	return nil, errors.New("pagination for Cloudflare buckets exceeded 100 pages; run yz setup")
}

// DomainURL turns an API hostname into an HTTPS base, rejecting URLs and paths.
func DomainURL(domain string) (string, error) {
	if len(domain) == 0 || len(domain) > 253 || net.ParseIP(domain) != nil || !strings.Contains(domain, ".") {
		return "", errors.New("invalid Cloudflare public domain; run yz setup")
	}
	for label := range strings.SplitSeq(domain, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("invalid Cloudflare public domain; run yz setup")
		}
		for _, ch := range label {
			if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '-' {
				return "", errors.New("invalid Cloudflare public domain; run yz setup")
			}
		}
	}
	return "https://" + strings.ToLower(domain), nil
}

// CustomDomains returns only enabled domains with active ownership and TLS.
func (c API) CustomDomains(ctx context.Context, account, bucket string) ([]string, error) {
	path, err := domainPath(account, bucket, "custom")
	if err != nil {
		return nil, err
	}
	var result struct {
		Domains []struct {
			Domain  string `json:"domain"`
			Enabled *bool  `json:"enabled"`
			Status  struct {
				Ownership string `json:"ownership"`
				SSL       string `json:"ssl"`
			} `json:"status"`
		} `json:"domains"`
	}
	if _, err := c.request(ctx, apiRequest{method: http.MethodGet, path: path}, &result); err != nil {
		return nil, err
	}
	if result.Domains == nil {
		return nil, errors.New("invalid Cloudflare custom domain result; run yz setup")
	}
	domains := []string{}
	for _, domain := range result.Domains {
		base, err := DomainURL(domain.Domain)
		if err != nil {
			return nil, err
		}
		if domain.Enabled == nil {
			return nil, errors.New("invalid Cloudflare custom domain result; run yz setup")
		}
		if *domain.Enabled && domain.Status.Ownership == "active" && domain.Status.SSL == "active" {
			domains = append(domains, base)
		}
	}
	return domains, nil
}

// ManagedDomain reads public access or enables it after the caller confirms.
func (c API) ManagedDomain(ctx context.Context, account, bucket string, enable bool) (string, bool, error) {
	path, err := domainPath(account, bucket, "managed")
	if err != nil {
		return "", false, err
	}
	method := http.MethodGet
	var body []byte
	if enable {
		method = http.MethodPut
		body = []byte(`{"enabled":true}`)
	}
	var result struct {
		Domain  string `json:"domain"`
		Enabled *bool  `json:"enabled"`
	}
	if _, err := c.request(ctx, apiRequest{method: method, path: path, body: body}, &result); err != nil {
		return "", false, err
	}
	base, err := DomainURL(result.Domain)
	if err != nil {
		return "", false, err
	}
	if result.Enabled == nil || !strings.HasSuffix(result.Domain, ".r2.dev") {
		return "", false, errors.New("invalid Cloudflare managed domain result; run yz setup")
	}
	if enable && !*result.Enabled {
		return "", false, errors.New("r2.dev was not enabled; run yz setup")
	}
	return base, *result.Enabled, nil
}
