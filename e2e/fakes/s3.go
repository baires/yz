package fakes

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// S3Object represents a stored object in fake S3.
type S3Object struct {
	Data        []byte
	ContentType string
	UploadedAt  time.Time
}

// multipartUpload tracks an in-progress multipart upload.
type multipartUpload struct {
	key         string
	contentType string
	parts       map[int][]byte
	etags       map[int]string
}

// S3Server is an in-process, SigV4-compliant fake S3 server with controllable
// clock and strict signature verification for PUT, multipart, and presigned GET.
type S3Server struct {
	srv            *httptest.Server
	mu             sync.Mutex
	clock          time.Time
	objects        map[string]S3Object
	uploads        map[string]*multipartUpload
	nextUploadID   int
	calls          []string
	StatusOverride int
	// FailPartNumber makes every PUT of that multipart part number fail with
	// 500, permanently, so tests can exercise retry and abort.
	CompleteStatus     int
	CompleteError      string
	FailPartNumber     int
	Bucket             string
	AccessKey          string
	SecretKey          string
	ClockSkewThreshold time.Duration
	ReadDelay          time.Duration
	ReadChunk          int
	ack                chan struct{}
	ackOnce            sync.Once
}

// NewS3Server creates a new S3Server fixture.
func NewS3Server(t *testing.T) *S3Server {
	t.Helper()
	s := &S3Server{
		clock:              time.Now().UTC(),
		objects:            make(map[string]S3Object),
		uploads:            make(map[string]*multipartUpload),
		calls:              []string{},
		Bucket:             "photos",
		AccessKey:          AccessKey,
		SecretKey:          SecretKey,
		ClockSkewThreshold: 15 * time.Minute,
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

// URL returns the base URL of the fake S3 server.
func (s *S3Server) URL() string {
	return s.srv.URL
}

// SetClock explicitly sets the fake server's current time.
func (s *S3Server) SetClock(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = t.UTC()
}

// Advance moves the fake server's clock forward by d.
func (s *S3Server) Advance(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = s.clock.Add(d)
}

// Now returns the fake server's current time.
func (s *S3Server) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock
}

// Calls returns a copy of the recorded request calls.
func (s *S3Server) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.calls...)
}

// HoldAck makes the next PUT wait to respond until ReleaseAck. The wait does
// not hold the fixture mutex.
func (s *S3Server) HoldAck() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ack == nil {
		s.ack = make(chan struct{})
		s.ackOnce = sync.Once{}
	}
}

// ReleaseAck lets a held PUT respond.
func (s *S3Server) ReleaseAck() {
	s.mu.Lock()
	ch := s.ack
	s.mu.Unlock()
	if ch == nil {
		return
	}
	s.ackOnce.Do(func() { close(ch) })
}

// GetObject retrieves a stored object by key.
func (s *S3Server) GetObject(key string) (S3Object, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[key]
	return obj, ok
}

// ObjectCount returns how many objects are stored.
func (s *S3Server) ObjectCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects)
}

// URIEncode implements RFC 3986 encoding for S3 SigV4.
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

