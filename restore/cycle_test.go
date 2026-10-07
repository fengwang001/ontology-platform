package restore

import "testing"

// TestCycleDetected 畸形数据通过 ExtraDeps 构成环时：
// 环成员与其下游不可恢复，错误为优先级3，且计划不含这些记录。
func TestCycleDetected(t *testing.T) {
	snap := healthySnapshot()
	// 人为制造 o1 -> o2 -> o1 的对象间环（正常领域结构不会出现）。
	snap.ExtraDeps = []Edge{
		{From: id(ClassObject, "o1"), To: id(ClassObject, "o2")},
		{From: id(ClassObject, "o2"), To: id(ClassObject, "o1")},
	}
	v := New().Adjudicate(snap)
	cyclic := map[RecordID]bool{
		id(ClassObject, "o1"): true, id(ClassObject, "o2"): true,
		id(ClassLink, "l1"): true, id(ClassLink, "l2"): true,
		id(ClassAction, "a1"): true, id(ClassAction, "a2"): true,
	}
	for rid := range cyclic {
		rv := v.Records[rid]
		if rv.Recoverable || rv.Reasons[0] != ReasonCycle {
			t.Fatalf("%s expected cycle-dead, got %+v", rid, rv)
		}
	}
	for _, e := range v.Errors {
		if cyclicRecord(e) && e.Code != ErrCycle {
			t.Fatalf("cyclic record %s error code = %d", e.Record, e.Code)
		}
	}
	// 同类环不连坐类内其余记录：o3 自身完好且不依赖环成员，保持可恢复。
	if rv := v.Records[id(ClassObject, "o3")]; !rv.Recoverable {
		t.Fatalf("o3 is intact and independent of the cycle, must remain recoverable; got %+v", rv)
	}
	// 不引用任何对象/链接的孤立结构不受环影响。
	if !v.Records[id(ClassType, "t1")].Recoverable {
		t.Fatalf("unrelated type definitions must remain recoverable")
	}
}

func cyclicRecord(e VerdictError) bool {
	if e.Record == nil {
		return false
	}
	return e.Record.Class == ClassObject || e.Record.Class == ClassLink || e.Record.Class == ClassAction
}

// TestDeterminismRepeat 同批数据反复裁决，顺序与范围必须逐字节一致（结构化比较）。
func TestDeterminismRepeat(t *testing.T) {
	base := healthySnapshot()
	var first *Verdict
	for i := 0; i < 20; i++ {
		v := New().Adjudicate(base)
		if first == nil {
			first = v
			continue
		}
		if !verdictsEqual(first, v) {
			t.Fatalf("iteration %d produced a different verdict", i)
		}
	}
}

// TestCrossClassReverseCycle 跨类别反向依赖构成环时，被跨越屏障之后的
// 更高类别记录全部无法定位；低类完好记录不连坐。
func TestCrossClassReverseCycle(t *testing.T) {
	snap := healthySnapshot()
	// 让类型 t1 反向依赖对象 o1，与 o1 -> t1 的正常边构成跨类环。
	snap.ExtraDeps = []Edge{
		{From: id(ClassType, "t1"), To: id(ClassObject, "o1")},
	}
	v := New().Adjudicate(snap)
	// 环成员及其依赖下游（o1,l1,l2,a1,a2 以及屏障跨越的全部更高类）判死。
	dead := []RecordID{
		id(ClassType, "t1"),
		id(ClassObject, "o1"),
	}
	for _, rid := range dead {
		if rv := v.Records[rid]; rv.Recoverable || rv.Reasons[0] != ReasonCycle {
			t.Fatalf("%s expected cycle-dead, got %+v", rid, rv)
		}
	}
	// 环成员之外：t2、o3 以及它们之上的记录可恢复性仍由依赖决定，
	// 但跨类屏障受阻使更高类别记录无法进入唯一计划。
	for _, rid := range []RecordID{
		id(ClassType, "t2"),
		id(ClassObject, "o3"),
	} {
		if !v.Records[rid].Recoverable {
			t.Fatalf("%s independent of the cycle must remain recoverable", rid)
		}
	}
	for _, rid := range []RecordID{
		id(ClassLink, "l1"), id(ClassLink, "l2"),
		id(ClassAction, "a1"), id(ClassAction, "a2"),
	} {
		if v.Records[rid].Recoverable {
			t.Fatalf("%s depends (directly or transitively) on the cycle and must be unrecoverable", rid)
		}
	}
	for _, st := range v.Plan {
		if st.Kind == StepRecord && st.Record.Class != ClassType && st.Record.Class != ClassObject {
			t.Fatalf("no link/action may be scheduled beyond the blocked object barrier: %s", st.Record)
		}
	}
}
