package wagg

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func ev(k string, ts int64) Event { return Event{Key: k, TS: ts} }

// enc 把一批变更编码成 "±start:count," 序列，便于表驱动逐字比对。
func enc(cs []Change) string {
	var b strings.Builder
	for _, c := range cs {
		fmt.Fprintf(&b, "%c%d:%d,", map[bool]byte{true: '+', false: '-'}[c.Plus], c.Start, c.Count)
	}
	return b.String()
}

func TestEightSteps(t *testing.T) {
	a := New(10, 3, 5, 0)
	steps := []struct {
		ts   int64
		want string
	}{
		{2, ""}, {9, ""}, {15, "+0:2,"}, {4, "-0:2,+0:3,"},
		{18, ""}, {5, ""}, {22, ""}, {7, ""},
	}
	for i, s := range steps {
		cs, err := a.Feed([]Event{ev("K", s.ts)})
		if err != nil || enc(cs) != s.want {
			t.Fatalf("step %d: cs=%q err=%v want %q", i+1, enc(cs), err, s.want)
		}
	}
	if a.Dropped() != 2 {
		t.Fatalf("dropped=%d want 2", a.Dropped())
	}
	if got := enc(a.Flush()); got != "+10:2,+20:1," {
		t.Fatalf("flush=%q want +10:2,+20:1,", got)
	}
}

// TestSublinearCheckCount：m 个未触发窗口下，wm 只 +1 且无触发/清除时，
// 检查数恒为小常数、不随 m 增长（证明按 end 有序定位而非整表扫描）。
func TestSublinearCheckCount(t *testing.T) {
	base := -1
	for _, m := range []int{100, 1000, 10000} {
		a, evs := New(1, 1<<40, 0, 0), make([]Event, m)
		for i := range evs {
			evs[i] = ev("K", int64(i))
		}
		a.Feed(evs)
		a.Feed([]Event{ev("K", int64(m))})
		if a.lastChecked > 2 || (base >= 0 && a.lastChecked != base) {
			t.Fatalf("m=%d lastChecked=%d base=%d，疑似线性扫描", m, a.lastChecked, base)
		}
		base = a.lastChecked
	}
}

func TestWatermarkMonotonic(t *testing.T) {
	a := New(10, 0, 0, 0)
	a.Feed([]Event{ev("K", 100)})
	for _, ts := range []int64{0, 1, 50, 99} {
		a.Feed([]Event{ev("K", ts)})
	}
	a.Flush()
	if a.Dropped() != 4 || len(a.ents) != 0 {
		t.Fatalf("dropped=%d entries=%d，水位线可能回退", a.Dropped(), len(a.ents))
	}
}

func TestDroppedAlsoAdvances(t *testing.T) {
	a := New(10, 0, 0, 0)
	a.Feed([]Event{ev("K", 100), ev("K", 0), ev("K", 1)})
	if a.Dropped() != 2 {
		t.Fatalf("dropped=%d want 2", a.Dropped())
	}
}

// TestBoundaryAndLate 表驱动：边界归丢弃、lateness 内迟到发补丁、临界 -1 接受。
func TestBoundaryAndLate(t *testing.T) {
	cases := []struct {
		name       string
		p          [3]int64
		seed, late []int64
		drop       int64
		patch      string
	}{
		{"等于end+lateness丢弃", [3]int64{10, 3, 5}, []int64{2, 9, 15, 18}, []int64{5}, 1, ""},
		{"lateness内迟到发补丁", [3]int64{10, 3, 5}, []int64{2, 9, 15}, []int64{4}, 0, "-0:2,+0:3,"},
		{"临界wm=end+lateness-1接受", [3]int64{10, 0, 5}, []int64{0, 10, 14}, []int64{1}, 0, "-0:1,+0:2,"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := New(c.p[0], c.p[1], c.p[2], 0)
			for _, ts := range c.seed {
				a.Feed([]Event{ev("K", ts)})
			}
			ps := ""
			for _, ts := range c.late {
				cs, _ := a.Feed([]Event{ev("K", ts)})
				ps += enc(cs)
			}
			if a.Dropped() != c.drop || ps != c.patch {
				t.Fatalf("drop=%d patch=%q want drop=%d patch=%q", a.Dropped(), ps, c.drop, c.patch)
			}
		})
	}
}

func TestSentinelErrors(t *testing.T) {
	if errors.Is(ErrEmptyKey, ErrTooManyOpen) || errors.Is(ErrEmptyKey, ErrInvalidParam) {
		t.Fatal("哨兵错误必须互不相同")
	}
	a := New(10, 1<<40, 0, 1)
	for _, c := range []struct {
		feed []Event
		want error
	}{
		{[]Event{ev("", 1)}, ErrEmptyKey},
		{[]Event{ev("a", 0), ev("b", 10)}, ErrTooManyOpen},
	} {
		if _, err := a.Feed(c.feed); !errors.Is(err, c.want) {
			t.Fatalf("err=%v want %v", err, c.want)
		}
	}
	defer func() {
		r := recover()
		if r == nil || !errors.Is(r.(error), ErrInvalidParam) {
			t.Fatalf("非法参数应 panic ErrInvalidParam，got %v", r)
		}
	}()
	New(0, 0, 0, 0)
}

func TestRejectedBatchAtomic(t *testing.T) {
	a := New(10, 1<<40, 0, 1)
	a.Feed([]Event{ev("a", 0)})
	n0, d0 := len(a.Log()), a.Dropped()
	for i, batch := range [][]Event{{ev("b", 0), ev("", 4)}, {ev("b", 0), ev("c", 10)}} {
		if _, err := a.Feed(batch); err == nil || len(a.Log()) != n0 || a.Dropped() != d0 || a.open != 1 {
			t.Fatalf("case %d：应被拒绝且不留痕", i)
		}
	}
	if _, err := a.Feed([]Event{ev("a", 1)}); err != nil {
		t.Fatalf("被拒后实例不可用：%v", err)
	}
}
