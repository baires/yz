// Package desktop provides best-effort clipboard and browser integration.
package desktop

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// CopyURL writes the exact URL, without a trailing newline, to the clipboard.
func CopyURL(ctx context.Context, target string) error {
	var commands [][]string
	switch runtime.GOOS {
	case "darwin":
		commands = [][]string{{"pbcopy"}}
	case "windows":
		commands = [][]string{{"clip.exe"}}
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly":
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			commands = append(commands, []string{"wl-copy"})
		}
		commands = append(commands,
			[]string{"xclip", "-selection", "clipboard"},
			[]string{"xsel", "--clipboard", "--input"},
		)
	default:
		return errors.New("clipboard unavailable on this OS")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var lastErr error
	for _, args := range commands {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Stdin = strings.NewReader(target)
		if err := cmd.Run(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastErr
}

// OpenURL opens an HTTP(S) link using the OS browser launcher.
func OpenURL(ctx context.Context, target string) error {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("invalid browser URL")
	}
	var name string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name = "explorer.exe"
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly":
		name = "xdg-open"
	default:
		return errors.New("browser opening unavailable on this OS")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, target).Run()
}

// ClipboardNotice reports copy success without treating a desktop failure as
// an upload failure.
func ClipboardNotice(err error) string {
	if err != nil {
		return "Couldn't copy URL to clipboard · use the printed link"
	}
	return "✓ URL copied to clipboard"
}
