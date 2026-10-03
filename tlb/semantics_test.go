package tlb

import (
	"errors"
	"reflect"
	"testing"
)

// state 是模型全部可观察状态的快照，用于"被拒绝的操作不得改变任何状态"的断言。
type state struct {
	gen     uint64
	mms     []mmState
	actives []Pair
	resvd   []Pair
	pending []bool
	tlbs    [][]Entry
	taken   []bool // 下标即 asid，0 号不用
}

func snapshot(m *Model) state {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := state{gen: m.gen}
	s.mms = append(s.mms, m.mms...)
	for i := range m.cpus {
		s.actives = append(s.actives, m.cpus[i].active)
		s.resvd = append(s.resvd, m.cpus[i].reserved)
		s.pending = append(s.pending, m.cpus[i].pending)
		s.tlbs = append(s.tlbs, m.cpus[i].tlb.entries())
	}
	s.taken = make([]bool, m.cfg.ASIDs+1)
	for a := 1; a <= m.cfg.ASIDs; a++ {
		s.taken[a] = m.alloc.taken(uint32(a))
	}
	return s
}

func assertStateEqual(t *testing.T, before, after state, what string) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("%s 改变了状态:\nbefore=%+v\nafter =%+v", what, before, after)
	}
}

// TestFastPathNoStateChange 快速路径（mm.gen == G 直接沿用）不改任何分配状态，
// 只更新该 CPU 的 active。
func TestFastPathNoStateChange(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 4, CPUs: 2, TLBCap: 2, MaxMMs: 8})
	mustCreateMMs(t, m, 2)
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	mustSwitch(t, m, 1, 1, switchRes{2, 1, false, false})

	before := snapshot(m)
	// 再次切到同一 mm：快速路径，只有 active[0] 变化（此处值相同）。
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	assertStateEqual(t, before, snapshot(m), "Switch 快速路径")

	// cpu0 切到 mm1（同世代）：快速路径，仅 active[0] 变为 (2,1)。
	mustSwitch(t, m, 0, 1, switchRes{2, 1, false, false})
	after := snapshot(m)
	before.actives[0] = Pair{2, 1}
	assertStateEqual(t, before, after, "Switch 快速路径（换 mm）")
}

// TestSmallestFreeASID 新 mm 总是取 taken 之外编号最小的 ASID。
func TestSmallestFreeASID(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 5, CPUs: 1, TLBCap: 1, MaxMMs: 8})
	mustCreateMMs(t, m, 5)
	for i := 0; i < 5; i++ {
		mustSwitch(t, m, 0, i, switchRes{uint32(i + 1), 1, false, false})
	}
	// 销毁 asid=2 与 asid=4 的 mm（均不在任何 CPU 活动），释放其 asid。
	if rel, _, err := m.DestroyMM(1); err != nil || !rel {
		t.Fatalf("DestroyMM(1) = (%v,%v)", rel, err)
	}
	if rel, _, err := m.DestroyMM(3); err != nil || !rel {
		t.Fatalf("DestroyMM(3) = (%v,%v)", rel, err)
	}
	id5, _ := m.CreateMM()
	id6, _ := m.CreateMM()
	// 最小空闲为 2，其次为 4。
	mustSwitch(t, m, 0, id5, switchRes{2, 1, false, false})
	mustSwitch(t, m, 0, id6, switchRes{4, 1, false, false})
}

// TestTightASIDBudget A=C+1 时每次分配都近乎回绕。
func TestTightASIDBudget(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 3, CPUs: 2, TLBCap: 1, MaxMMs: 16})
	mustCreateMMs(t, m, 8)
	// 占满 3 个 asid。
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	mustSwitch(t, m, 1, 1, switchRes{2, 1, false, false})
	mustSwitch(t, m, 0, 2, switchRes{3, 1, false, false})
	// 之后每次切到新 mm 都回绕：taken 重置后只剩 <= C 个保留 asid。
	for i := 3; i < 8; i++ {
		cpu := i % 2
		_, _, _, rolled, err := m.Switch(cpu, i)
		if err != nil {
			t.Fatalf("Switch(%d,%d): %v", cpu, i, err)
		}
		if !rolled {
			t.Fatalf("Switch(%d,%d) 应发生回绕", cpu, i)
		}
	}
	if g := m.Gen(); g != 6 {
		t.Fatalf("G = %d, want 6（5 次回绕）", g)
	}
}

