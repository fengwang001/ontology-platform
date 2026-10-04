package apply_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/apply"
)

func mustNew(t *testing.T, timeout int64, q int, allow ...int) *apply.Merger {
	t.Helper()
	m, err := apply.New(timeout, q, allow)
	if err != nil {
		t.Fatalf("New(%d,%d,%v): %v", timeout, q, allow, err)
	}
	return m
}

func wantResult(t *testing.T, got apply.Result, err error, oc apply.Outcome, tid int64) {
	t.Helper()
	if err != nil {
		t.Fatalf("got err %v, want %v tid=%d", err, oc, tid)
	}
	if got.Outcome != oc || got.Tid != tid {
		t.Fatalf("got %v tid=%d, want %v tid=%d", got.Outcome, got.Tid, oc, tid)
	}
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got err %v, want %v", err, want)
	}
}

func wantTarget(t *testing.T, m *apply.Merger, s, kind int, id int64, tid, a, b int64) {
	t.Helper()
	gtid, ga, gb, alive, ok := m.Target(s, kind, id)
	if !ok {
		t.Fatalf("Target(%d,%d,%d): not mapped", s, kind, id)
	}
	if gtid != tid || ga != a || gb != b || !alive {
		t.Fatalf("Target(%d,%d,%d) = tid=%d a=%d b=%d alive=%v, want tid=%d a=%d b=%d alive=true",
			s, kind, id, gtid, ga, gb, alive, tid, a, b)
	}
}

// 例一：广度优先释放次序 + 跨表（emp 等 dept、emp 等 emp）。
func TestExample1BFSReleaseOrder(t *testing.T) {
	m := mustNew(t, 1_000_000_000, 100, 1)

	r, err := m.Upsert(1, 1, 10, 0, 0, 0)
	wantResult(t, r, err, apply.Applied, 1)
	for i, id := range []int64{12, 13, 14, 15} {
		a := map[int64]int64{12: 11, 13: 11, 14: 12, 15: 13}[id]
		r, err = m.Upsert(1, 1, id, a, 0, int64(i+1))
		wantResult(t, r, err, apply.Pending, 0)
		row, ok := m.PendingRow(1, 1, id)
		if !ok || row.Aseq != int64(i+1) {
			t.Fatalf("id=%d pending aseq=%d ok=%v, want aseq=%d", id, row.Aseq, ok, i+1)
		}
	}
	r, err = m.Upsert(1, 1, 11, 0, 0, 5)
	wantResult(t, r, err, apply.Applied, 2)
	// BFS：12,13 先于 14,15 落库；DFS 会让 14 拿到 tid 4。
	wantTarget(t, m, 1, 1, 12, 3, 2, 0)
	wantTarget(t, m, 1, 1, 13, 4, 2, 0)
	wantTarget(t, m, 1, 1, 14, 5, 3, 0)
	wantTarget(t, m, 1, 1, 15, 6, 4, 0)
	if n := m.PendingLen(); n != 0 {
		t.Fatalf("PendingLen=%d, want 0", n)
	}

	// emp 100 等 emp 101（b），emp 101 等 dept 99（a）。
	r, err = m.Upsert(1, 2, 100, 12, 101, 6)
	wantResult(t, r, err, apply.Pending, 0)
	r, err = m.Upsert(1, 2, 101, 99, 0, 7)
	wantResult(t, r, err, apply.Pending, 0)
	r, err = m.Upsert(1, 1, 99, 0, 0, 8)
	wantResult(t, r, err, apply.Applied, 7)
	wantTarget(t, m, 1, 2, 101, 1, 7, 0)
	wantTarget(t, m, 1, 2, 100, 2, 3, 1)
}

