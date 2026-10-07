package restore

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func id(c Class, key string) RecordID { return RecordID{c, key} }

// healthySnapshot 构造一个四类齐全、依赖关系明确的基准快照：
// type t1,t2；object o1(t1),o2(t1),o3(t2)；
// link l1(o1->o2),l2(o2->o3)；action a1 依赖 o1,o2,l1；a2 依赖 o3,l2。
func healthySnapshot() *Snapshot {
	return &Snapshot{
		Classes: map[Class]ClassBackup{
			ClassType:   {},
			ClassObject: {},
			ClassLink:   {},
			ClassAction: {},
		},
		Types: []TypeDef{
			{Key: "t1", State: StateIntact},
			{Key: "t2", State: StateIntact},
		},
		Objects: []ObjectInstance{
			{Key: "o1", TypeKey: "t1", State: StateIntact},
			{Key: "o2", TypeKey: "t1", State: StateIntact},
			{Key: "o3", TypeKey: "t2", State: StateIntact},
		},
		Links: []LinkInstance{
			{Key: "l1", SourceObject: "o1", TargetObject: "o2", State: StateIntact},
			{Key: "l2", SourceObject: "o2", TargetObject: "o3", State: StateIntact},
		},
		Actions: []ActionRecord{
			{Key: "a1", Objects: []string{"o1", "o2"}, Links: []string{"l1"}, State: StateIntact},
			{Key: "a2", Objects: []string{"o3"}, Links: []string{"l2"}, State: StateIntact},
		},
	}
}

// TestReadOnlySnapshot 裁决前后序列化快照必须完全一致，证明裁决只读输入。
func TestReadOnlySnapshot(t *testing.T) {
	snap := healthySnapshot()
	before, _ := json.Marshal(snap)
	_ = New().Adjudicate(snap)
	after, _ := json.Marshal(snap)
	if string(before) != string(after) {
		t.Fatalf("snapshot mutated by adjudication")
	}
}

