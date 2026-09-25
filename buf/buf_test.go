package buf

import (
	"fmt"
	"testing"

	"ontology/tm"
)

// 发射阶段的候选读取个数：m 个全不可发射时 <=1；k 个可发射时 <= k+1。
// reads 是非导出字段，只能在包内测试直接读取，不经任何导出接口。
func TestEmitReadsSublinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		k := m / 10
		b := New(tm.New(3))
		evs := make([]Event, 0, m)
		for i := 0; i < m; i++ {
			ts := int64(10000)
			if i >= k { // 前 k 个 TS=10000，其余 TS=10001
				ts = 10001
			}
			evs = append(evs, Event{TS: ts, ID: fmt.Sprintf("e%d", i)})
		}
		b.Feed(evs) // wm = 10001-3 = 9998，全部不可发射
		if n := len(b.Output()); n != 0 {
			t.Fatalf("m=%d: emitted %d before wm raised", m, n)
		}
		b.Emit()
		if b.reads > 1 {
			t.Errorf("m=%d: all-unemittable reads=%d, want <= 1", m, b.reads)
		}
		b.wm.Observe(10003) // wm = 10000，恰好前 k 个变可发射
		if emitted := b.Emit(); len(emitted) != k {
			t.Errorf("m=%d: emitted %d, want %d", m, len(emitted), k)
		}
		if b.reads > k+1 {
			t.Errorf("m=%d: k-emittable reads=%d, want <= k+1=%d", m, b.reads, k+1)
		}
	}
}

// 发射顺序：按 TS 非递减，同 TS 按到达序；outTS 上钳。
func TestEmitOrder(t *testing.T) {
	cases := []struct {
		delay int64
		evs   []Event
		want  []Out
	}{
		{3, []Event{{TS: 10, ID: "a"}, {TS: 7, ID: "b"}}, // b 被水位线放出，a 留到 Flush
			[]Out{{ID: "b", TS: 7, OutTS: 7}, {ID: "a", TS: 10, OutTS: 10}}},
		{0, []Event{{TS: 5, ID: "x"}, {TS: 5, ID: "y"}, {TS: 3, ID: "z"}},
			[]Out{{ID: "z", TS: 3, OutTS: 3}, {ID: "x", TS: 5, OutTS: 5}, {ID: "y", TS: 5, OutTS: 5}}},
		{10, []Event{{TS: 1, ID: "p"}, {TS: 2, ID: "q"}}, // wm 为负先全缓冲，Flush 排空
			[]Out{{ID: "p", TS: 1, OutTS: 1}, {ID: "q", TS: 2, OutTS: 2}}},
	}
	for _, c := range cases {
		b := New(tm.New(c.delay))
		got := append(b.Feed(c.evs), b.Flush()...)
		if len(got) != len(c.want) {
			t.Fatalf("%+v: got %v, want %v", c.evs, got, c.want)
		}
		for i, o := range c.want {
			if got[i] != o {
				t.Errorf("%+v: got[%d]=%v, want %v", c.evs, i, got[i], o)
			}
		}
	}
}
