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
func TestStartupInitBudget(t *testing.T) {
	const (
		totalBudgetMs  = 5.0 // measured: ~0.6ms
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
	if total > totalBudgetMs {
		t.Errorf("package init CPU %.3f ms exceeds budget %.1f ms; heaviest: %s (%.3f ms) — see PERF.md", total, totalBudgetMs, maxPkg, maxSingle)
	}
	if maxSingle > singleBudgetMs {
		t.Errorf("package %s init CPU %.3f ms exceeds single-package budget %.1f ms — move eager work behind sync.Once", maxPkg, maxSingle, singleBudgetMs)
	}
}
