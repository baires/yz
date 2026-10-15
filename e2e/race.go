//go:build race

package e2e

// raceEnabled reports that this test binary was built with the race detector.
// make race passes -race to go test; init CPU clocks are not comparable to
// the normal startup budget in that configuration.
const raceEnabled = true
