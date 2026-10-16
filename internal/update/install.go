package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Method identifies how this binary was installed.
type Method int

const (
	// MethodDirect is a binary yz update may replace.
	MethodDirect Method = iota
	// MethodBrew is a Homebrew install.
	MethodBrew
	// MethodGo is a go install copy under GOPATH/bin or GOBIN.
	MethodGo
)

// InstallMethod reports how exe was installed. Homebrew matches a path under
// Cellar or homebrew, including the symlink in the prefix bin. go install
// matches GOBIN, any GOPATH/bin, or ~/go/bin when GOPATH is unset.
func InstallMethod(exe string) Method {
	if looksBrew(exe) || looksBrew(resolveExe(exe)) {
		return MethodBrew
	}
	dir := filepath.Dir(exe)
	for _, bin := range goBinDirs() {
		if sameDir(dir, bin) {
			return MethodGo
		}
	}
	return MethodDirect
}

// ManagedMessage tells the user how to update a package-manager install.
func ManagedMessage(m Method) string {
	switch m {
	case MethodBrew:
		return "yz is installed with Homebrew. Run: brew upgrade yz"
	case MethodGo:
		return "yz is installed with go install. Run: go install github.com/baires/yz/cmd/yz@latest"
	default:
		return ""
	}
}

// Replace atomically swaps path for data. The temporary file is created in
// the same directory so the rename does not tear the running binary.
func Replace(path string, data []byte) error {
	if path == "" {
		return errors.New("empty binary path")
	}
	if len(data) == 0 {
		return errors.New("empty release binary")
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".yz-update-*")
	if err != nil {
		return fmt.Errorf("creating temp binary: %w", err)
	}
	tmpName := tmp.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()
	mode := os.FileMode(0o755)
	if info, err := os.Stat(path); err == nil {
		if perm := info.Mode().Perm(); perm&0o111 != 0 {
			mode = perm
		}
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setting permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp binary: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing temp binary: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	success = true
	return nil
}

func looksBrew(path string) bool {
	slash := strings.ToLower(filepath.ToSlash(path))
	return strings.Contains(slash, "/cellar/") || strings.Contains(slash, "/homebrew/")
}

func resolveExe(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved == "" {
		return path
	}
	return resolved
}

// sameDir reports whether two directories name the same location. macOS
// temp paths sit under /var, which is a symlink to /private/var, so a string
// compare of GOPATH/bin against the resolved executable misses go install.
func sameDir(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if a == b {
		return true
	}
	ar, aerr := filepath.EvalSymlinks(a)
	br, berr := filepath.EvalSymlinks(b)
	if aerr == nil && berr == nil && filepath.Clean(ar) == filepath.Clean(br) {
		return true
	}
	if aerr == nil && filepath.Clean(ar) == b {
		return true
	}
	if berr == nil && filepath.Clean(br) == a {
		return true
	}
	ai, astat := os.Stat(a)
	bi, bstat := os.Stat(b)
	if astat != nil || bstat != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

func goBinDirs() []string {
	var dirs []string
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		dirs = append(dirs, gobin)
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		for p := range strings.SplitSeq(gopath, string(os.PathListSeparator)) {
			if p != "" {
				dirs = append(dirs, filepath.Join(p, "bin"))
			}
		}
		return dirs
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "go", "bin"))
	}
	return dirs
}
