package reorder

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/seq"
)

func nums(es []seq.Event) []int64 {
	var o []int64
	for _, e := range es {
		o = append(o, e.Seq)
	}
	return o
}

// eqInts 视 nil 与空切片等价地逐元素比较。
func eqInts(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// heapNums 返回缓冲内 Seq 的升序拷贝（堆内部是堆序，断言前先排序）。
func heapNums(b *Buffer) []int64 {
	o := make([]int64, b.pq.Len())
	for i, e := range b.pq.ev {
		o[i] = e.Seq
	}
	sort.Slice(o, func(i, j int) bool { return o[i] < o[j] })
	return o
}

// snap 是全状态快照（含非导出 cmp），供「失败不留痕」前后比对。
func snap(b *Buffer) []any {
	return []any{b.next, b.now, b.lastAdvance, heapNums(b), b.View(), b.Lost(), b.cmp}
}

// TestElevenSteps 逐字段钉第三节十一步：now/next/缓冲/lastAdvance/发出/丢失/报错。
func TestElevenSteps(t *testing.T) {
	b, _ := New(3, 2)
	type want struct {
		now, next, la  int64
		buf, out, lost []int64
		err            error
	}
	tab := []want{
		{0, 2, 0, nil, []int64{1}, nil, nil},
		{0, 2, 0, []int64{3}, nil, nil, nil},
		{0, 2, 0, []int64{3, 5}, nil, nil, nil},
		{1, 2, 0, []int64{3, 5}, nil, nil, nil},
		{2, 4, 2, []int64{5}, []int64{3}, []int64{2}, nil},
		{2, 4, 2, []int64{5}, nil, []int64{2}, seq.ErrExpired},
		{2, 6, 2, nil, []int64{4, 5}, []int64{2}, nil},
		{2, 6, 2, []int64{8}, nil, []int64{2}, nil},
		{2, 6, 2, []int64{8, 9}, nil, []int64{2}, nil},
		{2, 6, 2, []int64{8, 9, 10}, nil, []int64{2}, nil},
		{2, 6, 2, []int64{8, 9, 10}, nil, []int64{2}, ErrOverflow},
	}
	for i, s := range []int64{1, 3, 5, 0, 0, 2, 4, 8, 9, 10, 11} {
		var out []seq.Event
		var err error
		if s == 0 {
			out, err = b.Tick()
		} else {
			out, err = b.Feed(seq.Event{Seq: s, Value: int(s)})
		}
		w := tab[i]
		if !errors.Is(err, w.err) || b.now != w.now || b.next != w.next || b.lastAdvance != w.la ||
			!eqInts(heapNums(b), w.buf) || !eqInts(nums(out), w.out) || !eqInts(b.Lost(), w.lost) {
			t.Fatalf("步%d: now=%d next=%d la=%d buf=%v out=%v lost=%v err=%v",
				i+1, b.now, b.next, b.lastAdvance, heapNums(b), nums(out), b.Lost(), err)
		}
	}
}

// TestBufferBound 多档 maxBuffered × 随机到达顺序，任意时刻缓冲不得超界。
func TestBufferBound(t *testing.T) {
	for _, mb := range []int{1, 2, 5, 16, 64} {
		b, _ := New(mb, 2)
		r := rand.New(rand.NewSource(int64(mb) + 1))
		for range [500]struct{}{} {
			if r.Intn(3) == 0 {
				b.Tick()
			} else if s := b.next + int64(r.Intn(2*mb+2)) - 1; s >= 1 {
				b.Feed(seq.Event{Seq: s})
			}
			if b.pq.Len() > mb {
				t.Fatalf("mb=%d 缓冲达 %d", mb, b.pq.Len())
			}
		}
	}
}

// TestRejectionsLeaveNoTrace 四类拒绝前后全状态快照必须一致，且之后实例仍可用。
func TestRejectionsLeaveNoTrace(t *testing.T) {
	n2 := func() *Buffer { b, _ := New(2, 1); b.Feed(seq.Event{Seq: 1, Value: 9}); return b }
	n1 := func() *Buffer { b, _ := New(1, 1); b.Feed(seq.Event{Seq: 2, Value: 8}); return b }
	tab := []struct {
		b    *Buffer
		ev   seq.Event
		want error
	}{
		{n2(), seq.Event{Seq: 0}, seq.ErrInvalid},
		{n2(), seq.Event{Seq: 1}, seq.ErrExpired},
		{n1(), seq.Event{Seq: 4}, ErrOverflow},
	}
	for _, c := range tab {
		before := snap(c.b)
		_, err := c.b.Feed(c.ev)
		if !errors.Is(err, c.want) || !reflect.DeepEqual(before, snap(c.b)) {
			t.Fatalf("拒绝 %v: err=%v before=%v after=%v", c.want, err, before, snap(c.b))
		}
		if _, e := c.b.Feed(seq.Event{Seq: c.b.next, Value: 1}); e != nil {
			t.Fatalf("被拒后实例不可继续使用: %v", e)
		}
	}
	for _, bad := range [][2]int{{0, 1}, {1, -1}, {-1, 0}} {
		if x, e := New(bad[0], bad[1]); !errors.Is(e, ErrBadParams) || x != nil {
			t.Fatalf("New(%v) = %v, %v", bad, x, e)
		}
	}
}

// TestMinHeapCompares 白盒读非导出 cmp：滞留 m 条（多档循环生成）后一次超时 flush
// 恰好只发堆顶 3，定位它的比较条数必须落在与 m 无关的常数界内（堆 sift-down ≤ 2log2m）。
func TestMinHeapCompares(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		b, _ := New(m+1, 1)
		_, _ = b.Feed(seq.Event{Seq: 1, Value: 1})
		for j := 0; j < m; j++ { // 3,5,7,...,2m+1：互不相邻且都大于 next
			_, _ = b.Feed(seq.Event{Seq: int64(2*j + 3), Value: j})
		}
		out, _ := b.Tick() // timeout=1：丢 2、只发最小的 3 即停
		if len(out) != 1 || out[0].Seq != 3 || b.next != 4 || b.cmp == 0 || b.cmp > 32 {
			t.Fatalf("m=%d: out=%v next=%d cmp=%d（比较数随 m 线性增长或未用堆）", m, nums(out), b.next, b.cmp)
		}
	}
}
