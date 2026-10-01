package e2e

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// driverBuffer captures subprocess diagnostics while failure paths read them.
type driverBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *driverBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *driverBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type driverResponse struct {
	ID         int             `json:"id"`
	Op         string          `json:"op"`
	OK         bool            `json:"ok"`
	Error      string          `json:"error"`
	ExitCode   int             `json:"exit_code"`
	StdoutB64  string          `json:"stdout_b64"`
	StderrB64  string          `json:"stderr_b64"`
	ScreenB64  string          `json:"screen_b64"`
	FinalMode  json.RawMessage `json:"final_mode"`
	ModesEqual bool            `json:"modes_equal"`
}

type terminalRun struct {
	t          *testing.T
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	dec        *json.Decoder
	encMu      chan struct{}
	enc        *json.Encoder
	pyErr      *driverBuffer
	nextID     int
	waited     bool
	closed     bool
	result     runResult
	modesEqual bool
	finalMode  string
}

func startTerminal(t *testing.T, env map[string]string, args ...string) *terminalRun {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 is required for terminal tests: %v", err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locating terminal driver")
	}
	stdoutPath := filepath.Join(t.TempDir(), "stdout")
	driver := filepath.Join(filepath.Dir(file), "pty_driver.py")
	cmdArgs := append([]string{driver, "--stdout-file", stdoutPath, "--"}, append([]string{binPath}, args...)...)
	cmd := exec.Command(python, cmdArgs...)
	cmd.Env = terminalEnv(t, env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var pyErr driverBuffer
	cmd.Stderr = &pyErr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting pty driver: %v", err)
	}
	run := &terminalRun{
		t:     t,
		cmd:   cmd,
		stdin: stdin,
		dec:   json.NewDecoder(stdout),
		enc:   json.NewEncoder(stdin),
		pyErr: &pyErr,
		encMu: make(chan struct{}, 1),
	}
	run.encMu <- struct{}{}
	t.Cleanup(run.close)
	ready := run.read()
	if ready.Op != "ready" || !ready.OK {
		t.Fatalf("pty driver not ready: %s\ndriver stderr: %s", ready.Error, pyErr.String())
	}
	return run
}

func terminalEnv(t *testing.T, env map[string]string) []string {
	t.Helper()
	merged := map[string]string{}
	for _, kv := range os.Environ() {
		key, val, found := splitEnv(kv)
		if !found || key == "NO_COLOR" || key == "TERM" {
			continue
		}
		merged[key] = val
	}
	merged["TERM"] = "xterm-256color"
	if _, exists := env["YZ_CONFIG_DIR"]; !exists {
		merged["YZ_CONFIG_DIR"] = t.TempDir()
	}
	for key, val := range env {
		if val == "" && (key == "NO_COLOR" || key == "TERM") {
			delete(merged, key)
			continue
		}
		merged[key] = val
	}
	out := make([]string, 0, len(merged))
	for key, val := range merged {
		out = append(out, key+"="+val)
	}
	return out
}

func splitEnv(kv string) (string, string, bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return "", "", false
}

func (r *terminalRun) Send(s string) {
	r.t.Helper()
	r.roundtrip("send", map[string]any{"data": s})
}

func (r *terminalRun) Resize(cols, rows int) {
	r.t.Helper()
	r.roundtrip("resize", map[string]any{"cols": cols, "rows": rows})
}

func (r *terminalRun) Await(text string) string {
	r.t.Helper()
	return r.AwaitFor(text, 5000)
}

func (r *terminalRun) AwaitFor(text string, timeoutMS int) string {
	r.t.Helper()
	resp := r.roundtrip("await", map[string]any{"text": text, "timeout_ms": timeoutMS})
	stderr, _ := decodeB64(resp.StderrB64)
	screen, _ := decodeB64(resp.ScreenB64)
	if !resp.OK {
		r.t.Fatalf("await %q: %s\nstderr: %q\ndriver: %s", text, resp.Error, stderr, r.pyErr.String())
	}
	return stderr + "\n" + screen
}

func (r *terminalRun) Peek() (stdout, stderr string) {
	r.t.Helper()
	resp := r.roundtrip("peek", map[string]any{})
	stdout, err := decodeB64(resp.StdoutB64)
	if err != nil {
		r.t.Fatalf("decode peek stdout: %v", err)
	}
	stderr, err = decodeB64(resp.StderrB64)
	if err != nil {
		r.t.Fatalf("decode peek stderr: %v", err)
	}
	return stdout, stderr
}