// 例二：挂起超时进死信、ErrFull 与到期处理的先后、死信不再恢复。
func TestExample2TimeoutFullDead(t *testing.T) {
	m := mustNew(t, 10, 1, 2)

	r, err := m.Upsert(2, 1, 7, 6, 0, 5)
	wantResult(t, r, err, apply.Pending, 0)

	// 队列已满（到期处理在 now=9 时不移出任何行），ErrFull 且状态不变。
	_, err = m.Upsert(2, 1, 8, 6, 0, 9)
	wantErr(t, err, apply.ErrFull)
	if n := m.PendingLen(); n != 1 {
		t.Fatalf("after ErrFull PendingLen=%d, want 1", n)
	}
	row, ok := m.PendingRow(2, 1, 7)
	if !ok || row.Arrival != 5 || row.Aseq != 1 {
		t.Fatalf("after ErrFull row=%+v ok=%v, want arrival=5 aseq=1", row, ok)
	}
	// ErrFull 不推进最大 now：now=6 仍应被接受。
	if _, err = m.Tick(6); err != nil {
		t.Fatalf("Tick(6) after ErrFull: %v", err)
	}

	n, err := m.Tick(14) // 14-5=9 < 10，仍挂起
	if err != nil || n != 0 {
		t.Fatalf("Tick(14) = %d,%v, want 0,nil", n, err)
	}
	n, err = m.Tick(15) // 15-5=10 >= 10，进死信
	if err != nil || n != 1 {
		t.Fatalf("Tick(15) = %d,%v, want 1,nil", n, err)
	}
	dead := m.Dead()
	if len(dead) != 1 || dead[0] != (apply.Key{Shard: 2, Kind: 1, ID: 7}) {
		t.Fatalf("Dead()=%v, want [{2 1 7}]", dead)
	}

	// 队列已空，不再报满。
	r, err = m.Upsert(2, 1, 8, 6, 0, 15)
	wantResult(t, r, err, apply.Pending, 0)
	r, err = m.Upsert(2, 1, 6, 0, 0, 16)
	wantResult(t, r, err, apply.Applied, 1)
	wantTarget(t, m, 2, 1, 8, 2, 1, 0)
	// 7 已死，不会被释放。
	if _, _, _, _, ok := m.Target(2, 1, 7); ok {
		t.Fatal("id=7 must not be mapped after dead")
	}
	if len(m.Dead()) != 1 {
		t.Fatalf("Dead() len=%d, want 1", len(m.Dead()))
	}
}

// 到期处理在 Upsert 事件开头执行：到期行移出后队列有空位，不再 ErrFull。
func TestExpiryFreesSlotBeforeFullCheck(t *testing.T) {
	m := mustNew(t, 10, 1, 2)
	r, err := m.Upsert(2, 1, 7, 6, 0, 5)
	wantResult(t, r, err, apply.Pending, 0)
	// now=15：7 到期进死信，队列腾出空位，8 挂起成功而非 ErrFull。
	r, err = m.Upsert(2, 1, 8, 6, 0, 15)
	wantResult(t, r, err, apply.Pending, 0)
	dead := m.Dead()
	if len(dead) != 1 || dead[0] != (apply.Key{Shard: 2, Kind: 1, ID: 7}) {
		t.Fatalf("Dead()=%v, want [{2 1 7}]", dead)
	}
}

// 例三：自引用立刻就绪，a 改写为自己的 tid。
func TestExample3SelfReference(t *testing.T) {
	m := mustNew(t, 10, 4, 1)
	r, err := m.Upsert(1, 1, 3, 3, 0, 0)
	wantResult(t, r, err, apply.Applied, 1)
	wantTarget(t, m, 1, 1, 3, 1, 1, 0)
}

