package wagg

import (
	"fmt"
	"testing"
)

// 不变量 3：水位线只进不退，被丢弃事件与被接受事件影响相同。
func TestWatermarkMonotonic(t *testing.T) {
	agg, err := New(10, 3, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	prev := agg.Watermark()
	for _, ts := range []int64{2, 7, 13, 9, 18, 4, 10, 23} {
		if _, err := agg.Feed([]Event{{Key: "K", TS: ts}}); err != nil {
			t.Fatal(err)
		}
		if wm := agg.Watermark(); wm < prev {
			t.Fatalf("watermark regressed: %d -> %d", prev, wm)
		} else {
			prev = wm
		}
	}
	if agg.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", agg.Dropped())
	}
	if prev != 20 {
		t.Fatalf("final watermark = %d, want 20", prev)
	}
}

// 复杂度约束：水位线推进时检查过的未清除窗口个数不随 m 线性增长。
// 用很大的 delay 让 m 个窗口都保持未触发，再喂一个只让水位线前进 1 的事件。
func TestCheckedNotLinearInM(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			agg, err := New(10, 1<<40, 5, 0)
			if err != nil {
				t.Fatal(err)
			}
			evs := make([]Event, m+1)
			for i := 0; i < m; i++ {
				evs[i] = Event{Key: fmt.Sprintf("k%d", i), TS: int64(i) * 10}
			}
			evs[m] = Event{Key: "last", TS: int64(m-1)*10 + 1} // 水位线只前进 1
			if _, err := agg.Feed(evs); err != nil {
				t.Fatal(err)
			}
			if len(agg.states) != m+1 {
				t.Fatalf("open windows = %d, want %d", len(agg.states), m+1)
			}
			if agg.checked > 4 { // 两个堆各至多 1 次失败检查 + 本次真正触发/清除数(0)
				t.Fatalf("checked = %d grows with m=%d, want <= 4", agg.checked, m)
			}
		})
	}
}

// 迟到被接受且尚无输出值的窗口（包括新建即迟到的窗口）必须立即输出 +。
func TestLateNewWindowEmitsImmediately(t *testing.T) {
	agg, _ := New(10, 3, 5, 0)
	if _, err := agg.Feed([]Event{{Key: "K", TS: 23}}); err != nil { // wm=20
		t.Fatal(err)
	}
	ch, err := agg.Feed([]Event{{Key: "K", TS: 13}}) // [10,20) 新建即迟到，wm=20<25 接受
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 1 || ch[0] != (Change{Key: "K", Start: 10, End: 20, Count: 1}) {
		t.Fatalf("late new window changelog = %v", ch)
	}
}

// 清除必须真实释放内存：Flush 后不再保留任何窗口状态。
func TestFlushClearsState(t *testing.T) {
	agg, _ := New(10, 3, 5, 0)
	if _, err := agg.Feed([]Event{{Key: "a", TS: 2}, {Key: "b", TS: 13}}); err != nil {
		t.Fatal(err)
	}
	out := agg.Flush()
	if len(agg.states) != 0 || len(agg.open) != 0 {
		t.Fatal("state not cleared after flush")
	}
	if len(out) != 1 { // [0,10) 已在喂入 b@13 时触发，Flush 只补 [10,20)
		t.Fatalf("flush emitted %d changes, want 1", len(out))
	}
}
