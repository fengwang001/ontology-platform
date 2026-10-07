package adjudicator

import (
	"reflect"
	"testing"
)

// drain 依次取完会话中所有可推进的记录并标记完成。
func drain(t *testing.T, s *Session) []RecordRef {
	t.Helper()
	var done []RecordRef
	for {
		r, ok := s.Next()
		if !ok {
			return done
		}
		s.Complete(r)
		done = append(done, r)
	}
}

// 无中途损坏时，会话按裁决顺序逐条推进直至全部完成。
func TestSessionHappyPath(t *testing.T) {
	snap := standardSnapshot()
	sess := NewSession(snap)
	want := New().Adjudicate(snap).Order
	got := drain(t, sess)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("会话推进顺序与裁决顺序不一致:\n got=%v\nwant=%v", got, want)
	}
	if !reflect.DeepEqual(sess.Done(), want) {
		t.Fatalf("完成列表与裁决顺序不一致: %v", sess.Done())
	}
	if len(sess.Blocked()) != 0 {
		t.Fatalf("不应有被阻断的记录: %v", sess.Blocked())
	}
}

// 重建中途新发现此前认为完好的记录损坏：
// 依赖它的后续重建立即停止，已完成的部分不撤销，依赖关系全部重新评估。
func TestSessionLateCorruption(t *testing.T) {
	snap := standardSnapshot()
	sess := NewSession(snap)

	// 先完成 T1, T2, O1（裁决顺序的前三条）。
	for i := 0; i < 3; i++ {
		r, ok := sess.Next()
		if !ok {
			t.Fatal("会话提前耗尽")
		}
		sess.Complete(r)
	}

	// 中途发现 O2 损坏：O2 自身、依赖 O2 的 L1/L2、以及 A1/A2 必须被停止。
	sess.ReportCorruption(ref(CategoryObjectInstance, "O2"))

	blocked := map[RecordRef]bool{}
	for _, bv := range sess.Blocked() {
		if bv.Reason != ReasonLateCorruption {
			t.Fatalf("被阻断记录 %v 的原因码应为 LateCorruption, 实际为 %v", bv.Ref, bv.Reason)
		}
		blocked[bv.Ref] = true
	}
	for _, r := range []RecordRef{
		ref(CategoryObjectInstance, "O2"),
		ref(CategoryLinkInstance, "L1"), ref(CategoryLinkInstance, "L2"),
		ref(CategoryActionRecord, "A1"), ref(CategoryActionRecord, "A2"),
	} {
		if !blocked[r] {
			t.Fatalf("记录 %v 应被停止重建", r)
		}
	}

	// 已完成的部分不得撤销。
	wantDone := []RecordRef{
		ref(CategoryObjectTypeDef, "T1"), ref(CategoryObjectTypeDef, "T2"),
		ref(CategoryObjectInstance, "O1"),
	}
	if !reflect.DeepEqual(sess.Done(), wantDone) {
		t.Fatalf("已完成记录被撤销或顺序变化: got=%v want=%v", sess.Done(), wantDone)
	}

	// 剩余可推进的只有 O3。
	got := drain(t, sess)
	if !reflect.DeepEqual(got, []RecordRef{ref(CategoryObjectInstance, "O3")}) {
		t.Fatalf("重新评估后应只剩 O3 可推进, 实际为 %v", got)
	}
}

// 中途损坏发生在依赖链上游时，即使下游记录已重建完成也不撤销，
// 但未完成的更下游记录被停止。
func TestSessionLateCorruptionDoesNotUndoCompleted(t *testing.T) {
	snap := standardSnapshot()
	sess := NewSession(snap)

	// 完成 T1, T2, O1, O2 后才发现 T1 损坏。
	for i := 0; i < 4; i++ {
		r, ok := sess.Next()
		if !ok {
			t.Fatal("会话提前耗尽")
		}
		sess.Complete(r)
	}
	sess.ReportCorruption(ref(CategoryObjectTypeDef, "T1"))

	// 已完成的 O1, O2 不撤销。
	if got := len(sess.Done()); got != 4 {
		t.Fatalf("已完成记录数应为 4, 实际为 %d", got)
	}
	// L1 依赖已完成但其上游新发现损坏的 O1/O2 => 被停止。
	blocked := map[RecordRef]bool{}
	for _, bv := range sess.Blocked() {
		blocked[bv.Ref] = true
	}
	if !blocked[ref(CategoryLinkInstance, "L1")] {
		t.Fatal("L1 应因上游中途损坏被停止")
	}
	// O3 链路与 T1 无关，仍可推进。
	r, ok := sess.Next()
	if !ok || r != ref(CategoryObjectInstance, "O3") {
		t.Fatalf("下一条应为 O3, 实际为 %v", r)
	}
}

// 多次报告中途损坏：阻断列表不重复，重新评估保持一致。
func TestSessionRepeatedCorruptionReports(t *testing.T) {
	snap := standardSnapshot()
	sess := NewSession(snap)
	sess.ReportCorruption(ref(CategoryObjectInstance, "O2"))
	first := sess.Blocked()
	sess.ReportCorruption(ref(CategoryObjectInstance, "O2"))
	second := sess.Blocked()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重复报告同一损坏后阻断列表不一致:\n%v\n%v", first, second)
	}
}
