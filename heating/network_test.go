package heating

import (
	"errors"
	"reflect"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
}

func mustNode(t *testing.T, n *Network, id string, kind NodeKind) {
	t.Helper()
	must(t, n.AddNode(id, kind))
}

func mustSegment(t *testing.T, n *Network, id, a, b string) {
	t.Helper()
	must(t, n.AddSegment(id, a, b))
}

func mustValve(t *testing.T, n *Network, seg string, end End, id string) {
	t.Helper()
	must(t, n.InstallValve(seg, end, id))
}

func valveState(t *testing.T, n *Network, id string) ValveState {
	t.Helper()
	st, err := n.ValveStateOf(id)
	must(t, err)
	return st
}

func assertPlan(t *testing.T, p *Plan, toClose, domain, affected []string) {
	t.Helper()
	if !reflect.DeepEqual(norm(p.ValvesToClose), norm(toClose)) {
		t.Errorf("ValvesToClose = %v, 期望 %v", p.ValvesToClose, toClose)
	}
	if !reflect.DeepEqual(norm(p.Domain), norm(domain)) {
		t.Errorf("Domain = %v, 期望 %v", p.Domain, domain)
	}
	if !reflect.DeepEqual(norm(p.AffectedUsers), norm(affected)) {
		t.Errorf("AffectedUsers = %v, 期望 %v", p.AffectedUsers, affected)
	}
}