// TestRolloverPreservesSwitchingCPU 回绕时正在切换的 CPU 自身的旧 active 也被保留。
func TestRolloverPreservesSwitchingCPU(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 3, CPUs: 2, TLBCap: 1, MaxMMs: 8})
	mustCreateMMs(t, m, 4)
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	mustSwitch(t, m, 1, 1, switchRes{2, 1, false, false})
	mustSwitch(t, m, 0, 2, switchRes{3, 1, false, false})
	// cpu0 切到新 mm3 触发回绕；cpu0 当时的 active 是 (3,1)。
	mustSwitch(t, m, 0, 3, switchRes{1, 2, true, true})
	_, r0, _ := m.CPUState(0)
	_, r1, _ := m.CPUState(1)
	if r0 != (Pair{3, 1}) {
		t.Fatalf("cpu0 reserved = %v, want (3,1)（切换前的 active）", r0)
	}
	if r1 != (Pair{2, 1}) {
		t.Fatalf("cpu1 reserved = %v, want (2,1)", r1)
	}
	// taken = {1（新分给 mm3）, 2, 3（两个保留）}。
	for a := uint32(1); a <= 3; a++ {
		if !m.Taken(a) {
			t.Fatalf("asid %d 应在 taken 中", a)
		}
	}
}

// TestDuplicateReservedPairs 同一 mm 同时在两个 CPU 上活动时，
// reserved 对重复但 taken 只占一个。
func TestDuplicateReservedPairs(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 3, CPUs: 2, TLBCap: 1, MaxMMs: 8})
	mustCreateMMs(t, m, 4)
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	mustSwitch(t, m, 1, 1, switchRes{2, 1, false, false})
	mustSwitch(t, m, 0, 2, switchRes{3, 1, false, false})
	// 让两个 CPU 都活动于 mm0 的 (1,1)。
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	mustSwitch(t, m, 1, 0, switchRes{1, 1, false, false})
	// 回绕：两个 reserved 对都是 (1,1)，taken 中 asid 1 只占一个，mm3 分得 2。
	mustSwitch(t, m, 0, 3, switchRes{2, 2, true, true})
	_, r0, _ := m.CPUState(0)
	_, r1, _ := m.CPUState(1)
	if r0 != (Pair{1, 1}) || r1 != (Pair{1, 1}) {
		t.Fatalf("reserved = %v,%v, want 均为 (1,1)", r0, r1)
	}
	// taken = {1（保留，仅一个）, 2（mm3）}，asid 3 空闲。
	if !m.Taken(1) || !m.Taken(2) || m.Taken(3) {
		t.Fatalf("taken 应为 {1,2}: 1=%v 2=%v 3=%v", m.Taken(1), m.Taken(2), m.Taken(3))
	}
}

// TestPromotionRequiresExactPair 提升只在 (asid, gen) 与 reserved 对完全相等时发生；
// asid 相同而 gen 不同的旧 mm 不被提升。
func TestPromotionRequiresExactPair(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 3, CPUs: 2, TLBCap: 1, MaxMMs: 8})
	mustCreateMMs(t, m, 4)
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	mustSwitch(t, m, 1, 1, switchRes{2, 1, false, false})
	mustSwitch(t, m, 0, 2, switchRes{3, 1, false, false})
	// 回绕：reserved = {cpu0:(3,1), cpu1:(2,1)}，mm3 分得 1。
	mustSwitch(t, m, 0, 3, switchRes{1, 2, true, true})
	// mm0 是 (1,1)：asid 1 与 cpu1 的 reserved (2,1) 不同、与 cpu0 的 (3,1) 不同，
	// 不提升；taken={1,2,3} 已满，回绕。G=3，reserved={cpu0:(1,2), cpu1:(2,1)}。
	mustSwitch(t, m, 1, 0, switchRes{3, 3, true, true})
	if a, g, _ := m.MM(0); a != 3 || g != 3 {
		t.Fatalf("mm0 = (%d,%d), want (3,3)", a, g)
	}
	// mm3 是 (1,2)，恰等于 cpu0 的 reserved 对：提升为 (1,3)，不改 taken。
	// cpu0 的 pending 在第二次回绕时被置真，本次刷新。
	before := snapshot(m)
	mustSwitch(t, m, 0, 3, switchRes{1, 3, true, false})
	after := snapshot(m)
	if !reflect.DeepEqual(before.taken, after.taken) {
		t.Fatalf("提升不得改 taken: %v -> %v", before.taken, after.taken)
	}
	if a, g, _ := m.MM(3); a != 1 || g != 3 {
		t.Fatalf("mm3 = (%d,%d), want (1,3)", a, g)
	}
	// mm1 是 (2,1)：asid 2 与 cpu0 的旧 reserved (1,2) asid 不同；
	// 注意 cpu1 的 reserved 仍是 (2,1)（最近一次回绕时 cpu1 的 active），
	// 因此 mm1 恰好可提升。先确认其 gen 被提升。
	mustSwitch(t, m, 1, 1, switchRes{2, 3, false, false})
	// mm2 是 (3,1)：asid 相同 gen 不同的情形——构造一个 asid 相同但 gen 不同的 mm。
	// 当前 reserved = {cpu0:(1,2), cpu1:(2,1)}，mm2=(3,1) asid 3 不在其中，
	// taken 满，触发回绕而非提升。
	_, _, _, rolled, err := m.Switch(0, 2)
	if err != nil {
		t.Fatalf("Switch(0,2): %v", err)
	}
	if !rolled {
		t.Fatal("mm2 的 (3,1) 与任何 reserved 对都不完全相等，应回绕而非提升")
	}
}