// 释放时仍不就绪的行改等新的等待键，但保留 aseq 与到达 now。
func TestRependKeepsAseqAndArrival(t *testing.T) {
	m := mustNew(t, 1_000_000_000, 10, 1)

	r, err := m.Upsert(1, 2, 10, 5, 7, 3) // a=dept5 未映射 -> 挂起
	wantResult(t, r, err, apply.Pending, 0)
	r, err = m.Upsert(1, 1, 5, 0, 0, 4) // 落库，触发对 emp10 的重判
	wantResult(t, r, err, apply.Applied, 1)
	// emp10 的 a 就绪了，但 b=emp7 未映射：改等 (1,2,7)，aseq/到达 now 不变。
	row, ok := m.PendingRow(1, 2, 10)
	if !ok {
		t.Fatal("emp10 should still be pending")
	}
	if row.Aseq != 1 || row.Arrival != 3 {
		t.Fatalf("aseq=%d arrival=%d, want aseq=1 arrival=3", row.Aseq, row.Arrival)
	}
	if row.Wait != (apply.Key{Shard: 1, Kind: 2, ID: 7}) {
		t.Fatalf("wait=%v, want {1 2 7}", row.Wait)
	}
	if row.A != 5 || row.B != 7 {
		t.Fatalf("content a=%d b=%d, want 5,7", row.A, row.B)
	}

	r, err = m.Upsert(1, 2, 7, 5, 0, 5) // 就绪，落库后释放 emp10
	wantResult(t, r, err, apply.Applied, 1)
	wantTarget(t, m, 1, 2, 10, 2, 1, 1)
}

// 不就绪的 Upsert 替换同键挂起行内容，保留 aseq 与到达 now。
func TestPendingReplaceKeepsAseq(t *testing.T) {
	m := mustNew(t, 1_000_000_000, 10, 1)
	r, err := m.Upsert(1, 1, 5, 9, 0, 2)
	wantResult(t, r, err, apply.Pending, 0)
	r, err = m.Upsert(1, 1, 5, 8, 0, 7) // 仍不就绪（dept8 未映射），替换内容
	wantResult(t, r, err, apply.Pending, 0)
	row, ok := m.PendingRow(1, 1, 5)
	if !ok || row.Aseq != 1 || row.Arrival != 2 || row.A != 8 {
		t.Fatalf("row=%+v ok=%v, want aseq=1 arrival=2 a=8", row, ok)
	}
	if row.Wait != (apply.Key{Shard: 1, Kind: 1, ID: 8}) {
		t.Fatalf("wait=%v, want {1 1 8}", row.Wait)
	}
	r, err = m.Upsert(1, 1, 8, 0, 0, 8)
	wantResult(t, r, err, apply.Applied, 1)
	wantTarget(t, m, 1, 1, 5, 2, 1, 0)
}

// 存活行被再次 Upsert：沿用 tid、替换引用；同键挂起行被丢弃。
func TestAliveRowRefsReplaced(t *testing.T) {
	m := mustNew(t, 1_000_000_000, 10, 1)
	r, err := m.Upsert(1, 1, 1, 0, 0, 0)
	wantResult(t, r, err, apply.Applied, 1)
	r, err = m.Upsert(1, 1, 2, 0, 0, 1)
	wantResult(t, r, err, apply.Applied, 2)
	r, err = m.Upsert(1, 1, 1, 2, 0, 2) // 就绪：替换 a 为 dept2 的 tid
	wantResult(t, r, err, apply.Applied, 1)
	wantTarget(t, m, 1, 1, 1, 1, 2, 0)

	// 键有挂起行时就绪 Upsert：丢弃挂起行，以较新事件为准。
	r, err = m.Upsert(1, 1, 3, 9, 0, 3)
	wantResult(t, r, err, apply.Pending, 0)
	r, err = m.Upsert(1, 1, 3, 0, 0, 4)
	wantResult(t, r, err, apply.Applied, 3)
	if n := m.PendingLen(); n != 0 {
		t.Fatalf("PendingLen=%d, want 0", n)
	}
	wantTarget(t, m, 1, 1, 3, 3, 0, 0)
}

// 存活行 Upsert 不就绪的新引用：存活行不动，新内容进挂起。
func TestAliveRowPendingKeepsOldRefs(t *testing.T) {
	m := mustNew(t, 1_000_000_000, 10, 1)
	r, err := m.Upsert(1, 1, 1, 0, 0, 0)
	wantResult(t, r, err, apply.Applied, 1)
	r, err = m.Upsert(1, 1, 1, 7, 0, 1) // dept7 未映射 -> 挂起，存活行不动
	wantResult(t, r, err, apply.Pending, 0)
	wantTarget(t, m, 1, 1, 1, 1, 0, 0)
	r, err = m.Upsert(1, 1, 7, 0, 0, 2) // 落库后释放挂起的 id=1，a 改写为 2
	wantResult(t, r, err, apply.Applied, 2)
	wantTarget(t, m, 1, 1, 1, 1, 2, 0)
}

