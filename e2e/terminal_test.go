package e2e

import (
	"maps"
	"strings"
	"testing"
)

func TestTerminalBranding(t *testing.T) {
	scenario(t, "terminal help shows yz title", func(t *testing.T) {
		run := startTerminal(t, nil, "--help")
		run.Await("yz  /  share")
		res := run.Wait()
		if res.ExitCode != 2 {
			t.Fatalf("exit code = %d, want 2; stderr: %q", res.ExitCode, res.Stderr)
		}
		if res.Stdout != "" {
			t.Fatalf("stdout = %q, want empty", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "█▄▄▄▄█") || !strings.Contains(res.Stderr, "Share files as links") {
			t.Fatalf("stderr = %q, want the static mascot and introduction", res.Stderr)
		}
		run.AssertTerminalRestored()
	})
}

func TestTerminalPlainFallback(t *testing.T) {
	scenario(t, "plain and dumb terminals emit no ANSI", func(t *testing.T) {
		piped := runYz(t, nil, "--help")
		if piped.ExitCode != 2 {
			t.Fatalf("piped exit = %d, want 2; stderr: %q", piped.ExitCode, piped.Stderr)
		}
		if strings.Contains(piped.Stderr, "\x1b") {
			t.Fatalf("piped stderr contains ANSI: %q", piped.Stderr)
		}
		if !strings.Contains(piped.Stderr, plainUsage()) {
			t.Fatalf("piped stderr = %q, want %q", piped.Stderr, plainUsage())
		}
		if titleLine(piped.Stderr) {
			t.Fatalf("piped stderr has TUI title: %q", piped.Stderr)
		}

		run := startTerminal(t, map[string]string{"TERM": "dumb"}, "--help")
		res := run.Wait()
		if res.ExitCode != 2 {
			t.Fatalf("dumb exit = %d, want 2; stderr: %q", res.ExitCode, res.Stderr)
		}
		if strings.Contains(res.Stderr, "\x1b") {
			t.Fatalf("TERM=dumb stderr contains ANSI: %q", res.Stderr)
		}
		if titleLine(res.Stderr) {
			t.Fatalf("TERM=dumb stderr has TUI title: %q", res.Stderr)
		}
		if !strings.Contains(res.Stderr, plainUsage()) {
			t.Fatalf("TERM=dumb stderr = %q, want %q", res.Stderr, plainUsage())
		}
		run.AssertTerminalRestored()
	})
}

func TestTerminalSafeDisplay(t *testing.T) {
	scenario(t, "malicious path cannot emit terminal controls", func(t *testing.T) {
		_, _, _, env := shareFixture(t, "https://files.example")
		name := maliciousName()
		path := env["YZ_CONFIG_DIR"] + "/" + name

		run := startTerminal(t, env, path)
		res := run.Wait()
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1; stderr: %q", res.ExitCode, res.Stderr)
		}
		if res.Stdout != "" {
			t.Fatalf("stdout = %q, want empty", res.Stdout)
		}
		requireNoControls(t, "tty stderr", res.Stderr)
		if !titleLine(res.Stderr) {
			t.Fatalf("stderr = %q, want a %q title line", res.Stderr, "yz  /  share")
		}
		if !strings.Contains(res.Stderr, "naïve") || !strings.Contains(res.Stderr, "hidden.txt") {
			t.Fatalf("stderr = %q, want readable name fragments", res.Stderr)
		}
		if strings.Contains(res.Stderr, "\rINJECTED") || strings.Contains(res.Stderr, "\nINJECTED") {
			t.Fatalf("stderr forged an injected line: %q", res.Stderr)
		}
		run.AssertTerminalRestored()

		dumb := startTerminal(t, mergeEnv(env, map[string]string{"TERM": "dumb"}), path)
		dumbRes := dumb.Wait()
		if dumbRes.ExitCode != 1 {
			t.Fatalf("dumb exit = %d, want 1; stderr: %q", dumbRes.ExitCode, dumbRes.Stderr)
		}
		requireNoControls(t, "TERM=dumb stderr", dumbRes.Stderr)
		if titleLine(dumbRes.Stderr) {
			t.Fatalf("TERM=dumb stderr has TUI title: %q", dumbRes.Stderr)
		}

		piped := runYz(t, env, path)
		if piped.ExitCode != 1 {
			t.Fatalf("piped exit = %d, want 1; stderr: %q", piped.ExitCode, piped.Stderr)
		}
		requireNoControls(t, "piped stderr", piped.Stderr)
		if !strings.Contains(piped.Stderr, "naïve") {
			t.Fatalf("piped stderr = %q, want readable name", piped.Stderr)
		}
	})
}

func mergeEnv(base, extra map[string]string) map[string]string {
	out := map[string]string{}
	maps.Copy(out, base)
	maps.Copy(out, extra)
	return out
}