// norm 把空切片归一为 nil，便于断言。
func norm(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// buildRing 构造环网：src—n1—n2—n3—src，用户 u1 挂在 n3 上。
// 目标管段 e1 自身带一个可动阀门 v1（不应被列入关闭集合）。
func buildRing(t *testing.T) *Network {
	t.Helper()
	n := NewNetwork()
	mustNode(t, n, "src", NodeSource)
	mustNode(t, n, "n1", NodeBranch)
	mustNode(t, n, "n2", NodeBranch)
	mustNode(t, n, "n3", NodeBranch)
	mustNode(t, n, "u1", NodeUser)
	mustSegment(t, n, "e0", "src", "n1")
	mustSegment(t, n, "e1", "n1", "n2")
	mustSegment(t, n, "e2", "n2", "n3")
	mustSegment(t, n, "e3", "n3", "src")
	mustSegment(t, n, "eu", "n3", "u1")
	mustValve(t, n, "e0", EndB, "v0") // e0 在 n1 端
	mustValve(t, n, "e1", EndA, "v1") // e1 在 n1 端（域内管段自身阀门）
	mustValve(t, n, "e2", EndA, "v2") // e2 在 n2 端
	mustValve(t, n, "e3", EndA, "v3") // e3 在 n3 端
	mustValve(t, n, "eu", EndA, "vu") // eu 在 n3 端
	return n
}

// TestRingKeepsHeat 环网中失去一路仍有热：隔离 e1 后 u1 经另一路保持供热。
func TestRingKeepsHeat(t *testing.T) {
	n := buildRing(t)
	if !n.HasHeat("u1") {
		t.Fatal("初始状态 u1 应有热")
	}
	p, err := n.SimulateIsolation("e1")
	must(t, err)
	assertPlan(t, p, []string{"v0", "v2"}, []string{"e1"}, nil)

	got, err := n.ExecuteIsolation("e1", p.ValvesToClose)
	must(t, err)
	assertPlan(t, got, []string{"v0", "v2"}, []string{"e1"}, nil)

	if !n.HasHeat("u1") {
		t.Error("隔离 e1 后 u1 应经环网另一路保持供热")
	}
	if n.HasHeat("n1") || n.HasHeat("n2") {
		t.Error("隔离后 n1、n2 应失去供热")
	}
	if valveState(t, n, "v1") != ValveOpen {
		t.Error("域内管段自身阀门 v1 不应被关闭")
	}
	if lk, _ := n.SegmentLeaking("e1"); !lk {
		t.Error("e1 应处于泄漏中")
	}

	must(t, n.CompleteRepair("e1"))
	for _, v := range []string{"v0", "v2"} {
		if valveState(t, n, v) != ValveOpen {
			t.Errorf("修复后 %s 应恢复为开", v)
		}
	}
	if !n.HasHeat("n1") || !n.HasHeat("n2") {
		t.Error("修复后 n1、n2 应恢复供热")
	}
}

// buildChain 构造链网：src—b1—b2—u1，目标管段 t 为 b1—b2。
func buildChain(t *testing.T) *Network {
	t.Helper()
	n := NewNetwork()
	mustNode(t, n, "src", NodeSource)
	mustNode(t, n, "b1", NodeBranch)
	mustNode(t, n, "b2", NodeBranch)
	mustNode(t, n, "u1", NodeUser)
	mustSegment(t, n, "s0", "src", "b1")
	mustSegment(t, n, "t", "b1", "b2")
	mustSegment(t, n, "s2", "b2", "u1")
	mustValve(t, n, "s0", EndB, "v0") // s0 在 b1 端
	mustValve(t, n, "s2", EndA, "v2") // s2 在 b2 端
	return n
}

// TestStuckOpenExpandsDomain 边界阀门卡死在开导致隔离域扩大。
func TestStuckOpenExpandsDomain(t *testing.T) {
	n := buildChain(t)
	must(t, n.ReportStuck("v2", ValveStuckOpen))

	p, err := n.SimulateIsolation("t")
	must(t, err)
	// v2 卡死在开无法关闭，s2 必须并入隔离域。
	assertPlan(t, p, []string{"v0"}, []string{"s2", "t"}, []string{"u1"})

	got, err := n.ExecuteIsolation("t", p.ValvesToClose)
	must(t, err)
	assertPlan(t, got, []string{"v0"}, []string{"s2", "t"}, []string{"u1"})
	if n.HasHeat("u1") {
		t.Error("隔离后 u1 应失去供热")
	}
	if valveState(t, n, "v2") != ValveStuckOpen {
		t.Error("卡死在开的阀门不应被操作")
	}
}

// TestMissingValveExpandsDomain 边界处阀门缺失同样导致隔离域扩大。
func TestMissingValveExpandsDomain(t *testing.T) {
	n := buildChain(t)
	must(t, n.RemoveValve("s2", EndA))

	p, err := n.SimulateIsolation("t")
	must(t, err)
	assertPlan(t, p, []string{"v0"}, []string{"s2", "t"}, []string{"u1"})
}

// TestStuckClosedNoOp 边界阀门卡死在关视为已满足，无需操作、域不扩大。
func TestStuckClosedNoOp(t *testing.T) {
	n := buildChain(t)
	must(t, n.ReportStuck("v2", ValveStuckClosed))

	p, err := n.SimulateIsolation("t")
	must(t, err)
	// v2 已满足，不列入关闭集合；u1 此前已无热，不计入失去供热。
	assertPlan(t, p, []string{"v0"}, []string{"t"}, nil)
	if n.HasHeat("u1") {
		t.Fatal("v2 卡死在关，u1 本应无热")
	}

	_, err = n.ExecuteIsolation("t", p.ValvesToClose)
	must(t, err)
	if valveState(t, n, "v2") != ValveStuckClosed {
		t.Error("卡死在关的阀门应保持卡死状态")
	}
	must(t, n.CompleteRepair("t"))
	if valveState(t, n, "v2") != ValveStuckClosed {
		t.Error("修复后卡死在关的阀门仍应保持卡死状态")
	}
}

// TestSourceEndNotIsolatable 隔离域扩展到热源端且该端无法关闭时报无法隔离。
func TestSourceEndNotIsolatable(t *testing.T) {
	n := NewNetwork()
	mustNode(t, n, "src", NodeSource)
	mustNode(t, n, "u1", NodeUser)
	mustSegment(t, n, "t", "src", "u1")

	if _, err := n.SimulateIsolation("t"); !errors.Is(err, ErrNotIsolatable) {
		t.Fatalf("无阀门时应报无法隔离, got %v", err)
	}
	if _, err := n.ExecuteIsolation("t", nil); !errors.Is(err, ErrNotIsolatable) {
		t.Fatalf("执行时同样应报无法隔离, got %v", err)
	}

	// 卡死在开也无法关闭。
	mustValve(t, n, "t", EndA, "v")
	must(t, n.ReportStuck("v", ValveStuckOpen))
	if _, err := n.SimulateIsolation("t"); !errors.Is(err, ErrNotIsolatable) {
		t.Fatalf("热源端卡死在开应报无法隔离, got %v", err)
	}

	// 维修确认恢复为开后可以隔离：关闭域内管段自身在热源端的阀门。
	must(t, n.ConfirmValveRepaired("v"))
	p, err := n.SimulateIsolation("t")
	must(t, err)
	assertPlan(t, p, []string{"v"}, []string{"t"}, []string{"u1"})
}

// buildFork 构造叉网：src—X，X 分出 s1→P、s2→Q 两条用户支路，
// 边界阀门 v 装在 e0 的 X 端，被两个隔离共享。
func buildFork(t *testing.T) *Network {
	t.Helper()
	n := NewNetwork()
	mustNode(t, n, "src", NodeSource)
	mustNode(t, n, "X", NodeBranch)
	mustNode(t, n, "P", NodeUser)
	mustNode(t, n, "Q", NodeUser)
	mustSegment(t, n, "e0", "src", "X")
	mustSegment(t, n, "s1", "X", "P")
	mustSegment(t, n, "s2", "X", "Q")
	mustValve(t, n, "e0", EndB, "v") // e0 在 X 端
	return n
}

// TestSharedBoundaryValve 两个隔离共享边界阀门，先后修复时
// 阀门保持关闭直到最后一个需要它的隔离结束。
func TestSharedBoundaryValve(t *testing.T) {
	n := buildFork(t)

	p1, err := n.ExecuteIsolation("s1", nil)
	must(t, err)
	// s2 在 X 端无阀门，并入 s1 的隔离域；边界只需关闭共享阀门 v。
	assertPlan(t, p1, []string{"v"}, []string{"s1", "s2"}, []string{"P", "Q"})
	if valveState(t, n, "v") != ValveClosed {
		t.Fatal("v 应被第一个隔离关闭")
	}
	// 每次隔离记录自己关闭了哪些阀门。
	if rec := n.isolations["s1"]; len(rec.closed) != 1 || !reflect.DeepEqual(rec.closed, map[string]struct{}{"v": {}}) {
		t.Fatalf("s1 的隔离记录应为关闭了 {v}, got %v", rec.closed)
	}

	// 第二个隔离：v 已关，视为已满足，无需新关闭任何阀门。
	p2, err := n.ExecuteIsolation("s2", []string{})
	must(t, err)
	assertPlan(t, p2, nil, []string{"s1", "s2"}, nil)
	if rec := n.isolations["s2"]; len(rec.closed) != 0 {
		t.Fatalf("s2 的隔离记录应为未关闭任何阀门, got %v", rec.closed)
	}
	if rec := n.isolations["s2"]; !reflect.DeepEqual(rec.needs, map[string]struct{}{"v": {}}) {
		t.Fatalf("s2 的隔离仍需要 v 保持关闭, got %v", rec.needs)
	}

	// 先修复 s1：v 仍被 s2 的隔离需要，保持关闭。
	must(t, n.CompleteRepair("s1"))
	if valveState(t, n, "v") != ValveClosed {
		t.Error("v 仍被 s2 的隔离需要，应保持关闭")
	}
	if n.HasHeat("P") {
		t.Error("v 未重开，P 应仍无热")
	}

	// 后修复 s2：最后一个需要 v 的隔离结束，v 恢复为开。
	must(t, n.CompleteRepair("s2"))
	if valveState(t, n, "v") != ValveOpen {
		t.Error("所有需要 v 的隔离结束后，v 应恢复为开")
	}
	if !n.HasHeat("P") || !n.HasHeat("Q") {
		t.Error("全部修复后 P、Q 应恢复供热")
	}
	if got := n.ActiveIsolations(); len(got) != 0 {
		t.Errorf("不应有活动隔离, got %v", got)
	}
}

// TestRepairSegmentRemovalRejected 抢修中的管段不得拆除，其端上阀门不得拆除。
func TestRepairSegmentRemovalRejected(t *testing.T) {
	n := buildChain(t)
	mustValve(t, n, "t", EndA, "vt")
	if _, err := n.ExecuteIsolation("t", nil); err != nil {
		t.Fatalf("执行隔离失败: %v", err)
	}
	if err := n.RemoveSegment("t"); !errors.Is(err, ErrSegmentUnderRepair) {
		t.Errorf("抢修中拆除管段应被拒绝, got %v", err)
	}
	if err := n.RemoveValve("t", EndA); !errors.Is(err, ErrSegmentUnderRepair) {
		t.Errorf("抢修中拆除端上阀门应被拒绝, got %v", err)
	}
	// 状态未被拒绝的操作改变。
	if lk, _ := n.SegmentLeaking("t"); !lk {
		t.Error("t 应仍处于泄漏中")
	}
	if valveState(t, n, "vt") != ValveOpen {
		t.Error("vt 不应被拆除或改变")
	}

	must(t, n.CompleteRepair("t"))
	must(t, n.RemoveSegment("t"))
	// 拆除管段时其阀门一并删除。
	if _, err := n.ValveStateOf("vt"); !errors.Is(err, ErrValveNotFound) {
		t.Errorf("管段拆除后其阀门应一并删除, got %v", err)
	}
}

// TestManualCloseNotReopened 隔离之外被手工关闭的阀门，修复时不得被误开。
func TestManualCloseNotReopened(t *testing.T) {
	n := NewNetwork()
	mustNode(t, n, "src", NodeSource)
	mustNode(t, n, "X", NodeBranch)
	mustNode(t, n, "U", NodeUser)
	mustSegment(t, n, "e0", "src", "X")
	mustSegment(t, n, "s1", "X", "U")
	mustValve(t, n, "e0", EndB, "v0") // e0 在 X 端
	mustValve(t, n, "s1", EndA, "v1") // s1 在 X 端

	// 他人手工关闭 v0（与任何隔离无关）。
	must(t, n.CloseValve("v0"))

	// 隔离 s1：v0 已关视为满足，无需新关闭阀门。
	p, err := n.ExecuteIsolation("s1", nil)
	must(t, err)
	assertPlan(t, p, nil, []string{"s1"}, nil)

	must(t, n.CompleteRepair("s1"))
	if valveState(t, n, "v0") != ValveClosed {
		t.Error("手工关闭的 v0 不得被修复误开")
	}
	if n.HasHeat("U") {
		t.Error("v0 仍关，U 应无热")
	}
}

// TestErrorOrder 错误须可区分并按固定次序判定。
func TestErrorOrder(t *testing.T) {
	n := NewNetwork()

	// 参数非法优先于一切。
	if _, err := n.SimulateIsolation(""); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("空管段 id: got %v", err)
	}
	if _, err := n.ExecuteIsolation("", nil); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("空管段 id: got %v", err)
	}
	if err := n.CompleteRepair(""); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("空管段 id: got %v", err)
	}
	// 管段不存在次之。
	if _, err := n.SimulateIsolation("nope"); !errors.Is(err, ErrSegmentNotFound) {
		t.Errorf("管段不存在: got %v", err)
	}
	if _, err := n.ExecuteIsolation("nope", nil); !errors.Is(err, ErrSegmentNotFound) {
		t.Errorf("管段不存在: got %v", err)
	}
	if err := n.CompleteRepair("nope"); !errors.Is(err, ErrSegmentNotFound) {
		t.Errorf("管段不存在: got %v", err)
	}

	mustNode(t, n, "src", NodeSource)
	mustNode(t, n, "u", NodeUser)
	mustSegment(t, n, "t", "src", "u")

	// 未处于隔离（修复时）。
	if err := n.CompleteRepair("t"); !errors.Is(err, ErrSegmentNotIsolated) {
		t.Errorf("未处于隔离: got %v", err)
	}
	// 无法隔离优先于阀门状态已变化。
	if _, err := n.ExecuteIsolation("t", []string{"bogus"}); !errors.Is(err, ErrNotIsolatable) {
		t.Errorf("无法隔离应优先于阀门状态变化: got %v", err)
	}

	// 装上热源端阀门后可隔离。
	mustValve(t, n, "t", EndA, "v")
	p, err := n.SimulateIsolation("t")
	must(t, err)
	// 阀门状态已变化：先手工关闭 v，再按旧推演执行。
	must(t, n.CloseValve("v"))
	if _, err := n.ExecuteIsolation("t", p.ValvesToClose); !errors.Is(err, ErrValveStateChanged) {
		t.Fatalf("阀门状态已变化: got %v", err)
	}
	// 全有或全无：被拒绝后状态不变。
	if lk, _ := n.SegmentLeaking("t"); lk {
		t.Error("被拒绝后 t 不应被标记泄漏")
	}
	if got := n.ActiveIsolations(); len(got) != 0 {
		t.Errorf("被拒绝后不应有活动隔离, got %v", got)
	}
	if valveState(t, n, "v") != ValveClosed {
		t.Error("被拒绝后 v 应保持手工关闭状态")
	}

	// 已处于活动隔离（执行时）。
	must(t, n.OpenValve("v"))
	if _, err := n.ExecuteIsolation("t", nil); err != nil {
		t.Fatalf("执行隔离失败: %v", err)
	}
	if _, err := n.ExecuteIsolation("t", nil); !errors.Is(err, ErrSegmentIsolated) {
		t.Errorf("已处于活动隔离: got %v", err)
	}
	must(t, n.CompleteRepair("t"))
}

