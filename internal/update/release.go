package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

const (
	// DefaultBase is the GitHub web host releases are published on.
	DefaultBase = "https://github.com"
	// DefaultRepo is the owner/name that publishes yz.
	DefaultRepo = "baires/yz"
	// ChecksumsFile is the SHA-256 manifest published next to release assets.
	ChecksumsFile = "checksums.txt"

	maxBinarySize   = 12 << 20
	maxChecksumSize = 1 << 20
	maxTagLen       = 128
)

// AssetName is the release file for one target, matching scripts/release.sh:
// yz_<tag>_<os>_<arch>.
func AssetName(tag, goos, goarch string) string {
	return "yz_" + tag + "_" + goos + "_" + goarch
}

// LatestTag resolves <base>/<repo>/releases/latest the way the install script
// does: follow the redirect and read the tag from the final URL. The body of
// the release page is not downloaded.
func LatestTag(ctx context.Context, baseURL, repo, userAgent string) (string, error) {
	endpoint, err := releaseURL(baseURL, repo, "latest")
	if err != nil {
		return "", err
	}
	resp, err := get(ctx, endpoint, userAgent, true)
	if err != nil {
		return "", netErr(err, "timed out checking for updates", "could not check for updates")
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxChecksumSize))
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc, locErr := resp.Location()
		if locErr != nil {
			return "", errors.New("could not find the latest release")
		}
		if err := allowRedirect(loc, resp.Request.URL); err != nil {
			return "", err
		}
		tag := releaseTag(loc)
		if tag == "" {
			return "", errors.New("could not find the latest release")
		}
		return tag, nil
	}
	if resp.StatusCode == http.StatusOK {
		if tag := releaseTag(resp.Request.URL); tag != "" {
			return tag, nil
		}
	}
	return "", errors.New("could not find the latest release")
}

// Download fetches one release file. limit bounds the body so a hostile
// redirect cannot exhaust memory.
func Download(ctx context.Context, baseURL, repo, tag, name, userAgent string, limit int64) ([]byte, error) {
	if releaseTag(&url.URL{Path: "/releases/tag/" + tag}) == "" {
		return nil, errors.New("invalid release tag")
	}
	if !validAsset(name) {
		return nil, errors.New("invalid release asset")
	}
	endpoint, err := releaseURL(baseURL, repo, path.Join("download", tag, name))
	if err != nil {
		return nil, err
	}
	resp, err := get(ctx, endpoint, userAgent, false)
	if err != nil {
		return nil, netErr(err, "timed out downloading the release", "could not download the release")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s failed: HTTP %d", name, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, netErr(err, "timed out downloading the release", "could not download the release")
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("downloading %s failed: file is too large", name)
	}
	return data, nil
}

// ExpectedDigest finds asset in a checksums.txt body. Lines are
// "<hex>  <name>" or "<hex> *<name>", the formats sha256sum and shasum write.
func ExpectedDigest(checksums []byte, asset string) (string, error) {
	found := ""
	for line := range bytes.Lines(checksums) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		sum, name, ok := bytes.Cut(line, []byte(" "))
		if !ok {
			continue
		}
		assetName := strings.TrimPrefix(string(bytes.TrimSpace(name)), "*")
		if assetName != asset {
			continue
		}
		found = strings.ToLower(string(bytes.TrimSpace(sum)))
	}
	if found == "" {
		return "", fmt.Errorf("checksums.txt has no entry for %s", asset)
	}
	if len(found) != sha256.Size*2 || !isHex(found) {
		return "", fmt.Errorf("invalid checksum for %s", asset)
	}
	return found, nil
}

// Verify checks data against the checksums.txt entry for asset.
func Verify(asset string, data, checksums []byte) error {
	want, err := ExpectedDigest(checksums, asset)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != want {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}
	return nil
}

