package r2

import (
	"context"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	// DefaultMultipartThreshold is the file size at which uploads switch from
	// a single PUT to parallel multipart: below it the initiate/complete round
	// trips cost more than parallelism returns.
	DefaultMultipartThreshold = 64 << 20 // 64 MiB
	// DefaultPartSize balances per-request overhead against parallelism: at
	// 8 MiB a file reaches DefaultPartConcurrency parts quickly, and the
	// 10000-part ceiling still allows ~78 GiB objects. S3/R2 require every
	// part but the last to be at least 5 MiB.
	DefaultPartSize = 8 << 20 // 8 MiB
	// DefaultPartConcurrency is how many parts fly at once. Throughput
	// scales linearly with concurrency up to 32 on per-stream-throttled
	// links; memory cost is negligible because parts stream straight off
	// disk.
	DefaultPartConcurrency = 32

	maxParts     = 10000 // S3/R2 hard limit
	partAttempts = 3
)

// MultipartOptions tunes MultipartUpload. Zero values take the defaults.
type MultipartOptions struct {
	PartSize    int64
	Concurrency int
	// Progress is called with the cumulative bytes written to the wire,
	// capped at the object size (retried bytes may recount).
	Progress func(sent int64)
}

// objectURL validates the destination and returns the encoded object URL.
func (c S3) objectURL(bucket, key string) (string, error) {
	if _, err := accountPath(c.AccountID); err != nil {
		return "", err
	}
	if !bucketName.MatchString(bucket) {
		return "", errors.New("invalid R2 bucket name; run yz setup")
	}
	if !ValidHex(c.Credentials.AccessKeyID, 32) || !ValidHex(c.Credentials.Secret, 64) {
		return "", errors.New("R2 credentials rejected; check your Access Key ID and Secret Access Key, then run yz setup")
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = "https://" + c.AccountID + ".r2.cloudflarestorage.com"
	}
	return strings.TrimRight(endpoint, "/") + "/" + bucket + "/" + URIEncode(strings.TrimPrefix(key, "/"), false), nil
}

// MultipartUpload streams src as an S3 multipart upload: initiate, part PUTs
// in parallel, complete. Any permanent failure aborts the upload so no
// orphaned parts are left billing storage.
func (c S3) MultipartUpload(ctx context.Context, bucket, key string, src io.ReaderAt, size int64, contentType string, opts MultipartOptions) error {
	if opts.PartSize <= 0 {
		opts.PartSize = DefaultPartSize
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = DefaultPartConcurrency
	}
	// Stay under the 10000-part ceiling for huge objects.
	if minPart := (size + maxParts - 1) / maxParts; opts.PartSize < minPart {
		opts.PartSize = (minPart + (1 << 20) - 1) &^ (1<<20 - 1)
	}

	base, err := c.objectURL(bucket, key)
	if err != nil {
		return err
	}

	uploadID, err := c.initiateMultipart(ctx, base, contentType)
	if err != nil {
		return err
	}

	completed := false
	defer func() {
		if !completed {
			abortCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = c.abortMultipart(abortCtx, base, uploadID)
		}
	}()

	partCount := int((size + opts.PartSize - 1) / opts.PartSize)
	etags := make([]string, partCount)
	if err := c.uploadParts(ctx, base, uploadID, src, size, opts, etags); err != nil {
		return err
	}

	err = c.completeMultipart(ctx, base, uploadID, etags)
	completed = err == nil
	return err
}

func (c S3) multipartRequest(ctx context.Context, method, base string, q url.Values, body io.Reader, size int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, base+"?"+q.Encode(), body)
	if err != nil {
		return nil, fmt.Errorf("creating R2 multipart request: %w", err)
	}
	req.ContentLength = size
	payloadSha := "UNSIGNED-PAYLOAD"
	if size == 0 {
		payloadSha = fmt.Sprintf("%x", sha256.Sum256(nil))
	}
	SignRequest(req, c.Credentials, time.Now(), payloadSha)

	resp, err := s3Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("R2 connection failed; check your connection and retry")
	}
	return resp, nil
}

