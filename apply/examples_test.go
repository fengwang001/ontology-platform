package apply_test

import (
	"sort"
	"testing"

	"ontology/apply"
	"ontology/idmap"
)

func mustNew(t *testing.T, T int64, Q int, allow ...int) *apply.Engine {
	t.Helper()
	e, err := apply.New(T, Q, allow)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func sortedRows(e *apply.Engine) []apply.Row {
	rs := e.Snapshot()
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Key.Kind != rs[j].Key.Kind {
			return rs[i].Key.Kind < rs[j].Key.Kind
		}
		return rs[i].Tid < rs[j].Tid
	})
	return rs
}

// TestExamplesExample1 覆盖题面示例一：dept 链的 BFS 释放与 emp 跨表引用。
func TestExamplesExample1(t *testing.T) {
	e := mustNew(t, 1000, 100, 1)
	up := func(id, a int64) apply.Result {
		return e.Upsert(1, apply.KindDept, id, a, 0, 0)
	}
	if r := up(10, 0); r.Status != apply.StatusApplied || r.Tid != 1 {
		t.Fatalf("dept10 = %+v", r)
	}
	for i, id := range []int64{12, 13, 14, 15} {
		a := map[int64]int64{12: 11, 13: 11, 14: 12, 15: 13}[id]
		r := up(id, a)
		if r.Status != apply.StatusPending {
			t.Fatalf("dept%d = %+v, want Pending", id, r)
		}
		aseq := i + 1
		_ = aseq
	}
	if r := up(11, 0); r.Status != apply.StatusApplied || r.Tid != 2 {
		t.Fatalf("dept11 = %+v, want tid 2", r)
	}
	want := map[int64]int64{10: 1, 11: 2, 12: 3, 13: 4, 14: 5, 15: 6}
	for _, r := range sortedRows(e) {
		if r.Key.Kind != apply.KindDept || r.Tid != want[r.Key.Id] {
			t.Fatalf("dept rows = %+v", sortedRows(e))
		}
	}
	// 引用改写：14.a -> tid(12)=3，15.a -> tid(13)=4
	for _, r := range sortedRows(e) {
		switch r.Key.Id {
		case 14:
			if r.A != 3 {
				t.Fatalf("dept14.a = %d, want 3", r.A)
			}
		case 15:
			if r.A != 4 {
				t.Fatalf("dept15.a = %d, want 4", r.A)
			}
		}
	}

	// emp：100 等 101（b），101 等 dept99（a）；dept99 落库后按链释放。
	if r := e.Upsert(1, apply.KindEmp, 100, 12, 101, 0); r.Status != apply.StatusPending {
		t.Fatalf("emp100 = %+v", r)
	}
	if r := e.Upsert(1, apply.KindEmp, 101, 99, 0, 0); r.Status != apply.StatusPending {
		t.Fatalf("emp101 = %+v", r)
	}
	if r := up(99, 0); r.Status != apply.StatusApplied || r.Tid != 7 {
		t.Fatalf("dept99 = %+v, want tid 7", r)
	}
	var emp100, emp101 apply.Row
	for _, r := range e.Snapshot() {
		if r.Key.Kind == apply.KindEmp && r.Key.Id == 100 {
			emp100 = r
		}
		if r.Key.Kind == apply.KindEmp && r.Key.Id == 101 {
			emp101 = r
		}
	}
	if emp101.Tid != 1 || emp101.A != 7 {
		t.Fatalf("emp101 = %+v, want tid1 a=7", emp101)
	}
	if emp100.Tid != 2 || emp100.A != 3 || emp100.B != 1 {
		t.Fatalf("emp100 = %+v, want tid2 a=3 b=1", emp100)
	}
}

// TestExamplesExample2 覆盖题面示例二：T 恰等触发死信、ErrFull、死信不复活。
func TestExamplesExample2(t *testing.T) {
	e := mustNew(t, 10, 1, 2)
	up := func(id, a, now int64) apply.Result {
		return e.Upsert(2, apply.KindDept, id, a, 0, now)
	}
	if r := up(7, 6, 5); r.Status != apply.StatusPending || r.Dead != 0 {
		t.Fatalf("7 = %+v", r)
	}
	if r := up(8, 6, 9); r.Status != apply.StatusErrFull {
		t.Fatalf("8 = %+v, want ErrFull", r)
	}
	if r := e.Tick(14); r.Dead != 0 {
		t.Fatalf("tick14 dead=%d, want 0", r.Dead)
	}
	if r := e.Tick(15); r.Dead != 1 {
		t.Fatalf("tick15 dead=%d, want 1", r.Dead)
	}
	if d := e.Dead(); len(d) != 1 || d[0] != (idmap.Key{S: 2, Kind: apply.KindDept, Id: 7}) {
		t.Fatalf("dead = %+v", d)
	}
	if r := up(8, 6, 15); r.Status != apply.StatusPending {
		t.Fatalf("8 again = %+v, want Pending", r)
	}
	if r := up(6, 0, 16); r.Status != apply.StatusApplied || r.Tid != 1 {
		t.Fatalf("6 = %+v, want tid 1", r)
	}
	// 8 释放得 tid 2；7 已死，绝不复活、绝不占 tid。
	var row8 apply.Row
	for _, r := range e.Snapshot() {
		if r.Key.Id == 8 {
			row8 = r
		}
	}
	if row8.Tid != 2 || row8.A != 1 {
		t.Fatalf("dept8 = %+v, want tid2 a=1", row8)
	}
	if e.Next(apply.KindDept) != 3 {
		t.Fatalf("dept next = %d, want 3 (dead row never allocated)", e.Next(apply.KindDept))
	}
}

// TestExamplesExample3 覆盖题面示例三：自引用立即就绪，改写为自身 tid。
func TestExamplesExample3(t *testing.T) {
	e := mustNew(t, 10, 10, 1)
	r := e.Upsert(1, apply.KindDept, 3, 3, 0, 0)
	if r.Status != apply.StatusApplied || r.Tid != 1 {
		t.Fatalf("dept3 = %+v", r)
	}
	rows := sortedRows(e)
	if len(rows) != 1 || rows[0].A != 1 {
		t.Fatalf("dept3 row = %+v, want a->self tid 1", rows)
	}
}
