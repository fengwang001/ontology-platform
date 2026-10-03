package agg

import (
	"reflect"
	"testing"
)

func ev(ts int64, r, c string) Event {
	return Event{Ts: ts, Dims: map[string]string{"d1": r, "d2": c}}
}

func TestCrossTabBasicAndOrder(t *testing.T) {
	resetExamined()
	events := []Event{
		ev(5, "b", "y"), ev(1, "a", "x"), ev(2, "b", "x"),
		ev(3, "a", "y"), ev(4, "b", "y"),
	}
	m := CrossTab(events, "d1", "d2")
	if !reflect.DeepEqual(m.Rows, []string{"a", "b"}) ||
		!reflect.DeepEqual(m.Cols, []string{"x", "y"}) {
		t.Fatalf("order rows=%v cols=%v", m.Rows, m.Cols)
	}
	want := [][]int64{{1, 1}, {1, 2}}
	if !reflect.DeepEqual(m.Cells, want) {
		t.Fatalf("cells=%v want=%v", m.Cells, want)
	}
	if m.Total != 5 || ExaminedCount() != 5 {
		t.Fatalf("total=%d examined=%d", m.Total, ExaminedCount())
	}
	t.Logf("input=%v\noutput=%+v", events, m)
}

func TestCrossTabEmpty(t *testing.T) {
	resetExamined()
	m := CrossTab(nil, "d1", "d2")
	if len(m.Rows) != 0 || len(m.Cols) != 0 || m.Total != 0 || ExaminedCount() != 0 {
		t.Fatalf("empty should be empty: %+v examined=%d", m, ExaminedCount())
	}
}

func TestExaminedCountsOnlyInRange(t *testing.T) {
	// 模拟“存储 100 与 10000 事件、但范围内事件数相同”两档：
	// CrossTab 只看到调用方裁剪后的切片，故两档 examined 增量必须相等。
	mk := func(total int) []Event {
		es := make([]Event, total)
		for i := range es {
			ts := int64(i)
			r := "r"
			if ts >= 40 && ts < 60 {
				r = "rIn"
			}
			es[i] = ev(ts, r, "c")
		}
		return es
	}

	resetExamined()
	CrossTab(mk(100)[40:60], "d1", "d2")
	small := ExaminedCount()

	resetExamined()
	CrossTab(mk(10000)[40:60], "d1", "d2")
	big := ExaminedCount()

	if small != 20 || big != 20 || small != big {
		t.Fatalf("examined small=%d big=%d (must both equal in-range count 20)", small, big)
	}
	t.Logf("examined equal across 100/10000 storages: %d", small)
}
