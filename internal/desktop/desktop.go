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

// OpenURL opens an HTTP(S) link. When BROWSER is set it is a colon-separated
// list of commands: a %s placeholder is replaced with the URL, otherwise the
// URL is appended. If those commands fail, the OS launcher is used
// (open, xdg-open, or explorer.exe).
func OpenURL(ctx context.Context, target string) error {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("invalid browser URL")
	}
	commands := launchCommands(target)
	if len(commands) == 0 {
		return errors.New("browser opening unavailable on this OS")
	}
	var lastErr error
	for _, args := range commands {
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := exec.CommandContext(attempt, args[0], args[1:]...).Run()
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastErr
}

// browserFromEnv parses the BROWSER convention used by sensible-browser: a
// colon-separated list, with an optional %s for the URL.
func browserFromEnv(target string) [][]string {
	raw := strings.TrimSpace(os.Getenv("BROWSER"))
	if raw == "" {
		return nil
	}
	var commands [][]string
	for part := range strings.SplitSeq(raw, ":") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var args []string
		if strings.Contains(part, "%s") {
			args = strings.Fields(strings.ReplaceAll(part, "%s", target))
		} else {
			args = append(strings.Fields(part), target)
		}
		if len(args) == 0 || args[0] == "" {
			continue
		}
		commands = append(commands, args)
	}
	return commands
}

func defaultLauncher(target string) []string {
	var name string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name = "explorer.exe"
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly":
		name = "xdg-open"
	default:
		return nil
	}
	return []string{name, target}
}

func launchCommands(target string) [][]string {
	commands := browserFromEnv(target)
	if fallback := defaultLauncher(target); fallback != nil {
		commands = append(commands, fallback)
	}
	return commands
}

// ClipboardNotice reports copy success without treating a desktop failure as
// an upload failure.
func ClipboardNotice(err error) string {
	if err != nil {
		return "Couldn't copy URL to clipboard · use the printed link"
	}
	return "✓ URL copied to clipboard"
}
