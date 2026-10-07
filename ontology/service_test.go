package ontology

import (
	"errors"
	"fmt"
	"testing"
)

// 构造一个带两条历史的存活对象，返回两条记录 ID。
func setupObject(t *testing.T, s *Service) (ObjectID, RecordID, RecordID) {
	t.Helper()
	id := ObjectID("obj-1")
	if err := s.CreateObject(id); err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	r1, err := s.AddHistory(id, "status", "v1", 100)
	if err != nil {
		t.Fatalf("AddHistory r1: %v", err)
	}
	r2, err := s.AddHistory(id, "status", "v2", 200)
	if err != nil {
		t.Fatalf("AddHistory r2: %v", err)
	}
	return id, r1, r2
}

func TestDeleteRecordWhileObjectDeletedRejected(t *testing.T) {
	s := NewService()
	id, r1, _ := setupObject(t, s)
	if err := s.DeleteObject(id); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if err := s.DeleteRecord(id, r1); !errors.Is(err, ErrObjectDeleted) {
		t.Fatalf("want ErrObjectDeleted, got %v", err)
	}
	if err := s.UndeleteRecord(id, r1); !errors.Is(err, ErrObjectDeleted) {
		t.Fatalf("want ErrObjectDeleted, got %v", err)
	}
	// 状态未被改变：复活后 r1 仍然可见。
	if err := s.RestoreObject(id); err != nil {
		t.Fatalf("RestoreObject: %v", err)
	}
	res := s.QueryVisibleValue(id, "status", 150)
	if !res.Visible || res.Value != "v1" {
		t.Fatalf("state changed by rejected ops: %+v", res)
	}
}

func TestDuplicateDeleteRecordRejected(t *testing.T) {
	s := NewService()
	id, r1, _ := setupObject(t, s)
	if err := s.DeleteRecord(id, r1); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	before := s.Snapshot()
	if err := s.DeleteRecord(id, r1); !errors.Is(err, ErrRecordAlreadyDeleted) {
		t.Fatalf("want ErrRecordAlreadyDeleted, got %v", err)
	}
	after := s.Snapshot()
	if fmt.Sprintf("%v", before) != fmt.Sprintf("%v", after) {
		t.Fatalf("duplicate delete changed state: %v -> %v", before, after)
	}
}

func TestRestorePreservesIndividualDeletion(t *testing.T) {
	s := NewService()
	id, r1, r2 := setupObject(t, s)
	if err := s.DeleteRecord(id, r1); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	if err := s.DeleteObject(id); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if err := s.RestoreObject(id); err != nil {
		t.Fatalf("RestoreObject: %v", err)
	}
	// 复活不隐式恢复 r1，也不隐式删除 r2。
	if res := s.QueryVisibleValue(id, "status", 150); res.Visible || res.Reason != ReasonRecordDeleted {
		t.Fatalf("r1 should stay individually deleted: %+v", res)
	}
	if res := s.QueryVisibleValue(id, "status", 250); !res.Visible || res.Value != "v2" {
		t.Fatalf("r2 should stay visible: %+v", res)
	}
	snap := s.Snapshot()[id]
	if !snap.Records[r1] || snap.Records[r2] {
		t.Fatalf("record flags not preserved: %+v", snap)
	}
}

func TestDeleteRestoreCyclesConsistency(t *testing.T) {
	s := NewService()
	id, r1, r2 := setupObject(t, s)
	naive := NewNaiveService()
	if err := naive.CreateObject(id); err != nil {
		t.Fatal(err)
	}
	if _, err := naive.AddHistory(id, "status", "v1", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := naive.AddHistory(id, "status", "v2", 200); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 5; round++ {
		// 每轮交替：单独删除/撤销一条记录，然后整体删除再复活。
		if round%2 == 0 {
			if err := s.DeleteRecord(id, r1); err != nil {
				t.Fatalf("round %d DeleteRecord: %v", round, err)
			}
			if err := naive.DeleteRecord(id, r1); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := s.UndeleteRecord(id, r1); err != nil {
				t.Fatalf("round %d UndeleteRecord: %v", round, err)
			}
			if err := naive.UndeleteRecord(id, r1); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.DeleteObject(id); err != nil {
			t.Fatalf("round %d DeleteObject: %v", round, err)
		}
		if err := naive.DeleteObject(id); err != nil {
			t.Fatal(err)
		}
		if err := s.RestoreObject(id); err != nil {
			t.Fatalf("round %d RestoreObject: %v", round, err)
		}
		if err := naive.RestoreObject(id); err != nil {
			t.Fatal(err)
		}
	}
	// 多轮交替后与朴素实现逐条一致。
	got, want := s.Snapshot(), naive.Snapshot()
	if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
		t.Fatalf("snapshot mismatch:\ngot  %v\nwant %v", got, want)
	}
	// r2 全程未被单独删除，始终可见。
	if res := s.QueryVisibleValue(id, "status", 250); !res.Visible || res.Record != r2 {
		t.Fatalf("r2 should be visible: %+v", res)
	}
}

func TestInvisibleReasonPairwiseCombinations(t *testing.T) {
	s := NewService()
	id, r1, _ := setupObject(t, s)
	if err := s.DeleteRecord(id, r1); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}

	// 组合一：对象删除 ∩ 记录单独删除 → 归 OBJECT_DELETED（第一层优先）。
	if err := s.DeleteObject(id); err != nil {
		t.Fatal(err)
	}
	if res := s.QueryVisibleValue(id, "status", 150); res.Visible || res.Reason != ReasonObjectDeleted {
		t.Fatalf("obj-deleted ∩ rec-deleted: %+v", res)
	}
	// 组合二：对象删除 ∩ 时间无有效记录 → 归 OBJECT_DELETED。
	if res := s.QueryVisibleValue(id, "status", 50); res.Visible || res.Reason != ReasonObjectDeleted {
		t.Fatalf("obj-deleted ∩ not-current: %+v", res)
	}
	if err := s.RestoreObject(id); err != nil {
		t.Fatal(err)
	}
	// 组合三：记录单独删除 ∩ 该记录即最新有效 → 归 RECORD_DELETED。
	if res := s.QueryVisibleValue(id, "status", 150); res.Visible || res.Reason != ReasonRecordDeleted {
		t.Fatalf("rec-deleted ∩ current: %+v", res)
	}
	// 组合四：时间无有效记录（对象存活、无候选记录）→ 归 NOT_CURRENT。
	if res := s.QueryVisibleValue(id, "status", 50); res.Visible || res.Reason != ReasonNotCurrent {
		t.Fatalf("not-current only: %+v", res)
	}
	// 组合五：记录单独删除 ∩ 该记录已非时间最新（被 r2 取代）→
	// 候选记录为 r2，三层全通过，r1 的删除标记不外泄。
	if res := s.QueryVisibleValue(id, "status", 250); !res.Visible || res.Value != "v2" {
		t.Fatalf("rec-deleted ∩ not-current: %+v", res)
	}
	// 三层全通过 → 可见。
	if res := s.QueryVisibleValue(id, "status", 250); !res.Visible || res.Value != "v2" {
		t.Fatalf("all layers pass: %+v", res)
	}
}

