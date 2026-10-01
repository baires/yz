package share

import (
	"io"
	"sync/atomic"
)

type countingReader struct {
	r        io.Reader
	sent     atomic.Int64
	total    int64
	progress func(sent, total int64)
}

func newCountingReader(r io.Reader, total int64, progress func(int64, int64)) *countingReader {
	return &countingReader{r: r, total: total, progress: progress}
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 && c.progress != nil {
		c.progress(c.sent.Add(int64(n)), c.total)
	}
	return n, err
}
