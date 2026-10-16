package update

// NoticePolicy decides whether a command may check for a newer release.
// The check is skipped unless both stdout and stderr are terminals, the
// build is a release, and the process is outside CI with checks enabled.
type NoticePolicy struct {
	Current   string
	StdoutTTY bool
	StderrTTY bool
	CI        bool
	Disabled  bool
}

// Skip reports that this command must not contact the release host and
// must not print an update notice.
func (p NoticePolicy) Skip() bool {
	if p.Disabled || p.CI || !p.StdoutTTY || !p.StderrTTY {
		return true
	}
	return !IsRelease(p.Current)
}