// Delete 后再 Upsert 沿用原 tid；计数器不回收。
func TestDeleteThenUpsertKeepsTid(t *testing.T) {
	m := mustNew(t, 1_000_000_000, 10, 1)
	r, err := m.Upsert(1, 1, 1, 0, 0, 0)
	wantResult(t, r, err, apply.Applied, 1)
	r, err = m.Delete(1, 1, 1, 1)
	wantResult(t, r, err, apply.Deleted, 0)
	tid, _, _, alive, ok := m.Target(1, 1, 1)
	if !ok || alive || tid != 1 {
		t.Fatalf("after delete: tid=%d alive=%v ok=%v, want tid=1 alive=false ok=true", tid, alive, ok)
	}
	r, err = m.Upsert(1, 1, 1, 0, 0, 2)
	wantResult(t, r, err, apply.Applied, 1) // 沿用 tid 1
	r, err = m.Upsert(1, 1, 2, 0, 0, 3)
	wantResult(t, r, err, apply.Applied, 2) // 计数器不回收
}

// Delete 挂起行只删挂起行；重复 Delete 报 ErrUnknown。
func TestDeletePendingRow(t *testing.T) {
	m := mustNew(t, 1_000_000_000, 10, 1)
	r, err := m.Upsert(1, 1, 1, 2, 0, 0)
	wantResult(t, r, err, apply.Pending, 0)
	r, err = m.Delete(1, 1, 1, 1)
	wantResult(t, r, err, apply.DroppedPending, 0)
	if n := m.PendingLen(); n != 0 {
		t.Fatalf("PendingLen=%d, want 0", n)
	}
	_, err = m.Delete(1, 1, 1, 2)
	wantErr(t, err, apply.ErrUnknown)
	// 挂起行被删后再次 Upsert 同一键：aseq 重新计数。
	r, err = m.Upsert(1, 1, 1, 2, 0, 3)
	wantResult(t, r, err, apply.Pending, 0)
	row, _ := m.PendingRow(1, 1, 1)
	if row.Aseq != 2 {
		t.Fatalf("aseq=%d, want 2", row.Aseq)
	}
}

// Delete 存活行后映射保留、目标行不存活；再次 Delete 报 ErrUnknown。
func TestDeleteAliveRow(t *testing.T) {
	m := mustNew(t, 1_000_000_000, 10, 1)
	r, err := m.Upsert(1, 1, 1, 0, 0, 0)
	wantResult(t, r, err, apply.Applied, 1)
	r, err = m.Delete(1, 1, 1, 1)
	wantResult(t, r, err, apply.Deleted, 0)
	_, err = m.Delete(1, 1, 1, 2)
	wantErr(t, err, apply.ErrUnknown)
	_, err = m.Delete(1, 1, 99, 2)
	wantErr(t, err, apply.ErrUnknown)
}