// TestPendingFlushSemantics 回绕时所有 CPU 的 pending 置真，
// 但只在该 CPU 下一次 Switch 时刷新；未回绕但 pending 为真时也刷新。
func TestPendingFlushSemantics(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 4, CPUs: 3, TLBCap: 2, MaxMMs: 8})
	mustCreateMMs(t, m, 5)
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	mustSwitch(t, m, 1, 1, switchRes{2, 1, false, false})
	mustSwitch(t, m, 2, 2, switchRes{3, 1, false, false})
	mustSwitch(t, m, 0, 3, switchRes{4, 1, false, false}) // taken 满
	if _, _, err := m.Fill(0, 10, 100); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Fill(1, 10, 100); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Fill(2, 10, 100); err != nil {
		t.Fatal(err)
	}
	// cpu0 切到新 mm4：回绕，G=2，三个 CPU 的 pending 全置真，
	// reserved = {cpu0:(4,1), cpu1:(2,1), cpu2:(3,1)}，mm4 分得 1。
	mustSwitch(t, m, 0, 4, switchRes{1, 2, true, true})
	for c := 0; c < 3; c++ {
		if _, _, p := m.CPUState(c); c != 0 && !p {
			t.Fatalf("cpu%d pending 应为真", c)
		}
	}
	if _, _, p := m.CPUState(0); p {
		t.Fatal("cpu0 pending 应已被本次 Switch 清除")
	}
	// cpu0 已刷新；cpu1、cpu2 的 TLB 尚未被清（惰性）。
	mustTLB(t, m, 0, nil)
	mustTLB(t, m, 1, []Entry{ent(2, 10, 100)})
	mustTLB(t, m, 2, []Entry{ent(3, 10, 100)})
	// cpu1 的下一次 Switch 才刷新：mm1 的 (2,1) 等于 cpu1 的 reserved 对，
	// 提升为 (2,2)；本次无回绕但 pending 为真，仍刷新。
	mustSwitch(t, m, 1, 1, switchRes{2, 2, true, false})
	mustTLB(t, m, 1, nil)
	// cpu2 仍然 pending，TLB 仍未清。
	mustTLB(t, m, 2, []Entry{ent(3, 10, 100)})
	mustSwitch(t, m, 2, 2, switchRes{3, 2, true, false})
	mustTLB(t, m, 2, nil)
}

// TestLRU Fill 淘汰最久未用；Lookup 命中改变次序；更新已存在的键不扩容。
func TestLRU(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 2, CPUs: 1, TLBCap: 3, MaxMMs: 4})
	mustCreateMMs(t, m, 1)
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	fill := func(vpn, pfn uint64) Key {
		t.Helper()
		ev, ok, err := m.Fill(0, vpn, pfn)
		if err != nil {
			t.Fatalf("Fill(0,%d,%d): %v", vpn, pfn, err)
		}
		if !ok {
			return Key{}
		}
		return ev
	}
	fill(1, 10)
	fill(2, 20)
	fill(3, 30)
	mustTLB(t, m, 0, []Entry{ent(1, 3, 30), ent(1, 2, 20), ent(1, 1, 10)})
	// Lookup 命中把 (1,1) 提到最前。
	if pfn, hit, _ := m.Lookup(0, 1); !hit || pfn != 10 {
		t.Fatalf("Lookup(0,1) = (%v,%v)", pfn, hit)
	}
	mustTLB(t, m, 0, []Entry{ent(1, 1, 10), ent(1, 3, 30), ent(1, 2, 20)})
	// 更新已存在的键：不淘汰、置为最近使用。
	if ev := fill(2, 22); ev != (Key{}) {
		t.Fatalf("更新不应淘汰, got %v", ev)
	}
	mustTLB(t, m, 0, []Entry{ent(1, 2, 22), ent(1, 1, 10), ent(1, 3, 30)})
	// 新键淘汰最久未用的 (1,3)。
	if ev := fill(4, 40); ev != (Key{1, 3}) {
		t.Fatalf("应淘汰 (1,3), got %v", ev)
	}
	mustTLB(t, m, 0, []Entry{ent(1, 4, 40), ent(1, 2, 22), ent(1, 1, 10)})
	// Lookup 未命中不改任何状态。
	before := snapshot(m)
	if _, hit, err := m.Lookup(0, 99); hit || err != nil {
		t.Fatalf("Lookup(0,99) = (%v,%v)", hit, err)
	}
	assertStateEqual(t, before, snapshot(m), "Lookup 未命中")
}

