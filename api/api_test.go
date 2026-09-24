package api

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func chg(p bool, k string, s, e, c int64) Change {
	return Change{Plus: p, Key: k, Start: s, End: e, Count: c}
}

// TestEightSteps 钉住 NOTES.md 的八步分步表：每步变更日志、Flush 后视图、丢弃数。
func TestEightSteps(t *testing.T) {
	e, _ := New(10, 3, 5, 1000)
	want := [][]Change{
		nil, nil,
		{chg(true, "K", 0, 10, 2)},
		{chg(false, "K", 0, 10, 2), chg(true, "K", 0, 10, 3)},
		nil, nil, nil,
		{chg(true, "K", 10, 20, 3)},
	}
	for i, ts := range []int64{2, 7, 13, 9, 18, 4, 10, 23} {
		got, err := e.Feed([]Event{{Key: "K", TS: ts}})
		if err != nil || !reflect.DeepEqual(got, want[i]) {
			t.Fatalf("step %d ts=%d got=%v want=%v err=%v", i+1, ts, got, want[i], err)
		}
	}
	e.Flush()
	wantView := map[ViewKey]int64{
		{Key: "K", Start: 0, End: 10}:  3,
		{Key: "K", Start: 10, End: 20}: 3,
		{Key: "K", Start: 20, End: 30}: 1,
	}
	if !reflect.DeepEqual(e.View(), wantView) || e.Dropped() != 1 {
		t.Fatalf("view=%v dropped=%d", e.View(), e.Dropped())
	}
	if err := e.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestBatchRecompute 钉 I1：Flush 后视图逐 (Key,窗口) 等于「只取被接受事件」的批量重算。
func TestBatchRecompute(t *testing.T) {
	cases := []struct {
		name                string
		size, delay, late   int64
		evs                 []Event
	}{
		{"eight", 10, 3, 5, evs("K", 2, 7, 13, 9, 18, 4, 10, 23)},
		{"negative-ts", 10, 0, 0, evs("K", -25, -15, -5)},
		{"late-correction", 10, 3, 5, evs("K", 2, 7, 13, 9)},
		{"multi-key", 10, 1, 2, []Event{{Key: "A", TS: 5}, {Key: "B", TS: 5}, {Key: "A", TS: 15}}},
		{"empty", 10, 0, 0, nil},
	}
	for _, c := range cases {
		e, _ := New(c.size, c.delay, c.late, 10000)
		if _, err := e.Feed(c.evs); err != nil {
			t.Fatalf("%s: feed %v", c.name, err)
		}
		e.Flush()
		if got := e.View(); !reflect.DeepEqual(got, oracle(c.size, c.delay, c.late, c.evs)) {
			t.Fatalf("%s: view=%v", c.name, got)
		}
	}
}

// TestChangelogPrefixes 钉 I2：严格折叠每个前缀——同槽位不撤回不得出现第二个 +，每条 - 恰撤现值。
func TestChangelogPrefixes(t *testing.T) {
	e, _ := New(10, 3, 5, 1000)
	var log []Change
	for _, ts := range []int64{2, 7, 13, 9, 18, 4, 10, 23, 9, 9} { // 末尾再补两条迟到修正
		ch, _ := e.Feed([]Event{{Key: "K", TS: ts}})
		log = append(log, ch...)
		cur := map[ViewKey]int64{}
		for _, x := range log {
			vk := ViewKey{Key: x.Key, Start: x.Start, End: x.End}
			if x.Plus {
				if _, dup := cur[vk]; dup {
					t.Fatalf("second + without - at %+v", x)
				}
				cur[vk] = x.Count
			} else {
				if v, ok := cur[vk]; !ok || v != x.Count {
					t.Fatalf("minus %+v not current(ok=%v)", x, ok)
				}
				delete(cur, vk)
			}
		}
	}
	log = append(log, e.Flush()...)
	if err := prefixOK(log, e.View()); err != nil {
		t.Fatalf("final fold: %v", err)
	}
}

// TestMonotonicWatermark 钉 I3：高水位后小 TS 不会让水位线回退（旧事件按当前 wm 丢弃）。
func TestMonotonicWatermark(t *testing.T) {
	e, _ := New(10, 3, 5, 1000)
	e.Feed([]Event{{Key: "K", TS: 100}}) // wm=97
	if ch, _ := e.Feed([]Event{{Key: "K", TS: 0}}); e.Dropped() != 1 || len(ch) != 0 {
		t.Fatalf("stale event after high wm: dropped=%d ch=%v", e.Dropped(), ch)
	}
	if ch, _ := e.Feed([]Event{{Key: "K", TS: 105}}); len(ch) != 0 { // 新窗口仍正常受理
		t.Fatalf("new event after high wm emitted %v", ch)
	}
	e.Flush()
	if _, present := e.View()[ViewKey{Key: "K", Start: 0, End: 10}]; present {
		t.Fatal("rolled-back wm resurrected purged window [0,10)")
	}
}

// TestInvalidParams 钉 I4 参数非法哨兵错误（含 maxOpen 为负）。
func TestInvalidParams(t *testing.T) {
	for _, c := range [][4]int64{{0, 3, 5, 10}, {-1, 3, 5, 10}, {10, -1, 5, 10}, {10, 3, -1, 10}, {10, 3, 5, -1}} {
		if _, err := New(c[0], c[1], c[2], int(c[3])); !errors.Is(err, ErrInvalidParams) {
			t.Fatalf("params %v: err=%v", c, err)
		}
	}
}

// TestRejectedBatchAtomic 钉 I4：任一事件被拒，整批（含本该产生的变更日志）不留痕，之后可继续用。
func TestRejectedBatchAtomic(t *testing.T) {
	a, _ := New(10, 3, 5, 1)
	a.Feed([]Event{{Key: "K", TS: 2}})
	v0, d0 := a.View(), a.Dropped()
	if ch, err := a.Feed([]Event{{Key: "K2", TS: 2}}); !errors.Is(err, ErrTooManyOpen) || ch != nil {
		t.Fatalf("maxOpen err=%v ch=%v", err, ch)
	}
	if ch, err := a.Feed([]Event{{Key: "", TS: 2}}); !errors.Is(err, ErrEmptyKey) || ch != nil {
		t.Fatalf("emptyKey err=%v ch=%v", err, ch)
	}
	b, _ := New(10, 3, 5, 10)
	b.Feed([]Event{{Key: "K", TS: 2}})
	if ch, err := b.Feed([]Event{{Key: "K", TS: 13}, {Key: "", TS: 1}}); err == nil || ch != nil {
		t.Fatalf("atomic trigger batch err=%v ch=%v", err, ch)
	}
	if !reflect.DeepEqual(a.View(), v0) || a.Dropped() != d0 || len(b.View()) != 0 {
		t.Fatalf("rejected batch left state: a=%v b=%v", a.View(), b.View())
	}
	if _, err := a.Feed([]Event{{Key: "K", TS: 3}}); err != nil {
		t.Fatalf("unusable after rejection: %v", err)
	}
	if ErrInvalidParams == ErrTooManyOpen || ErrTooManyOpen == ErrEmptyKey || ErrInvalidParams == ErrEmptyKey {
		t.Fatal("sentinel errors not distinct")
	}
}

// TestConcurrentReaders 钉并发：N 个 goroutine 并发只读同一实例，视图逐字段相同；无 sleep。
func TestConcurrentReaders(t *testing.T) {
	e, _ := New(10, 3, 5, 1000)
	e.Feed(evs("K", 2, 7, 13, 9, 18, 4, 10, 23))
	e.Feed([]Event{{Key: "K2", TS: 12}, {Key: "K2", TS: 22}})
	ref := e.View()
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, d := e.View(), e.Dropped()
			if !reflect.DeepEqual(v, ref) || d != 1 || e.SelfCheck() != nil {
				errs <- errors.New("concurrent read mismatch")
			}
		}()
	}
	wg.Wait()
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
}

func evs(k string, ts ...int64) []Event {
	out := make([]Event, len(ts))
	for i, x := range ts {
		out[i] = Event{Key: k, TS: x}
	}
	return out
}
