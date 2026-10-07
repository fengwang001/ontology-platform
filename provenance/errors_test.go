package provenance

import (
	"errors"
	"reflect"
	"testing"
)

// 四类互斥错误的固定判定次序：只报第一类。
func TestErrorOrdering(t *testing.T) {
	s := NewStore()
	mustWriteObject(t, s, "A", 10, Interval{0, 100}, true)

	_, err := s.Traverse(Query{Source: "X", ValidAt: IllegalTime, AsOf: IllegalTime, MaxDepth: 0})
	if !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("order 1: %v", err)
	}
	_, err = s.Traverse(Query{Source: "A", ValidAt: IllegalTime, AsOf: 0, MaxDepth: 0})
	if !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("order 2: %v", err)
	}
	_, err = s.Traverse(Query{Source: "A", ValidAt: 0, AsOf: IllegalTime, MaxDepth: 0})
	if !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("order 2b: %v", err)
	}
	_, err = s.Traverse(Query{Source: "A", ValidAt: 0, AsOf: 5, MaxDepth: 0})
	if !errors.Is(err, ErrInvalidDepth) {
		t.Fatalf("order 3: %v", err)
	}
	_, err = s.Traverse(Query{Source: "A", ValidAt: 0, AsOf: 5, MaxDepth: -3})
	if !errors.Is(err, ErrInvalidDepth) {
		t.Fatalf("order 3b: %v", err)
	}
	_, err = s.Traverse(Query{Source: "A", ValidAt: 0, AsOf: 9, MaxDepth: 1})
	if !errors.Is(err, ErrAsOfBeforeSource) {
		t.Fatalf("order 4: %v", err)
	}
}

// 修正写入时间必须严格晚于被修正记录；非法区间与端点变更被拒绝。
func TestCorrectionMonotonicWrite(t *testing.T) {
	s := NewStore()
	mustWriteObject(t, s, "A", 10, Interval{0, 100}, true)
	if err := s.WriteObject("A", 10, Interval{0, 200}, true); !errors.Is(err, ErrOutOfOrderWrite) {
		t.Fatalf("equal write time: %v", err)
	}
	if err := s.WriteObject("A", 9, Interval{0, 200}, true); !errors.Is(err, ErrOutOfOrderWrite) {
		t.Fatalf("earlier write time: %v", err)
	}
	if err := s.WriteObject("A", IllegalTime, Interval{0, 200}, true); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("illegal write time: %v", err)
	}
	if err := s.WriteObject("A", 11, Interval{200, 100}, true); !errors.Is(err, ErrInvalidInterval) {
		t.Fatalf("invalid interval: %v", err)
	}

	mustWriteLink(t, s, "L", 1, Interval{0, 100}, "A", "B", true)
	if err := s.WriteLink("L", 2, Interval{0, 200}, "A", "C", true); !errors.Is(err, ErrInvalidInterval) {
		t.Fatalf("link endpoint must be immutable: %v", err)
	}
	if err := s.WriteLink("L", 1, Interval{0, 200}, "A", "B", true); !errors.Is(err, ErrOutOfOrderWrite) {
		t.Fatalf("link equal write time: %v", err)
	}
}

// 同一查询无新写入时重复执行，结果（含次序与记录引用）完全一致。
func TestRepeatableReads(t *testing.T) {
	s := buildDemoGraph(t)
	q := Query{Source: "A", ValidAt: 150, AsOf: 50, MaxDepth: 3}
	first, err := s.Traverse(q)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got, err := s.Traverse(q)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("repeat query %d differs", i)
		}
	}
}

// 区间延长的修正不会回溯污染历史 asOf 下的结果；消亡与复活也按双时态判定。
func TestCorrectionExtendLifecycle(t *testing.T) {
	s := NewStore()
	mustWriteObject(t, s, "A", 1, Interval{0, 1000}, true)
	mustWriteObject(t, s, "B", 1, Interval{0, 1000}, true)
	mustWriteLink(t, s, "L", 10, Interval{100, 150}, "A", "B", true)
	mustWriteLink(t, s, "L", 20, Interval{100, 300}, "A", "B", true) // 延长

	// asOf=10：旧认知只覆盖到 150（不含），160 不可见 => not_established。
	res, _ := s.Traverse(Query{Source: "A", ValidAt: 160, AsOf: 10, MaxDepth: 1})
	// 延长记录（写入时间 20）已存在于存储 => 对 asOf=10 属 not_yet_visible。
	if len(res.Paths) != 0 || res.Rejected[0].Reason != ReasonNotYetVisible {
		t.Fatalf("future extension must be not_yet_visible, not leaked: %+v", res.Rejected)
	}
	// asOf=15：延长记录写入时间 20 > 15 => not_yet_visible。
	res, _ = s.Traverse(Query{Source: "A", ValidAt: 160, AsOf: 15, MaxDepth: 1})
	if len(res.Paths) != 0 || res.Rejected[0].Reason != ReasonNotYetVisible {
		t.Fatalf("extension not yet visible: %+v", res.Rejected)
	}
	// asOf=20：延长生效。
	res, _ = s.Traverse(Query{Source: "A", ValidAt: 160, AsOf: 20, MaxDepth: 1})
	if len(res.Paths) != 1 {
		t.Fatalf("extended path visible: %+v", res.Paths)
	}

	// 消亡（exists=false）后在旧 validAt 不可见；再用更晚写入复活。
	mustWriteLink(t, s, "L", 30, Interval{100, 300}, "A", "B", false)
	res, _ = s.Traverse(Query{Source: "A", ValidAt: 160, AsOf: 30, MaxDepth: 1})
	if len(res.Paths) != 0 || res.Rejected[0].Reason != ReasonNotEstablished {
		t.Fatalf("tombstone: %+v", res.Rejected)
	}
	mustWriteLink(t, s, "L", 40, Interval{100, 400}, "A", "B", true)
	res, _ = s.Traverse(Query{Source: "A", ValidAt: 160, AsOf: 40, MaxDepth: 1})
	if len(res.Paths) != 1 {
		t.Fatalf("revived path: %+v", res.Paths)
	}
	// asOf=35 看到的仍是消亡认知，复活记录尚未可见。
	res, _ = s.Traverse(Query{Source: "A", ValidAt: 160, AsOf: 35, MaxDepth: 1})
	if len(res.Paths) != 0 {
		t.Fatalf("revival must not leak into asOf=35: %+v", res.Paths)
	}
}

// 结果排序与执行次序无关：乱序插入的链接仍得到确定顺序。
func TestDeterministicOrdering(t *testing.T) {
	s := NewStore()
	mustWriteObject(t, s, "A", 1, Interval{0, 1000}, true)
	for _, id := range []ObjectID{"Z", "M", "B", "Y"} {
		mustWriteObject(t, s, id, 1, Interval{0, 1000}, true)
		mustWriteLink(t, s, LinkID("e-"+id), 1, Interval{0, 1000}, "A", id, true)
	}
	res, err := s.Traverse(Query{Source: "A", ValidAt: 10, AsOf: 1, MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]ObjectID, len(res.Paths))
	for i, p := range res.Paths {
		got[i] = p.Nodes[1]
	}
	want := []ObjectID{"B", "M", "Y", "Z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deterministic order = %v, want %v", got, want)
	}
}