// TestOtherASIDOccupiesCapacity 其他 ASID 的条目也占用容量。
func TestOtherASIDOccupiesCapacity(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 3, CPUs: 1, TLBCap: 2, MaxMMs: 4})
	mustCreateMMs(t, m, 2)
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	if _, _, err := m.Fill(0, 1, 10); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Fill(0, 2, 20); err != nil {
		t.Fatal(err)
	}
	// 切到 mm1（asid 2），旧 asid 的条目仍在并占容量。
	mustSwitch(t, m, 0, 1, switchRes{2, 1, false, false})
	mustTLB(t, m, 0, []Entry{ent(1, 2, 20), ent(1, 1, 10)})
	// 再插入一条即超容量，淘汰最久未用的 (1,1)。
	ev, ok, err := m.Fill(0, 3, 30)
	if err != nil || !ok || ev != (Key{1, 1}) {
		t.Fatalf("Fill evict = (%v,%v,%v), want ((1,1),true,nil)", ev, ok, err)
	}
	mustTLB(t, m, 0, []Entry{ent(2, 3, 30), ent(1, 2, 20)})
}

// TestInvalidate Invalidate 的各分支。
func TestInvalidate(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 3, CPUs: 2, TLBCap: 4, MaxMMs: 8})
	mustCreateMMs(t, m, 4)
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	mustSwitch(t, m, 1, 0, switchRes{1, 1, false, false})
	mustSwitch(t, m, 0, 1, switchRes{2, 1, false, false})
	mustSwitch(t, m, 1, 2, switchRes{3, 1, false, false})
	// 两个 CPU 都填入 (1,7)（asid 1 属于 mm0）。
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	if _, _, err := m.Fill(0, 7, 70); err != nil {
		t.Fatal(err)
	}
	mustSwitch(t, m, 1, 0, switchRes{1, 1, false, false})
	if _, _, err := m.Fill(1, 7, 70); err != nil {
		t.Fatal(err)
	}
	// 有效上下文：从两个 CPU 各删 1 条。
	if n, err := m.Invalidate(0, 7); err != nil || n != 2 {
		t.Fatalf("Invalidate(0,7) = (%d,%v), want (2,nil)", n, err)
	}
	// 条目已删，再删返回 0。
	if n, err := m.Invalidate(0, 7); err != nil || n != 0 {
		t.Fatalf("Invalidate(0,7) again = (%d,%v), want (0,nil)", n, err)
	}
	// 未分配 asid 的 mm：返回 0 且不动状态。
	before := snapshot(m)
	if n, err := m.Invalidate(3, 7); err != nil || n != 0 {
		t.Fatalf("Invalidate(3,7) = (%d,%v), want (0,nil)", n, err)
	}
	assertStateEqual(t, before, snapshot(m), "Invalidate 未分配 mm")
	// 失效上下文（世代已旧且不在 reserved）：返回 0 且不动任何条目。
	// 回绕：G=2，reserved 为两 CPU 的 active (1,1)、(1,1)，taken={1}，
	// mm3 分得最小空闲 asid 2。
	mustSwitch(t, m, 0, 3, switchRes{2, 2, true, true})
	// mm1 是 (2,1)：世代已旧；reserved 不含 (2,1)。
	_, r0, _ := m.CPUState(0)
	_, r1, _ := m.CPUState(1)
	if r0 == (Pair{2, 1}) || r1 == (Pair{2, 1}) {
		t.Fatalf("测试前提失败：(2,1) 不应被保留, reserved=%v,%v", r0, r1)
	}
	if _, _, err := m.Fill(0, 5, 50); err != nil { // asid 1（mm3）的条目
		t.Fatal(err)
	}
	before = snapshot(m)
	if n, err := m.Invalidate(1, 5); err != nil || n != 0 {
		t.Fatalf("Invalidate(1,5) 失效上下文 = (%d,%v), want (0,nil)", n, err)
	}
	assertStateEqual(t, before, snapshot(m), "Invalidate 失效上下文")
}

