package apply_test

import (
	"testing"

	"ontology/apply"
	"ontology/idmap"
)

// TestEdgeBFSReleaseOrder 广度优先释放：多个等待者按 aseq 释放，
// 新释放行的等待者追加到队尾，而非沿一条链深入。
func TestEdgeBFSReleaseOrder(t *testing.T) {
	e := mustNew(t, 1000, 100, 1)
	for _, c := range []struct{ id, a int64 }{
		{21, 20}, {22, 20}, {211, 21}, {221, 22},
	} {
		if r := e.Upsert(1, apply.KindDept, c.id, c.a, 0, 0); r.Status != apply.StatusPending {
			t.Fatalf("id%d = %+v, want Pending", c.id, r)
		}
	}
	if r := e.Upsert(1, apply.KindDept, 20, 0, 0, 0); r.Status != apply.StatusApplied || r.Tid != 1 {
		t.Fatalf("20 = %+v", r)
	}
	wantTid := map[int64]int64{20: 1, 21: 2, 22: 3, 211: 4, 221: 5}
	got := map[int64]int64{}
	for _, r := range e.Snapshot() {
		got[r.Key.Id] = r.Tid
	}
	for id, w := range wantTid {
		if got[id] != w {
			t.Fatalf("id%d tid=%d want %d; all=%v", id, got[id], w, got)
		}
	}
}

// TestEdgeRewaitKeepsAseq 重新挂起保留 aseq：等待键改变后，
// 原 aseq 与到达 now 不变，等待键为新内容的首个不就绪引用。
func TestEdgeRewaitKeepsAseq(t *testing.T) {
	e := mustNew(t, 1000, 100, 1)
	up := func(id, a, now int64) apply.Result {
		return e.Upsert(1, apply.KindDept, id, a, 0, now)
	}
	if r := up(1, 2, 10); r.Status != apply.StatusPending {
		t.Fatalf("p1 = %+v", r)
	}
	if r := up(2, 3, 11); r.Status != apply.StatusPending {
		t.Fatalf("p2 = %+v", r)
	}
	if r := up(1, 4, 12); r.Status != apply.StatusPending {
		t.Fatalf("p1 replace = %+v", r)
	}
	p1 := idmap.Key{S: 1, Kind: apply.KindDept, Id: 1}
	for _, p := range e.PendingAll() {
		if p.Entry.Key == p1 {
			if p.Aseq != 1 || p.Arrive != 10 {
				t.Fatalf("p1 = aseq%d arrive%d, want 1/10", p.Aseq, p.Arrive)
			}
			if p.Wait != (idmap.Key{S: 1, Kind: apply.KindDept, Id: 4}) {
				t.Fatalf("p1 wait = %+v, want dept4", p.Wait)
			}
		}
	}
}

// TestEdgeAliveRowRefReplacement 键已存活时重复 Upsert：tid 不变、引用被替换。
func TestEdgeAliveRowRefReplacement(t *testing.T) {
	e := mustNew(t, 1000, 100, 1)
	e.Upsert(1, apply.KindDept, 10, 0, 0, 0)
	e.Upsert(1, apply.KindDept, 11, 0, 0, 0)
	e.Upsert(1, apply.KindDept, 12, 0, 0, 0)
	r := e.Upsert(1, apply.KindDept, 12, 10, 0, 0)
	if r.Status != apply.StatusApplied || r.Tid != 3 {
		t.Fatalf("replace 12 = %+v", r)
	}
	var row apply.Row
	for _, x := range e.Snapshot() {
		if x.Key.Id == 12 {
			row = x
		}
	}
	if row.A != 1 {
		t.Fatalf("12.a = %d, want 1", row.A)
	}
	if e.Next(apply.KindDept) != 4 {
		t.Fatalf("next = %d, want 4", e.Next(apply.KindDept))
	}
}

// TestEdgeDeleteKeepsTid Delete 后再 Upsert 沿用原 tid，计数器无空洞。
func TestEdgeDeleteKeepsTid(t *testing.T) {
	e := mustNew(t, 1000, 100, 1)
	e.Upsert(1, apply.KindDept, 10, 0, 0, 0)
	e.Upsert(1, apply.KindDept, 11, 0, 0, 0)
	if r := e.Delete(1, apply.KindDept, 10, 0); r.Status != apply.StatusDeleted || r.Tid != 1 {
		t.Fatalf("del 10 = %+v", r)
	}
	if r := e.Upsert(1, apply.KindDept, 12, 0, 0, 0); r.Tid != 3 {
		t.Fatalf("12 = %+v, want tid3", r)
	}
	if r := e.Upsert(1, apply.KindDept, 10, 0, 0, 0); r.Tid != 1 {
		t.Fatalf("reup 10 = %+v, want tid1", r)
	}
	if e.Next(apply.KindDept) != 4 {
		t.Fatalf("next = %d, want 4", e.Next(apply.KindDept))
	}
}

