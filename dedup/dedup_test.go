package dedup

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// TestCanonicalSteps 逐步钉住 NOTES 十行表：判定、水位线、记忆、重复计数。
func TestCanonicalSteps(t *testing.T) {
	tbl, _ := NewTable(10, 2, 1000)
	steps := []struct {
		ev   Event
		dup  bool
		wm   int64
		mem  map[string]int64
		dups int64
	}{
		{Event{ID: "a", TS: 5}, false, 3, map[string]int64{"a": 5}, 0},
		{Event{ID: "b", TS: 8}, false, 6, map[string]int64{"a": 5, "b": 8}, 0},
		{Event{ID: "a", TS: 9}, true, 7, map[string]int64{"a": 5, "b": 8}, 1},
		{Event{ID: "c", TS: 17}, false, 15, map[string]int64{"b": 8, "c": 17}, 1},
		{Event{ID: "a", TS: 14}, false, 15, map[string]int64{"a": 14, "b": 8, "c": 17}, 1},
		{Event{ID: "b", TS: 20}, false, 18, map[string]int64{"a": 14, "b": 20, "c": 17}, 1},
		{Event{ID: "d", TS: 4}, false, 18, map[string]int64{"a": 14, "b": 20, "c": 17}, 1},
		{Event{ID: "d", TS: 6}, false, 18, map[string]int64{"a": 14, "b": 20, "c": 17}, 1},
		{Event{ID: "c", TS: 26}, true, 24, map[string]int64{"b": 20, "c": 17}, 2},
		{Event{ID: "a", TS: 25}, false, 24, map[string]int64{"a": 25, "b": 20, "c": 17}, 2},
	}
	for i, s := range steps {
		out, e := tbl.Apply([]Event{s.ev})
		if e != nil {
			t.Fatalf("step %d: %v", i+1, e)
		}
		wm, ok := tbl.Watermark()
		if (len(out) == 0) != s.dup || !ok || wm != s.wm ||
			!reflect.DeepEqual(tbl.Mem(), s.mem) || tbl.Dups() != s.dups {
			t.Errorf("step %d: dup=%v wm=%d/%v mem=%v dups=%d; want dup=%v wm=%d mem=%v dups=%d",
				i+1, len(out) == 0, wm, ok, tbl.Mem(), tbl.Dups(), s.dup, s.wm, s.mem, s.dups)
		}
	}
}

// TestProbeComplexity：m 个不过期记忆后推进水位线 1，探测条数恒 1，不随 m 增长。
func TestProbeComplexity(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		tbl, _ := NewTable(10, 2, m+1)
		for i := 0; i < m; i++ {
			_, e := tbl.Apply([]Event{{ID: fmt.Sprintf("k%d", i), TS: 1_000_000}})
			if e != nil {
				t.Fatal(e)
			}
		}
		if _, e := tbl.Apply([]Event{{ID: "probe", TS: 1_000_001}}); e != nil {
			t.Fatal(e)
		}
		if p := tbl.probes; p != 1 || len(tbl.Mem()) != m+1 {
			t.Errorf("m=%d: probes=%d mem=%d（应 probes=1, mem=%d）", m, p, len(tbl.Mem()), m+1)
		}
	}
}

// TestRejectAtomic：三类拒绝互不相同、整批不留痕、拒绝后仍可正常使用。
func TestRejectAtomic(t *testing.T) {
	for _, c := range []struct {
		ttl, delay int64
		maxIDs     int
	}{{0, 2, 1}, {-1, 2, 1}, {10, -1, 1}, {10, 2, 0}} {
		if _, e := NewTable(c.ttl, c.delay, c.maxIDs); !errors.Is(e, ErrInvalidParam) {
			t.Errorf("参数 %+v: e=%v want ErrInvalidParam", c, e)
		}
	}
	if errors.Is(ErrEmptyID, ErrTooMany) || errors.Is(ErrEmptyID, ErrInvalidParam) {
		t.Fatal("哨兵错误不互异")
	}
	tbl, _ := NewTable(10, 2, 3) // 空 ID 在批中第二条：第一条先推进 wm 清旧记忆，失败须全还原
	tbl.Apply([]Event{{ID: "x", TS: 1}, {ID: "y", TS: 1}})
	m0, d0 := tbl.Mem(), tbl.Dups()
	wm0, _ := tbl.Watermark()
	if _, e := tbl.Apply([]Event{{ID: "z", TS: 100}, {ID: "", TS: 0}}); !errors.Is(e, ErrEmptyID) {
		t.Fatalf("e=%v want ErrEmptyID", e)
	}
	if wm, _ := tbl.Watermark(); wm != wm0 || !reflect.DeepEqual(tbl.Mem(), m0) || tbl.Dups() != d0 {
		t.Errorf("空 ID 批留痕: wm=%d mem=%v dups=%d", wm, tbl.Mem(), tbl.Dups())
	}
	t2, _ := NewTable(10, 2, 1) // 第二条使记忆 2 > maxIDs=1，整批回滚为空
	if _, e := t2.Apply([]Event{{ID: "x", TS: 1}, {ID: "y", TS: 1}}); !errors.Is(e, ErrTooMany) ||
		len(t2.Mem()) != 0 || t2.Dups() != 0 {
		t.Errorf("超限: e=%v mem=%v dups=%d（应 ErrTooMany 且全回滚）", e, t2.Mem(), t2.Dups())
	}
	if _, e := tbl.Apply([]Event{{ID: "q", TS: 1}}); e != nil {
		t.Errorf("拒绝后表不可用: %v", e)
	}
}

// TestNaiveReference：随机（含迟到）序列，逐步与朴素整表扫描参照一致。
func TestNaiveReference(t *testing.T) {
	const ttl, delay int64 = 10, 2
	for trial := 0; trial < 200; trial++ {
		tbl, _ := NewTable(ttl, delay, 1_000_000)
		rng := rand.New(rand.NewSource(int64(trial)))
		type hr struct {
			id string
			ts int64
		}
		var hist []hr
		var maxTS int64
		ts := int64(50)
		for n := 0; n < 300; n++ {
			ts += int64(rng.Intn(15)) - 4 // 可回退 → 迟到事件不丢弃
			ev := Event{ID: fmt.Sprintf("id%d", rng.Intn(7)), TS: ts}
			out, e := tbl.Apply([]Event{ev})
			if e != nil {
				t.Fatal(e)
			}
			if n == 0 || ev.TS > maxTS {
				maxTS = ev.TS
			}
			wm := maxTS - delay
			dup := false
			for j := len(hist) - 1; j >= 0; j-- {
				if hist[j].id == ev.ID {
					dup = wm < hist[j].ts+ttl
					break
				}
			}
			if dup != (len(out) == 0) {
				t.Fatalf("trial %d event %d (%v): dup=%v 与参照不一致", trial, n, ev, dup)
			}
			if !dup {
				hist = append(hist, hr{ev.ID, ev.TS})
			}
			want := map[string]int64{}
			for _, h := range hist {
				if wm < h.ts+ttl {
					want[h.id] = h.ts
				}
			}
			if got := tbl.Mem(); !reflect.DeepEqual(got, want) {
				t.Fatalf("trial %d event %d: mem=%v want %v", trial, n, got, want)
			}
		}
	}
}