// TestInvalidateReservedContext 对 reserved 上下文仍删除。
func TestInvalidateReservedContext(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 3, CPUs: 2, TLBCap: 4, MaxMMs: 8})
	mustCreateMMs(t, m, 4)
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	mustSwitch(t, m, 1, 1, switchRes{2, 1, false, false})
	mustSwitch(t, m, 0, 2, switchRes{3, 1, false, false})
	// cpu0 活动于 (3,1)，填入 (3,9)。
	if _, _, err := m.Fill(0, 9, 90); err != nil {
		t.Fatal(err)
	}
	// 回绕：reserved = {cpu0:(3,1), cpu1:(2,1)}，mm3 分得 1。
	mustSwitch(t, m, 1, 3, switchRes{1, 2, true, true})
	// mm2 的 (3,1) 等于 cpu0 的 reserved 对：上下文有效，删除 (3,9)。
	if n, err := m.Invalidate(2, 9); err != nil || n != 1 {
		t.Fatalf("Invalidate(2,9) = (%d,%v), want (1,nil)", n, err)
	}
	mustTLB(t, m, 0, nil)
}

// TestDestroyMMCases DestroyMM 的三类结果、ErrBusy 与释放后 asid 的本世代重用。
//
// 推演（A=3, C=2, T=4）：
//
//	Switch(0,mm0)->(1,1)  Switch(1,mm1)->(2,1)  Switch(0,mm2)->(3,1)
//	Switch(1,mm3)：回绕，G=2，reserved={(3,1),(2,1)}，mm3->(1,2)，cpu1 刷新
//	Switch(0,mm1)：mm1 的 (2,1)==cpu1 reserved，提升为 (2,2)，cpu0 刷新
//	Switch(0,mm3)：快速路径 (1,2)
//	Switch(1,mm4)：taken 满，回绕，G=3，reserved={(1,2),(1,2)}，mm4->(2,3)，cpu1 刷新
//	Switch(0,mm4)：快速路径但 cpu0 pending，刷新
//	Switch(0,mm3)：mm3 的 (1,2)==reserved，提升为 (1,3)
//	Switch(1,mm3)：快速路径 (1,3)
func TestDestroyMMCases(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 3, CPUs: 2, TLBCap: 4, MaxMMs: 8})
	mustCreateMMs(t, m, 5)
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	mustSwitch(t, m, 1, 1, switchRes{2, 1, false, false})
	mustSwitch(t, m, 0, 2, switchRes{3, 1, false, false})
	mustSwitch(t, m, 1, 3, switchRes{1, 2, true, true})

	// 情形一：世代已旧，只标记。mm0 是 (1,1)，gen!=G 且不涉及 reserved。
	rel, n, err := m.DestroyMM(0)
	if err != nil || rel || n != 0 {
		t.Fatalf("DestroyMM(0) = (%v,%v,%v), want (false,0,nil)", rel, n, err)
	}
	if _, _, alive := m.MM(0); alive {
		t.Fatal("mm0 应已标记销毁")
	}
	for a := uint32(1); a <= 3; a++ {
		if !m.Taken(a) {
			t.Fatalf("世代已旧的销毁不得动 taken，asid %d 丢失", a)
		}
	}

	// 情形二：asid 属 reserved 对，只标记。
	mustSwitch(t, m, 0, 1, switchRes{2, 2, true, false}) // 提升 mm1
	mustSwitch(t, m, 0, 3, switchRes{1, 2, false, false})
	rel, n, err = m.DestroyMM(1)
	if err != nil || rel || n != 0 {
		t.Fatalf("DestroyMM(1) = (%v,%v,%v), want (false,0,nil)", rel, n, err)
	}
	if !m.Taken(2) {
		t.Fatal("asid 2 属 reserved 对，不应释放")
	}

	// 情形三：可释放，清 TLB 并允许本世代重用。
	mustSwitch(t, m, 1, 4, switchRes{2, 3, true, true}) // 回绕，G=3，mm4->(2,3)
	if _, _, err := m.Fill(1, 8, 80); err != nil {      // TLB(1): (2,8)
		t.Fatal(err)
	}
	mustSwitch(t, m, 0, 4, switchRes{2, 3, true, false}) // cpu0 pending，刷新
	if _, _, err := m.Fill(0, 9, 90); err != nil {       // TLB(0): (2,9)
		t.Fatal(err)
	}
	mustSwitch(t, m, 0, 3, switchRes{1, 3, false, false}) // mm3 提升为 (1,3)
	mustSwitch(t, m, 1, 3, switchRes{1, 3, false, false})
	// ErrBusy：mm3 正被两个 CPU 活动使用。
	before := snapshot(m)
	if _, _, err := m.DestroyMM(3); !errors.Is(err, ErrBusy) {
		t.Fatalf("DestroyMM(3) err = %v, want ErrBusy", err)
	}
	assertStateEqual(t, before, snapshot(m), "DestroyMM ErrBusy")
	// 释放 mm4 的 asid 2：不在任何 active，也不属 reserved（reserved asid 为 1）。
	rel, n, err = m.DestroyMM(4)
	if err != nil || !rel || n != 2 {
		t.Fatalf("DestroyMM(4) = (%v,%v,%v), want (true,2,nil)", rel, n, err)
	}
	if m.Taken(2) {
		t.Fatal("asid 2 应已释放")
	}
	mustTLB(t, m, 0, nil)
	mustTLB(t, m, 1, nil)
	// 本世代内新 mm 重新分到 asid 2，旧条目不得命中。
	id, err := m.CreateMM()
	if err != nil {
		t.Fatal(err)
	}
	mustSwitch(t, m, 0, id, switchRes{2, 3, false, false})
	if _, hit, err := m.Lookup(0, 9); err != nil || hit {
		t.Fatalf("Lookup(0,9) = hit=%v err=%v, want miss（旧条目已清除）", hit, err)
	}
}

