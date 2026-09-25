package reorder

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"

	"ontology/seq"
)

func seqsOf(evs []seq.Event) (s []int64) {
	for _, e := range evs {
		s = append(s, e.Seq)
	}
	return
}

// TestCompareCountSublinear 证明定位最小 Seq 用堆而非线性扫描：
// 缓冲滞留 m 条互不相邻事件后，一次超时 flush 只发出最小那条，
// 该次 flush/级联的堆比较次数不随 m 线性增长（堆 sift-down 为 O(log m)）。
func TestCompareCountSublinear(t *testing.T) {
	const bound = 64 // 与 m 无关的小常数：2*log2(10000) < 28
	for _, m := range []int{100, 1000, 10000} {
		c := New(m, 1)
		for i := 1; i <= m; i++ { // Seq 2,4,...,2m：互不相邻且都大于 next=1
			if _, err := c.Feed(seq.Event{Seq: int64(2 * i), Value: i}); err != nil {
				t.Fatalf("m=%d 第 %d 条 Feed 报错: %v", m, i, err)
			}
		}
		out, err := c.Tick() // now-lastAdvance=1>=1，恰好触发一次 flush
		if err != nil {
			t.Fatalf("m=%d Tick 报错: %v", m, err)
		}
		if len(out) != 1 || out[0].Seq != 2 {
			t.Fatalf("m=%d 应只发出 Seq=2 一条, 实际 %v", m, out)
		}
		if c.cmp > bound {
			t.Fatalf("m=%d 比较次数 %d 超过常数界 %d（疑似线性扫描）", m, c.cmp, bound)
		}
		if c.cmp >= m {
			t.Fatalf("m=%d 比较次数 %d 随 m 线性增长", m, c.cmp)
		}
	}
}

// TestCascadeAndFlush 白盒核验级联与超时 flush 的基本行为。
func TestCascadeAndFlush(t *testing.T) {
	c := New(3, 2)
	mustFeed := func(s int64) []seq.Event {
		out, err := c.Feed(seq.Event{Seq: s, Value: int(s)})
		if err != nil {
			t.Fatalf("Feed(%d) 报错: %v", s, err)
		}
		return out
	}
	if out := mustFeed(1); len(out) != 1 || out[0].Seq != 1 {
		t.Fatalf("Feed(1) 应发出 [1], 实际 %v", out)
	}
	mustFeed(3)
	mustFeed(5)
	c.Tick() // now=1，未达 timeout
	out, _ := c.Tick()
	if len(out) != 1 || out[0].Seq != 3 { // now=2 触发 flush：丢 2、发 3
		t.Fatalf("flush 应发出 [3], 实际 %v", out)
	}
	if lost := c.Lost(); len(lost) != 1 || lost[0] != 2 {
		t.Fatalf("丢失应为 [2], 实际 %v", lost)
	}
	if out := mustFeed(4); len(out) != 2 || out[0].Seq != 4 || out[1].Seq != 5 {
		t.Fatalf("Feed(4) 应级联发出 [4 5], 实际 %v", out)
	}
	if c.Next() != 6 {
		t.Fatalf("next 应为 6, 实际 %d", c.Next())
	}
}

// TestElevenSteps 钉住 NOTES.md 的十一行分步表（maxBuffered=3, timeout=2）。
func TestElevenSteps(t *testing.T) {
	c := New(3, 2)
	type step struct {
		feed, now, next int64
		buf, out, lost  []int64
		wantErr         error
	}
	steps := []step{
		{1, 0, 2, nil, []int64{1}, nil, nil},
		{3, 0, 2, []int64{3}, nil, nil, nil},
		{5, 0, 2, []int64{3, 5}, nil, nil, nil},
		{0, 1, 2, []int64{3, 5}, nil, nil, nil},
		{0, 2, 4, []int64{5}, []int64{3}, []int64{2}, nil},
		{2, 2, 4, []int64{5}, nil, []int64{2}, seq.ErrStale},
		{4, 2, 6, nil, []int64{4, 5}, []int64{2}, nil},
		{8, 2, 6, []int64{8}, nil, []int64{2}, nil},
		{9, 2, 6, []int64{8, 9}, nil, []int64{2}, nil},
		{10, 2, 6, []int64{8, 9, 10}, nil, []int64{2}, nil},
		{11, 2, 6, []int64{8, 9, 10}, nil, []int64{2}, ErrOverflow},
	}
	for i, st := range steps {
		var out []seq.Event
		var err error
		if st.feed == 0 {
			out, err = c.Tick()
		} else {
			out, err = c.Feed(seq.Event{Seq: st.feed})
		}
		if !errors.Is(err, st.wantErr) || c.Now() != st.now || c.Next() != st.next ||
			!reflect.DeepEqual(seqsOf(c.Buffered()), st.buf) ||
			!reflect.DeepEqual(seqsOf(out), st.out) || !reflect.DeepEqual(c.Lost(), st.lost) {
			t.Fatalf("第%d步: now=%d next=%d buf=%v out=%v lost=%v err=%v",
				i+1, c.Now(), c.Next(), c.Buffered(), out, c.Lost(), err)
		}
	}
}

// TestBufferBound 不变量3：随机操作下任意时刻缓冲条数不超过 maxBuffered。
func TestBufferBound(t *testing.T) {
	for _, maxB := range []int{1, 2, 5, 17} {
		c := New(maxB, 2)
		r := rand.New(rand.NewSource(int64(maxB)))
		for i := 0; i < 400; i++ {
			if r.Intn(2) == 0 {
				c.Feed(seq.Event{Seq: int64(r.Intn(300) + 1)})
			} else {
				c.Tick()
			}
			if len(c.Buffered()) > maxB {
				t.Fatalf("maxB=%d 缓冲 %d 条超界", maxB, len(c.Buffered()))
			}
		}
	}
}
