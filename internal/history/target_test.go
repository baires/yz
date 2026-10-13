package history

import (
	"testing"
	"time"
)

func TestTarget(t *testing.T) {
	cases := []struct {
		e      Entry
		key    string
		prefix bool
	}{
		{Entry{URL: "https://cdn.example.com/abcdefghijklmnop/a%20b.png"}, "abcdefghijklmnop/a b.png", false},
		{Entry{URL: "https://acc.r2.cloudflarestorage.com/photos/abcdefghijklmnop/x.pdf?X-Amz-Signature=1"}, "abcdefghijklmnop/x.pdf", false},
		{Entry{Kind: KindSite, URL: "https://cdn.example.com/s/ABCDEFGHijklmnop/"}, "sites/ABCDEFGHijklmnop/", true},
	}
	for _, c := range cases {
		key, prefix, err := c.e.Target("photos")
		if err != nil || key != c.key || prefix != c.prefix {
			t.Errorf("Target(%s) = %q %v %v", c.e.URL, key, prefix, err)
		}
	}
	for _, bad := range []Entry{
		{URL: "https://cdn.example.com/"},
		{URL: "https://cdn.example.com/short/x"},
		{Kind: KindSite, URL: "https://cdn.example.com/s/../"},
		{Kind: KindSite, URL: "https://cdn.example.com/abcdefghijklmnop/x"},
	} {
		if _, _, err := bad.Target("photos"); err == nil {
			t.Errorf("Target(%s) accepted", bad.URL)
		}
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	for _, e := range []Entry{{URL: "a", CreatedAt: t1}, {URL: "b", CreatedAt: t2}, {URL: "a", CreatedAt: t2}} {
		if err := Add(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := Remove(dir, "a", t1); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(dir)
	if len(got) != 2 || got[0].URL != "a" || !got[0].CreatedAt.Equal(t2) || got[1].URL != "b" {
		t.Fatalf("after remove = %+v", got)
	}
}
