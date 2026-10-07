package snapshot

import (
	"errors"
	"testing"
)

func exportErr(t *testing.T, err error) *ExportError {
	t.Helper()
	var ee *ExportError
	if !errors.As(err, &ee) {
		t.Fatalf("expected *ExportError, got %v", err)
	}
	return ee
}

// 写入被接受时刻恰好与边界重合（LSN == N）：恒判给快照侧，
// 且重复判定结果一致。
func TestBoundaryTieGoesToSnapshot(t *testing.T) {
	j := NewJournal()
	j.AppendRaw(objSpec("t1", "o1"))
	j.AppendRaw(objSpec("t2", "o2")) // LSN 2，将成为边界
	j.AppendRaw(objSpec("t3", "o3")) // LSN 3，边界之后

	c := NewCoordinator(j)
	sess, err := c.BeginExport(At(2), Options{})
	if err != nil {
		t.Fatalf("BeginExport: %v", err)
	}
	for run := 0; run < 3; run++ { // 重复判定，结果必须可复现
		state, err := sess.Snapshot()
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		if _, ok := state.Objects["o2"]; !ok {
			t.Fatalf("run %d: write at boundary LSN must belong to snapshot (R1)", run)
		}
		if _, ok := state.Objects["o3"]; ok {
			t.Fatalf("run %d: write after boundary must not be in snapshot", run)
		}
	}
	incrs, err := sess.Drain()
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(incrs) != 1 || incrs[0].Writes[0].Object.ID != "o3" {
		t.Fatalf("expected exactly one increment carrying o3, got %+v", incrs)
	}
}

// 跨越边界的原子事务：显式边界切分事务时报边界定义冲突；
// 自动边界回退到事务安全点，使事务整体落在增量侧。
func TestCrossBoundaryTransaction(t *testing.T) {
	j := NewJournal()
	j.AppendRaw(objSpec("t1", "o1"))       // LSN 1
	j.AppendTransaction("t2", []WriteSpec{ // LSN 2,3
		objSpec("", "o2"), objSpec("", "o3"),
	})
	c := NewCoordinator(j)

	if _, err := c.BeginExport(At(2), Options{}); err == nil {
		t.Fatal("expected boundary conflict for txn-splitting boundary")
	} else if ee := exportErr(t, err); ee.Kind != ErrBoundaryConflict || ee.Rule != RuleBoundaryTxnSafe {
		t.Fatalf("expected boundary conflict R3, got %v", ee)
	}

	sess, err := c.BeginExport(Auto(), Options{}) // 自动边界
	if err != nil {
		t.Fatalf("BeginExport: %v", err)
	}
	if sess.Boundary() != 3 {
		t.Fatalf("auto boundary should be tip 3 (txn-safe), got %d", sess.Boundary())
	}
	state, err := sess.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// 事务 t2 整体可见，不允许部分可见。
	_, hasO2 := state.Objects["o2"]
	_, hasO3 := state.Objects["o3"]
	if hasO2 != hasO3 {
		t.Fatalf("transaction t2 partially visible: o2=%v o3=%v", hasO2, hasO3)
	}
}

// 自动边界遇到尖端处的交错事务时回退到安全点。
func TestAutoBoundaryRollsBackFromInterleavedTip(t *testing.T) {
	j := NewJournal()
	j.AppendRaw(objSpec("t1", "o1")) // LSN 1
	j.AppendRaw(objSpec("t2", "o2")) // LSN 2
	j.AppendRaw(objSpec("t1", "o3")) // LSN 3：t1 与 t2 交错
	sess, err := NewCoordinator(j).BeginExport(Auto(), Options{})
	if err != nil {
		t.Fatalf("BeginExport: %v", err)
	}
	if sess.Boundary() != 3 {
		// 位置 3 是安全的（t1、t2 都完整落在边界内），交错本身
		// 不影响边界安全性，只影响增量段的可聚合性。
		t.Fatalf("expected boundary 3, got %d", sess.Boundary())
	}
}