// TestAuditJSONL 审计轨迹可被逐行解析，类别判定先于记录判定，顺序固定。
func TestAuditJSONL(t *testing.T) {
	v := New().Adjudicate(healthySnapshot())
	var buf strings.Builder
	if err := v.WriteAuditJSON(&buf); err != nil {
		t.Fatalf("audit write: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != len(v.Audit) {
		t.Fatalf("audit lines = %d, want %d", len(lines), len(v.Audit))
	}
	for i := 0; i < len(Classes()); i++ {
		var ev AuditEvent
		if err := json.Unmarshal([]byte(lines[i]), &ev); err != nil {
			t.Fatalf("audit line %d: %v", i, err)
		}
		if ev.Stage != "class" {
			t.Fatalf("audit must start with class judgments, got %s", ev.Stage)
		}
	}
}

func recoverableSet(v *Verdict) map[RecordID]bool {
	out := map[RecordID]bool{}
	for rid, rv := range v.Records {
		out[rid] = rv.Recoverable
	}
	return out
}

// TestHealthyPlanOrder 全部完好时：全部可恢复，计划按固定顺序并带屏障。
func TestHealthyPlanOrder(t *testing.T) {
	v := New().Adjudicate(healthySnapshot())
	if len(v.Errors) != 0 {
		t.Fatalf("expected no errors, got %+v", v.Errors)
	}
	var got []string
	for _, st := range v.Plan {
		switch st.Kind {
		case StepRecord:
			got = append(got, st.Record.String())
		case StepBarrier:
			got = append(got, "barrier:"+st.CompletedClass.String())
		}
	}
	want := []string{
		"type:t1", "type:t2", "barrier:type",
		"object:o1", "object:o2", "object:o3", "barrier:object",
		"link:l1", "link:l2", "barrier:link",
		"action:a1", "action:a2", "barrier:action",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan order mismatch:\n got %v\nwant %v", got, want)
	}
}

// TestTypeClassMissing 类型定义整体缺失：其下对象、相关链接与动作全部不可重建，
// 且不允许凭残留实例猜测类型定义；不相关的 t2 分支正常推进。
func TestTypeClassMissing(t *testing.T) {
	snap := healthySnapshot()
	cb := snap.Classes[ClassType]
	cb.Missing = true
	snap.Classes[ClassType] = cb

	v := New().Adjudicate(snap)
	if v.ClassStatuses[ClassType] != ClassUnavailable {
		t.Fatalf("type class status = %v", v.ClassStatuses[ClassType])
	}
	dead := map[RecordID]bool{
		id(ClassType, "t1"): true, id(ClassType, "t2"): true,
		id(ClassObject, "o1"): true, id(ClassObject, "o2"): true, id(ClassObject, "o3"): true,
		id(ClassLink, "l1"): true, id(ClassLink, "l2"): true,
		id(ClassAction, "a1"): true, id(ClassAction, "a2"): true,
	}
	for rid, wantDead := range dead {
		if v.Records[rid].Recoverable == wantDead {
			t.Fatalf("record %s recoverable=%v want %v", rid, v.Records[rid].Recoverable, !wantDead)
		}
	}
	if v.Errors[0].Code != ErrClassUnavailable {
		t.Fatalf("first error priority = %d, want ErrClassUnavailable", v.Errors[0].Code)
	}
	if len(v.Plan) != 0 {
		t.Fatalf("expected empty plan, got %d steps", len(v.Plan))
	}
}

// TestTypeClassCorruptAll 类型定义整体损坏与缺失同效。
func TestTypeClassCorruptAll(t *testing.T) {
	snap := healthySnapshot()
	cb := snap.Classes[ClassType]
	cb.CorruptAll = true
	snap.Classes[ClassType] = cb
	v := New().Adjudicate(snap)
	if v.ClassStatuses[ClassType] != ClassUnavailable {
		t.Fatalf("expected type class unavailable")
	}
	if v.Errors[0].Code != ErrClassUnavailable {
		t.Fatalf("expected class-unavailable error first")
	}
}

// TestEachClassWholeUnavailable 每一类备份单独整体缺失时的可重建范围。
func TestEachClassWholeUnavailable(t *testing.T) {
	cases := []struct {
		name      string
		missing   Class
		wantAlive []RecordID
	}{
		{"object-missing", ClassObject, []RecordID{id(ClassType, "t1"), id(ClassType, "t2")}},
		{"link-missing", ClassLink, []RecordID{
			id(ClassType, "t1"), id(ClassType, "t2"),
			id(ClassObject, "o1"), id(ClassObject, "o2"), id(ClassObject, "o3"),
		}},
		{"action-missing", ClassAction, []RecordID{
			id(ClassType, "t1"), id(ClassType, "t2"),
			id(ClassObject, "o1"), id(ClassObject, "o2"), id(ClassObject, "o3"),
			id(ClassLink, "l1"), id(ClassLink, "l2"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := healthySnapshot()
			cb := snap.Classes[tc.missing]
			cb.Missing = true
			snap.Classes[tc.missing] = cb
			v := New().Adjudicate(snap)
			if v.ClassStatuses[tc.missing] != ClassUnavailable {
				t.Fatalf("class %s expected unavailable", tc.missing)
			}
			alive := map[RecordID]bool{}
			for _, r := range tc.wantAlive {
				alive[r] = true
			}
			for rid, rv := range v.Records {
				if rv.Recoverable != alive[rid] {
					t.Fatalf("record %s recoverable=%v want %v", rid, rv.Recoverable, alive[rid])
				}
			}
		})
	}
}

// TestPartialObjectDamage 对象实例部分损坏：完好部分继续推进，
// 依赖不可恢复实例的链接与动作被级联判定为不可重建。
func TestPartialObjectDamage(t *testing.T) {
	snap := healthySnapshot()
	for i := range snap.Objects {
		if snap.Objects[i].Key == "o2" {
			snap.Objects[i].State = StateCorrupt
		}
	}
	v := New().Adjudicate(snap)
	if v.ClassStatuses[ClassObject] != ClassPartial {
		t.Fatalf("object class status = %v, want partial", v.ClassStatuses[ClassObject])
	}
	wantDead := map[RecordID]bool{
		id(ClassObject, "o2"): true,
		id(ClassLink, "l1"):   true, // l1: o1->o2
		id(ClassLink, "l2"):   true, // l2: o2->o3
		id(ClassAction, "a1"): true, // a1 依赖 o2,l1
		id(ClassAction, "a2"): true, // a2 依赖 l2
	}
	for rid, dead := range wantDead {
		if v.Records[rid].Recoverable {
			t.Fatalf("%s should be unrecoverable", rid)
		}
		if dead && v.Records[rid].Reasons[0] == ReasonCycle {
			t.Fatalf("%s should not be cycle", rid)
		}
	}
	wantAlive := []RecordID{
		id(ClassType, "t1"), id(ClassType, "t2"),
		id(ClassObject, "o1"), id(ClassObject, "o3"),
	}
	for _, rid := range wantAlive {
		if !v.Records[rid].Recoverable {
			t.Fatalf("%s should stay recoverable", rid)
		}
	}
	// 计划中不得出现任何不可恢复记录。
	for _, st := range v.Plan {
		if st.Kind == StepRecord && wantDead[*st.Record] {
			t.Fatalf("dead record %s leaked into plan", st.Record)
		}
	}
}

// TestBoundaryDependencyAction 动作依赖恰好横跨可恢复/不可恢复分界：
// 只依赖可恢复侧的动作可重建；任一依赖落在不可恢复侧即严格判死，无中间态。
func TestBoundaryDependencyAction(t *testing.T) {
	snap := healthySnapshot()
	// 令 o2 损坏：l1(o1->o2) 死，o1/o3 与类型存活。
	for i := range snap.Objects {
		if snap.Objects[i].Key == "o2" {
			snap.Objects[i].State = StateCorrupt
		}
	}
	// a3 恰好只依赖可恢复侧：o3（无链接）。
	snap.Actions = append(snap.Actions, ActionRecord{Key: "a3", Objects: []string{"o3"}, State: StateIntact})
	// a4 依赖同时落在分界线两侧：o1(活) + o2(死)。
	snap.Actions = append(snap.Actions, ActionRecord{Key: "a4", Objects: []string{"o1", "o2"}, State: StateIntact})

	v := New().Adjudicate(snap)
	if !v.Records[id(ClassAction, "a3")].Recoverable {
		t.Fatalf("a3 (deps wholly inside recoverable side) must recover")
	}
	a4 := v.Records[id(ClassAction, "a4")]
	if a4.Recoverable {
		t.Fatalf("a4 (one dep on unrecoverable side) must not recover")
	}
	if a4.Reasons[0] != ReasonDependencyUnavailable && a4.Reasons[0] != ReasonClassUnavailable {
		t.Fatalf("a4 reason = %v, want dependency cascade", a4.Reasons)
	}
}

// TestBrokenReference 悬空依赖按断引用判死，属于优先级2级联类错误。
func TestBrokenReference(t *testing.T) {
	snap := healthySnapshot()
	snap.Links[0].TargetObject = "ghost"
	v := New().Adjudicate(snap)
	l1 := v.Records[id(ClassLink, "l1")]
	if l1.Recoverable || l1.Reasons[0] != ReasonDependencyUnavailable {
		t.Fatalf("l1 should cascade-dead on broken reference, got %+v", l1)
	}
	if v.Records[id(ClassAction, "a1")].Recoverable {
		t.Fatalf("a1 depending on dead l1 must be unrecoverable")
	}
}

// TestErrorPriorityOrder 四类错误按固定优先级排序：整体不可用 > 级联 > 环 > 新损坏。
func TestErrorPriorityOrder(t *testing.T) {
	snap := healthySnapshot()
	cb := snap.Classes[ClassAction]
	cb.Missing = true
	snap.Classes[ClassAction] = cb
	for i := range snap.Objects {
		if snap.Objects[i].Key == "o2" {
			snap.Objects[i].State = StateCorrupt
		}
	}
	v := New().Adjudicate(snap)
	last := ErrorCode(0)
	for _, e := range v.Errors {
		if e.Code < last {
			t.Fatalf("error priority not sorted: %d after %d", e.Code, last)
		}
		last = e.Code
	}
	if v.Errors[0].Code != ErrClassUnavailable {
		t.Fatalf("want class-unavailable first, got %d", v.Errors[0].Code)
	}
}
