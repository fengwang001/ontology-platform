package absent

import "testing"

func TestPartitionObserveAccumulateDelete(t *testing.T) {
	p := New(2)
	seen := map[int64]struct{}{}
	if got := p.Observe(1, 10, seen); got != Insert {
		t.Fatalf("observe new = %v, want Insert", got)
	}
	if got := p.Observe(1, 10, seen); got != Same {
		t.Fatalf("observe same = %v, want Same", got)
	}
	if got := p.Observe(1, 11, seen); got != Update {
		t.Fatalf("observe update = %v, want Update", got)
	}
	p.Observe(2, 20, map[int64]struct{}{}) // 独立 seen，不影响主会话

	// 主会话只见到 1：累计后键 2 的 a=1（未达 K=2），不产生候选。
	p.Accumulate(seen)
	n0, cand := p.Snapshot()
	if n0 != 2 || len(cand) != 0 {
		t.Fatalf("n0=%d cand=%v, want n0=2 cand empty", n0, cand)
	}
	if a := p.AbsentCount(2); a != 1 {
		t.Fatalf("a(2)=%d want 1", a)
	}
	if a := p.AbsentCount(1); a != 0 {
		t.Fatalf("a(1)=%d want 0", a)
	}

	// 再来一轮仍只见 1：a(2)=2，达阈值，删除。
	p.Accumulate(seen)
	n0, cand = p.Snapshot()
	if n0 != 2 || len(cand) != 1 || cand[0] != 2 {
		t.Fatalf("n0=%d cand=%v, want [2]", n0, cand)
	}
	if d := p.DeleteCandidates(cand); d != 1 {
		t.Fatalf("deleted=%d want 1", d)
	}
	if _, ok := p.Get(2); ok || p.LiveCount() != 1 || p.AbsentCount(2) != 0 {
		t.Fatal("deleted key 2 should be gone without a")
	}

	// DeleteAbsent（放行路径）：a>=K 即删，不判比例。
	p2 := New(1)
	p2.Observe(5, 5, nil)
	p2.Observe(6, 6, nil)
	p2.Accumulate(map[int64]struct{}{}) // 两者都未见
	if d := p2.DeleteAbsent(); d != 2 {
		t.Fatalf("DeleteAbsent=%d want 2", d)
	}
	if p2.LiveCount() != 0 {
		t.Fatal("all keys should be deleted")
	}
}