// 引用完整性：对象与链接以各种交织顺序到达。
func TestReferentialIntegrityInterleavings(t *testing.T) {
	cases := []struct {
		name     string
		specs    []WriteSpec
		boundary LSN
		wantErr  ErrorKind
		wantOK   bool
	}{
		{
			name:     "object then link, different txns",
			specs:    []WriteSpec{objSpec("t1", "o1"), objSpec("t1", "o2"), linkSpec("t2", "l1", "o1", "o2")},
			boundary: 0, // 全部走增量
			wantOK:   true,
		},
		{
			name:     "link before object, different txns",
			specs:    []WriteSpec{linkSpec("t1", "l1", "o1", "o2"), objSpec("t2", "o1"), objSpec("t2", "o2")},
			boundary: 0,
			wantErr:  ErrReferentialConflict,
		},
		{
			name:     "link and objects in same txn, link first",
			specs:    []WriteSpec{linkSpec("t1", "l1", "o1", "o2"), objSpec("t1", "o1"), objSpec("t1", "o2")},
			boundary: 0,
			wantOK:   true,
		},
		{
			name:     "object in snapshot, link in increment",
			specs:    []WriteSpec{objSpec("t1", "o1"), objSpec("t1", "o2"), linkSpec("t2", "l1", "o1", "o2")},
			boundary: 2,
			wantOK:   true,
		},
		{
			name:     "link in snapshot, object only in increment",
			specs:    []WriteSpec{objSpec("t1", "o1"), linkSpec("t2", "l1", "o1", "o2"), objSpec("t3", "o2")},
			boundary: 2,
			wantErr:  ErrReferentialConflict,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := NewJournal()
			for _, s := range tc.specs {
				j.AppendRaw(s)
			}
			sess, err := NewCoordinator(j).BeginExport(At(tc.boundary), Options{})
			if err != nil {
				t.Fatalf("BeginExport: %v", err)
			}
			state, serr := sess.Snapshot()
			incrs, derr := sess.Drain()
			err = serr
			if err == nil {
				err = derr
			}
			if tc.wantOK {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				// 拼接快照+全部增量后，链接与端点必须同时可见。
				for _, incr := range incrs {
					for _, w := range incr.Writes {
						state.Apply(w)
					}
				}
				for id, l := range state.Links {
					if _, ok := state.Objects[l.Src]; !ok {
						t.Fatalf("final state: link %s missing src %s", id, l.Src)
					}
					if _, ok := state.Objects[l.Dst]; !ok {
						t.Fatalf("final state: link %s missing dst %s", id, l.Dst)
					}
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got none")
			}
			if ee := exportErr(t, err); ee.Kind != tc.wantErr {
				t.Fatalf("expected %v, got %v", tc.wantErr, ee)
			}
		})
	}
}

// 原子性：同事务写入被其他事务隔开时，导出必须在第二段处拒绝，
// 且已成功输出的前缀保持有效。
func TestAtomicityConflictRejectsRemainder(t *testing.T) {
	j := NewJournal()
	j.AppendRaw(objSpec("t1", "o1")) // LSN 1
	j.AppendRaw(objSpec("t2", "o2")) // LSN 2
	j.AppendRaw(objSpec("t1", "o3")) // LSN 3：t1 被 t2 隔开
	// 显式空快照边界，保证三条写入全部落在增量段。
	sess, err := NewCoordinator(j).BeginExport(At(0), Options{})
	if err != nil {
		t.Fatalf("BeginExport: %v", err)
	}
	if _, err := sess.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	incr1, err := sess.Next()
	if err != nil || incr1 == nil {
		t.Fatalf("first increment should succeed, got %v %v", incr1, err)
	}
	incr2, err := sess.Next()
	if err != nil || incr2 == nil {
		t.Fatalf("second increment should succeed, got %v %v", incr2, err)
	}
	if _, err := sess.Next(); err == nil {
		t.Fatal("expected atomicity conflict at third segment")
	} else if ee := exportErr(t, err); ee.Kind != ErrAtomicityConflict || ee.Rule != RuleTxnContiguous {
		t.Fatalf("expected atomicity conflict R4, got %v", ee)
	}
	// 已输出的两条增量不被撤销。
	if incr1.Txn != "t1" || incr2.Txn != "t2" {
		t.Fatalf("emitted prefix must stay valid: %v %v", incr1.Txn, incr2.Txn)
	}
}

// 错误优先级：同一决策点上原子性冲突优先于引用完整性冲突被报告。
func TestErrorPriorityAtomicityBeforeReferential(t *testing.T) {
	j := NewJournal()
	j.AppendRaw(objSpec("t1", "o1"))                 // LSN 1
	j.AppendRaw(objSpec("t2", "o2"))                 // LSN 2
	j.AppendRaw(linkSpec("t1", "l1", "o1", "ghost")) // LSN 3：t1 非连续（R4）且端点缺失（R5）
	sess, err := NewCoordinator(j).BeginExport(At(0), Options{})
	if err != nil {
		t.Fatalf("BeginExport: %v", err)
	}
	if _, err := sess.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	sess.Next()
	sess.Next()
	_, err = sess.Next()
	ee := exportErr(t, err)
	if ee.Kind != ErrAtomicityConflict {
		t.Fatalf("atomicity (rank 1) must mask referential (rank 2), got %v", ee)
	}
}