func s3Hmac(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func (s *S3Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls = append(s.calls, r.Method+" "+r.URL.RequestURI())
	status := s.StatusOverride
	s.mu.Unlock()

	if status != 0 {
		w.WriteHeader(status)
		return
	}

	// Route based on request path and method.
	path := r.URL.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	bucketPrefix := "/" + s.Bucket
	if path == bucketPrefix {
		if r.Method == http.MethodHead {
			s.handleHead(w, r)
			return
		}
	}

	if after, ok := strings.CutPrefix(path, bucketPrefix+"/"); ok {
		key := after
		q := r.URL.Query()
		_, initiate := q["uploads"]
		uploadID := q.Get("uploadId")
		switch {
		case r.Method == http.MethodPut && uploadID != "" && q.Get("partNumber") != "":
			s.handlePartPut(w, r, key)
			return
		case r.Method == http.MethodPut:
			s.handlePut(w, r, key)
			return
		case r.Method == http.MethodPost && initiate:
			s.handleInitiate(w, r, key)
			return
		case r.Method == http.MethodPost && uploadID != "":
			s.handleComplete(w, r, key)
			return
		case r.Method == http.MethodDelete && uploadID != "":
			s.handleAbort(w, r, key)
			return
		case r.Method == http.MethodGet:
			s.handleGet(w, r, key)
			return
		}
	}

	http.Error(w, "Not Found", http.StatusNotFound)
}

func (s *S3Server) handleHead(w http.ResponseWriter, r *http.Request) {
	now := s.Now()
	stamp := r.Header.Get("X-Amz-Date")
	date, err := time.Parse("20060102T150405Z", stamp)
	if err != nil || now.Sub(date).Abs() > s.ClockSkewThreshold {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	empty := fmt.Sprintf("%x", sha256.Sum256(nil))
	if r.Header.Get("X-Amz-Content-Sha256") != empty {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	scope := date.Format("20060102") + "/auto/s3/aws4_request"
	signed := "host;x-amz-content-sha256;x-amz-date"
	canonical := strings.Join([]string{
		"HEAD", "/" + s.Bucket, "",
		"host:" + r.Host + "\nx-amz-content-sha256:" + empty + "\nx-amz-date:" + stamp + "\n",
		signed, empty,
	}, "\n")
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	key := []byte("AWS4" + s.SecretKey)
	for _, part := range []string{date.Format("20060102"), "auto", "s3", "aws4_request", toSign} {
		key = s3Hmac(key, part)
	}
	expected := "AWS4-HMAC-SHA256 Credential=" + s.AccessKey + "/" + scope + ", SignedHeaders=" + signed + ", Signature=" + fmt.Sprintf("%x", key)
	if !hmac.Equal([]byte(r.Header.Get("Authorization")), []byte(expected)) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// canonicalQueryString renders q as a SigV4 canonical query string: keys
// sorted lexicographically, every key and value RFC 3986 encoded. The key
// named exclude (X-Amz-Signature for presigned URLs) is dropped.
func canonicalQueryString(q url.Values, exclude string) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		if k != exclude {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		vals := q[k]
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, URIEncode(k, true)+"="+URIEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

// verifySignature checks the SigV4 Authorization header of a header-signed
// request (PUT, multipart POST/DELETE) against the canonical request,
// including the actual query string. On failure it writes a 403 and returns
// false.
func (s *S3Server) verifySignature(w http.ResponseWriter, r *http.Request, bodyBytes []byte) bool {
	now := s.Now()

	// Parse Authorization header.
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		http.Error(w, "Missing or invalid Authorization header", http.StatusForbidden)
		return false
	}
	parts := strings.Split(strings.TrimPrefix(auth, "AWS4-HMAC-SHA256 "), ", ")
	authMap := make(map[string]string)
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			authMap[kv[0]] = kv[1]
		}
	}
	cred := authMap["Credential"]
	signedHeadersStr := authMap["SignedHeaders"]
	signature := authMap["Signature"]

	if cred == "" || signedHeadersStr == "" || signature == "" {
		http.Error(w, "Malformed Authorization header", http.StatusForbidden)
		return false
	}

	credParts := strings.Split(cred, "/")
	if len(credParts) < 5 {
		http.Error(w, "Malformed Credential in Authorization header", http.StatusForbidden)
		return false
	}
	reqAccessKey := credParts[0]
	dateOnly := credParts[1]
	scope := strings.Join(credParts[1:], "/")

	if reqAccessKey != s.AccessKey {
		http.Error(w, "InvalidAccessKeyId", http.StatusForbidden)
		return false
	}

	// Validate date & clock skew.
	stamp := r.Header.Get("X-Amz-Date")
	reqDate, err := time.Parse("20060102T150405Z", stamp)
	if err != nil || now.Sub(reqDate).Abs() > s.ClockSkewThreshold {
		http.Error(w, "RequestTimeTooSkewed", http.StatusForbidden)
		return false
	}

	// Validate payload hash if specified.
	contentSha := r.Header.Get("X-Amz-Content-Sha256")
	if contentSha == "" {
		http.Error(w, "Missing X-Amz-Content-Sha256 header", http.StatusForbidden)
		return false
	}
	if contentSha != "UNSIGNED-PAYLOAD" {
		actualHash := fmt.Sprintf("%x", sha256.Sum256(bodyBytes))
		if actualHash != contentSha {
			http.Error(w, "Content-SHA256 mismatch", http.StatusForbidden)
			return false
		}
	}

	// Build canonical headers from SignedHeaders.
	signedHeaders := strings.Split(signedHeadersStr, ";")
	var canonHeaders strings.Builder
	for _, h := range signedHeaders {
		hLower := strings.ToLower(h)
		var val string
		switch hLower {
		case "host":
			val = r.Host
		case "content-type":
			val = r.Header.Get("Content-Type")
		default:
			val = r.Header.Get(h)
		}
		val = strings.TrimSpace(val)
		fmt.Fprintf(&canonHeaders, "%s:%s\n", hLower, val)
	}

	canonicalURI := URIEncode(r.URL.Path, false)
	canonical := strings.Join([]string{
		r.Method,
		canonicalURI,
		canonicalQueryString(r.URL.Query(), ""),
		canonHeaders.String(),
		signedHeadersStr,
		contentSha,
	}, "\n")

	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	signingKey := []byte("AWS4" + s.SecretKey)
	for _, part := range []string{dateOnly, "auto", "s3", "aws4_request"} {
		signingKey = s3Hmac(signingKey, part)
	}
	expectedSig := hex.EncodeToString(s3Hmac(signingKey, toSign))

	if !hmac.Equal([]byte(signature), []byte(expectedSig)) {
		http.Error(w, "SignatureDoesNotMatch", http.StatusForbidden)
		return false
	}
	return true
}

func (s *S3Server) handlePut(w http.ResponseWriter, r *http.Request, key string) {
	bodyBytes, err := s.readBody(r)
	if err != nil {
		return
	}
	if !s.verifySignature(w, r, bodyBytes) {
		return
	}

	if err := s.waitAck(r); err != nil {
		return
	}

	// Store object.
	s.mu.Lock()
	s.objects[key] = S3Object{
		Data:        bodyBytes,
		ContentType: r.Header.Get("Content-Type"),
		UploadedAt:  s.clock,
	}
	s.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

// handleInitiate starts a multipart upload and returns a unique UploadId.
func (s *S3Server) handleInitiate(w http.ResponseWriter, r *http.Request, key string) {
	bodyBytes, err := s.readBody(r)
	if err != nil {
		return
	}
	if !s.verifySignature(w, r, bodyBytes) {
		return
	}

	s.mu.Lock()
	s.nextUploadID++
	uploadID := fmt.Sprintf("upload-%d", s.nextUploadID)
	s.uploads[uploadID] = &multipartUpload{
		key:         key,
		contentType: r.Header.Get("Content-Type"),
		parts:       make(map[int][]byte),
		etags:       make(map[int]string),
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+
		`<InitiateMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`+
		`<Bucket>%s</Bucket><Key>%s</Key><UploadId>%s</UploadId>`+
		`</InitiateMultipartUploadResult>`, s.Bucket, key, uploadID)
}

// handlePartPut stores one part of a multipart upload and returns its ETag.
func (s *S3Server) handlePartPut(w http.ResponseWriter, r *http.Request, key string) {
	partNumber, err := strconv.Atoi(r.URL.Query().Get("partNumber"))
	if err != nil || partNumber < 1 {
		http.Error(w, "InvalidArgument", http.StatusBadRequest)
		return
	}

	bodyBytes, err := s.readBody(r)
	if err != nil {
		return
	}

	s.mu.Lock()
	fail := s.FailPartNumber == partNumber
	s.mu.Unlock()
	if fail {
		http.Error(w, "InternalError", http.StatusInternalServerError)
		return
	}

	if !s.verifySignature(w, r, bodyBytes) {
		return
	}

	uploadID := r.URL.Query().Get("uploadId")
	etag := fmt.Sprintf(`"etag-%s-%d"`, uploadID, partNumber)
	s.mu.Lock()
	upload, ok := s.uploads[uploadID]
	if !ok || upload.key != key {
		s.mu.Unlock()
		http.Error(w, "NoSuchUpload", http.StatusNotFound)
		return
	}
	upload.parts[partNumber] = bodyBytes
	upload.etags[partNumber] = etag
	s.mu.Unlock()

	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
}

// handleComplete validates the part list against the issued ETags, assembles
// the final object, and drops the upload state.
func (s *S3Server) handleComplete(w http.ResponseWriter, r *http.Request, key string) {
	bodyBytes, err := s.readBody(r)
	if err != nil {
		return
	}
	if !s.verifySignature(w, r, bodyBytes) {
		return
	}
	uploadID := r.URL.Query().Get("uploadId")
	if s.CompleteStatus != 0 {
		w.WriteHeader(s.CompleteStatus)
		_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
		return
	}
	if s.CompleteError != "" {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `<Error><Code>%s</Code></Error>`, s.CompleteError)
		return
	}

	var complete struct {
		Parts []struct {
			PartNumber int    `xml:"PartNumber"`
			ETag       string `xml:"ETag"`
		} `xml:"Part"`
	}
	if err := xml.Unmarshal(bodyBytes, &complete); err != nil || len(complete.Parts) == 0 {
		http.Error(w, "MalformedXML", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	upload, ok := s.uploads[uploadID]
	if !ok || upload.key != key {
		s.mu.Unlock()
		http.Error(w, "NoSuchUpload", http.StatusNotFound)
		return
	}
	var assembled []byte
	for i, p := range complete.Parts {
		if p.PartNumber != i+1 {
			s.mu.Unlock()
			http.Error(w, "InvalidPartOrder", http.StatusBadRequest)
			return
		}
		data, ok := upload.parts[p.PartNumber]
		if !ok || upload.etags[p.PartNumber] != p.ETag {
			s.mu.Unlock()
			http.Error(w, "InvalidPart", http.StatusBadRequest)
			return
		}
		assembled = append(assembled, data...)
	}
	s.objects[key] = S3Object{
		Data:        assembled,
		ContentType: upload.contentType,
		UploadedAt:  s.clock,
	}
	delete(s.uploads, uploadID)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+
		`<CompleteMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`+
		`<Bucket>%s</Bucket><Key>%s</Key><ETag>"etag-%s-complete"</ETag>`+
		`</CompleteMultipartUploadResult>`, s.Bucket, key, uploadID)
}

// handleAbort discards an in-progress multipart upload.
func (s *S3Server) handleAbort(w http.ResponseWriter, r *http.Request, key string) {
	bodyBytes, err := s.readBody(r)
	if err != nil {
		return
	}
	if !s.verifySignature(w, r, bodyBytes) {
		return
	}

	s.mu.Lock()
	delete(s.uploads, r.URL.Query().Get("uploadId"))
	s.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func (s *S3Server) readBody(r *http.Request) ([]byte, error) {
	s.mu.Lock()
	delay := s.ReadDelay
	chunk := s.ReadChunk
	s.mu.Unlock()
	if delay == 0 && chunk == 0 {
		return io.ReadAll(r.Body)
	}
	if chunk <= 0 {
		chunk = 32 * 1024
	}
	buf := make([]byte, chunk)
	var out bytes.Buffer
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
		}
		if err == io.EOF {
			return out.Bytes(), nil
		}
		if err != nil {
			return nil, err
		}
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-r.Context().Done():
				timer.Stop()
				return nil, r.Context().Err()
			}
		}
	}
}