// TestTakenRebuiltFromReserved 销毁后回绕时，taken 恰好重建为 reserved 的
// asid 集合；已销毁 mm 的 asid 不再被保留。
func TestTakenRebuiltFromReserved(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 3, CPUs: 2, TLBCap: 2, MaxMMs: 8})
	mustCreateMMs(t, m, 5)
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	mustSwitch(t, m, 1, 1, switchRes{2, 1, false, false})
	mustSwitch(t, m, 0, 2, switchRes{3, 1, false, false})
	mustSwitch(t, m, 1, 3, switchRes{1, 2, true, true}) // reserved={(3,1),(2,1)}
	mustSwitch(t, m, 0, 1, switchRes{2, 2, true, false})
	mustSwitch(t, m, 0, 3, switchRes{1, 2, false, false})
	// mm1 是 (2,2)，asid 2 属 reserved：只标记，taken 仍含 2。
	if rel, _, err := m.DestroyMM(1); err != nil || rel {
		t.Fatalf("DestroyMM(1) = (%v,%v)", rel, err)
	}
	if !m.Taken(2) {
		t.Fatal("asid 2 属 reserved 对，销毁时不应释放")
	}
	// 回绕：reserved 重建为两 CPU 的 active (1,2)、(1,2)，taken 只含 1；
	// 已销毁 mm1 的 asid 2 不再被保留，mm4 分得 2，mm2 的 asid 3 被丢弃。
	mustSwitch(t, m, 0, 4, switchRes{2, 3, true, true})
	_, r0, _ := m.CPUState(0)
	_, r1, _ := m.CPUState(1)
	if r0 != (Pair{1, 2}) || r1 != (Pair{1, 2}) {
		t.Fatalf("reserved = %v,%v, want 均为 (1,2)", r0, r1)
	}
	if !m.Taken(1) || !m.Taken(2) || m.Taken(3) {
		t.Fatalf("taken 应为 {1,2}: %v %v %v", m.Taken(1), m.Taken(2), m.Taken(3))
	}
}

// TestSwitchInvalidateOnDeadMM 对已销毁 mm 的 Switch 与 Invalidate 报 ErrDead。
func TestSwitchInvalidateOnDeadMM(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 2, CPUs: 1, TLBCap: 2, MaxMMs: 4})
	mustCreateMMs(t, m, 2)
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	mustSwitch(t, m, 0, 1, switchRes{2, 1, false, false})
	if rel, _, err := m.DestroyMM(0); err != nil || !rel {
		t.Fatalf("DestroyMM(0) = (%v,%v)", rel, err)
	}
	before := snapshot(m)
	if _, _, _, _, err := m.Switch(0, 0); !errors.Is(err, ErrDead) {
		t.Fatalf("Switch 到已销毁 mm err = %v, want ErrDead", err)
	}
	assertStateEqual(t, before, snapshot(m), "Switch 到已销毁 mm")
	if _, err := m.Invalidate(0, 1); !errors.Is(err, ErrDead) {
		t.Fatalf("Invalidate 已销毁 mm err = %v, want ErrDead", err)
	}
	assertStateEqual(t, before, snapshot(m), "Invalidate 已销毁 mm")
	// 重复销毁报 ErrDead。
	if _, _, err := m.DestroyMM(0); !errors.Is(err, ErrDead) {
		t.Fatalf("重复 DestroyMM err = %v, want ErrDead", err)
	}
	assertStateEqual(t, before, snapshot(m), "重复 DestroyMM")
}