// TestExecuteAllOrNothing 执行隔离时任一阀门已变化则整体拒绝。
func TestExecuteAllOrNothing(t *testing.T) {
	n := buildRing(t)
	p, err := n.SimulateIsolation("e1")
	must(t, err)
	if len(p.ValvesToClose) != 2 {
		t.Fatalf("推演应需关闭两个阀门, got %v", p.ValvesToClose)
	}
	// 把其中一个待关阀门手工关闭，使重新推演的待关集合发生变化。
	must(t, n.CloseValve(p.ValvesToClose[0]))
	if _, err := n.ExecuteIsolation("e1", p.ValvesToClose); !errors.Is(err, ErrValveStateChanged) {
		t.Fatalf("应整体拒绝: got %v", err)
	}
	// 另一个阀门未被关闭，管段未标记泄漏。
	if valveState(t, n, p.ValvesToClose[1]) != ValveOpen {
		t.Error("整体拒绝后另一阀门不应被关闭")
	}
	if lk, _ := n.SegmentLeaking("e1"); lk {
		t.Error("整体拒绝后 e1 不应被标记泄漏")
	}
}

// TestSimulateReadOnlyDeterministic 推演只读且对同一状态结果唯一。
func TestSimulateReadOnlyDeterministic(t *testing.T) {
	n := buildRing(t)
	p1, err := n.SimulateIsolation("e1")
	must(t, err)
	p2, err := n.SimulateIsolation("e1")
	must(t, err)
	if !reflect.DeepEqual(p1, p2) {
		t.Errorf("同一状态两次推演结果应一致:\n%+v\n%+v", p1, p2)
	}
	// 推演不改变任何状态。
	if lk, _ := n.SegmentLeaking("e1"); lk {
		t.Error("推演不应标记泄漏")
	}
	for _, v := range []string{"v0", "v1", "v2", "v3", "vu"} {
		if valveState(t, n, v) != ValveOpen {
			t.Errorf("推演不应改变阀门状态: %s", v)
		}
	}
	if !n.HasHeat("u1") {
		t.Error("推演不应改变供热状态")
	}
}