// 参数非法先于权限，权限先于时钟，时钟先于 ErrUnknown。
func TestRejectionOrder(t *testing.T) {
	m := mustNew(t, 10, 5, 1)

	// 参数非法（dept 的 b 非 0）先于 ErrDenied。
	_, err := m.Upsert(2, 1, 1, 0, 1, 0)
	wantErr(t, err, apply.ErrInvalid)
	// emp 的 a 为 0 非法。
	_, err = m.Upsert(1, 2, 1, 0, 0, 0)
	wantErr(t, err, apply.ErrInvalid)
	// 分片越界、kind 越界、id 越界、now 越界。
	for _, s := range []int{0, 17} {
		if _, err := m.Upsert(s, 1, 1, 0, 0, 0); !errors.Is(err, apply.ErrInvalid) {
			t.Fatalf("s=%d: %v, want ErrInvalid", s, err)
		}
	}
	_, err = m.Upsert(1, 3, 1, 0, 0, 0)
	wantErr(t, err, apply.ErrInvalid)
	_, err = m.Upsert(1, 1, 0, 0, 0, 0)
	wantErr(t, err, apply.ErrInvalid)
	_, err = m.Upsert(1, 1, 1, 0, 0, 1_000_000_000_001)
	wantErr(t, err, apply.ErrInvalid)

	// 分片不在 allow。
	_, err = m.Upsert(2, 1, 1, 0, 0, 0)
	wantErr(t, err, apply.ErrDenied)
	_, err = m.Delete(2, 1, 1, 0)
	wantErr(t, err, apply.ErrDenied)

	// 时钟回退。
	r, err := m.Upsert(1, 1, 1, 0, 0, 5)
	wantResult(t, r, err, apply.Applied, 1)
	_, err = m.Upsert(1, 1, 2, 0, 0, 4)
	wantErr(t, err, apply.ErrClock)
	_, err = m.Tick(4)
	wantErr(t, err, apply.ErrClock)
	// 时钟先于 Delete 的 ErrUnknown。
	_, err = m.Delete(1, 1, 99, 4)
	wantErr(t, err, apply.ErrClock)
	// now 等于最大 now 合法。
	if _, err = m.Tick(5); err != nil {
		t.Fatalf("Tick(5): %v", err)
	}
	// 构造参数非法。
	if _, err = apply.New(0, 1, []int{1}); !errors.Is(err, apply.ErrInvalid) {
		t.Fatalf("New t=0: %v", err)
	}
	if _, err = apply.New(1, 0, []int{1}); !errors.Is(err, apply.ErrInvalid) {
		t.Fatalf("New q=0: %v", err)
	}
	if _, err = apply.New(1, 1, []int{17}); !errors.Is(err, apply.ErrInvalid) {
		t.Fatalf("New allow=17: %v", err)
	}
}

// 被拒事件不改变映射、计数器、挂起队列、死信与最大 now。
func TestRejectionKeepsState(t *testing.T) {
	m := mustNew(t, 100, 1, 1)
	r, err := m.Upsert(1, 1, 1, 9, 0, 10)
	wantResult(t, r, err, apply.Pending, 0)

	rejected := []func() error{
		func() error { _, e := m.Upsert(1, 1, 2, 0, 1, 11); return e }, // ErrInvalid
		func() error { _, e := m.Upsert(2, 1, 2, 0, 0, 11); return e }, // ErrDenied
		func() error { _, e := m.Upsert(1, 1, 2, 0, 0, 9); return e },  // ErrClock
		func() error { _, e := m.Delete(1, 1, 7, 11); return e },       // ErrUnknown
		func() error { _, e := m.Upsert(1, 1, 2, 8, 0, 11); return e }, // ErrFull
	}
	for i, f := range rejected {
		if err := f(); err == nil {
			t.Fatalf("rejected[%d] unexpectedly accepted", i)
		}
	}
	if n := m.PendingLen(); n != 1 {
		t.Fatalf("PendingLen=%d, want 1", n)
	}
	row, ok := m.PendingRow(1, 1, 1)
	if !ok || row.Arrival != 10 || row.Aseq != 1 || row.A != 9 {
		t.Fatalf("row=%+v ok=%v, want arrival=10 aseq=1 a=9", row, ok)
	}
	if len(m.Dead()) != 0 {
		t.Fatalf("Dead()=%v, want empty", m.Dead())
	}
	if _, _, _, _, ok := m.Target(1, 1, 1); ok {
		t.Fatal("id=1 must not be mapped")
	}
	// 最大 now 未被被拒事件推进：now=10 与 now=11 仍应被接受。
	if _, err := m.Tick(10); err != nil {
		t.Fatalf("Tick(10): %v", err)
	}
	if _, err := m.Tick(11); err != nil {
		t.Fatalf("Tick(11): %v", err)
	}
}