func TestErrorPrecedenceFixed(t *testing.T) {
	s := NewService()
	id, r1, _ := setupObject(t, s)
	if err := s.DeleteRecord(id, r1); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteObject(id); err != nil {
		t.Fatal(err)
	}

	// 第一类：对象不存在（其余条件再坏也只报它）。
	if err := s.DeleteRecord("ghost", "no-such"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("precedence 1: %v", err)
	}
	// 第二类：记录不存在（对象删除中也不报第三类）。
	if err := s.DeleteRecord(id, "no-such"); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("precedence 2: %v", err)
	}
	// 第三类：对象删除中（记录同时已被单独删除也不报第四类）。
	if err := s.DeleteRecord(id, r1); !errors.Is(err, ErrObjectDeleted) {
		t.Fatalf("precedence 3: %v", err)
	}
	// 第四类：仅当前三类都不触发时，才报重复单独删除。
	if err := s.RestoreObject(id); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRecord(id, r1); !errors.Is(err, ErrRecordAlreadyDeleted) {
		t.Fatalf("precedence 4: %v", err)
	}
}

func TestObjectDeleteDoesNotTouchRecordFlags(t *testing.T) {
	s := NewService()
	id, r1, _ := setupObject(t, s)
	if err := s.DeleteRecord(id, r1); err != nil {
		t.Fatal(err)
	}
	writesBefore := s.RecordFlagWrites()
	snapBefore := s.Snapshot()

	if err := s.DeleteObject(id); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreObject(id); err != nil {
		t.Fatal(err)
	}
	if got := s.RecordFlagWrites(); got != writesBefore {
		t.Fatalf("object delete/restore wrote record flags: %d -> %d", writesBefore, got)
	}
	snapAfter := s.Snapshot()
	for rid, deleted := range snapBefore[id].Records {
		if snapAfter[id].Records[rid] != deleted {
			t.Fatalf("record %s flag overwritten by object delete/restore", rid)
		}
	}
}

func TestDeleteRestoreCostIndependentOfHistorySize(t *testing.T) {
	for _, n := range []int{100, 10000} {
		s := NewService()
		id := ObjectID(fmt.Sprintf("obj-%d", n))
		if err := s.CreateObject(id); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			if _, err := s.AddHistory(id, "p", "v", int64(i+1)); err != nil {
				t.Fatal(err)
			}
		}
		before := s.RecordFlagWrites()
		if err := s.DeleteObject(id); err != nil {
			t.Fatal(err)
		}
		if err := s.RestoreObject(id); err != nil {
			t.Fatal(err)
		}
		if delta := s.RecordFlagWrites() - before; delta != 0 {
			t.Fatalf("n=%d: delete/restore touched %d record flags, want 0", n, delta)
		}
	}
}

func TestOperationLogRecordsIOAndConditions(t *testing.T) {
	s := NewService()
	id, r1, _ := setupObject(t, s)
	if err := s.DeleteRecord(id, r1); err != nil {
		t.Fatal(err)
	}
	s.QueryVisibleValue(id, "status", 150)

	log := s.Log()
	if len(log) == 0 {
		t.Fatal("empty log")
	}
	for i, e := range log {
		if e.Seq != uint64(i+1) {
			t.Fatalf("log seq not ordered: entry %d has seq %d", i, e.Seq)
		}
		if e.Op == "" || e.ObjectID == "" {
			t.Fatalf("log entry missing input: %+v", e)
		}
	}
	last := log[len(log)-1]
	if last.Op != OpQueryVisibleValue || last.Result == nil {
		t.Fatalf("last entry should be the query with output: %+v", last)
	}
	if last.Result.Reason != ReasonRecordDeleted {
		t.Fatalf("query output not logged: %+v", last.Result)
	}
	// 三层条件取值已记录：对象存活、记录被单独删除、是时间最新。
	if !last.Cond.ObjectAlive || last.Cond.RecordAlive || !last.Cond.IsCurrent {
		t.Fatalf("tri-condition not logged: %+v", last.Cond)
	}
}
