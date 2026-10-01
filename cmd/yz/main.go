// Command yz uploads files to Cloudflare R2 and prints share URLs.
package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	os.Exit(Execute(os.Args[1:], os.Stdout, os.Stderr))
}

// Execute dispatches to the requested command and returns the process exit
// code: 0 success, 1 runtime error, 2 usage error.
func Execute(args []string, stdout, stderr io.Writer) int {
	env := readEnv()
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "setup":
			return runSetup(env, os.Stdin, stderr)
		case "version":
			return runVersion(stdout)
		}
	}
	return runShare(args, env, stdout, stderr)
}

// envConfig carries the environment overrides that let E2E tests point the
// binary at in-process fakes.
type envConfig struct {
	ConfigDir     string
	OAuthBaseURL  string
	OAuthClientID string
	APIBaseURL    string
	R2Endpoint    string
}

func readEnv() envConfig {
	dir := os.Getenv("YZ_CONFIG_DIR")
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".config", "yz")
		}
	}
	return envConfig{
		ConfigDir:     dir,
		OAuthBaseURL:  os.Getenv("YZ_OAUTH_BASE_URL"),
		OAuthClientID: os.Getenv("YZ_OAUTH_CLIENT_ID"),
		APIBaseURL:    os.Getenv("YZ_API_BASE_URL"),
		R2Endpoint:    os.Getenv("YZ_R2_ENDPOINT"),
	}
}