func (s *S3Server) waitAck(r *http.Request) error {
	s.mu.Lock()
	ch := s.ack
	s.mu.Unlock()
	if ch == nil {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-r.Context().Done():
		return r.Context().Err()
	}
}

func (s *S3Server) handleGet(w http.ResponseWriter, r *http.Request, key string) {
	now := s.Now()
	q := r.URL.Query()

	algo := q.Get("X-Amz-Algorithm")
	cred := q.Get("X-Amz-Credential")
	stamp := q.Get("X-Amz-Date")
	expiresStr := q.Get("X-Amz-Expires")
	signedHeadersStr := q.Get("X-Amz-SignedHeaders")
	signature := q.Get("X-Amz-Signature")

	if algo != "AWS4-HMAC-SHA256" || cred == "" || stamp == "" || expiresStr == "" || signedHeadersStr == "" || signature == "" {
		http.Error(w, "Missing presigned query parameters", http.StatusForbidden)
		return
	}

	credParts := strings.Split(cred, "/")
	if len(credParts) < 5 {
		http.Error(w, "Malformed Credential", http.StatusForbidden)
		return
	}
	reqAccessKey := credParts[0]
	dateOnly := credParts[1]
	scope := strings.Join(credParts[1:], "/")

	if reqAccessKey != s.AccessKey {
		http.Error(w, "InvalidAccessKeyId", http.StatusForbidden)
		return
	}

	signDate, err := time.Parse("20060102T150405Z", stamp)
	if err != nil {
		http.Error(w, "Invalid X-Amz-Date", http.StatusForbidden)
		return
	}

	expiresSec, err := strconv.Atoi(expiresStr)
	if err != nil || expiresSec < 0 {
		http.Error(w, "Invalid X-Amz-Expires", http.StatusForbidden)
		return
	}

	// Check expiry against server clock.
	// Exactly at signDate + expiresSec is valid; after is expired.
	deadline := signDate.Add(time.Duration(expiresSec) * time.Second)
	if now.After(deadline) {
		http.Error(w, "Request has expired", http.StatusForbidden)
		return
	}

	// Check if date is in the far future.
	if signDate.After(now.Add(s.ClockSkewThreshold)) {
		http.Error(w, "RequestTimeTooSkewed", http.StatusForbidden)
		return
	}

	// Canonical query string: all query parameters except X-Amz-Signature, sorted.
	canonQuery := canonicalQueryString(q, "X-Amz-Signature")

	// Canonical headers.
	signedHeaders := strings.Split(signedHeadersStr, ";")
	var canonHeaders strings.Builder
	for _, h := range signedHeaders {
		hLower := strings.ToLower(h)
		var val string
		switch hLower {
		case "host":
			val = r.Host
		default:
			val = r.Header.Get(h)
		}
		val = strings.TrimSpace(val)
		fmt.Fprintf(&canonHeaders, "%s:%s\n", hLower, val)
	}

	canonicalURI := URIEncode(r.URL.Path, false)
	canonical := strings.Join([]string{
		http.MethodGet,
		canonicalURI,
		canonQuery,
		canonHeaders.String(),
		signedHeadersStr,
		"UNSIGNED-PAYLOAD",
	}, "\n")

	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	signingKey := []byte("AWS4" + s.SecretKey)
	for _, part := range []string{dateOnly, "auto", "s3", "aws4_request"} {
		signingKey = s3Hmac(signingKey, part)
	}
	expectedSig := hex.EncodeToString(s3Hmac(signingKey, toSign))

	if !hmac.Equal([]byte(signature), []byte(expectedSig)) {
		http.Error(w, "SignatureDoesNotMatch", http.StatusForbidden)
		return
	}

	// Find object.
	s.mu.Lock()
	obj, ok := s.objects[key]
	s.mu.Unlock()

	if !ok {
		http.Error(w, "NoSuchKey", http.StatusNotFound)
		return
	}

	if obj.ContentType != "" {
		w.Header().Set("Content-Type", obj.ContentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(obj.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, bytes.NewReader(obj.Data))
}

// NewPublicHost creates a fake public HTTP server that serves objects stored in s.
func NewPublicHost(t *testing.T, s *S3Server) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/")
		obj, ok := s.GetObject(key)
		if !ok {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		if obj.ContentType != "" {
			w.Header().Set("Content-Type", obj.ContentType)
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.Data)))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, bytes.NewReader(obj.Data))
	}))
	t.Cleanup(srv.Close)
	return srv
}
