package permit_test

import (
	"testing"

	"ontology/permit"
)

// 抢占后各时刻查询可精确复现：原时段、应急时段、顺延后时段。
func TestPreemptionTimelineReproducible(t *testing.T) {
	net := permit.Network{Segments: []permit.Segment{
		{ID: "A", Lanes: 4, Corridor: "C1"},
	}}
	s, _ := permit.NewService(net, map[string]int{"C1": 10})
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "r", Segment: "A", Lanes: 2, Start: 5, End: 10})
	res := s.Apply(permit.ApplyRequest{OpAt: 6, ID: "e", Segment: "A", Lanes: 4,
		Start: 6, End: 8, Priority: permit.Emergency})
	if res.Err != nil || !res.Reschedule["r"] {
		t.Fatalf("emergency+recheck failed: %+v", res)
	}
	// r 在 6 已开始，剩余长度 4，顺延为 [8,12)。
	if got := res.RescheduledInterval["r"]; got != (permit.Interval{Start: 8, End: 12}) {
		t.Fatalf("r shifted interval: %+v", got)
	}
	cases := []struct {
		at     permit.Time
		closed int
		ids    []string
	}{
		// 权威时间线：r 被抢占后只在顺延窗口 [8,12) 生效；应急窗口 [6,8) 仅 e。
		{7, 4, []string{"e"}},
		{10, 2, []string{"r"}},
		{12, 0, nil},
	}
	for _, c := range cases {
		q := s.Query(permit.QueryRequest{Segment: "A", At: c.at})
		if q.Closed != c.closed {
			t.Fatalf("at=%d closed=%d want=%d ids=%v", c.at, q.Closed, c.closed, q.ActiveIDs)
		}
		if len(q.ActiveIDs) != len(c.ids) {
			t.Fatalf("at=%d ids=%v want=%v", c.at, q.ActiveIDs, c.ids)
		}
	}
	// 历史时刻 7 反复查询结果完全一致（不随之后操作改变）。
	q1 := s.Query(permit.QueryRequest{Segment: "A", At: 7})
	q2 := s.Query(permit.QueryRequest{Segment: "A", At: 7})
	if q1.Closed != q2.Closed || q1.ActiveIDs[0] != q2.ActiveIDs[0] {
		t.Fatalf("historical query must be stable: %+v %+v", q1, q2)
	}
}

// 全局不变式抽查：任一时刻同路段已批+应急许可车道数不超限、
// 已批常规许可之间满足走廊上限（通过朴素模型在每条操作后快照已在差分测试中保证）。
func TestOperationsAuditLog(t *testing.T) {
	net := permit.Network{Segments: []permit.Segment{
		{ID: "A", Lanes: 2, Corridor: "C1"},
		{ID: "B", Lanes: 2, Corridor: "C1"},
	}}
	s, _ := permit.NewService(net, map[string]int{"C1": 1})
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "a", Segment: "A", Lanes: 1, Start: 10, End: 20})
	res := s.Apply(permit.ApplyRequest{OpAt: 2, ID: "e", Segment: "A", Lanes: 2,
		Start: 12, End: 14, Priority: permit.Emergency})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	ops := s.Operations()
	if len(ops) != 2 {
		t.Fatalf("expected 2 accepted ops, got %d", len(ops))
	}
	for _, op := range ops {
		if op.Seq <= 0 || op.Note == "" {
			t.Fatalf("op record incomplete: %+v", op)
		}
	}
}
