package desktop

import (
	"runtime"
	"testing"
)

func TestBrowserFromEnv(t *testing.T) {
	target := "https://dash.cloudflare.com/abc/r2/overview"
	t.Setenv("BROWSER", "firefox %s:chromium --incognito")
	got := browserFromEnv(target)
	if len(got) != 2 || len(got[0]) != 2 || got[0][0] != "firefox" || got[0][1] != target {
		t.Fatalf("first command = %#v", got)
	}
	if len(got[1]) != 3 || got[1][0] != "chromium" || got[1][1] != "--incognito" || got[1][2] != target {
		t.Fatalf("second command = %#v", got[1])
	}
}

func TestBrowserFromEnvBlank(t *testing.T) {
	t.Setenv("BROWSER", "  :  ")
	if cmds := browserFromEnv("https://example.com"); len(cmds) != 0 {
		t.Fatalf("blank BROWSER = %#v", cmds)
	}
}

func TestLaunchCommandsPreferBrowserThenOS(t *testing.T) {
	target := "https://example.com/a"
	t.Setenv("BROWSER", "my-browser")
	cmds := launchCommands(target)
	if len(cmds) == 0 || cmds[0][0] != "my-browser" || cmds[0][len(cmds[0])-1] != target {
		t.Fatalf("commands = %#v", cmds)
	}
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
		if len(cmds) < 2 {
			t.Fatalf("missing OS fallback: %#v", cmds)
		}
	}
}
