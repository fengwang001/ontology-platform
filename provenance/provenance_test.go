package provenance

import (
	"errors"
	"reflect"
	"testing"
)

// 基础三方联合可见性：源、链接、目标必须同时覆盖且均已写入。
func TestThreePartyVisibility(t *testing.T) {
	s := NewStore()
	mustWriteObject(t, s, "A", 10, Interval{100, 200}, true)
	mustWriteObject(t, s, "B", 10, Interval{100, 200}, true)
	mustWriteLink(t, s, "L", 5, Interval{100, 200}, "A", "B", true)

	if _, err := s.Traverse(Query{Source: "A", ValidAt: 150, AsOf: 4, MaxDepth: 1}); !errors.Is(err, ErrAsOfBeforeSource) {
		t.Fatalf("want ErrAsOfBeforeSource, got %v", err)
	}

	res, err := s.Traverse(Query{Source: "A", ValidAt: 150, AsOf: 10, MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) != 1 || !reflect.DeepEqual(res.Paths[0].Nodes, []ObjectID{"A", "B"}) {
		t.Fatalf("want single visible path A>B, got %+v", res.Paths)
	}
	hop := res.Paths[0].Hops[0]
	if res.Source.WriteAt != 10 || hop.Link.WriteAt != 5 || hop.Target.WriteAt != 10 {
		t.Fatalf("trace record refs wrong: source=%+v hop=%+v", res.Source, hop)
	}
}

// 左闭右开边界：链接区间从 [100,150) 整体替换为 [150,200)。
// validAt=150 恰为旧区间右端点（不含）与新区间左端点（含）。
func TestIntervalBoundaryHalfOpen(t *testing.T) {
	s := NewStore()
	mustWriteObject(t, s, "A", 1, Interval{0, 1000}, true)
	mustWriteObject(t, s, "B", 1, Interval{0, 1000}, true)
	mustWriteLink(t, s, "L", 10, Interval{100, 150}, "A", "B", true)
	mustWriteLink(t, s, "L", 20, Interval{150, 200}, "A", "B", true)

	res, _ := s.Traverse(Query{Source: "A", ValidAt: 150, AsOf: 10, MaxDepth: 1})
	if len(res.Paths) != 0 || len(res.Rejected) != 1 ||
		res.Rejected[0].Failed != "link" || res.Rejected[0].Reason != ReasonNotYetVisible {
		t.Fatalf("boundary old-end: %+v", res)
	}
	res, _ = s.Traverse(Query{Source: "A", ValidAt: 150, AsOf: 15, MaxDepth: 1})
	if len(res.Paths) != 0 || res.Rejected[0].Reason != ReasonNotYetVisible {
		t.Fatalf("want not_yet_visible, got %+v", res.Rejected)
	}
	res, _ = s.Traverse(Query{Source: "A", ValidAt: 150, AsOf: 20, MaxDepth: 1})
	if len(res.Paths) != 1 {
		t.Fatalf("boundary new-start should be visible: %+v", res.Paths)
	}
	res, _ = s.Traverse(Query{Source: "A", ValidAt: 149, AsOf: 20, MaxDepth: 1})
	if len(res.Paths) != 0 || res.Rejected[0].Reason != ReasonNotEstablished {
		t.Fatalf("replaced interval must not cover 149: %+v", res.Rejected)
	}
}

// 链接写入时间早于两端对象：链接先建、对象后补。
func TestLinkWrittenBeforeObjects(t *testing.T) {
	s := NewStore()
	mustWriteLink(t, s, "L", 1, Interval{100, 200}, "A", "B", true)
	mustWriteObject(t, s, "A", 5, Interval{100, 200}, true)
	mustWriteObject(t, s, "B", 6, Interval{100, 200}, true)

	res, err := s.Traverse(Query{Source: "A", ValidAt: 150, AsOf: 5, MaxDepth: 1})
	if err != nil {
		t.Fatalf("asOf == earliest write must be allowed: %v", err)
	}
	if len(res.Paths) != 0 || res.Rejected[0].Failed != "target" ||
		res.Rejected[0].Reason != ReasonNotYetVisible {
		t.Fatalf("target B not yet visible at asOf=5: %+v", res.Rejected)
	}
	res, _ = s.Traverse(Query{Source: "A", ValidAt: 150, AsOf: 6, MaxDepth: 1})
	if len(res.Paths) != 1 {
		t.Fatalf("all three visible: %+v", res.Paths)
	}
}

// 链接写入时间晚于两端对象。
func TestLinkWrittenAfterObjects(t *testing.T) {
	s := NewStore()
	mustWriteObject(t, s, "A", 1, Interval{100, 200}, true)
	mustWriteObject(t, s, "B", 1, Interval{100, 200}, true)
	mustWriteLink(t, s, "L", 9, Interval{100, 200}, "A", "B", true)

	res, _ := s.Traverse(Query{Source: "A", ValidAt: 150, AsOf: 8, MaxDepth: 1})
	if len(res.Paths) != 0 || res.Rejected[0].Failed != "link" ||
		res.Rejected[0].Reason != ReasonNotYetVisible {
		t.Fatalf("link not yet visible: %+v", res.Rejected)
	}
	res, _ = s.Traverse(Query{Source: "A", ValidAt: 150, AsOf: 9, MaxDepth: 1})
	if len(res.Paths) != 1 {
		t.Fatalf("link becomes visible: %+v", res.Paths)
	}
}

// 多跳：中间一跳目标在边界上不满足；全部满足时返回各深度前缀路径。
func TestMultiHopMidFailure(t *testing.T) {
	s := NewStore()
	mustWriteObject(t, s, "A", 1, Interval{0, 1000}, true)
	mustWriteObject(t, s, "B", 1, Interval{0, 150}, true)
	mustWriteObject(t, s, "C", 1, Interval{0, 1000}, true)
	mustWriteObject(t, s, "D", 1, Interval{0, 1000}, true)
	mustWriteLink(t, s, "L1", 1, Interval{0, 1000}, "A", "B", true)
	mustWriteLink(t, s, "L2", 1, Interval{0, 1000}, "B", "C", true)
	mustWriteLink(t, s, "L3", 1, Interval{0, 1000}, "C", "D", true)

	res, err := s.Traverse(Query{Source: "A", ValidAt: 150, AsOf: 10, MaxDepth: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) != 0 {
		t.Fatalf("no path should pass: %+v", res.Paths)
	}
	rj := res.Rejected[0]
	if rj.Link != "L1" || rj.Failed != "target" || rj.Reason != ReasonNotEstablished {
		t.Fatalf("mid hop failure classification wrong: %+v", rj)
	}

	res, _ = s.Traverse(Query{Source: "A", ValidAt: 149, AsOf: 10, MaxDepth: 3})
	if len(res.Paths) != 3 {
		t.Fatalf("want 3 prefix paths, got %d", len(res.Paths))
	}
	want := map[string]bool{"A>B": false, "A>B>C": false, "A>B>C>D": false}
	for _, p := range res.Paths {
		want[joinIDs(p.Nodes)] = true
	}
	for k, v := range want {
		if !v {
			t.Fatalf("missing path %s", k)
		}
	}
}
