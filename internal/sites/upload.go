package sites

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"mime"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	"github.com/baires/yz/internal/r2"
)

const idAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// MaxFiles bounds a single site upload.
const MaxFiles = 10000

// MaxZipBytes bounds extracted zip content.
const MaxZipBytes = 2 << 30

// File is one asset relative to the site root, with forward slashes.
type File struct {
	Rel  string
	Path string
	Size int64
}

// NewID returns a 16-char base62 site id.
func NewID() string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = idAlphabet[rand.N(len(idAlphabet))]
	}
	return string(b)
}

// Prefix is the R2 key prefix for a site.
func Prefix(id string) string { return "sites/" + id + "/" }

// URL is the public address of a site.
func URL(urlBase, id string) string {
	return strings.TrimRight(urlBase, "/") + "/s/" + id + "/"
}

func skipName(name string) bool {
	return name == ".DS_Store" || name == "__MACOSX" || name == "Thumbs.db" || name == ".git"
}

// Prepare resolves src (a directory or .zip) to a site root on disk. cleanup
// removes any temporary extraction.
func Prepare(src string) (root string, cleanup func(), err error) {
	noop := func() {}
	info, err := os.Stat(src)
	if err != nil {
		return "", noop, fmt.Errorf("yz: %w", err)
	}
	if info.IsDir() {
		return src, noop, nil
	}
	if !strings.EqualFold(filepath.Ext(src), ".zip") {
		return "", noop, fmt.Errorf("yz: %q is not a directory or .zip", src)
	}
	dir, err := os.MkdirTemp("", "yz-site-")
	if err != nil {
		return "", noop, fmt.Errorf("yz: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	if err := extract(src, dir); err != nil {
		cleanup()
		return "", noop, err
	}
	root = dir
	// A zip of a folder: descend into its single top-level directory.
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		entries, _ := os.ReadDir(dir)
		var dirs []os.DirEntry
		for _, e := range entries {
			if !skipName(e.Name()) {
				dirs = append(dirs, e)
			}
		}
		if len(dirs) == 1 && dirs[0].IsDir() {
			root = filepath.Join(dir, dirs[0].Name())
		}
	}
	return root, cleanup, nil
}

func extract(src, dst string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("yz: reading zip %q: %w", src, err)
	}
	defer func() { _ = zr.Close() }()
	root, err := os.OpenRoot(dst)
	if err != nil {
		return fmt.Errorf("yz: %w", err)
	}
	defer func() { _ = root.Close() }()
	var total int64
	for _, f := range zr.File {
		name := path.Clean(strings.ReplaceAll(f.Name, "\\", "/"))
		if name == "." || strings.HasPrefix(name, "../") || name == ".." || path.IsAbs(name) {
			return fmt.Errorf("yz: zip entry %q escapes the site root", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := root.MkdirAll(name, 0o700); err != nil {
				return fmt.Errorf("yz: zip: %w", err)
			}
			continue
		}
		if !f.Mode().IsRegular() {
			continue // symlinks and devices are not site content
		}
		if dir := path.Dir(name); dir != "." {
			if err := root.MkdirAll(dir, 0o700); err != nil {
				return fmt.Errorf("yz: zip: %w", err)
			}
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("yz: zip entry %q: %w", f.Name, err)
		}
		out, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = rc.Close()
			return fmt.Errorf("yz: zip entry %q: %w", f.Name, err)
		}
		n, copyErr := io.Copy(out, io.LimitReader(rc, MaxZipBytes-total+1))
		_ = rc.Close()
		closeErr := out.Close()
		total += n
		if total > MaxZipBytes {
			return errors.New("yz: zip expands beyond 2 GiB")
		}
		if copyErr != nil || closeErr != nil {
			return fmt.Errorf("yz: extracting %q failed", f.Name)
		}
	}
	return nil
}

// Collect lists regular files under root, skipping symlinks and OS junk,
// and requires an index.html at the root.
func Collect(root string) ([]File, error) {
	var files []File
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != root && skipName(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, File{Rel: filepath.ToSlash(rel), Path: p, Size: info.Size()})
		if len(files) > MaxFiles {
			return fmt.Errorf("site has more than %d files", MaxFiles)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("yz: reading site: %w", err)
	}
	if len(files) == 0 {
		return nil, errors.New("yz: site is empty")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	hasIndex := false
	for _, f := range files {
		if f.Rel == "index.html" {
			hasIndex = true
		}
	}
	if !hasIndex {
		return nil, errors.New("yz: site needs an index.html at its root")
	}
	return files, nil
}

// ContentType picks a MIME type by extension, defaulting to octet-stream.
func ContentType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	switch ext {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".map":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".wasm":
		return "application/wasm"
	case ".webmanifest":
		return "application/manifest+json"
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

// Upload puts every file under Prefix(id). Progress reports bytes done.
func Upload(ctx context.Context, s3 r2.S3, bucket, id string, files []File, concurrency int, progress func(done, total int64)) error {
	if concurrency <= 0 {
		concurrency = 8
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}
	var done atomic.Int64
	if progress != nil {
		progress(0, total)
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)
	for _, f := range files {
		g.Go(func() error {
			fh, err := os.Open(f.Path)
			if err != nil {
				return fmt.Errorf("yz: %w", err)
			}
			defer func() { _ = fh.Close() }()
			key := Prefix(id) + f.Rel
			ct := ContentType(f.Rel)
			if f.Size >= r2.DefaultMultipartThreshold {
				err = s3.MultipartUpload(gctx, bucket, key, fh, f.Size, ct, r2.MultipartOptions{})
			} else {
				err = s3.PutObject(gctx, bucket, key, fh, f.Size, ct)
			}
			if err != nil {
				return fmt.Errorf("uploading %s: %w", f.Rel, err)
			}
			if progress != nil {
				progress(done.Add(f.Size), total)
			}
			return nil
		})
	}
	return g.Wait()
}
