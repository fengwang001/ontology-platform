package bkt

import (
	"testing"

	"ontology/tzone"
)

func newBucketer(t *testing.T) *Bucketer {
	t.Helper()
	z, err := tzone.Load("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	return New(z)
}

// TestCountDoesNotRescan 证明 Count 是 O(1) 查表：扫描事件数不随 m 增长。
func TestCountDoesNotRescan(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		b := newBucketer(t)
		evs := make([]Event, m)
		want := 0
		for i := range evs {
			evs[i] = Event{EventTime: 1767225600 + int64(i%200)*86400, Key: "k"}
			if i%200 == 2 {
				want++
			}
		}
		if err := b.Feed(evs); err != nil {
			t.Fatal(err)
		}
		if got := b.Count("2026-01-02"); got != want {
			t.Fatalf("m=%d: Count=%d, want %d", m, got, want)
		}
		if scanned := b.lastScan.Load(); scanned > 1 {
			t.Fatalf("m=%d: Count scanned %d events, want <= 1", m, scanned)
		}
	}
}

// TestFeedAtomic 非法 Feed 整体失败且状态不变。
func TestFeedAtomic(t *testing.T) {
	cases := []struct {
		name string
		evs  []Event
		want error
	}{
		{"negative ts", []Event{{EventTime: 1768451400, Key: "a"}, {EventTime: -1, Key: "b"}}, ErrNegativeTS},
		{"empty key", []Event{{EventTime: 1768451400, Key: ""}}, ErrEmptyKey},
	}
	for _, c := range cases {
		b := newBucketer(t)
		if err := b.Feed([]Event{{EventTime: 1768451400, Key: "ok"}}); err != nil {
			t.Fatal(err)
		}
		if err := b.Feed(c.evs); err != c.want {
			t.Fatalf("%s: err=%v, want %v", c.name, err, c.want)
		}
		if got := b.Count("2026-01-14"); got != 1 {
			t.Fatalf("%s: state changed, Count=%d", c.name, got)
		}
		if err := b.Feed([]Event{{EventTime: 1768451400, Key: "more"}}); err != nil {
			t.Fatalf("%s: unusable after rejection: %v", c.name, err)
		}
	}
}

// TestBucketNegativeTS 负 ts 被 Bucket 拒绝。
func TestBucketNegativeTS(t *testing.T) {
	b := newBucketer(t)
	if _, err := b.Bucket(-1); err != ErrNegativeTS {
		t.Fatalf("err=%v, want ErrNegativeTS", err)
	}
}
