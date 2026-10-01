package r2

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// PutObject streams body into the specified bucket and key using SigV4 authorization.
func (c S3) PutObject(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	if _, err := accountPath(c.AccountID); err != nil {
		return err
	}
	if !bucketName.MatchString(bucket) {
		return errors.New("invalid R2 bucket name; run yz setup")
	}
	if !ValidHex(c.Credentials.AccessKeyID, 32) || !ValidHex(c.Credentials.Secret, 64) {
		return errors.New("R2 credentials rejected; check your Access Key ID and Secret Access Key, then run yz setup")
	}

	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = "https://" + c.AccountID + ".r2.cloudflarestorage.com"
	}

	cleanPath := "/" + bucket + "/" + URIEncode(strings.TrimPrefix(key, "/"), false)
	targetURL := strings.TrimRight(endpoint, "/") + cleanPath

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, body)
	if err != nil {
		return fmt.Errorf("creating R2 upload request: %w", err)
	}
	req.ContentLength = size
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	payloadSha := "UNSIGNED-PAYLOAD"
	if size == 0 {
		payloadSha = fmt.Sprintf("%x", sha256.Sum256(nil))
	}
	SignRequest(req, c.Credentials, time.Now(), payloadSha)

	resp, err := s3Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("R2 connection failed; check your connection and run yz setup")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusCreated {
		return nil
	}

	errBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	errStr := string(errBytes)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		if strings.Contains(errStr, "RequestTimeTooSkewed") {
			return errors.New("R2 request rejected: local clock is out of sync; check your system clock")
		}
		return errors.New("R2 credentials rejected; check your Access Key ID and Secret Access Key, then run yz setup")
	}

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("R2 bucket %q not found; run yz setup", bucket)
	}

	if resp.StatusCode >= 500 {
		return fmt.Errorf("R2 service error (HTTP %d); check your connection and retry", resp.StatusCode)
	}

	return fmt.Errorf("R2 upload returned HTTP %d; run yz setup", resp.StatusCode)
}

// PresignGet creates a presigned GET URL for an object in the specified bucket and key.
func (c S3) PresignGet(bucket, key string, expires time.Duration, now time.Time) (string, error) {
	if !ValidHex(c.Credentials.AccessKeyID, 32) || !ValidHex(c.Credentials.Secret, 64) {
		return "", errors.New("R2 credentials rejected; check your Access Key ID and Secret Access Key, then run yz setup")
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = "https://" + c.AccountID + ".r2.cloudflarestorage.com"
	}
	return PresignURL(endpoint, bucket, key, c.Credentials, expires, now)
}