// TestErrorOrdering 各类错误按规定的先后顺序只报第一个。
func TestErrorOrdering(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 3, CPUs: 2, TLBCap: 1, MaxMMs: 4})
	mustCreateMMs(t, m, 2)
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	// mm1 从未分配 asid，销毁只标记、不释放。
	if rel, _, err := m.DestroyMM(1); err != nil || rel {
		t.Fatalf("DestroyMM(1) = (%v,%v)", rel, err)
	}
	const bad = 1 << 32 // 越界参数

	// Switch：ErrBadCPU > ErrBadMM > ErrDead。
	if _, _, _, _, err := m.Switch(9, 9); !errors.Is(err, ErrBadCPU) {
		t.Errorf("Switch(9,9) = %v, want ErrBadCPU", err)
	}
	if _, _, _, _, err := m.Switch(-1, 1); !errors.Is(err, ErrBadCPU) {
		t.Errorf("Switch(-1,1) = %v, want ErrBadCPU", err)
	}
	if _, _, _, _, err := m.Switch(0, 9); !errors.Is(err, ErrBadMM) {
		t.Errorf("Switch(0,9) = %v, want ErrBadMM", err)
	}
	if _, _, _, _, err := m.Switch(0, 1); !errors.Is(err, ErrDead) {
		t.Errorf("Switch(0,1) = %v, want ErrDead", err)
	}

	// Fill/Lookup：ErrBadCPU > ErrBadParam > ErrNoContext。
	if _, _, err := m.Fill(9, bad, bad); !errors.Is(err, ErrBadCPU) {
		t.Errorf("Fill(9,bad,bad) = %v, want ErrBadCPU", err)
	}
	if _, _, err := m.Fill(1, bad, 0); !errors.Is(err, ErrBadParam) {
		t.Errorf("Fill(1,bad,0) = %v, want ErrBadParam（先于 ErrNoContext）", err)
	}
	if _, _, err := m.Fill(1, 0, bad); !errors.Is(err, ErrBadParam) {
		t.Errorf("Fill(1,0,bad) = %v, want ErrBadParam", err)
	}
	if _, _, err := m.Fill(1, 0, 0); !errors.Is(err, ErrNoContext) {
		t.Errorf("Fill(1,0,0) = %v, want ErrNoContext", err)
	}
	if _, _, err := m.Lookup(9, bad); !errors.Is(err, ErrBadCPU) {
		t.Errorf("Lookup(9,bad) = %v, want ErrBadCPU", err)
	}
	if _, _, err := m.Lookup(1, bad); !errors.Is(err, ErrBadParam) {
		t.Errorf("Lookup(1,bad) = %v, want ErrBadParam（先于 ErrNoContext）", err)
	}
	if _, _, err := m.Lookup(1, 0); !errors.Is(err, ErrNoContext) {
		t.Errorf("Lookup(1,0) = %v, want ErrNoContext", err)
	}

	// Invalidate：ErrBadMM > ErrDead > ErrBadParam。
	if _, err := m.Invalidate(9, bad); !errors.Is(err, ErrBadMM) {
		t.Errorf("Invalidate(9,bad) = %v, want ErrBadMM", err)
	}
	if _, err := m.Invalidate(1, bad); !errors.Is(err, ErrDead) {
		t.Errorf("Invalidate(1,bad) = %v, want ErrDead（先于参数非法）", err)
	}
	if _, err := m.Invalidate(0, bad); !errors.Is(err, ErrBadParam) {
		t.Errorf("Invalidate(0,bad) = %v, want ErrBadParam", err)
	}

	// DestroyMM：ErrBadMM > ErrDead > ErrBusy。
	if _, _, err := m.DestroyMM(9); !errors.Is(err, ErrBadMM) {
		t.Errorf("DestroyMM(9) = %v, want ErrBadMM", err)
	}
	if _, _, err := m.DestroyMM(1); !errors.Is(err, ErrDead) {
		t.Errorf("DestroyMM(1) = %v, want ErrDead", err)
	}
	if _, _, err := m.DestroyMM(0); !errors.Is(err, ErrBusy) {
		t.Errorf("DestroyMM(0) = %v, want ErrBusy", err)
	}
}

