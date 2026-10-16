package update

import "testing"

func TestParseAndCompare(t *testing.T) {
	ordered := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"1.0.1",
		"1.1.0",
		"2.0.0",
	}
	for i := range ordered {
		if !IsRelease(ordered[i]) {
			t.Fatalf("Parse(%q) failed", ordered[i])
		}
		for j := range ordered {
			got := mustParse(t, ordered[i]).Compare(mustParse(t, ordered[j]))
			want := 0
			if i > j {
				want = 1
			} else if i < j {
				want = -1
			}
			if got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}

	if mustParse(t, "v1.2.3").Compare(mustParse(t, "1.2.3")) != 0 {
		t.Fatal("v prefix should not change precedence")
	}
	if mustParse(t, "1.0.0+build.1").Compare(mustParse(t, "1.0.0+exp.sha")) != 0 {
		t.Fatal("build metadata should be ignored")
	}
	if Display("v1.4.0-rc.1") != "1.4.0-rc.1" {
		t.Fatalf("Display = %q", Display("v1.4.0-rc.1"))
	}
}

func TestParseRejects(t *testing.T) {
	for _, s := range []string{"", "dev", "e2e-test", "1.0", "1", "v1.2", "01.2.3", "1.02.3", "1.0.0-", "1.0.0-01", "1.0.0-alpha_1", "1.0.0-rc..1"} {
		if IsRelease(s) {
			t.Errorf("Parse(%q) succeeded", s)
		}
	}
}

func TestIsNewerAndNotices(t *testing.T) {
	if !IsNewer("v1.4.0", "v1.3.0") || !IsNewer("v1.4.0", "dev") {
		t.Fatal("expected a newer release")
	}
	if IsNewer("v1.4.0", "v1.4.0") || IsNewer("v1.4.0", "v1.5.0") || IsNewer("nope", "v1.0.0") {
		t.Fatal("did not expect a newer release")
	}
	const want = "yz 1.4.0 is available, run yz update"
	if got := AvailableLine("v1.3.0", "v1.4.0"); got != want {
		t.Fatalf("AvailableLine = %q", got)
	}
	if AvailableLine("v1.4.0", "v1.4.0") != "" || AvailableLine("v1.4.0", "not a version") != "" {
		t.Fatal("notice should be empty")
	}
	if got := UpdatedLine("v1.4.0"); got != "yz updated to 1.4.0" {
		t.Fatalf("UpdatedLine = %q", got)
	}
	if UpdatedLine("\x1b[31mred") != "" {
		t.Fatal("invalid version leaked into the notice")
	}
}

func TestNoticePolicy(t *testing.T) {
	ok := NoticePolicy{Current: "v1.2.3", StdoutTTY: true, StderrTTY: true}
	if ok.Skip() {
		t.Fatal("release on a terminal should check")
	}
	cases := []NoticePolicy{
		{Current: "dev", StdoutTTY: true, StderrTTY: true},
		{Current: "v1.2.3", StdoutTTY: false, StderrTTY: true},
		{Current: "v1.2.3", StdoutTTY: true, StderrTTY: false},
		{Current: "v1.2.3", StdoutTTY: true, StderrTTY: true, CI: true},
		{Current: "v1.2.3", StdoutTTY: true, StderrTTY: true, Disabled: true},
	}
	for _, p := range cases {
		if !p.Skip() {
			t.Errorf("Skip() = false for %+v", p)
		}
	}
}

func mustParse(t *testing.T, s string) Version {
	t.Helper()
	v, ok := Parse(s)
	if !ok {
		t.Fatalf("Parse(%q) failed", s)
	}
	return v
}
