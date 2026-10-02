package sps

import "testing"

// 增权紧边而终点另有紧边：距离不变，仅父边变。
func TestScenarioIncreaseTightAlt(t *testing.T) {
	svc, _ := New(3, 0, 10, 4)
	_r1, err := svc.AddEdge(0, 1, 5) // id1 紧（最小）
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r1
	_r2, err := svc.AddEdge(0, 1, 5) // id2 平行，也紧但编号大
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r2
	_r3, err := svc.AddEdge(0, 2, 7) // id3
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r3
	_r4, err := svc.AddEdge(1, 2, 2) // id4: 0->1->2 = 7，也紧
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r4
	r, err := svc.SetWeight(1, 9) // id1 变长不再紧；id2 保持紧
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	wantResult(t, r, 5, nil, []int{1}) // id1 离开紧边集合，父边切到 id2
	if p, ok, _ := svc.Parent(1); !ok || p != 2 {
		t.Fatalf("parent(1) = (%d,%v), want 2", p, ok)
	}
	// 节点2距离仍7，父边保持编号更小的 id3（0->2 直连）。
	wantDist(t, svc, 2, 7, true)
	if p, ok, _ := svc.Parent(2); !ok || p != 3 {
		t.Fatalf("parent(2) = (%d,%v), want 3", p, ok)
	}
}

// 增权紧边而终点唯一：连带所有因此失去紧边的后继。
func TestScenarioIncreaseTightUniqueCascade(t *testing.T) {
	svc, _ := New(4, 0, 10, 4)
	_r5, err := svc.AddEdge(0, 1, 1) // 1
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r5
	_r6, err := svc.AddEdge(1, 2, 1) // 2
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r6
	_r7, err := svc.AddEdge(2, 3, 1) // 3
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r7
	r, err := svc.SetWeight(1, 5)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	wantResult(t, r, 4, []int{1, 2, 3}, nil)
	wantDist(t, svc, 1, 5, true)
	wantDist(t, svc, 2, 6, true)
	wantDist(t, svc, 3, 7, true)
}

// 后继另有来自未受影响节点的紧边：不连带。
func TestScenarioSuccessorHasOutsideTight(t *testing.T) {
	svc, _ := New(4, 0, 10, 4)
	_r8, err := svc.AddEdge(0, 1, 1) // 1
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r8
	_r9, err := svc.AddEdge(0, 2, 3) // 2
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r9
	_r10, err := svc.AddEdge(1, 3, 4) // 3: d3=5
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r10
	_r11, err := svc.AddEdge(2, 3, 2) // 4: d3=5（0->2->3），两条紧边
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r11
	// 删除边3后节点3仍有边4紧，距离不变；边4是最小（唯一）紧边。
	r, err := svc.RemoveEdge(3)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	wantResult(t, r, 5, nil, []int{3})
	wantDist(t, svc, 3, 5, true)
	if p, ok, _ := svc.Parent(3); !ok || p != 4 {
		t.Fatalf("parent(3)=%d, want 4", p)
	}
}

// 父边只在编号更小的边变紧时切换。
func TestScenarioParentSwitchByID(t *testing.T) {
	svc, _ := New(3, 0, 10, 8)
	_r12, err := svc.AddEdge(0, 1, 5) // id1
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r12
	_r13, err := svc.AddEdge(0, 1, 8) // id2 非紧
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r13
	_r14, err := svc.AddEdge(0, 1, 5) // id3 平行紧，但编号更大 -> 不切换
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r14
	if p, _, _ := svc.Parent(1); p != 1 {
		t.Fatalf("parent = %d, want 1", p)
	}
	r, err := svc.SetWeight(2, 5) // id2 降权恰好变紧，编号仍大于1
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	wantResult(t, r, 4, nil, nil)
	if p, _, _ := svc.Parent(1); p != 1 {
		t.Fatalf("parent = %d, still want 1", p)
	}
	_r15, err := svc.RemoveEdge(1) // v5 删除最小紧边，父边切到编号最小的剩余紧边 id2
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r15
	if p, _, _ := svc.Parent(1); p != 2 {
		t.Fatalf("parent = %d, want 2", p)
	}
	// id2 增权一点离开紧边；id3 仍紧，父边切到3。
	r2, err := svc.SetWeight(2, 6) // v6
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	wantResult(t, r2, 6, nil, []int{1})
	if p, _, _ := svc.Parent(1); p != 3 {
		t.Fatalf("parent = %d, want 3", p)
	}
}

// 降权使边恰好变紧但不改距离。
func TestScenarioDecreaseBecomeTight(t *testing.T) {
	svc, _ := New(3, 0, 10, 4)
	_r16, err := svc.AddEdge(0, 1, 5) // id1
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r16
	_r17, err := svc.AddEdge(0, 2, 7) // id2
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r17
	_r18, err := svc.AddEdge(1, 2, 3) // id3: 8 > 7 非紧
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r18
	// id3 降权到2，0->1->2=7 恰好紧；编号3 > id2，父边不变，无报告。
	r, err := svc.SetWeight(3, 2)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	wantResult(t, r, 4, nil, nil)
	// 新增一条编号更大的不影响；编号更小的新紧边（不可能新增更小id）由删除路径覆盖。
}