func (r *terminalRun) Hangup() {
	r.t.Helper()
	r.roundtrip("hangup", map[string]any{})
}

func (r *terminalRun) Interrupt() {
	r.t.Helper()
	r.roundtrip("interrupt", map[string]any{})
}

func (r *terminalRun) Wait() runResult {
	r.t.Helper()
	if r.waited {
		return r.result
	}
	resp := r.roundtrip("wait", map[string]any{"timeout_ms": 10000})
	stdout, err := decodeB64(resp.StdoutB64)
	if err != nil {
		r.t.Fatalf("decode stdout: %v", err)
	}
	stderr, err := decodeB64(resp.StderrB64)
	if err != nil {
		r.t.Fatalf("decode stderr: %v", err)
	}
	r.result = runResult{Stdout: stdout, Stderr: stderr, ExitCode: resp.ExitCode}
	r.modesEqual = resp.ModesEqual
	r.finalMode = string(resp.FinalMode)
	r.waited = true
	r.close()
	return r.result
}

func (r *terminalRun) AssertTerminalRestored() {
	r.t.Helper()
	if !r.waited {
		r.Wait()
	}
	if !r.modesEqual {
		r.t.Fatalf("terminal mode not restored, final mode %s", r.finalMode)
	}
}

func (r *terminalRun) roundtrip(op string, fields map[string]any) driverResponse {
	r.t.Helper()
	r.nextID++
	msg := map[string]any{"id": r.nextID, "op": op}
	maps.Copy(msg, fields)
	select {
	case <-r.encMu:
	case <-time.After(5 * time.Second):
		r.t.Fatalf("pty driver encode lock timed out\ndriver: %s", r.pyErr.String())
	}
	err := r.enc.Encode(msg)
	r.encMu <- struct{}{}
	if err != nil {
		r.t.Fatalf("writing %s: %v\ndriver: %s", op, err, r.pyErr.String())
	}
	return r.read()
}

func (r *terminalRun) read() driverResponse {
	r.t.Helper()
	done := make(chan struct{})
	var resp driverResponse
	var err error
	go func() {
		err = r.dec.Decode(&resp)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		r.t.Fatalf("pty driver response timed out\ndriver: %s", r.pyErr.String())
	}
	if err != nil {
		r.t.Fatalf("reading pty driver: %v\ndriver: %s", err, r.pyErr.String())
	}
	return resp
}

func (r *terminalRun) close() {
	if r.closed {
		return
	}
	r.closed = true
	if r.stdin != nil {
		_ = r.stdin.Close()
	}
	if r.cmd.Process == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_ = r.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = r.cmd.Process.Kill()
		<-done
	}
}

func decodeB64(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func requireNoControls(t *testing.T, label, text string) {
	t.Helper()
	if strings.Contains(text, "\x1b]") || strings.Contains(text, "\x1b[31m") || strings.Contains(text, "\x07") {
		t.Fatalf("%s contains terminal control payload: %q", label, text)
	}
	for _, r := range text {
		if r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			t.Fatalf("%s contains bidi format U+%04X: %q", label, r, text)
		}
	}
}

var sgrPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func titleLine(text string) bool {
	clean := sgrPattern.ReplaceAllString(text, "")
	for line := range bytes.SplitSeq([]byte(clean), []byte("\n")) {
		if bytes.Equal(bytes.TrimSpace(line), []byte("yz  /  share")) {
			return true
		}
	}
	return false
}

func plainUsage() string {
	return "usage: yz [--signed] [--expires 24h] [--domain HOST] <file>"
}

func maliciousName() string {
	return "naïve-\x1b]0;pwned\x07\x1b[31mred\x1b[0m-\rINJECTED\u202ehidden.txt"
}

func TestDriverBufferConcurrentDiagnostics(t *testing.T) {
	var buffer driverBuffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			_, _ = buffer.Write([]byte("diagnostic\n"))
		}
	}()
	for range 1000 {
		_ = buffer.String()
	}
	<-done
	if got := strings.Count(buffer.String(), "diagnostic\n"); got != 1000 {
		t.Fatalf("captured %d diagnostics, want 1000", got)
	}
}
