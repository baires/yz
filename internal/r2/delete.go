package r2

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

func (c S3) signedDo(ctx context.Context, method, target string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, fmt.Errorf("creating R2 request: %w", err)
	}
	signHead(req, c.Credentials, time.Now())
	resp, err := s3Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("R2 connection failed; check your connection and retry")
	}
	return resp, nil
}

func statusError(op string, resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrCredentialsRejected
	}
	return fmt.Errorf("R2 %s returned HTTP %d", op, resp.StatusCode)
}

// DeleteObject removes one object. A missing object is not an error.
func (c S3) DeleteObject(ctx context.Context, bucket, key string) error {
	target, err := c.objectURL(bucket, key)
	if err != nil {
		return err
	}
	resp, err := c.signedDo(ctx, http.MethodDelete, target)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return statusError("delete", resp)
}

// ListKeys returns every key under prefix using ListObjectsV2 pagination.
func (c S3) ListKeys(ctx context.Context, bucket, prefix string) ([]string, error) {
	base, err := c.objectURL(bucket, "")
	if err != nil {
		return nil, err
	}
	base = strings.TrimRight(base, "/")
	var keys []string
	token := ""
	for range 1000 {
		q := url.Values{"list-type": {"2"}, "prefix": {prefix}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		resp, err := c.signedDo(ctx, http.MethodGet, base+"?"+canonicalQuery(q))
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, statusError("list", resp)
		}
		var out struct {
			Contents []struct {
				Key string `xml:"Key"`
			} `xml:"Contents"`
			IsTruncated           bool   `xml:"IsTruncated"`
			NextContinuationToken string `xml:"NextContinuationToken"`
		}
		err = xml.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&out)
		_ = resp.Body.Close()
		if err != nil {
			return nil, errors.New("invalid R2 list response")
		}
		for _, o := range out.Contents {
			if strings.HasPrefix(o.Key, prefix) {
				keys = append(keys, o.Key)
			}
		}
		if !out.IsTruncated || out.NextContinuationToken == "" {
			return keys, nil
		}
		token = out.NextContinuationToken
	}
	return nil, errors.New("R2 list exceeded 1000 pages")
}

// DeletePrefix removes every object under prefix and returns how many.
func (c S3) DeletePrefix(ctx context.Context, bucket, prefix string) (int, error) {
	if prefix == "" || !strings.HasSuffix(prefix, "/") {
		return 0, errors.New("refusing to delete without a directory prefix")
	}
	keys, err := c.ListKeys(ctx, bucket, prefix)
	if err != nil {
		return 0, err
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for _, k := range keys {
		g.Go(func() error { return c.DeleteObject(gctx, bucket, k) })
	}
	return len(keys), g.Wait()
}
