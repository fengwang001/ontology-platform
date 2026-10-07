package ontology

import (
	"errors"
	"testing"
)

func newEngine(t *testing.T, disk Disk, hook func(StagePoint) bool) *Engine {
	t.Helper()
	e, err := NewEngine(disk, nil, hook)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func TestBatchCommitNormal(t *testing.T) {
	disk := NewSimDisk()
	e := newEngine(t, disk, nil)
	seedInstances(t, e, map[InstanceID]map[string]string{
		"a": {"x": "1"}, "b": {"y": "2"},
	})
	pre := e.Snapshot()

	muts := []Mutation{
		{Instance: "a", Props: map[string]string{"x": "10"}},
		{Instance: "b", Props: map[string]string{"y": "20", "z": "30"}},
	}
	id, status, err := e.RunBatch(muts, BatchOptions{})
	if err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if status != StatusCommitted {
		t.Fatalf("status = %s, want COMMITTED", status)
	}
	assertState(t, "提交后", e.Snapshot(), expectInstances(pre, id, muts))

	st, err := e.BatchStatus(id)
	if err != nil || st != StatusCommitted {
		t.Fatalf("BatchStatus = %s, %v", st, err)
	}
	// 干净结束后不残留日志段。
	if segs := disk.List("journal/"); len(segs) != 0 {
		t.Fatalf("干净结束后残留日志段: %v", segs)
	}
	// 重启后状态保持。
	e2 := newEngine(t, disk, nil)
	assertState(t, "重启后", e2.Snapshot(), expectInstances(pre, id, muts))
	rep, err := e2.Recover()
	if err != nil || rep.HadActiveBatch {
		t.Fatalf("干净重启后不应有待恢复批次: %+v, %v", rep, err)
	}
}

func TestBatchRollbackValidation(t *testing.T) {
	disk := NewSimDisk()
	e := newEngine(t, disk, nil)
	seedInstances(t, e, map[InstanceID]map[string]string{"a": {"x": "1"}})
	pre := e.Snapshot()

	// 目标实例不存在：校验失败，正常回退。
	id, status, err := e.RunBatch([]Mutation{
		{Instance: "ghost", Props: map[string]string{"x": "9"}},
	}, BatchOptions{})
	if err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if status != StatusRolledBack {
		t.Fatalf("status = %s, want ROLLED_BACK", status)
	}
	assertState(t, "校验回退后", e.Snapshot(), pre)
	if st, _ := e.BatchStatus(id); st != StatusRolledBack {
		t.Fatalf("BatchStatus = %s, want ROLLED_BACK", st)
	}
}

func TestBatchRollbackAbort(t *testing.T) {
	disk := NewSimDisk()
	e := newEngine(t, disk, nil)
	seedInstances(t, e, map[InstanceID]map[string]string{
		"a": {"x": "1"}, "b": {"y": "2"},
	})
	pre := e.Snapshot()

	id, status, err := e.RunBatch([]Mutation{
		{Instance: "a", Props: map[string]string{"x": "10"}},
		{Instance: "b", Props: map[string]string{"y": "20"}},
	}, BatchOptions{AbortAtPreCommit: true})
	if err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if status != StatusRolledBack {
		t.Fatalf("status = %s, want ROLLED_BACK", status)
	}
	// 已应用的变更必须被撤销，版本号与属性与批次发起前完全一致。
	assertState(t, "主动回退后", e.Snapshot(), pre)
	if st, _ := e.BatchStatus(id); st != StatusRolledBack {
		t.Fatalf("BatchStatus = %s, want ROLLED_BACK", st)
	}
	// 重启后依旧与批次发起前一致。
	e2 := newEngine(t, disk, nil)
	assertState(t, "回退并重启后", e2.Snapshot(), pre)
}

func TestUncertainInstanceRejected(t *testing.T) {
	disk := NewSimDisk()
	// 在 Apply[0] 之后、Commit 之前崩溃：批次处于未决状态。
	hook := crashHook(StagePoint{Phase: PhasePreCommit})
	e := newEngine(t, disk, hook)
	seedInstances(t, e, map[InstanceID]map[string]string{
		"a": {"x": "1"}, "b": {"y": "2"}, "c": {"z": "3"},
	})
	_, _, err := e.RunBatch([]Mutation{
		{Instance: "a", Props: map[string]string{"x": "10"}},
		{Instance: "b", Props: map[string]string{"y": "20"}},
	}, BatchOptions{})
	if !errors.Is(err, ErrEngineCrashed) {
		t.Fatalf("err = %v, want ErrEngineCrashed", err)
	}

	// 重启后、恢复前：写集内实例的读写必须被拒绝，写集外不受影响。
	e2 := newEngine(t, disk, nil)
	for _, id := range []InstanceID{"a", "b"} {
		if _, err := e2.Read(id); !errors.Is(err, ErrInstanceUncertain) {
			t.Fatalf("Read(%s) = %v, want ErrInstanceUncertain", id, err)
		}
		if err := e2.Write(id, map[string]string{"k": "v"}); !errors.Is(err, ErrInstanceUncertain) {
			t.Fatalf("Write(%s) = %v, want ErrInstanceUncertain", id, err)
		}
	}
	if _, err := e2.Read("c"); err != nil {
		t.Fatalf("Read(c) 不应被拒绝: %v", err)
	}
	if err := e2.Write("c", map[string]string{"z": "4"}); err != nil {
		t.Fatalf("Write(c) 不应被拒绝: %v", err)
	}
	// 针对写集内实例的新批次也必须被拒绝。
	if _, _, err := e2.RunBatch([]Mutation{
		{Instance: "a", Props: map[string]string{"x": "99"}},
	}, BatchOptions{}); !errors.Is(err, ErrInstanceUncertain) {
		t.Fatalf("RunBatch 涉及未决实例 = %v, want ErrInstanceUncertain", err)
	}
}

func TestDuplicateInstanceRejected(t *testing.T) {
	disk := NewSimDisk()
	e := newEngine(t, disk, nil)
	seedInstances(t, e, map[InstanceID]map[string]string{"a": {"x": "1"}})
	_, _, err := e.RunBatch([]Mutation{
		{Instance: "a", Props: map[string]string{"x": "2"}},
		{Instance: "a", Props: map[string]string{"x": "3"}},
	}, BatchOptions{})
	if !errors.Is(err, ErrDuplicateInstance) {
		t.Fatalf("err = %v, want ErrDuplicateInstance", err)
	}
}
