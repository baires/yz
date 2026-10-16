package update

import (
	"cmp"
	"strconv"
	"strings"
)

// Version is a parsed semantic version. Build metadata is ignored.
type Version struct {
	major, minor, patch int
	pre                 []string
}

// Parse accepts a version with an optional leading v, a required
// major.minor.patch, an optional pre-release, and ignored build metadata.
func Parse(s string) (Version, bool) {
	if strings.HasPrefix(s, "v") || strings.HasPrefix(s, "V") {
		s = s[1:]
	}
	s, _, _ = strings.Cut(s, "+")
	num, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(num, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	major, ok := parseNum(parts[0])
	if !ok {
		return Version{}, false
	}
	minor, ok := parseNum(parts[1])
	if !ok {
		return Version{}, false
	}
	patch, ok := parseNum(parts[2])
	if !ok {
		return Version{}, false
	}
	v := Version{major: major, minor: minor, patch: patch}
	if !hasPre {
		return v, true
	}
	if pre == "" {
		return Version{}, false
	}
	ids := strings.Split(pre, ".")
	for _, id := range ids {
		if !validIdent(id) {
			return Version{}, false
		}
	}
	v.pre = ids
	return v, true
}

// IsRelease reports whether s is a release version. Development builds
// ("dev", "e2e-test") are not.
func IsRelease(s string) bool {
	_, ok := Parse(s)
	return ok
}

// Display renders the version without a leading v. Invalid input is empty
// so a cache file cannot inject terminal text into a notice.
func Display(s string) string {
	v, ok := Parse(s)
	if !ok {
		return ""
	}
	return v.String()
}

func (v Version) String() string {
	s := strconv.Itoa(v.major) + "." + strconv.Itoa(v.minor) + "." + strconv.Itoa(v.patch)
	if len(v.pre) > 0 {
		s += "-" + strings.Join(v.pre, ".")
	}
	return s
}

// Compare returns -1, 0, or 1 following semantic version precedence.
func (v Version) Compare(o Version) int {
	if c := cmp.Compare(v.major, o.major); c != 0 {
		return c
	}
	if c := cmp.Compare(v.minor, o.minor); c != 0 {
		return c
	}
	if c := cmp.Compare(v.patch, o.patch); c != 0 {
		return c
	}
	if len(v.pre) == 0 && len(o.pre) == 0 {
		return 0
	}
	if len(v.pre) == 0 {
		return 1
	}
	if len(o.pre) == 0 {
		return -1
	}
	n := min(len(v.pre), len(o.pre))
	for i := range n {
		if c := cmpIdent(v.pre[i], o.pre[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(v.pre), len(o.pre))
}

// IsNewer reports whether latest is a newer release than current.
// A current value that is not a release (a dev build) is older than any
// valid latest. An unparsable latest is not newer.
func IsNewer(latest, current string) bool {
	next, ok := Parse(latest)
	if !ok {
		return false
	}
	have, ok := Parse(current)
	if !ok {
		return true
	}
	return next.Compare(have) > 0
}

// AvailableLine is the post-command notice. It is empty when latest is not
// newer than current.
func AvailableLine(current, latest string) string {
	if !IsNewer(latest, current) {
		return ""
	}
	shown := Display(latest)
	if shown == "" {
		return ""
	}
	return "yz " + shown + " is available, run yz update"
}

// UpdatedLine is the one-line note printed after a background install.
func UpdatedLine(latest string) string {
	shown := Display(latest)
	if shown == "" {
		return ""
	}
	return "yz updated to " + shown
}

func parseNum(s string) (int, bool) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '-':
		default:
			return false
		}
	}
	if digits(s) && len(s) > 1 && s[0] == '0' {
		return false
	}
	return true
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func numericIdent(s string) (int, bool) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

func cmpIdent(a, b string) int {
	an, aNum := numericIdent(a)
	bn, bNum := numericIdent(b)
	switch {
	case aNum && bNum:
		return cmp.Compare(an, bn)
	case aNum:
		return -1
	case bNum:
		return 1
	default:
		return cmp.Compare(a, b)
	}
}
