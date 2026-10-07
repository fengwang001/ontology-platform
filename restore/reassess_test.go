package restore

import "testing"

// TestReassessNewDamage 已完成部分不撤销；新损坏立即阻断其未完成下游。
func TestReassessNewDamage(t *testing.T) {
	snap := healthySnapshot()
	adj := New()
	v1 := adj.Adjudicate(snap)

	// 假设类型屏障与对象 o1 已完成，o2 尚未完成时发现 o2 损坏。
	prog := Progress{Completed: map[RecordID]bool{
		id(ClassType, "t1"): true, id(ClassType, "t2"): true,
		id(ClassObject, "o1"): true,
	}}
	for i := range snap.Objects {
		if snap.Objects[i].Key == "o2" {
			snap.Objects[i].State = StateCorrupt
		}
	}
	v2 := adj.Reassess(v1, snap, prog, DamageReport{
		NewlyCorrupt: []RecordID{id(ClassObject, "o2")},
	})

	if !v2.Records[id(ClassObject, "o1")].Recoverable {
		t.Fatalf("completed o1 must remain completed")
	}
	if got := v2.Records[id(ClassObject, "o1")].Reasons[0]; got != ReasonAlreadyCompleted {
		t.Fatalf("o1 reason = %v, want already-completed", got)
	}
	if v2.Records[id(ClassObject, "o2")].Reasons[0] != ReasonNewlyCorrupt {
		t.Fatalf("o2 reason = %v, want newly-corrupt", v2.Records[id(ClassObject, "o2")].Reasons)
	}
	blocked := []RecordID{
		id(ClassLink, "l1"), id(ClassLink, "l2"),
		id(ClassAction, "a1"), id(ClassAction, "a2"),
	}
	for _, rid := range blocked {
		rv := v2.Records[rid]
		if rv.Recoverable {
			t.Fatalf("%s must be blocked after new damage", rid)
		}
	}
	// 计划不得安排任何依赖 o2 的后续动作。
	for _, st := range v2.Plan {
		if st.Kind == StepRecord {
			for _, bad := range blocked {
				if *st.Record == bad {
					t.Fatalf("blocked record %s still scheduled", bad)
				}
			}
		}
	}
	// 优先级4错误必须存在。
	foundNewDamage := false
	for _, e := range v2.Errors {
		if e.Code == ErrNewDamage {
			foundNewDamage = true
		}
	}
	if !foundNewDamage {
		t.Fatalf("expected ErrNewDamage (priority 4) errors")
	}
}

// TestReassessNewClassUnavailable 中途某类整体不可用：未完成记录全部停止，已完成保留。
func TestReassessNewClassUnavailable(t *testing.T) {
	snap := healthySnapshot()
	adj := New()
	v1 := adj.Adjudicate(snap)
	prog := Progress{Completed: map[RecordID]bool{
		id(ClassType, "t1"): true, id(ClassType, "t2"): true,
		id(ClassObject, "o1"): true, id(ClassObject, "o2"): true, id(ClassObject, "o3"): true,
	}}
	cb := snap.Classes[ClassLink]
	cb.CorruptAll = true
	snap.Classes[ClassLink] = cb
	v2 := adj.Reassess(v1, snap, prog, DamageReport{
		NewlyClassUnavailable: []Class{ClassLink},
	})
	if !v2.Records[id(ClassObject, "o1")].Recoverable {
		t.Fatalf("completed objects must remain")
	}
	if v2.Records[id(ClassAction, "a1")].Recoverable {
		t.Fatalf("actions depending on links must stop")
	}
}