// TestEdgeDeletePending 挂起行 Delete 返回 DroppedPending，等待根消失后不再释放。
func TestEdgeDeletePending(t *testing.T) {
	e := mustNew(t, 1000, 100, 1)
	e.Upsert(1, apply.KindDept, 20, 19, 0, 0)
	if r := e.Delete(1, apply.KindDept, 20, 0); r.Status != apply.StatusDroppedPending {
		t.Fatalf("del pending = %+v", r)
	}
	e.Upsert(1, apply.KindDept, 19, 0, 0, 0)
	if e.PendingLen() != 0 {
		t.Fatalf("pending len = %d, want 0", e.PendingLen())
	}
	if r := e.Delete(1, apply.KindDept, 20, 0); r.Status != apply.StatusErrUnknown {
		t.Fatalf("del unknown = %+v", r)
	}
	if r := e.Delete(1, apply.KindDept, 19, 0); r.Status != apply.StatusDeleted {
		t.Fatalf("del alive = %+v", r)
	}
	if r := e.Delete(1, apply.KindDept, 19, 0); r.Status != apply.StatusErrUnknown {
		t.Fatalf("del again = %+v, want ErrUnknown", r)
	}
}

// TestEdgeRejections 参数、权限、时钟拒绝，且拒绝不改变任何状态。
func TestEdgeRejections(t *testing.T) {
	e := mustNew(t, 1000, 100, 1)
	check := func(name string, r apply.Result, want apply.Status) {
		t.Helper()
		if r.Status != want {
			t.Fatalf("%s = %+v, want %v", name, r, want)
		}
	}
	check("shard0", e.Upsert(0, apply.KindDept, 1, 0, 0, 0), apply.StatusErrParam)
	check("shard17", e.Upsert(17, apply.KindDept, 1, 0, 0, 0), apply.StatusErrParam)
	check("id0", e.Upsert(1, apply.KindDept, 0, 0, 0, 0), apply.StatusErrParam)
	check("bigid", e.Upsert(1, apply.KindDept, 1_000_000_001, 0, 0, 0), apply.StatusErrParam)
	check("dept b", e.Upsert(1, apply.KindDept, 1, 0, 1, 0), apply.StatusErrParam)
	check("emp a0", e.Upsert(1, apply.KindEmp, 1, 0, 0, 0), apply.StatusErrParam)
	check("bad kind", e.Upsert(1, 3, 1, 0, 0, 0), apply.StatusErrParam)
	check("neg now", e.Upsert(1, apply.KindDept, 1, 0, 0, -1), apply.StatusErrParam)
	check("big now", e.Upsert(1, apply.KindDept, 1, 0, 0, 1_000_000_000_001), apply.StatusErrParam)
	check("denied", e.Upsert(2, apply.KindDept, 1, 0, 0, 0), apply.StatusErrDenied)

	e.Upsert(1, apply.KindDept, 1, 0, 0, 5)
	check("clock back", e.Upsert(1, apply.KindDept, 2, 0, 0, 4), apply.StatusErrClock)
	if r := e.Tick(3); r.Status != apply.StatusErrClock {
		t.Fatalf("tick back = %+v, want ErrClock", r)
	}

	if e.Next(apply.KindDept) != 2 || len(e.Snapshot()) != 1 || e.PendingLen() != 0 || len(e.Dead()) != 0 {
		t.Fatalf("state changed by rejection: next=%d rows=%d pend=%d dead=%d",
			e.Next(apply.KindDept), len(e.Snapshot()), e.PendingLen(), len(e.Dead()))
	}

	if _, err := apply.New(0, 10, []int{1}); err == nil {
		t.Fatal("New T=0 should fail")
	}
	if _, err := apply.New(10, 0, []int{1}); err == nil {
		t.Fatal("New Q=0 should fail")
	}
	if _, err := apply.New(10, 10, nil); err == nil {
		t.Fatal("New empty allow should fail")
	}
	if _, err := apply.New(10, 10, []int{0}); err == nil {
		t.Fatal("New allow shard0 should fail")
	}
}

