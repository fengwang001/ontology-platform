package collab

import "testing"

// Add 对并发可交换：不同客户端不同顺序给出相同终态。
func TestAddCommutative(t *testing.T) {
	run := func(a1, a2, b1, b2 int64) int64 {
		s := newTestServer(t, "d", map[string]Kind{"count": KindAdd})
		mustSync(t, s, "A", "d", []Op{addOp(1, "count", a1, "g"), addOp(2, "count", a2, "g")}, 1)
		mustSync(t, s, "B", "d", []Op{addOp(1, "count", b1, "g"), addOp(2, "count", b2, "g")}, 2)
		snap, _ := s.Get("d")
		return snap.Fields["count"].Value
	}
	v1 := run(3, 7, -2, 5)
	v2 := run(7, 3, 5, -2)
	if v1 != 13 || v2 != 13 {
		t.Fatalf("non-commutative: %d vs %d", v1, v2)
	}
}

// Add 溢出：组内累加基于组开始快照，第二笔越界 => 首笔也不生效。
func TestAddOverflow(t *testing.T) {
	s := newTestServer(t, "d", basicSchema)
	mustSync(t, s, "c", "d", []Op{addOp(1, "count", MaxInt, "g1")}, 1)
	res := mustSync(t, s, "c", "d",
		[]Op{addOp(2, "count", MaxInt, "g1"), addOp(3, "count", 1, "g1")}, 2)
	if res[0].Kind != ResOverflow || res[1].Kind != ResGroupAborted {
		t.Fatalf("got %v/%v", res[0].Kind, res[1].Kind)
	}
	snap, _ := s.Get("d")
	if snap.Fields["count"].Value != MaxInt {
		t.Fatalf("failed group must not apply, got %d", snap.Fields["count"].Value)
	}
}

func TestKindMismatch(t *testing.T) {
	s := newTestServer(t, "d", basicSchema)
	res := mustSync(t, s, "c", "d",
		[]Op{addOp(1, "title", 1, "g1"), setOp(2, "count", 1, 0, "g1")}, 1)
	if res[0].Kind != ResKindMismatch || res[1].Kind != ResGroupAborted {
		t.Fatalf("got %v/%v", res[0].Kind, res[1].Kind)
	}
	if p := s.Pending("c", "d"); p != 2 {
		t.Fatalf("pending=%d", p)
	}
	snap, _ := s.Get("d")
	if snap.Revision != 0 {
		t.Fatalf("revision must stay 0")
	}
}

// 事务组全有或全无：冲突导致同组 Add 连带失败且不生效。
func TestGroupAtomicity(t *testing.T) {
	s := newTestServer(t, "d", basicSchema)
	mustSync(t, s, "c1", "d", []Op{setOp(1, "title", 10, 0, "g1")}, 1)
	res := mustSync(t, s, "c2", "d", []Op{
		setOp(1, "title", 20, 0, "g1"),
		addOp(2, "count", 5, "g1"),
	}, 2)
	if res[0].Kind != ResConflict || res[1].Kind != ResGroupAborted {
		t.Fatalf("got %+v", res)
	}
	snap, _ := s.Get("d")
	if snap.Fields["count"].Value != 0 || snap.Revision != 1 {
		t.Fatalf("group must be all-or-nothing: rev=%d count=%d",
			snap.Revision, snap.Fields["count"].Value)
	}
}

// 依赖失败链：g1 失败 -> g2(g3) 依赖失败；g4 不依赖正常应用。
func TestDependencyChain(t *testing.T) {
	s := newTestServer(t, "d", basicSchema)
	mustSync(t, s, "c1", "d", []Op{setOp(1, "title", 10, 0, "g1")}, 1)
	ops := []Op{
		setOp(1, "title", 20, 0, "g1"),
		addOp(2, "count", 1, "g2"),
		addOp(3, "count", 2, "g3"),
		addOp(4, "count", 4, "g4"),
	}
	ops[1].Depends = true
	ops[2].Depends = true
	res := mustSync(t, s, "c2", "d", ops, 2)
	want := []ResultKind{ResConflict, ResDepFailed, ResDepFailed, ResApplied}
	for i, w := range want {
		if res[i].Kind != w {
			t.Fatalf("op%d want %v got %v", i+1, w, res[i].Kind)
		}
	}
	snap, _ := s.Get("d")
	if snap.Fields["count"].Value != 4 || snap.Revision != 2 {
		t.Fatalf("snapshot rev=%d count=%d", snap.Revision, snap.Fields["count"].Value)
	}
}

// 依赖前一组合并/应用均视为成功。
func TestDependencyOnMergedGroup(t *testing.T) {
	s := newTestServer(t, "d", basicSchema)
	mustSync(t, s, "c1", "d", []Op{setOp(1, "title", 10, 0, "g1")}, 1)
	ops := []Op{
		setOp(1, "title", 10, 0, "g1"), // merged
		addOp(2, "count", 3, "g2"),     // depends, applied
	}
	ops[1].Depends = true
	res := mustSync(t, s, "c2", "d", ops, 2)
	if res[0].Kind != ResMerged || res[1].Kind != ResApplied {
		t.Fatalf("got %v/%v", res[0].Kind, res[1].Kind)
	}
}
