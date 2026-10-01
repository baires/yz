package r2

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// URIEncode implements RFC 3986 encoding for AWS SigV4.
// If encodeSlash is false, '/' characters are preserved (for canonical URI paths).
// If encodeSlash is true, '/' is encoded as %2F (for query strings).
func URIEncode(str string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(str); i++ {
		c := str[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else if c == '/' && !encodeSlash {
			b.WriteByte('/')
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func deriveSigningKey(secret, dateOnly, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), dateOnly)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}

// canonicalQuery renders url.Values as a SigV4 canonical query string: keys
// sorted lexicographically, every key and value RFC 3986 encoded.
func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		vals := append([]string{}, q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, URIEncode(k, true)+"="+URIEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

// SignRequest signs an HTTP request with AWS SigV4 headers (Authorization,
// X-Amz-Date, X-Amz-Content-Sha256). Any query parameters on the request URL
// (e.g. partNumber/uploadId for multipart) are included in the signature.
func SignRequest(req *http.Request, creds Credentials, now time.Time, payloadSha string) {
	stamp := now.UTC().Format("20060102T150405Z")
	dateOnly := now.UTC().Format("20060102")
	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", payloadSha)

	host := req.URL.Host
	if host == "" {
		host = req.Host
	}

	var signedHeaders string
	var canonHeaders strings.Builder
	if ct := req.Header.Get("Content-Type"); ct != "" {
		signedHeaders = "content-type;host;x-amz-content-sha256;x-amz-date"
		canonHeaders.WriteString("content-type:" + strings.TrimSpace(ct) + "\n")
		canonHeaders.WriteString("host:" + host + "\n")
		canonHeaders.WriteString("x-amz-content-sha256:" + payloadSha + "\n")
		canonHeaders.WriteString("x-amz-date:" + stamp + "\n")
	} else {
		signedHeaders = "host;x-amz-content-sha256;x-amz-date"
		canonHeaders.WriteString("host:" + host + "\n")
		canonHeaders.WriteString("x-amz-content-sha256:" + payloadSha + "\n")
		canonHeaders.WriteString("x-amz-date:" + stamp + "\n")
	}

	canonURI := URIEncode(req.URL.Path, false)
	canonical := strings.Join([]string{
		req.Method,
		canonURI,
		canonicalQuery(req.URL.Query()),
		canonHeaders.String(),
		signedHeaders,
		payloadSha,
	}, "\n")

	scope := dateOnly + "/auto/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	signingKey := deriveSigningKey(creds.Secret, dateOnly, "auto", "s3")
	signature := hex.EncodeToString(hmacSHA256(signingKey, toSign))

	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+creds.AccessKeyID+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

// PresignURL builds a presigned GET URL for an S3 object with the given expiry.
func PresignURL(endpoint, bucket, key string, creds Credentials, expires time.Duration, now time.Time) (string, error) {
	expiresSec := int(expires.Seconds())
	if expiresSec <= 0 || expiresSec > 604800 {
		return "", fmt.Errorf("invalid presign expiry duration %v (must be 1s to 7d)", expires)
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid endpoint %q: %w", endpoint, err)
	}

	stamp := now.UTC().Format("20060102T150405Z")
	dateOnly := now.UTC().Format("20060102")
	scope := dateOnly + "/auto/s3/aws4_request"

	host := u.Host
	cleanPath := "/" + bucket + "/" + strings.TrimPrefix(key, "/")
	canonURI := URIEncode(cleanPath, false)

	q := url.Values{}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", creds.AccessKeyID+"/"+scope)
	q.Set("X-Amz-Date", stamp)
	q.Set("X-Amz-Expires", strconv.Itoa(expiresSec))
	q.Set("X-Amz-SignedHeaders", "host")
	canonQuery := canonicalQuery(q)

	canonHeaders := "host:" + host + "\n"
	signedHeaders := "host"

	canonical := strings.Join([]string{
		http.MethodGet,
		canonURI,
		canonQuery,
		canonHeaders,
		signedHeaders,
		"UNSIGNED-PAYLOAD",
	}, "\n")

	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	signingKey := deriveSigningKey(creds.Secret, dateOnly, "auto", "s3")
	signature := hex.EncodeToString(hmacSHA256(signingKey, toSign))

	finalURL := fmt.Sprintf("%s%s?%s&X-Amz-Signature=%s",
		strings.TrimRight(endpoint, "/"), canonURI, canonQuery, signature)

	return finalURL, nil
}
