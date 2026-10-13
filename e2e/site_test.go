package e2e

import (
	"archive/zip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/baires/yz/e2e/fakes"
	"github.com/baires/yz/internal/config"
	"github.com/baires/yz/internal/history"
)

var siteURL = regexp.MustCompile(`^https://cdn\.example\.com/s/([0-9A-Za-z]{16})/$`)

func siteFixture(t *testing.T) (string, *fakes.S3Server, *fakes.CFAPI, map[string]string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "yz")
	if err := config.Save(dir, &config.Config{
		AccountID: fakes.AccountID, Bucket: "photos", URLBase: "https://cdn.example.com",
		S3AccessKeyID: fakes.AccessKey, S3Secret: fakes.SecretKey,
		AccessToken: "seed-access", RefreshToken: "seed-refresh", TokenExpiry: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	s3 := fakes.NewS3Server(t)
	api := fakes.NewCFAPI(t)
	api.ZoneName = "example.com"
	env := map[string]string{"YZ_CONFIG_DIR": dir, "YZ_R2_ENDPOINT": s3.URL(), "YZ_API_BASE_URL": api.URL()}
	fakeDesktop(t, env)
	return dir, s3, api, env
}

func writeSite(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "dist")
	for name, body := range map[string]string{
		"index.html": "<h1>hi</h1>", "app.js": "console.log(1)", "css/site.css": "body{}", ".DS_Store": "junk",
	} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestSiteFolder(t *testing.T) {
	scenario(t, "yz site uploads a folder and deploys the Worker once", func(t *testing.T) {
		dir, s3, api, env := siteFixture(t)
		res := runYz(t, env, "site", writeSite(t))
		if res.ExitCode != 0 {
			t.Fatalf("site = %+v", res)
		}
		m := siteURL.FindStringSubmatch(strings.TrimSpace(res.Stdout))
		if m == nil {
			t.Fatalf("stdout = %q", res.Stdout)
		}
		prefix := "sites/" + m[1] + "/"
		for key, ct := range map[string]string{"index.html": "text/html", "app.js": "text/javascript", "css/site.css": "text/css"} {
			obj, ok := s3.GetObject(prefix + key)
			if !ok || !strings.HasPrefix(obj.ContentType, ct) {
				t.Errorf("object %s = %+v, %v", key, obj.ContentType, ok)
			}
		}
		if s3.ObjectCount() != 3 {
			t.Errorf("objects = %d, want 3 (junk skipped)", s3.ObjectCount())
		}
		if len(api.ScriptBodies) != 1 || len(api.Routes) != 1 {
			t.Fatalf("worker deploys = %d routes = %v", len(api.ScriptBodies), api.Routes)
		}
		body := string(api.ScriptBodies[0])
		if !strings.Contains(body, `"bucket_name":"photos"`) || !strings.Contains(body, "export default") {
			t.Error("script upload lacks binding or module")
		}
		entries, _ := history.Load(dir)
		if len(entries) != 1 || !entries[0].IsSite() || entries[0].File != "dist" {
			t.Errorf("history = %+v", entries)
		}
		// A second site reuses the deployed Worker.
		if res := runYz(t, env, "site", writeSite(t)); res.ExitCode != 0 {
			t.Fatalf("second site = %+v", res)
		}
		if len(api.ScriptBodies) != 1 {
			t.Error("Worker redeployed although unchanged")
		}
		list := runYz(t, env, "list")
		if !strings.Contains(list.Stdout, "dist/") {
			t.Errorf("list = %q", list.Stdout)
		}
	})
}

func TestSiteZip(t *testing.T) {
	scenario(t, "yz site unwraps a zipped folder", func(t *testing.T) {
		_, s3, _, env := siteFixture(t)
		zipPath := filepath.Join(t.TempDir(), "site.zip")
		f, _ := os.Create(zipPath)
		zw := zip.NewWriter(f)
		for name, body := range map[string]string{"build/index.html": "<p>z</p>", "build/a/b.svg": "<svg/>"} {
			w, _ := zw.Create(name)
			_, _ = w.Write([]byte(body))
		}
		_ = zw.Close()
		_ = f.Close()
		res := runYz(t, env, "site", zipPath)
		m := siteURL.FindStringSubmatch(strings.TrimSpace(res.Stdout))
		if res.ExitCode != 0 || m == nil {
			t.Fatalf("site = %+v", res)
		}
		if obj, ok := s3.GetObject("sites/" + m[1] + "/a/b.svg"); !ok || obj.ContentType != "image/svg+xml" {
			t.Errorf("svg = %+v %v", obj, ok)
		}
	})
}

func TestSiteRejects(t *testing.T) {
	scenario(t, "yz site rejects missing index, r2.dev, and private buckets", func(t *testing.T) {
		dir, s3, api, env := siteFixture(t)
		noIndex := t.TempDir()
		_ = os.WriteFile(filepath.Join(noIndex, "a.html"), []byte("x"), 0o600)
		if res := runYz(t, env, "site", noIndex); res.ExitCode != 1 || !strings.Contains(res.Stderr, "index.html") {
			t.Errorf("no index = %+v", res)
		}
		if res := runYz(t, env, "site", t.TempDir()); res.ExitCode != 1 || !strings.Contains(res.Stderr, "empty") {
			t.Errorf("empty = %+v", res)
		}
		cfg := loadSavedConfig(t, dir)
		cfg.URLBase = "https://pub-test.r2.dev"
		_ = config.Save(dir, cfg)
		if res := runYz(t, env, "site", writeSite(t)); res.ExitCode != 1 || !strings.Contains(res.Stderr, "custom domain") {
			t.Errorf("r2.dev = %+v", res)
		}
		if s3.ObjectCount() != 0 || len(api.Calls()) != 0 {
			t.Error("rejected sites touched Cloudflare")
		}
		if res := runYz(t, env, "site"); res.ExitCode != 2 {
			t.Errorf("usage = %+v", res)
		}
	})
}
