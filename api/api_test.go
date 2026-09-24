package api

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func u(k string, v int64, s string) Event { return Event{Op: 'U', Key: k, Ver: v, Val: s} }
func d(k string, v int64) Event           { return Event{Op: 'D', Key: k, Ver: v} }

// 不变量 1：版本只进不退；墓碑清除后该键当前版本归 0，旧版本 U 可再被应用。
func TestVersionMonotonic(t *testing.T) {
	tab, _ := New(3, 100)
	seq := []struct {
		e   Event
		cur int64
	}{
		{u("a", 1, "x"), 1}, {u("a", 2, "y"), 2}, {d("a", 5), 5},
		{u("a", 4, "z"), 5}, // 被墓碑挡住
		{u("b", 9, "w"), 0}, // G=9，清除 a 的墓碑（9-5>=3），cur 归 0
		{u("a", 3, "p"), 3}, // 墓碑已清除，旧版本 U 被应用
	}
	for i, s := range seq {
		_ = tab.Apply([]Event{s.e})
		cur, _ := tab.Tomb("a")
		if r, ok := tab.Get("a"); ok {
			cur = r.Ver
		}
		if cur != s.cur {
			t.Fatalf("step %d: cur=%d, want %d", i, cur, s.cur)
		}
	}
}

// 不变量 4 + 故障注入：三类可判定且互不相同的错误；被拒后状态不变、可继续用。
func TestFailureNoTrace(t *testing.T) {
	all := []error{ErrBadParam, ErrBadEvent, ErrTooMany}
	for i, a := range all {
		for j, b := range all {
			if (i == j) != errors.Is(a, b) {
				t.Fatalf("errors not distinct: %v vs %v", a, b)
			}
		}
	}
	if _, err := New(0, 1); !errors.Is(err, ErrBadParam) {
		t.Fatalf("New(0,1) err=%v", err)
	}
	tab, _ := New(1<<60, 2)
	_ = tab.Apply([]Event{u("x", 1, "a")})
	keys := []string{"x", "y", "z"}
	snap := func() string {
		ig, g := tab.Stats()
		tm := ""
		for _, k := range keys {
			if v, ok := tab.Tomb(k); ok {
				tm += fmt.Sprintf("%s=%d,", k, v)
			}
		}
		return fmt.Sprintf("%v|%s|%d|%d", tab.GetMany(keys), tm, ig, g)
	}
	before := snap()
	cases := []struct {
		name string
		b    []Event
		want error
	}{
		{"empty key", []Event{{Op: 'U', Ver: 2, Val: "a"}}, ErrBadEvent},
		{"bad ver", []Event{u("y", 0, "b")}, ErrBadEvent},
		{"bad op", []Event{{Op: 'X', Key: "y", Ver: 2}}, ErrBadEvent},
		{"too many", []Event{u("y", 2, "b"), u("z", 3, "c")}, ErrTooMany},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := tab.Apply(c.b); !errors.Is(err, c.want) {
				t.Fatalf("err=%v, want %v", err, c.want)
			}
			if after := snap(); after != before {
				t.Fatalf("state changed: %s -> %s", before, after)
			}
		})
	}
	if err := tab.Apply([]Event{u("y", 4, "d")}); err != nil {
		t.Fatalf("table unusable: %v", err)
	}
}

// 第六节：并发读不得看到半批。
func TestConcurrentGetMany(t *testing.T) {
	tab, _ := New(1<<60, 100)
	keys := []string{"a", "b", "c", "d"}
	var stop atomic.Bool
	var wg sync.WaitGroup
	bad := make(chan map[string]Row, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				s := tab.GetMany(keys)
				if len(s) == 0 {
					continue
				}
				for _, k := range keys {
					if r, ok := s[k]; !ok || r.Ver != s[keys[0]].Ver {
						bad <- s
						return
					}
				}
			}
		}()
	}
	for b := int64(1); b <= 50; b++ {
		_ = tab.Apply([]Event{u("a", b, "x"), u("b", b, "x"), u("c", b, "x"), u("d", b, "x")})
	}
	stop.Store(true)
	wg.Wait()
	select {
	case s := <-bad:
		t.Fatalf("half batch visible: %v", s)
	default:
	}
}

func TestSelfCheck(t *testing.T) {
	tab, _ := New(1, 1)
	if err := tab.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