// 边界定义冲突优先于一切下游错误。
func TestErrorPriorityBoundaryFirst(t *testing.T) {
	// 固定优先级：边界 < 原子性 < 引用完整性 < 资源。
	if !(Precedes(&ExportError{Kind: ErrBoundaryConflict}, &ExportError{Kind: ErrAtomicityConflict}) &&
		Precedes(&ExportError{Kind: ErrAtomicityConflict}, &ExportError{Kind: ErrReferentialConflict}) &&
		Precedes(&ExportError{Kind: ErrReferentialConflict}, &ExportError{Kind: ErrResourceExhausted})) {
		t.Fatal("error kind ranks must follow the fixed priority order")
	}
	j := NewJournal()
	j.AppendRaw(linkSpec("t1", "l1", "x", "y")) // 引用完整性必然违反
	_, err := NewCoordinator(j).BeginExport(At(99), Options{})
	if ee := exportErr(t, err); ee.Kind != ErrBoundaryConflict || ee.Rule != RuleBoundaryExists {
		t.Fatalf("boundary conflict must be reported first, got %v", ee)
	}
}

// 引用完整性冲突优先于资源不足。
func TestErrorPriorityReferentialBeforeResource(t *testing.T) {
	j := NewJournal()
	j.AppendRaw(linkSpec("t1", "l1", "x", "y"))
	sess, err := NewCoordinator(j).BeginExport(Auto(), Options{MaxSnapshotEntries: 0})
	if err != nil {
		t.Fatalf("BeginExport: %v", err)
	}
	_, err = sess.Snapshot()
	if ee := exportErr(t, err); ee.Kind != ErrReferentialConflict {
		t.Fatalf("expected referential conflict, got %v", ee)
	}
}

// 资源不足：配额耗尽时中止，已输出部分保持有效。
func TestResourceExhaustion(t *testing.T) {
	j := NewJournal()
	for _, id := range []string{"o1", "o2", "o3"} {
		j.AppendRaw(objSpec(TxnID("t-"+id), id))
	}
	sess, err := NewCoordinator(j).BeginExport(At(0), Options{MaxIncrements: 2})
	if err != nil {
		t.Fatalf("BeginExport: %v", err)
	}
	if _, err := sess.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	incrs, err := sess.Drain()
	if len(incrs) != 2 {
		t.Fatalf("expected 2 emitted increments before exhaustion, got %d", len(incrs))
	}
	if ee := exportErr(t, err); ee.Kind != ErrResourceExhausted || ee.Rule != RuleResourceLimit {
		t.Fatalf("expected resource exhaustion R6, got %v", ee)
	}
}

// 归属判定的工作量不随数据总规模增长：用操作计数器复核。
func TestClassificationConstantWork(t *testing.T) {
	for _, size := range []int{10, 1000, 100000} {
		j := NewJournal()
		for i := 0; i < size; i++ {
			j.AppendRaw(objSpec("t", "o"))
		}
		tip := j.Tip()
		ops := &OpCounter{}
		c := NewClassifier(tip/2, ops)
		for lsn := LSN(1); lsn <= tip; lsn++ {
			c.Classify(lsn)
		}
		if ops.Comparisons != size {
			t.Fatalf("size %d: expected exactly %d comparisons (1 per record), got %d",
				size, size, ops.Comparisons)
		}
	}
}

// 快照不得包含边界之后才被接受的写入，也不得遗漏边界之前的写入。
func TestSnapshotExactness(t *testing.T) {
	j := NewJournal()
	j.AppendRaw(objSpec("t1", "o1"))
	sess, err := NewCoordinator(j).BeginExport(Auto(), Options{})
	if err != nil {
		t.Fatalf("BeginExport: %v", err)
	}
	// 边界确定后继续写入。
	j.AppendRaw(objSpec("t2", "o2"))
	j.AppendRaw(objSpec("t3", "o1")) // 覆盖 o1
	state, err := sess.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(state.Objects) != 1 {
		t.Fatalf("snapshot must contain exactly the pre-boundary writes, got %v", state.Objects)
	}
	incrs, err := sess.Drain()
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(incrs) != 2 {
		t.Fatalf("post-boundary writes must appear as increments, got %d", len(incrs))
	}
	// 增量顺序必须与接受顺序一致。
	if incrs[0].Writes[0].Object.ID != "o2" || incrs[1].Writes[0].Object.ID != "o1" {
		t.Fatalf("increment order must match acceptance order: %+v", incrs)
	}
}
