// Package share orchestrates key generation, URL building, and file uploads to R2.
package share

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baires/yz/internal/config"
	"github.com/baires/yz/internal/r2"
)

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Options holds flags and parameters for sharing a file.
type Options struct {
	Path       string
	Signed     bool
	Expires    time.Duration
	HasExpires bool
	Domain     string
	R2Endpoint string
	// Multipart tuning; zero values take r2 package defaults. Exposed through
	// undocumented env vars for benchmarking and power users.
	PartSize    int64
	Concurrency int
	Progress    func(sent, total int64)
}

// GenerateKey produces an unguessable object key: <16 base62 chars>/<sanitized filename>.
func GenerateKey(filename string) string {
	prefix := make([]byte, 16)
	for i := range prefix {
		prefix[i] = base62Alphabet[rand.N(len(base62Alphabet))]
	}
	return string(prefix) + "/" + SanitizeFilename(filename)
}

// SanitizeFilename cleans the filename to prevent path traversal and remove dangerous characters.
func SanitizeFilename(name string) string {
	base := filepath.Base(name)
	if base == "." || base == "/" || base == "\\" {
		return "file"
	}
	var b strings.Builder
	for _, r := range base {
		if r < 32 || r == 127 || r == '/' || r == '\\' || r == '\x00' {
			b.WriteByte('-')
		} else {
			b.WriteRune(r)
		}
	}
	res := strings.Trim(b.String(), "-")
	if res == "" || res == "." || res == ".." {
		return "file"
	}
	return res
}

// BuildURL determines the appropriate share URL based on configuration and flags.
// Priority: --signed / --expires (presigned) > --domain > cfg.URLBase > presigned fallback.
// The returned time is when a presigned link stops working. It is nil for a public link.
func BuildURL(cfg *config.Config, s3 r2.S3, key string, opts Options, now time.Time) (string, *time.Time, error) {
	if expiry, ok := presignTTL(cfg, opts); ok {
		raw, err := s3.PresignGet(cfg.Bucket, key, expiry, now)
		if err != nil {
			return "", nil, err
		}
		t := now.Add(expiry)
		return raw, &t, nil
	}

	if opts.Domain != "" {
		dom := opts.Domain
		if !strings.HasPrefix(dom, "http://") && !strings.HasPrefix(dom, "https://") {
			dom = "https://" + dom
		}
		return strings.TrimRight(dom, "/") + "/" + r2.URIEncode(key, false), nil, nil
	}

	return strings.TrimRight(cfg.URLBase, "/") + "/" + r2.URIEncode(key, false), nil, nil
}

// presignTTL reports how long a link should be signed for. Public links,
// those with a domain or URL base and no signing flag, do not expire.
func presignTTL(cfg *config.Config, opts Options) (time.Duration, bool) {
	if opts.Signed || opts.HasExpires || (opts.Domain == "" && cfg.URLBase == "") {
		expiry := opts.Expires
		if expiry <= 0 {
			expiry = 24 * time.Hour
		}
		return expiry, true
	}
	return 0, false
}

func openInRoot(path string) (*os.File, os.FileInfo, error) {
	clean := filepath.Clean(path)
	dir := filepath.Dir(clean)
	base := filepath.Base(clean)

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("yz: opening %q: %w", path, err)
	}
	defer func() { _ = root.Close() }()

	f, err := root.Open(base)
	if err != nil {
		return nil, nil, fmt.Errorf("yz: opening %q: %w", path, err)
	}

	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("yz: stating %q: %w", path, err)
	}
	if stat.IsDir() {
		_ = f.Close()
		return nil, nil, fmt.Errorf("yz: %q is a directory; sharing directories is not supported", path)
	}

	return f, stat, nil
}

// Share uploads the file at path and writes its share URL to w.
// The returned time is when the link stops working, or nil if it does not expire.
func Share(ctx context.Context, cfg *config.Config, opts Options, w io.Writer) (*time.Time, error) {
	f, stat, err := openInRoot(opts.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	contentType := http.DetectContentType(buf[:n])
	if ext := filepath.Ext(opts.Path); ext != "" {
		if mimeType := mime.TypeByExtension(ext); mimeType != "" {
			contentType = mimeType
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("yz: seeking %q: %w", opts.Path, err)
	}

	if opts.Progress != nil {
		opts.Progress(0, stat.Size())
	}

	key := GenerateKey(stat.Name())
	s3 := r2.S3{
		AccountID:   cfg.AccountID,
		Endpoint:    opts.R2Endpoint,
		Credentials: r2.Credentials{AccessKeyID: cfg.S3AccessKeyID, Secret: cfg.S3Secret},
	}

	if stat.Size() >= r2.DefaultMultipartThreshold {
		var progress func(int64)
		if opts.Progress != nil {
			progress = func(sent int64) { opts.Progress(sent, stat.Size()) }
		}
		err := s3.MultipartUpload(ctx, cfg.Bucket, key, f, stat.Size(), contentType, r2.MultipartOptions{
			PartSize:    opts.PartSize,
			Concurrency: opts.Concurrency,
			Progress:    progress,
		})
		if err != nil {
			return nil, err
		}
	} else {
		body := io.Reader(f)
		if opts.Progress != nil {
			body = newCountingReader(f, stat.Size(), opts.Progress)
		}
		if err := s3.PutObject(ctx, cfg.Bucket, key, body, stat.Size(), contentType); err != nil {
			return nil, err
		}
	}

	now := time.Now()
	url, expires, err := BuildURL(cfg, s3, key, opts, now)
	if err != nil {
		return nil, err
	}

	_, _ = fmt.Fprintln(w, url)
	return expires, nil
}
