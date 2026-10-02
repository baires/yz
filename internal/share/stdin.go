package share

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// SpoolStdin copies r to a temp file named from the payload (paste.png, paste.tar, ...).
// The caller must remove the returned directory when the upload finishes.
func SpoolStdin(r io.Reader) (dir, path string, err error) {
	dir, err = os.MkdirTemp("", "yz-stdin-")
	if err != nil {
		return "", "", fmt.Errorf("yz: stdin: %w", err)
	}
	head := make([]byte, 512)
	n, readErr := io.ReadFull(r, head)
	if n == 0 {
		_ = os.RemoveAll(dir)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return "", "", fmt.Errorf("yz: reading stdin: %w", readErr)
		}
		return "", "", fmt.Errorf("yz: stdin is empty")
	}
	name := sniffPasteName(head[:n])
	path = filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", "", fmt.Errorf("yz: stdin: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(head[:n]); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", fmt.Errorf("yz: stdin: %w", err)
	}
	if readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		if _, err := io.Copy(f, r); err != nil {
			_ = os.RemoveAll(dir)
			return "", "", fmt.Errorf("yz: reading stdin: %w", err)
		}
	}
	return dir, path, nil
}

func sniffPasteName(head []byte) string {
	switch {
	case bytes.HasPrefix(head, []byte{0x89, 'P', 'N', 'G'}):
		return "paste.png"
	case bytes.HasPrefix(head, []byte{0xff, 0xd8, 0xff}):
		return "paste.jpg"
	case bytes.HasPrefix(head, []byte("GIF8")):
		return "paste.gif"
	case bytes.HasPrefix(head, []byte("%PDF")):
		return "paste.pdf"
	case bytes.HasPrefix(head, []byte{0x1f, 0x8b}):
		return "paste.gz"
	case len(head) > 262 && bytes.Equal(head[257:262], []byte("ustar")):
		return "paste.tar"
	case bytes.HasPrefix(head, []byte("II*\x00")) || bytes.HasPrefix(head, []byte("MM\x00*")):
		return "paste.tiff"
	default:
		return "paste.bin"
	}
}
