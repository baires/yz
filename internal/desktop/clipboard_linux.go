//go:build linux

package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ClipboardImage reads a PNG or JPEG from the clipboard via wl-paste or xclip.
func ClipboardImage() (path string, cleanup func(), err error) {
	noop := func() {}
	dir, err := os.MkdirTemp("", "yz-clip-")
	if err != nil {
		return "", noop, fmt.Errorf("yz: clipboard: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	attempts := []struct {
		args   []string
		name   string
		magics [][]byte
	}{
		{[]string{"wl-paste", "--no-newline", "--type", "image/png"}, "paste.png", [][]byte{{0x89, 'P', 'N', 'G'}}},
		{[]string{"xclip", "-selection", "clipboard", "-t", "image/png", "-o"}, "paste.png", [][]byte{{0x89, 'P', 'N', 'G'}}},
		{[]string{"wl-paste", "--no-newline", "--type", "image/jpeg"}, "paste.jpg", [][]byte{{0xff, 0xd8, 0xff}}},
		{[]string{"xclip", "-selection", "clipboard", "-t", "image/jpeg", "-o"}, "paste.jpg", [][]byte{{0xff, 0xd8, 0xff}}},
	}
	for _, attempt := range attempts {
		cmd := exec.CommandContext(ctx, attempt.args[0], attempt.args[1:]...)
		raw, runErr := cmd.Output()
		if runErr != nil || len(raw) == 0 {
			if ctx.Err() != nil {
				cleanup()
				return "", noop, errors.New("yz: reading the clipboard timed out")
			}
			continue
		}
		ok := false
		for _, magic := range attempt.magics {
			if bytes.HasPrefix(raw, magic) {
				ok = true
				break
			}
		}
		if !ok {
			continue
		}
		path = filepath.Join(dir, attempt.name)
		if writeErr := os.WriteFile(path, raw, 0o600); writeErr != nil {
			cleanup()
			return "", noop, fmt.Errorf("yz: clipboard: %w", writeErr)
		}
		return path, cleanup, nil
	}
	cleanup()
	return "", noop, errors.New("yz: no image on the clipboard")
}