// TestEdgeFullVsExpiry ErrFull 按到期后的队列长度判定，但被拒时到期不生效。
func TestEdgeFullVsExpiry(t *testing.T) {
	e := mustNew(t, 10, 1, 1)
	if r := e.Upsert(1, apply.KindDept, 5, 4, 0, 0); r.Status != apply.StatusPending {
		t.Fatalf("5 = %+v", r)
	}
	if r := e.Upsert(1, apply.KindDept, 6, 4, 0, 9); r.Status != apply.StatusErrFull {
		t.Fatalf("6@9 = %+v, want ErrFull", r)
	}
	if e.PendingLen() != 1 || len(e.Dead()) != 0 {
		t.Fatalf("state after full: pend=%d dead=%d", e.PendingLen(), len(e.Dead()))
	}
	// now=10 时 5 恰到期：到期后队列腾空，新增 6 可挂起，本次事件同时完成到期（dead=1）。
	if r := e.Upsert(1, apply.KindDept, 6, 4, 0, 10); r.Status != apply.StatusPending || r.Dead != 1 {
		t.Fatalf("6@10 = %+v, want Pending dead1", r)
	}
	if r := e.Upsert(1, apply.KindDept, 4, 0, 0, 10); r.Status != apply.StatusApplied || r.Tid != 1 {
		t.Fatalf("4 = %+v", r)
	}
	// 5 已死不分配 tid：6 释放得 tid2。
	var row6 apply.Row
	for _, x := range e.Snapshot() {
		if x.Key.Id == 6 {
			row6 = x
		}
	}
	if row6.Tid != 2 {
		t.Fatalf("6 tid = %d, want 2", row6.Tid)
	}
}

// TestEdgeReleaseInspectBound 释放在大挂起队列中只检视被放行/重挂的行：
// 检视数不超过二者之和，与队列总长无关。
func TestEdgeReleaseInspectBound(t *testing.T) {
	e := mustNew(t, 1000, 10000, 1)
	for i := int64(100); i < 300; i++ {
		if r := e.Upsert(1, apply.KindDept, i, 999, 0, 0); r.Status != apply.StatusPending {
			t.Fatalf("id%d = %+v", i, r)
		}
	}
	e.Upsert(1, apply.KindDept, 1, 2, 0, 0)
	e.Upsert(1, apply.KindDept, 2, 3, 0, 0)
	e.ResetInspected()
	e.Upsert(1, apply.KindDept, 3, 0, 0, 0)
	// 释放 3 -> 检视 2（落库）-> 检视 1（落库）；200 个无关挂起行不被检视。
	if got := e.Inspected(); got != 2 {
		t.Fatalf("inspected = %d, want 2 (queue len %d)", got, e.PendingLen())
	}
	if e.PendingLen() != 200 {
		t.Fatalf("pending len = %d, want 200", e.PendingLen())
	}
}

// TestEdgeTickDeadOrder 同批到期按 aseq 升序入死信，T 恰等触发。
func TestEdgeTickDeadOrder(t *testing.T) {
	e := mustNew(t, 5, 100, 1)
	e.Upsert(1, apply.KindDept, 1, 9, 0, 1) // arrive1，expireAt6
	e.Upsert(1, apply.KindDept, 2, 9, 0, 1) // arrive1，expireAt6（同刻按 aseq）
	e.Upsert(1, apply.KindDept, 3, 9, 0, 3) // arrive3
	// 1、2 恰在 now=6 到期（6-1==5），同刻按 aseq；3 在 now=8 到期。
	if r := e.Tick(6); r.Dead != 2 {
		t.Fatalf("tick6 = %+v, want 2", r)
	}
	d := e.Dead()
	if len(d) != 2 || d[0].Id != 1 || d[1].Id != 2 {
		t.Fatalf("dead = %+v, want [1 2]", d)
	}
	if r := e.Tick(8); r.Dead != 1 {
		t.Fatalf("tick8 = %+v, want 1", r)
	}
	// 死信键 Delete 为 ErrUnknown，且落库根不会复活它。
	if r := e.Delete(1, apply.KindDept, 1, 8); r.Status != apply.StatusErrUnknown {
		t.Fatalf("delete dead = %+v", r)
	}
	if r := e.Upsert(1, apply.KindDept, 9, 0, 0, 8); r.Status != apply.StatusApplied {
		t.Fatalf("root 9 = %+v", r)
	}
	if len(e.Dead()) != 3 {
		t.Fatalf("dead len = %d, want 3", len(e.Dead()))
	}
}

// TestEdgeEmpSelfRef emp 自引用 b：先分 tid 再改写，b 指向自己。
func TestEdgeEmpSelfRef(t *testing.T) {
	e := mustNew(t, 1000, 100, 1)
	e.Upsert(1, apply.KindDept, 1, 0, 0, 0) // dept tid1
	r := e.Upsert(1, apply.KindEmp, 50, 1, 50, 0)
	if r.Status != apply.StatusApplied || r.Tid != 1 {
		t.Fatalf("emp50 = %+v", r)
	}
	for _, x := range e.Snapshot() {
		if x.Key.Kind == apply.KindEmp {
			if x.A != 1 || x.B != 1 {
				t.Fatalf("emp row = %+v, want a=1 b=1(self)", x)
			}
		}
	}
}