// Request describes one verified install of a published asset.
type Request struct {
	BaseURL   string
	Repo      string
	Tag       string
	GOOS      string
	GOARCH    string
	Exe       string
	UserAgent string
}

// Apply downloads the asset and checksums for req.Tag, verifies the digest,
// and atomically replaces req.Exe. An empty tag resolves the latest release.
func Apply(ctx context.Context, req Request) (string, error) {
	tag := req.Tag
	if tag == "" {
		var err error
		tag, err = LatestTag(ctx, req.BaseURL, req.Repo, req.UserAgent)
		if err != nil {
			return "", err
		}
	}
	asset := AssetName(tag, req.GOOS, req.GOARCH)
	sums, err := Download(ctx, req.BaseURL, req.Repo, tag, ChecksumsFile, req.UserAgent, maxChecksumSize)
	if err != nil {
		return "", err
	}
	bin, err := Download(ctx, req.BaseURL, req.Repo, tag, asset, req.UserAgent, maxBinarySize)
	if err != nil {
		return "", err
	}
	if err := Verify(asset, bin, sums); err != nil {
		return "", err
	}
	if err := Replace(resolveExe(req.Exe), bin); err != nil {
		return "", err
	}
	return tag, nil
}

func get(ctx context.Context, rawURL, userAgent string, stopAtTag bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	origin := req.URL
	client := &http.Client{
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if err := allowRedirect(next.URL, origin); err != nil {
				return err
			}
			if stopAtTag && releaseTag(next.URL) != "" {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	return client.Do(req)
}

func releaseURL(baseURL, repo, tail string) (string, error) {
	u, err := parseBase(baseURL)
	if err != nil {
		return "", err
	}
	owner, name, err := parseRepo(repo)
	if err != nil {
		return "", err
	}
	u.Path = "/" + path.Join(strings.Trim(u.Path, "/"), owner, name, "releases", tail)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func parseBase(base string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil {
		return nil, errors.New("invalid release URL")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid release URL")
	}
	if err := allowBase(u); err != nil {
		return nil, err
	}
	return u, nil
}

func allowBase(u *url.URL) error {
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLocal(u.Hostname()) {
			return nil
		}
	}
	return errors.New("release URL must be https, or http on localhost")
}

// allowRedirect permits https anywhere (GitHub sends assets to its CDN) and
// http only when the original host is already a local test server.
func allowRedirect(next, origin *url.URL) error {
	if next == nil || origin == nil {
		return errors.New("refusing release redirect")
	}
	if next.Scheme == "https" {
		return nil
	}
	if next.Scheme == "http" && origin.Scheme == "http" && isLocal(origin.Hostname()) && isLocal(next.Hostname()) {
		return nil
	}
	return errors.New("refusing release redirect")
}

func isLocal(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func parseRepo(repo string) (string, string, error) {
	owner, name, ok := strings.Cut(strings.TrimSpace(repo), "/")
	if !ok || !validRepoPart(owner) || !validRepoPart(name) || strings.Contains(name, "/") {
		return "", "", errors.New("invalid release repository")
	}
	return owner, name, nil
}

func validRepoPart(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

func releaseTag(u *url.URL) string {
	if u == nil {
		return ""
	}
	parts := strings.Split(strings.Trim(path.Clean(u.Path), "/"), "/")
	if len(parts) < 2 || parts[len(parts)-2] != "tag" {
		return ""
	}
	tag, err := url.PathUnescape(parts[len(parts)-1])
	if err != nil || tag == "" || len(tag) > maxTagLen || strings.ContainsAny(tag, `/\`) {
		return ""
	}
	if _, ok := Parse(tag); !ok {
		return ""
	}
	return tag
}

func validAsset(name string) bool {
	if name == "" || len(name) > 200 || strings.ContainsAny(name, `/\`) {
		return false
	}
	return name == path.Base(name)
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

func netErr(err error, timeoutMsg, otherMsg string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New(timeoutMsg)
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return errors.New(otherMsg)
}