func (c S3) initiateMultipart(ctx context.Context, base, contentType string) (string, error) {
	// Content-Type belongs to the initiate request; part PUTs must not carry it.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"?uploads=", nil)
	if err != nil {
		return "", fmt.Errorf("creating R2 multipart request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	SignRequest(req, c.Credentials, time.Now(), fmt.Sprintf("%x", sha256.Sum256(nil)))

	resp, err := s3Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("R2 connection failed; check your connection and retry")
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", multipartStatusError(resp.StatusCode, body)
	}
	var result struct {
		UploadID string `xml:"UploadId"`
	}
	if err := xml.Unmarshal(body, &result); err != nil || result.UploadID == "" {
		return "", errors.New("invalid R2 multipart initiate response; run yz setup")
	}
	return result.UploadID, nil
}

func (c S3) uploadParts(ctx context.Context, base, uploadID string, src io.ReaderAt, size int64, opts MultipartOptions, etags []string) error {
	jobs := make(chan int)
	g, gctx := errgroup.WithContext(ctx)
	var sent int64

	for range opts.Concurrency {
		g.Go(func() error {
			for num := range jobs {
				offset := int64(num-1) * opts.PartSize
				length := min(opts.PartSize, size-offset)
				etag, err := c.uploadPartWithRetry(gctx, base, uploadID, src, offset, length, num, &sent, size, opts.Progress)
				if err != nil {
					return err
				}
				etags[num-1] = etag
			}
			return nil
		})
	}

send:
	for num := 1; num <= len(etags); num++ {
		select {
		case jobs <- num:
		case <-gctx.Done():
			break send
		}
	}
	close(jobs)
	return g.Wait()
}

func (c S3) uploadPartWithRetry(ctx context.Context, base, uploadID string, src io.ReaderAt, offset, length int64, num int, sent *int64, total int64, progress func(int64)) (string, error) {
	var err error
	for attempt := range partAttempts {
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(200<<attempt) * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return "", ctx.Err()
			}
		}
		var etag string
		etag, err = c.uploadPart(ctx, base, uploadID, src, offset, length, num, sent, total, progress)
		if err == nil {
			return etag, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var status r2StatusError
		if errors.As(err, &status) && status.status < 500 {
			return "", err // 4xx will not heal with a retry
		}
	}
	return "", err
}

func (c S3) uploadPart(ctx context.Context, base, uploadID string, src io.ReaderAt, offset, length int64, num int, sent *int64, total int64, progress func(int64)) (string, error) {
	var body io.Reader = io.NewSectionReader(src, offset, length)
	if progress != nil {
		body = &partProgressReader{r: body, sent: sent, total: total, cb: progress}
	}
	q := url.Values{"partNumber": {strconv.Itoa(num)}, "uploadId": {uploadID}}
	resp, err := c.multipartRequest(ctx, http.MethodPut, base, q, body, length)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))

	if resp.StatusCode != http.StatusOK {
		return "", multipartStatusError(resp.StatusCode, nil)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		return "", errors.New("R2 multipart part response missing ETag; run yz setup")
	}
	return etag, nil
}

type partProgressReader struct {
	r     io.Reader
	sent  *int64
	total int64
	cb    func(int64)
}

func (p *partProgressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.cb(min(atomic.AddInt64(p.sent, int64(n)), p.total))
	}
	return n, err
}

func (c S3) completeMultipart(ctx context.Context, base, uploadID string, etags []string) error {
	var b strings.Builder
	b.WriteString(`<CompleteMultipartUpload xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
	for i, etag := range etags {
		fmt.Fprintf(&b, `<Part><PartNumber>%d</PartNumber><ETag>%s</ETag></Part>`, i+1, etag)
	}
	b.WriteString(`</CompleteMultipartUpload>`)

	payload := b.String()
	q := url.Values{"uploadId": {uploadID}}
	resp, err := c.multipartRequest(ctx, http.MethodPost, base, q, strings.NewReader(payload), int64(len(payload)))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return multipartStatusError(resp.StatusCode, body)
	}
	// S3 can report completion errors inside a 200 response body.
	var result struct {
		XMLName xml.Name
		Code    string `xml:"Code"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		return errors.New("invalid R2 multipart complete response; run yz setup")
	}
	if result.XMLName.Local == "Error" {
		return fmt.Errorf("R2 multipart completion failed: %s; retry the upload", result.Code)
	}
	if result.XMLName.Local != "CompleteMultipartUploadResult" {
		return errors.New("invalid R2 multipart complete response; run yz setup")
	}
	return nil
}

func (c S3) abortMultipart(ctx context.Context, base, uploadID string) error {
	q := url.Values{"uploadId": {uploadID}}
	resp, err := c.multipartRequest(ctx, http.MethodDelete, base, q, nil, 0)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("R2 multipart abort returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// multipartStatusError classifies R2 multipart failures. The status travels
// in r2StatusError so the per-part retry loop can tell transient 5xx from
// permanent 4xx failures.
type r2StatusError struct {
	status int
	msg    string
}

func (e r2StatusError) Error() string { return e.msg }

func multipartStatusError(status int, body []byte) error {
	errStr := string(body)
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		if strings.Contains(errStr, "RequestTimeTooSkewed") {
			return r2StatusError{status, "R2 request rejected: local clock is out of sync; check your system clock"}
		}
		return r2StatusError{status, "R2 credentials rejected; check your Access Key ID and Secret Access Key, then run yz setup"}
	}
	if status == http.StatusNotFound {
		return r2StatusError{status, "R2 bucket not found; run yz setup"}
	}
	if status >= 500 {
		return r2StatusError{status, fmt.Sprintf("R2 service error (HTTP %d); check your connection and retry", status)}
	}
	return r2StatusError{status, fmt.Sprintf("R2 multipart upload returned HTTP %d; run yz setup", status)}
}
