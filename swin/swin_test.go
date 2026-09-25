package swin

import (
	"errors"
	"math/rand"
	"sort"
	"testing"

	"ontology/sess"
)

var eightEvents = []Event{{Key: "K", TS: 10}, {Key: "K", TS: 13}, {Key: "K", TS: 20}, {Key: "K", TS: 16}, {Key: "K", TS: 23}, {Key: "K", TS: 17}, {Key: "K", TS: 25}, {Key: "K", TS: 11}}

// TestViewMatchesBatch 钉不变量1：视图（开放+闭合，按区间排序）== 对被接受事件的朴素批量重算。
func TestViewMatchesBatch(t *testing.T) {
	cases := [][]Event{eightEvents, {{Key: "A", TS: 1}, {Key: "B", TS: 1}, {Key: "A", TS: 9}}}
	for s := int64(1); s <= 8; s++ { // 循环生成多组随机序列（含同值、逆序、迟到、多 Key）
		r := rand.New(rand.NewSource(s))
		evs := make([]Event, 400)
		for i := range evs {
			evs[i] = Event{Key: string(rune('A' + r.Intn(5))), TS: int64(r.Intn(100))}
		}
		cases = append(cases, evs)
	}
	for _, evs := range cases {
		w, _ := NewWindow(3, 1<<30)
		var acc []Event // 用 Dropped 增量独立识别被接受事件，不读内部状态
		for _, e := range evs {
			d := w.Dropped()
			if _, err := w.Feed([]Event{e}); err != nil {
				t.Fatal(err)
			}
			if w.Dropped() == d {
				acc = append(acc, e)
			}
		}
		by := map[string][]int64{} // 朴素批量重算：分组→排序→间隔<=gap 连通
		for _, e := range acc {
			by[e.Key] = append(by[e.Key], e.TS)
		}
		got := w.View()
		if len(got) != len(by) {
			t.Fatalf("keys %v vs %v", got, by)
		}
		for k, ts := range by {
			sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
			var want []sess.Session
			for _, x := range ts {
				if n := len(want); n > 0 && x-want[n-1].End <= 3 {
					want[n-1].Absorb(x)
				} else {
					want = append(want, sess.New(x))
				}
			}
			if len(got[k]) != len(want) {
				t.Fatalf("key %s len %v vs %v", k, got[k], want)
			}
			for i := range want {
				if g, q := got[k][i], want[i]; g.Start != q.Start || g.End != q.End || g.Count != q.Count {
					t.Fatalf("key %s[%d] %+v vs %+v", k, i, g, q)
				}
			}
		}
	}
}

// TestClosedSessionImmutable 钉不变量2：闭合三元组在后续任意 Feed 后与闭合时刻完全相同。
func TestClosedSessionImmutable(t *testing.T) {
	w, _ := NewWindow(3, 16)
	if _, err := w.Feed(eightEvents); err != nil {
		t.Fatal(err)
	}
	frozen := w.View()["K"][0]
	for _, e := range []Event{{Key: "K", TS: 1_000_000}, {Key: "K", TS: 12}, {Key: "X", TS: -50}} {
		if _, err := w.Feed([]Event{e}); err != nil || w.View()["K"][0] != frozen {
			t.Fatalf("closed mutated %+v", w.View()["K"][0])
		}
	}
}

// TestDroppedLateConsistency 钉不变量3：仅第4、8步（迟到且 M 含已闭合会话）使 Dropped 递增。
func TestDroppedLateConsistency(t *testing.T) {
	w, _ := NewWindow(3, 16)
	var wm, c int64
	for step, e := range eightEvents {
		prev := w.Dropped()
		if _, err := w.Feed([]Event{e}); err != nil {
			t.Fatal(err)
		}
		late := step > 0 && e.TS < wm // 本步 wm 推进之前的水位线
		wm = max(wm, e.TS)
		want := late && hitOne(w.keys["K"].closed, e.TS, 3, &c) >= 0
		if drop := w.Dropped() == prev+1; drop != want {
			t.Fatalf("step %d drop=%v want=%v", step+1, drop, want)
		}
	}
	v := w.View()["K"]
	s0, s1 := v[0], v[1]
	if w.Dropped() != 2 || len(v) != 2 || s0.Start != 10 || s0.End != 13 || s0.Count != 2 || !s0.Closed() ||
		s1.Start != 17 || s1.End != 25 || s1.Count != 4 || s1.Closed() {
		t.Fatalf("drops=%d view=%+v", w.Dropped(), v)
	}
}

// TestRejectedOpsLeaveNoTrace 钉不变量4：三类错误互异，非法整批失败、状态（含水位线/丢弃数）零改动、可续用。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if w, err := NewWindow(0, 1); !errors.Is(err, ErrBadGap) || w != nil {
		t.Fatal("gap<=0")
	}
	if ErrBadGap == ErrEmptyKey || ErrEmptyKey == ErrTooManyOpen || ErrBadGap == ErrTooManyOpen {
		t.Fatal("sentinels distinct")
	}
	bad := [][]Event{{{Key: "A", TS: 2}, {Key: "B", TS: 2}},
		{{Key: "", TS: 9}, {Key: "A", TS: 2}}, {{Key: "A", TS: 2}, {Key: "", TS: 9}}}
	for i, b := range bad {
		w, _ := NewWindow(3, 1)
		if _, err := w.Feed([]Event{{Key: "A", TS: 1}}); err != nil {
			t.Fatal(err)
		}
		pre := w.View()["A"][0]
		_, err := w.Feed(b)
		if !(i == 0 && errors.Is(err, ErrTooManyOpen)) && !errors.Is(err, ErrEmptyKey) {
			t.Fatalf("case %d %v", i, err)
		}
		if w.View()["A"][0] != pre || w.Dropped() != 0 || w.wm != 1 {
			t.Fatalf("case %d left state", i)
		}
		if _, err := w.Feed([]Event{{Key: "A", TS: 2}}); err != nil {
			t.Fatalf("case %d reuse %v", i, err)
		}
	}
}

// TestComparisonCountBounded 钉有界查找：m 个 2*gap 间隔会话后喂远低于首 start 的事件，
// lastCmp 须为与 m 无关的小常数；仅经非导出字段读取，公开接口不可见。
func TestComparisonCountBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		w, _ := NewWindow(3, m+1)
		evs := make([]Event, m)
		for i := range evs {
			evs[i] = Event{Key: "K", TS: int64(6 * i)}
		}
		if _, err := w.Feed(evs); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Feed([]Event{{Key: "K", TS: -1_000_000}}); err != nil || w.lastCmp > 3 {
			t.Fatalf("m=%d err/cmp=%d", m, w.lastCmp)
		}
	}
}