// 相同事件序列重放得到相同 tid 与死信；同一事件重复 Upsert 不改 tid。
func TestReplayDeterministic(t *testing.T) {
	type ev struct {
		op           int // 0 upsert 1 delete 2 tick
		s, kind      int
		id, a, b, nw int64
	}
	seq := []ev{
		{0, 1, 1, 10, 0, 0, 0}, {0, 1, 1, 12, 11, 0, 1}, {0, 1, 1, 13, 11, 0, 2},
		{0, 1, 1, 14, 12, 0, 3}, {0, 1, 1, 15, 13, 0, 4}, {0, 1, 1, 11, 0, 0, 5},
		{0, 1, 2, 100, 12, 101, 6}, {0, 1, 2, 101, 99, 0, 7}, {0, 1, 1, 99, 0, 0, 8},
		{1, 1, 1, 12, 0, 0, 9}, {0, 1, 1, 12, 0, 0, 10}, {2, 0, 0, 0, 0, 0, 11},
		{0, 1, 1, 12, 0, 0, 12}, // 重复 Upsert：tid 不变
	}
	run := func() ([]apply.Result, []apply.Key) {
		m := mustNew(t, 1_000_000_000, 100, 1)
		var out []apply.Result
		for _, e := range seq {
			var r apply.Result
			var err error
			switch e.op {
			case 0:
				r, err = m.Upsert(e.s, e.kind, e.id, e.a, e.b, e.nw)
			case 1:
				r, err = m.Delete(e.s, e.kind, e.id, e.nw)
			case 2:
				var n int
				n, err = m.Tick(e.nw)
				r = apply.Result{Outcome: apply.Outcome(n)}
			}
			if err != nil {
				t.Fatalf("event %+v: %v", e, err)
			}
			out = append(out, r)
		}
		return out, m.Dead()
	}
	out1, dead1 := run()
	out2, dead2 := run()
	if len(out1) != len(out2) {
		t.Fatal("replay length mismatch")
	}
	for i := range out1 {
		if out1[i] != out2[i] {
			t.Fatalf("event %d: %v vs %v", i, out1[i], out2[i])
		}
	}
	if len(dead1) != len(dead2) {
		t.Fatalf("dead %v vs %v", dead1, dead2)
	}
	for i := range dead1 {
		if dead1[i] != dead2[i] {
			t.Fatalf("dead[%d]: %v vs %v", i, dead1[i], dead2[i])
		}
	}
	// Delete 后沿用原 tid；同一事件重复 Upsert 不改 tid。
	if out1[10].Tid != out1[12].Tid || out1[12].Tid == 0 {
		t.Fatalf("repeated upsert tid %d != %d", out1[12].Tid, out1[10].Tid)
	}
}

// 并发调用等价于某个串行顺序：映射保持单射、tid 连续无空洞。
func TestConcurrentSmoke(t *testing.T) {
	m := mustNew(t, 1_000_000_000, 10_000, 1, 2, 3, 4, 5, 6, 7, 8)
	const shards = 8
	const rows = 200
	var wg sync.WaitGroup
	for g := 0; g < shards; g++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			if _, err := m.Upsert(s, 1, 1, 0, 0, 0); err != nil {
				t.Errorf("dept upsert s=%d: %v", s, err)
				return
			}
			for i := int64(1); i <= rows; i++ {
				if _, err := m.Upsert(s, 2, i, 1, 0, 0); err != nil {
					t.Errorf("emp upsert s=%d id=%d: %v", s, i, err)
					return
				}
			}
		}(g + 1)
	}
	wg.Wait()
	// 校验每表 tid 恰好是 1..N 的排列（连续无空洞、单射）。
	check := func(kind, want int) {
		seen := make(map[int64]bool)
		for s := 1; s <= shards; s++ {
			for i := int64(1); i <= int64(want)/shards; i++ {
				tid, _, _, _, ok := m.Target(s, kind, i)
				if !ok {
					t.Fatalf("kind=%d s=%d id=%d not mapped", kind, s, i)
				}
				if seen[tid] {
					t.Fatalf("kind=%d tid=%d mapped twice", kind, tid)
				}
				seen[tid] = true
			}
		}
		for tid := int64(1); tid <= int64(want); tid++ {
			if !seen[tid] {
				t.Fatalf("kind=%d tid=%d missing (hole)", kind, tid)
			}
		}
	}
	check(1, shards)
	check(2, shards*rows)
}
