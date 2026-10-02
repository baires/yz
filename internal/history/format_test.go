package history

import (
	"testing"
	"time"
)

func TestFormatTimeAndExpired(t *testing.T) {
	created := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	expires := created.Add(time.Hour)
	e := Entry{CreatedAt: created, ExpiresAt: &expires}
	if got := FormatTime(created); got != created.Local().Format(TimeLayout) {
		t.Fatalf("FormatTime = %q", got)
	}
	if e.Expired(created) {
		t.Fatal("entry expired at creation")
	}
	if !e.Expired(expires.Add(time.Second)) {
		t.Fatal("entry not expired after ExpiresAt")
	}
	open := Entry{CreatedAt: created}
	if open.Expired(expires) {
		t.Fatal("entry without ExpiresAt reported expired")
	}
}
