//go:build darwin

package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ClipboardFile reads a file, image, or text from the macOS clipboard.
// A copied Finder file is returned as-is. Images and text are written to a
// temp file, which cleanup removes.
func ClipboardFile() (path string, cleanup func(), err error) {
	noop := func() {}
	dir, err := os.MkdirTemp("", "yz-clip-")
	if err != nil {
		return "", noop, fmt.Errorf("yz: clipboard: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return clipboardFromScript(ctx, dir, cleanup, clipboardScript, true)
}

// ClipboardImage uploads a screenshot or other image on the clipboard.
// Text and copied files are ignored.
func ClipboardImage() (path string, cleanup func(), err error) {
	noop := func() {}
	dir, err := os.MkdirTemp("", "yz-clip-")
	if err != nil {
		return "", noop, fmt.Errorf("yz: clipboard: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return clipboardFromScript(ctx, dir, cleanup, clipboardImageScript, false)
}

func clipboardFromScript(ctx context.Context, dir string, cleanup func(), script string, allowText bool) (string, func(), error) {
	noop := func() {}
	var path string
	cmd := exec.CommandContext(ctx, "osascript", "-e", script, dir)
	out, err := cmd.Output()
	if err != nil {
		cleanup()
		if ctx.Err() != nil {
			return "", noop, errors.New("yz: reading the clipboard timed out")
		}
		return "", noop, errors.New("yz: could not read the clipboard")
	}
	line := strings.TrimSpace(string(out))
	switch {
	case strings.HasPrefix(line, "file:"):
		path = strings.TrimPrefix(line, "file:")
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() {
			cleanup()
			return "", noop, errors.New("yz: clipboard file is not readable")
		}
		return path, cleanup, nil
	case strings.HasPrefix(line, "wrote:"):
		path = strings.TrimPrefix(line, "wrote:")
		if !strings.HasPrefix(path, dir+string(os.PathSeparator)) {
			cleanup()
			return "", noop, errors.New("yz: clipboard image was not written")
		}
		return path, cleanup, nil
	case line == "text" && allowText:
		paste := exec.CommandContext(ctx, "pbpaste")
		raw, pasteErr := paste.Output()
		if pasteErr != nil || len(bytes.TrimSpace(raw)) == 0 {
			cleanup()
			return "", noop, errors.New("yz: clipboard is empty")
		}
		path = filepath.Join(dir, "paste.txt")
		if writeErr := os.WriteFile(path, raw, 0o600); writeErr != nil {
			cleanup()
			return "", noop, fmt.Errorf("yz: clipboard: %w", writeErr)
		}
		return path, cleanup, nil
	default:
		cleanup()
		if allowText {
			return "", noop, errors.New("yz: clipboard is empty")
		}
		return "", noop, errors.New("yz: no image on the clipboard")
	}
}

const clipboardScript = `
on run argv
	set outDir to item 1 of argv
	try
		set furl to the clipboard as «class furl»
		set p to POSIX path of furl
		do shell script "test -f " & quoted form of p
		return "file:" & p
	end try
	try
		set pngData to the clipboard as «class PNGf»
		set dest to outDir & "/paste.png"
		set fh to open for access POSIX file dest with write permission
		set eof of fh to 0
		write pngData to fh
		close access fh
		return "wrote:" & dest
	end try
	try
		set tiffData to the clipboard as «class TIFF»
		set dest to outDir & "/paste.tiff"
		set fh to open for access POSIX file dest with write permission
		set eof of fh to 0
		write tiffData to fh
		close access fh
		return "wrote:" & dest
	end try
	return "text"
end run
`

const clipboardImageScript = `
on run argv
	set outDir to item 1 of argv
	try
		set pngData to the clipboard as «class PNGf»
		set dest to outDir & "/paste.png"
		set fh to open for access POSIX file dest with write permission
		set eof of fh to 0
		write pngData to fh
		close access fh
		return "wrote:" & dest
	end try
	try
		set tiffData to the clipboard as «class TIFF»
		set dest to outDir & "/paste.tiff"
		set fh to open for access POSIX file dest with write permission
		set eof of fh to 0
		write tiffData to fh
		close access fh
		return "wrote:" & dest
	end try
	return "empty"
end run
`
