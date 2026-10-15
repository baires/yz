package e2e

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestStartupInitBudget guards the startup-latency regression class: package
// initializers doing eager computation. A single 29ms init in
// github.com/mattn/go-runewidth once accounted for 90% of yz's startup
// (see PERF.md, attempt 1). The runtime reports per-package init CPU clocks
// with GODEBUG=inittrace=1 — CPU time, not wall time — so this budget stays
// stable on shared CI runners where wall-clock gates flake.
//
// The race detector is exempt. It instruments every write, so init CPU tracks
// the host instead of startup work: github.com/charmbracelet/x/ansi 0.11.9
// fills a Unicode zero-width bitset in init (~0.04ms normally) and that alone
// put a macOS race run at 5.6ms total. The same tree passed the 5ms gate on
// the previous macOS race run. make test keeps the strict budget.
func TestStartupInitBudget(t *testing.T) {
	const (
		totalBudgetMs  = 5.0 // measured: ~0.7ms without -race
		singleBudgetMs = 2.0
	)

	cmd := exec.Command(binPath, "version")
	cmd.Env = append(os.Environ(), "GODEBUG=inittrace=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running version with inittrace: %v\n%s", err, out)
	}

	var total, maxSingle float64
	var maxPkg string
	for line := range strings.Lines(string(out)) {
		// init <pkg> @<when> ms, <clock> ms clock, <bytes> bytes, <n> allocs
		fields := strings.Fields(line)
		if len(fields) < 7 || fields[0] != "init" {
			continue
		}
		clock, err := strconv.ParseFloat(fields[4], 64)
		if err != nil {
			t.Fatalf("unparseable inittrace line %q: %v", line, err)
		}
		total += clock
		if clock > maxSingle {
			maxSingle, maxPkg = clock, fields[1]
		}
	}
	if total == 0 {
		t.Fatalf("no inittrace lines parsed from:\n%s", out)
	}
	t.Logf("init clock total %.3f ms; heaviest %s %.3f ms", total, maxPkg, maxSingle)
	// YZ_E2E_RACE rebuilds the yz binary with -race (see TestMain). The race
	// build tag covers go test -race, which is how make race invokes this test.
	if raceEnabled || os.Getenv("YZ_E2E_RACE") == "1" {
		t.Skipf("race detector inflates init CPU (total %.3f ms, heaviest %s %.3f ms); strict %.1f ms budget applies to normal builds", total, maxPkg, maxSingle, totalBudgetMs)
	}
	if total > totalBudgetMs {
		t.Errorf("package init CPU %.3f ms exceeds budget %.1f ms; heaviest: %s (%.3f ms) — see PERF.md", total, totalBudgetMs, maxPkg, maxSingle)
	}
	if maxSingle > singleBudgetMs {
		t.Errorf("package %s init CPU %.3f ms exceeds single-package budget %.1f ms — move eager work behind sync.Once", maxPkg, maxSingle, singleBudgetMs)
	}
}