// TestValveCommands 卡死只能由上报产生，卡死后不接受开关指令。
func TestValveCommands(t *testing.T) {
	n := buildChain(t)

	if err := n.ReportStuck("v0", ValveOpen); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("上报非卡死状态: got %v", err)
	}
	if err := n.ReportStuck("nope", ValveStuckOpen); !errors.Is(err, ErrValveNotFound) {
		t.Errorf("阀门不存在: got %v", err)
	}
	if err := n.ConfirmValveRepaired("v0"); !errors.Is(err, ErrValveNotStuck) {
		t.Errorf("未卡死: got %v", err)
	}

	must(t, n.ReportStuck("v0", ValveStuckClosed))
	if err := n.OpenValve("v0"); !errors.Is(err, ErrValveStuck) {
		t.Errorf("卡死后不接受开指令: got %v", err)
	}
	if err := n.CloseValve("v0"); !errors.Is(err, ErrValveStuck) {
		t.Errorf("卡死后不接受关指令: got %v", err)
	}
	must(t, n.ConfirmValveRepaired("v0"))
	if valveState(t, n, "v0") != ValveOpen {
		t.Error("维修确认后应恢复为开")
	}
}

// TestTopologyRules 拓扑约束：自环、重名、平行管段、每端一个阀门。
func TestTopologyRules(t *testing.T) {
	n := NewNetwork()
	mustNode(t, n, "a", NodeBranch)
	mustNode(t, n, "b", NodeBranch)

	if err := n.AddSegment("s0", "a", "a"); !errors.Is(err, ErrSelfLoop) {
		t.Errorf("自环: got %v", err)
	}
	if err := n.AddSegment("s0", "a", "nope"); !errors.Is(err, ErrNodeNotFound) {
		t.Errorf("节点不存在: got %v", err)
	}
	if err := n.AddNode("a", NodeBranch); !errors.Is(err, ErrNodeExists) {
		t.Errorf("节点重名: got %v", err)
	}
	mustSegment(t, n, "s1", "a", "b")
	if err := n.AddSegment("s1", "a", "b"); !errors.Is(err, ErrSegmentExists) {
		t.Errorf("管段重名: got %v", err)
	}
	// 两节点间允许多条管段。
	mustSegment(t, n, "s2", "a", "b")
	mustSegment(t, n, "s3", "b", "a")

	mustValve(t, n, "s1", EndA, "v1")
	if err := n.InstallValve("s1", EndA, "v2"); !errors.Is(err, ErrValveExists) {
		t.Errorf("每端至多一个阀门: got %v", err)
	}
	if err := n.InstallValve("s2", EndA, "v1"); !errors.Is(err, ErrValveExists) {
		t.Errorf("阀门重名: got %v", err)
	}
	if err := n.RemoveValve("s1", EndB); !errors.Is(err, ErrValveNotFound) {
		t.Errorf("无端上阀门: got %v", err)
	}
	if err := n.RemoveSegment("nope"); !errors.Is(err, ErrSegmentNotFound) {
		t.Errorf("管段不存在: got %v", err)
	}
}