// TestRejectedOpsNoStateChange 所有被拒绝的操作不得改变任何状态。
func TestRejectedOpsNoStateChange(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 3, CPUs: 2, TLBCap: 2, MaxMMs: 4})
	mustCreateMMs(t, m, 4)
	mustSwitch(t, m, 0, 0, switchRes{1, 1, false, false})
	if _, _, err := m.Fill(0, 5, 50); err != nil {
		t.Fatal(err)
	}
	// mm2 从未分配 asid，销毁只标记，用作"已销毁"的测试对象；cpu1 保持无上下文。
	if rel, _, err := m.DestroyMM(2); err != nil || rel {
		t.Fatalf("DestroyMM(2) = (%v,%v)", rel, err)
	}

	rejected := map[string]func() error{
		"Switch ErrBadCPU":       func() error { _, _, _, _, e := m.Switch(5, 0); return e },
		"Switch ErrBadMM":        func() error { _, _, _, _, e := m.Switch(0, 9); return e },
		"Switch ErrDead":         func() error { _, _, _, _, e := m.Switch(0, 2); return e },
		"Fill ErrBadCPU":         func() error { _, _, e := m.Fill(5, 1, 1); return e },
		"Fill ErrBadParam":       func() error { _, _, e := m.Fill(0, 1<<32, 1); return e },
		"Fill ErrNoContext":      func() error { _, _, e := m.Fill(1, 1, 1); return e },
		"Lookup ErrBadCPU":       func() error { _, _, e := m.Lookup(5, 1); return e },
		"Lookup ErrBadParam":     func() error { _, _, e := m.Lookup(0, 1<<32); return e },
		"Lookup ErrNoContext":    func() error { _, _, e := m.Lookup(1, 1); return e },
		"Lookup miss":            func() error { _, _, e := m.Lookup(0, 99); return e },
		"Invalidate ErrBadMM":    func() error { _, e := m.Invalidate(9, 1); return e },
		"Invalidate ErrDead":     func() error { _, e := m.Invalidate(2, 1); return e },
		"Invalidate ErrBadParam": func() error { _, e := m.Invalidate(0, 1<<32); return e },
		"Invalidate 失效上下文":       func() error { _, e := m.Invalidate(3, 1); return e },
		"DestroyMM ErrBadMM":     func() error { _, _, e := m.DestroyMM(9); return e },
		"DestroyMM ErrDead":      func() error { _, _, e := m.DestroyMM(2); return e },
		"DestroyMM ErrBusy":      func() error { _, _, e := m.DestroyMM(0); return e },
		"CreateMM ErrTooManyMM": func() error {
			_, e := m.CreateMM() // 已有 4 个，达到 M=4
			return e
		},
	}
	for name, op := range rejected {
		before := snapshot(m)
		err := op()
		if err == nil && name != "Lookup miss" && name != "Invalidate 失效上下文" {
			t.Errorf("%s 应被拒绝", name)
		}
		assertStateEqual(t, before, snapshot(m), name)
	}
}

// TestTooManyMM 超过 M 个 mm 报 ErrTooManyMM；销毁不回收编号，仍占名额。
func TestTooManyMM(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 2, CPUs: 1, TLBCap: 1, MaxMMs: 2})
	mustCreateMMs(t, m, 2)
	if rel, _, err := m.DestroyMM(0); err != nil || rel {
		t.Fatalf("DestroyMM(0) = (%v,%v)（未分配 asid，只标记）", rel, err)
	}
	if _, err := m.CreateMM(); !errors.Is(err, ErrTooManyMM) {
		t.Fatalf("CreateMM err = %v, want ErrTooManyMM（销毁不回收名额）", err)
	}
}

// TestNoMMTableScan 在 1e5 个 mm 上做 1000 次回绕，mmVisited 必须保持 0，
// 且 Invalidate 每次调用的探测总数不超过 C。
func TestNoMMTableScan(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 2, CPUs: 1, TLBCap: 1, MaxMMs: 100000})
	// A=2、C=1：占满 2 个 asid 后，每次切到新 mm 都回绕。
	const rollovers = 1000
	for i := 0; i < rollovers+2; i++ {
		id, err := m.CreateMM()
		if err != nil {
			t.Fatalf("CreateMM #%d: %v", i, err)
		}
		if _, _, _, _, err := m.Switch(0, id); err != nil {
			t.Fatalf("Switch #%d: %v", i, err)
		}
	}
	if g := m.Gen(); g != rollovers+1 {
		t.Fatalf("G = %d, want %d", g, rollovers+1)
	}
	if m.mmVisited != 0 {
		t.Fatalf("mmVisited = %d, want 0（任何操作不得遍历 mm 表）", m.mmVisited)
	}
	// Invalidate 探测数有界。
	before := m.invalidateProbes
	if _, err := m.Invalidate(0, 1); err != nil {
		t.Fatal(err)
	}
	if got := m.invalidateProbes - before; got > uint64(m.cfg.CPUs) {
		t.Fatalf("Invalidate 探测 %d 次, 应 <= C=%d", got, m.cfg.CPUs)
	}
	if m.mmVisited != 0 {
		t.Fatalf("mmVisited = %d, want 0", m.mmVisited)
	}
}
