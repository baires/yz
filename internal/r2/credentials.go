package r2

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ErrCredentialsRejected means the signed check was refused. The text is the
// user-facing message; wrap it so callers can classify the failure.
var ErrCredentialsRejected = errors.New("R2 credentials rejected; check the account, bucket, and Object Read & Write permission, then run yz setup")

// Credentials holds the long-lived R2 S3 pair supplied by the user.
type Credentials struct {
	AccessKeyID string
	Secret      string
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(value))
	return mac.Sum(nil)
}

// signHead delegates to SignRequest with an empty-body payload hash.
func signHead(req *http.Request, creds Credentials, now time.Time) {
	SignRequest(req, creds, now, fmt.Sprintf("%x", sha256.Sum256(nil)))
}

// S3 carries the account endpoint and credential pair for S3 requests.
type S3 struct {
	AccountID   string
	Endpoint    string
	Credentials Credentials
}

// CheckBucket checks the pair against the chosen bucket with a signed
// HEAD. This checks access without uploading or exposing any object.
func (c S3) CheckBucket(ctx context.Context, bucket string) error {
	if _, err := accountPath(c.AccountID); err != nil {
		return err
	}
	if !bucketName.MatchString(bucket) {
		return errors.New("invalid R2 bucket name; run yz setup")
	}
	if !ValidHex(c.Credentials.AccessKeyID, 32) || !ValidHex(c.Credentials.Secret, 64) {
		return errors.New("invalid R2 credentials; paste the Access Key ID and Secret Access Key from the R2 dashboard")
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = "https://" + c.AccountID + ".r2.cloudflarestorage.com"
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodHead,
		strings.TrimRight(endpoint, "/")+"/"+bucket,
		nil,
	)
	if err != nil {
		return errors.New("invalid R2 endpoint; run yz setup")
	}
	if req.URL.RawQuery != "" || req.URL.User != nil {
		return errors.New("invalid R2 endpoint; run yz setup")
	}
	signHead(req, c.Credentials, time.Now())
	resp, err := s3Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("R2 connection failed; check your connection and run yz setup")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return fmt.Errorf("%w", ErrCredentialsRejected)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("R2 credential check returned HTTP %d; check the bucket and run yz setup", resp.StatusCode)
	}
	return nil
}
