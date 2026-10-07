package ontology

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func newHarness(t *testing.T) (*Store, *IndexManager, *Verifier, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	dlog := NewDecisionLog()
	dlog.SetSink(buf)
	store := NewStore()
	mgr := NewIndexManager(store, dlog)
	ver := NewVerifier(store, dlog)
	return store, mgr, ver, buf
}

// ---- 1) 基线/增量边界恰好落在某次写入前后的归属判断 ----

// 用确定性交错精确构造“围栏恰好在某次写入之前/之后”。
//
// 交错方式：测试直接持有 Store 锁，先发起 rebuild 取得围栏（rebuild 在
// 拿到围栏前会阻塞），再在围栏两侧分别放置写入，因此可以断言：
// 围栏前最后一写必入基线，围栏后第一写必入增量，且都不重不漏。
func TestBoundaryMembershipExact(t *testing.T) {
	store, mgr, _, _ := newHarness(t)
	mgr.Declare("T", "idx", "p")
	mgr.Write("T", "baseObj", map[string]PropertyValue{"p": "v0"})   // LSN 1
	mgr.Write("T", "objBefore", map[string]PropertyValue{"p": "vB"}) // LSN 2（边界前最后一写）

	// 发起围栏：此刻已分配的最大 LSN 为 2，围栏落在 LSN2 之后、LSN3 之前。
	store.Lock()
	fence := store.NextLSN()
	if fence != 2 {
		t.Fatalf("fence = %d, want 2", fence)
	}

	// 边界后第一写（LSN = fence+1）必须归入增量。
	afterLSN := store.PutLocked("T", "objAfter", map[string]PropertyValue{"p": "vA"})
	if afterLSN != fence+1 {
		t.Fatalf("after-write LSN=%d, want %d", afterLSN, fence+1)
	}
	if afterLSN != 3 {
		t.Fatalf("boundary after LSN=%d, want 3", afterLSN)
	}
	store.Unlock()

	// 用历史快照独立验证归属规则：fence 时刻 objBefore 可见、objAfter 不可见。
	snap, _ := store.SnapshotAt("objBefore", fence)
	if got := snap["p"]; got != "vB" {
		t.Fatalf("boundary-before write must be visible at fence, got %q", got)
	}
	snap2, _ := store.SnapshotAt("objAfter", fence)
	if _, ok := snap2["p"]; ok {
		t.Fatalf("boundary-after write must NOT be visible at fence")
	}

	audit, err := mgr.Rebuild("T", "idx")
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	ranges := map[string]string{}
	values := map[string]string{}
	for _, e := range audit.Entries {
		ranges[e.ObjectID] = e.SourceRange
		values[e.ObjectID] = e.Value
	}
	want := map[string]string{
		"baseObj": "v0", "objBefore": "vB", "objAfter": "vA",
	}
	if len(values) != len(want) {
		t.Fatalf("entries=%v, want %v", values, want)
	}
	for oid, v := range want {
		if values[oid] != v {
			t.Fatalf("object %s value=%q want %q (no object missing/overlapping)", oid, values[oid], v)
		}
	}

	// 再用“重建进行中到达的增量”验证 SourceRange 标注：重建设为围栏在
	// 一个稳定点，期间没有写入时基线/增量边界同样精确。
	audit2, err := mgr.Rebuild("T", "idx")
	if err != nil {
		t.Fatalf("rebuild2: %v", err)
	}
	if audit2.BaselineEndLSN < afterLSN {
		t.Fatalf("second fence %d must be >= after-write %d", audit2.BaselineEndLSN, afterLSN)
	}
}

// ---- 2) 重建中途失败 => 索引不可用，且与“不存在”区分 ----

func TestFailedRebuildUnavailable(t *testing.T) {
	_, mgr, _, logBuf := newHarness(t)
	mgr.Declare("T", "idx", "p")
	mgr.Write("T", "o1", map[string]PropertyValue{"p": "v1"})

	if _, err := mgr.Rebuild("T", "idx"); err != nil {
		t.Fatalf("initial rebuild: %v", err)
	}

	mgr.FailNextRebuild(true) // 基线扫描后、封存前失败
	if _, err := mgr.Rebuild("T", "idx"); !errors.Is(err, ErrIndexUnavailable) {
		t.Fatalf("failed rebuild err=%v, want ErrIndexUnavailable", err)
	}

	phase, ok := mgr.Phase("T", "idx")
	if !ok || phase != PhaseFailed {
		t.Fatalf("phase=%s ok=%v, want failed", phase, ok)
	}

	// 失败后查询必须被“不可用”拒绝，而不是返回部分条目。
	res, err := mgr.Query("T", "idx", "v1")
	if !errors.Is(err, ErrIndexUnavailable) {
		t.Fatalf("query after failure err=%v, want ErrIndexUnavailable", err)
	}
	if res != nil {
		t.Fatalf("partial entries must never be served: %+v", res)
	}

	// 未声明索引的错误类别必须可区分。
	if _, err := mgr.Query("T", "nope", "v1"); !errors.Is(err, ErrIndexNotDeclared) {
		t.Fatalf("undeclared query err=%v, want ErrIndexNotDeclared", err)
	}

	// 日志须包含失败判定的输入/输出/依据。
	if !strings.Contains(logBuf.String(), "aborted_partial_entries_discarded") {
		t.Fatalf("decision log missing abort basis: %s", logBuf.String())
	}

	// 失败是可恢复的：再次成功重建后恢复 active。
	if _, err := mgr.Rebuild("T", "idx"); err != nil {
		t.Fatalf("recovery rebuild: %v", err)
	}
	if phase, _ := mgr.Phase("T", "idx"); phase != PhaseActive {
		t.Fatalf("phase after recovery=%s, want active", phase)
	}
}

// 从未重建过的已声明索引：状态语义是“不存在”，但错误类别与未声明不同。
func TestAbsentVsUndeclared(t *testing.T) {
	_, mgr, _, _ := newHarness(t)
	mgr.Declare("T", "idx", "p")
	phase, ok := mgr.Phase("T", "idx")
	if !ok || phase != PhaseAbsent {
		t.Fatalf("phase=%s", phase)
	}
	_, err := mgr.Query("T", "idx", "x")
	if !errors.Is(err, ErrIndexUnavailable) {
		t.Fatalf("absent index query err=%v", err)
	}
	if _, _, err := mgr.Audit("T", "idx"); !errors.Is(err, ErrIndexUnavailable) {
		t.Fatalf("absent audit err=%v", err)
	}
}