// 降权使距离变小并沿后继传播。
func TestScenarioDecreasePropagates(t *testing.T) {
	svc, _ := New(4, 0, 10, 4)
	_r19, err := svc.AddEdge(0, 1, 10) // 1
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r19
	_r20, err := svc.AddEdge(0, 2, 10) // 2
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r20
	_r21, err := svc.AddEdge(2, 1, 10) // 3: d1=10（via 0），d1 via2=20
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r21
	_r22, err := svc.AddEdge(1, 3, 10) // 4: d3=20
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r22
	r, err := svc.SetWeight(2, 3) // d2=3 -> d1 via2=13? 仍>10；
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	// 重新构造：直接降id1
	_ = r
	r2, err := svc.SetWeight(1, 2) // d1=2, d3=12
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	wantResult(t, r2, 6, []int{1, 3}, nil)
	wantDist(t, svc, 1, 2, true)
	wantDist(t, svc, 3, 12, true)
}

// 新增边使原不可达节点可达；删除使节点变不可达。
func TestScenarioReachabilityFlip(t *testing.T) {
	svc, _ := New(4, 0, 10, 8)
	_r23, err := svc.AddEdge(0, 1, 3) // 1
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r23
	r, err := svc.AddEdge(1, 2, 4) // 2
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	wantResult(t, r, 2, []int{2}, nil)
	wantDist(t, svc, 2, 7, true)
	// 节点3始终不可达。
	if _, ok, _ := svc.Dist(3); ok {
		t.Fatal("node3 should be unreachable")
	}
	if _, ok, _ := svc.Parent(3); ok {
		t.Fatal("unreachable node has parent")
	}
	// 删除边2，节点2变不可达。
	r2, err := svc.RemoveEdge(2)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	wantResult(t, r2, 3, []int{2}, nil)
	if _, ok, _ := svc.Dist(2); ok {
		t.Fatal("node2 should become unreachable")
	}
	if _, ok, _ := svc.Parent(2); ok {
		t.Fatal("unreachable node still has parent")
	}
	// Path 对不可达节点报错。
	if _, err := svc.Path(2); err != ErrUnreachable {
		t.Fatalf("Path err = %v, want ErrUnreachable", err)
	}
	// 源点无父边，Path(0) 为空。
	p0, err := svc.Path(0)
	if err != nil || len(p0) != 0 {
		t.Fatalf("Path(0) = %v,%v", p0, err)
	}
}

// Path 沿父边回溯并按源到终点顺序给出边编号。
func TestScenarioPath(t *testing.T) {
	svc, _ := New(4, 0, 10, 4)
	_r24, err := svc.AddEdge(0, 1, 1) // 1
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r24
	_r25, err := svc.AddEdge(0, 2, 5) // 2
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r25
	_r26, err := svc.AddEdge(1, 2, 4) // 3: 0-1-2=5 紧，id3>id2，父边仍是2
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r26
	_r27, err := svc.AddEdge(2, 3, 1) // 4
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r27
	p, err := svc.Path(3)
	if err != nil {
		t.Fatal(err)
	}
	// d2 父边 id2（0->2），所以路径为 2,4。
	if len(p) != 2 || p[0] != 2 || p[1] != 4 {
		t.Fatalf("Path(3) = %v, want [2 4]", p)
	}
}

// DistAt 恰为最旧保留版本与恰早一版的区别。
func TestScenarioHistoryBoundary(t *testing.T) {
	k := 3
	svc, _ := New(2, 0, 10, k)
	_r28, err := svc.AddEdge(0, 1, 10) // v1: d1=10
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r28
	_r29, err := svc.SetWeight(1, 20) // v2: d1=20
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r29
	_r30, err := svc.SetWeight(1, 30) // v3: d1=30
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r30
	_r31, err := svc.SetWeight(1, 40) // v4: d1=40，窗口=2,3,4
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r31
	if d, ok, _ := svc.DistAt(1, 2); !ok || d != 20 {
		t.Fatalf("oldest kept v2: got (%d,%v)", d, ok)
	}
	if _, _, err := svc.DistAt(1, 1); err != ErrVersionStale {
		t.Fatalf("just before oldest: %v", err)
	}
	// 版本0在窗口外时仍然按“历史过期”报告吗？规格只保证版本0语义；
	// 这里版本0已早于最旧窗口 -> ErrVersionStale。
	if _, _, err := svc.DistAt(1, 0); err != ErrVersionStale {
		t.Fatalf("v0 outside window: %v", err)
	}
}

// 被拒绝操作不耗编号（参数非法 + 容量满）。
func TestScenarioRejectedNoSideEffects(t *testing.T) {
	svc, _ := New(3, 0, 2, 4)
	_r32, err := svc.AddEdge(0, 1, 1) // id1
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r32
	if _, err := svc.AddEdge(0, 0, 1); err != ErrInvalidArgument {
		t.Fatal(err)
	}
	_r33, err := svc.AddEdge(0, 2, 1) // id2，容量满
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r33
	if _, err := svc.AddEdge(1, 2, 1); err != ErrCapacityFull {
		t.Fatal(err)
	}
	_r34, err := svc.RemoveEdge(1) // v3 释放容量
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	_ = _r34
	r, err := svc.AddEdge(2, 1, 1) // id3
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if r.EdgeID != 3 || r.Version != 4 {
		t.Fatalf("got id=%d ver=%d", r.EdgeID, r.Version)
	}
}
