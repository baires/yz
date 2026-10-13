// Package sites publishes static sites to R2 and keeps the yz-sites Worker
// that serves them deployed on the configured public host.
package sites

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

// WorkerName is the stable per-account script name.
const WorkerName = "yz-sites"

// CompatibilityDate pins the Worker runtime behavior.
const CompatibilityDate = "2026-10-01"

//go:embed worker.js
var workerScript []byte

// ErrPermission means the saved login lacks the Workers or zone scopes.
var ErrPermission = errors.New("cloudflare login lacks Workers permissions; run yz setup and approve the new Workers access")

// Script returns the embedded Worker source.
func Script() []byte { return workerScript }

// Version identifies the script, bucket, and host combination that was
// deployed, so an unchanged deployment is skipped.
func Version(bucket, host string) string {
	sum := sha256.New()
	sum.Write(workerScript)
	sum.Write([]byte("\x00" + CompatibilityDate + "\x00" + bucket + "\x00" + host))
	return hex.EncodeToString(sum.Sum(nil))[:16]
}

// Deployer calls the Cloudflare REST API with the OAuth bearer.
type Deployer struct {
	BaseURL     string
	AccessToken string
	Client      *http.Client
}

// Host extracts the hostname a site URL base serves from. Managed r2.dev
// hosts cannot carry Worker routes.
func Host(urlBase string) (string, error) {
	if urlBase == "" {
		return "", errors.New("static sites need a public custom domain on the bucket; the bucket is private — connect a custom domain in R2 and run yz setup")
	}
	u, err := url.Parse(urlBase)
	if err != nil || u.Hostname() == "" {
		return "", errors.New("invalid URL base; run yz setup")
	}
	host := strings.ToLower(u.Hostname())
	if strings.HasSuffix(host, ".r2.dev") {
		return "", errors.New("static sites need a custom domain on the bucket: r2.dev hosts cannot run Workers — connect a custom domain in R2 and run yz setup")
	}
	return host, nil
}

// RoutePattern is the Worker route for a host.
func RoutePattern(host string) string { return host + "/s/*" }

// Deploy uploads the Worker with an R2 binding to bucket and routes
// <host>/s/* to it. It is idempotent.
func (d Deployer) Deploy(ctx context.Context, account, bucket, host string) error {
	if err := d.putScript(ctx, account, bucket); err != nil {
		return err
	}
	zone, err := d.zoneFor(ctx, host)
	if err != nil {
		return err
	}
	return d.ensureRoute(ctx, zone, RoutePattern(host))
}

type apiError struct {
	status int
	op     string
}

func (e apiError) Error() string {
	return fmt.Sprintf("Cloudflare API returned HTTP %d while %s", e.status, e.op)
}

func (d Deployer) do(ctx context.Context, op, method, path, contentType string, body []byte, target any) error {
	base := d.BaseURL
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid Cloudflare API endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+d.AccessToken)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("connection to Cloudflare API failed while %s", op)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w (HTTP %d while %s)", ErrPermission, resp.StatusCode, op)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError{resp.StatusCode, op}
	}
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope); err != nil || !envelope.Success {
		return fmt.Errorf("unsuccessful Cloudflare API response while %s", op)
	}
	if target != nil {
		if err := json.Unmarshal(envelope.Result, target); err != nil {
			return fmt.Errorf("invalid Cloudflare API result while %s", op)
		}
	}
	return nil
}

func (d Deployer) putScript(ctx context.Context, account, bucket string) error {
	metadata, _ := json.Marshal(map[string]any{
		"main_module":        "worker.js",
		"compatibility_date": CompatibilityDate,
		"bindings": []map[string]string{
			{"type": "r2_bucket", "name": "BUCKET", "bucket_name": bucket},
		},
	})
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	mh := textproto.MIMEHeader{}
	mh.Set("Content-Disposition", `form-data; name="metadata"`)
	mh.Set("Content-Type", "application/json")
	part, _ := w.CreatePart(mh)
	_, _ = part.Write(metadata)
	sh := textproto.MIMEHeader{}
	sh.Set("Content-Disposition", `form-data; name="worker.js"; filename="worker.js"`)
	sh.Set("Content-Type", "application/javascript+module")
	part, _ = w.CreatePart(sh)
	_, _ = part.Write(workerScript)
	_ = w.Close()
	path := "/accounts/" + url.PathEscape(account) + "/workers/scripts/" + WorkerName
	return d.do(ctx, "uploading the yz-sites Worker", http.MethodPut, path, w.FormDataContentType(), body.Bytes(), nil)
}

// zoneFor finds the zone that owns host by trying each parent domain.
func (d Deployer) zoneFor(ctx context.Context, host string) (string, error) {
	labels := strings.Split(host, ".")
	for i := 0; i < len(labels)-1; i++ {
		name := strings.Join(labels[i:], ".")
		var zones []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := d.do(ctx, "looking up the zone for "+host, http.MethodGet, "/zones?name="+url.QueryEscape(name), "", nil, &zones); err != nil {
			return "", err
		}
		for _, z := range zones {
			if strings.EqualFold(z.Name, name) && z.ID != "" {
				return z.ID, nil
			}
		}
	}
	return "", fmt.Errorf("no Cloudflare zone found for %s; the custom domain must be on a zone in this account", host)
}

func (d Deployer) ensureRoute(ctx context.Context, zone, pattern string) error {
	base := "/zones/" + url.PathEscape(zone) + "/workers/routes"
	var routes []struct {
		ID      string `json:"id"`
		Pattern string `json:"pattern"`
		Script  string `json:"script"`
	}
	if err := d.do(ctx, "listing Worker routes", http.MethodGet, base, "", nil, &routes); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"pattern": pattern, "script": WorkerName})
	for _, r := range routes {
		if r.Pattern != pattern {
			continue
		}
		if r.Script == WorkerName {
			return nil
		}
		if r.Script != "" {
			return fmt.Errorf("route %s already points at Worker %q; remove it or point it at %s", pattern, r.Script, WorkerName)
		}
		return d.do(ctx, "updating the Worker route", http.MethodPut, base+"/"+url.PathEscape(r.ID), "application/json", body, nil)
	}
	return d.do(ctx, "creating the Worker route", http.MethodPost, base, "application/json", body, nil)
}
