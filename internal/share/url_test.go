package share

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/baires/yz/internal/config"
	"github.com/baires/yz/internal/r2"
)

func TestBuildURLReportsExpiry(t *testing.T) {
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	s3 := r2.S3{
		AccountID: "abc",
		Endpoint:  "https://example.r2.cloudflarestorage.com",
		Credentials: r2.Credentials{
			AccessKeyID: strings.Repeat("ab", 16),
			Secret:      strings.Repeat("cd", 32),
		},
	}
	cfg := &config.Config{Bucket: "photos", URLBase: "https://files.example.com"}

	cases := []struct {
		name    string
		cfg     *config.Config
		opts    Options
		expires time.Duration
		public  bool
	}{
		{name: "public base", cfg: cfg, public: true},
		{name: "domain override", cfg: &config.Config{Bucket: "photos"}, opts: Options{Domain: "cdn.example.com"}, public: true},
		{name: "signed default", cfg: cfg, opts: Options{Signed: true}, expires: 24 * time.Hour},
		{name: "explicit expiry", cfg: cfg, opts: Options{HasExpires: true, Expires: time.Hour}, expires: time.Hour},
		{name: "presign fallback", cfg: &config.Config{Bucket: "photos"}, expires: 24 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, exp, err := BuildURL(tc.cfg, s3, "key/file.txt", tc.opts, now)
			if err != nil {
				t.Fatalf("BuildURL: %v", err)
			}
			if tc.public {
				if exp != nil {
					t.Fatalf("ExpiresAt = %v, want nil", exp)
				}
				if strings.Contains(raw, "X-Amz-Signature") {
					t.Fatalf("public URL was presigned: %s", raw)
				}
				return
			}
			if exp == nil || !exp.Equal(now.Add(tc.expires)) {
				t.Fatalf("ExpiresAt = %v, want %v", exp, now.Add(tc.expires))
			}
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("parsing URL: %v", err)
			}
			secs, err := strconv.Atoi(u.Query().Get("X-Amz-Expires"))
			if err != nil {
				t.Fatalf("X-Amz-Expires: %v", err)
			}
			if time.Duration(secs)*time.Second != tc.expires {
				t.Fatalf("signed for %s, recorded %s", time.Duration(secs)*time.Second, tc.expires)
			}
		})
	}
}
