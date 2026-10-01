package scaledown

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, p int, dur int64, min int) *Scaler {
	t.Helper()
	s, err := New(p, dur, min)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func addNode(t *testing.T, s *Scaler, name string, ca, ma int64) {
	t.Helper()
	if err := s.AddNode(name, ca, ma); err != nil {
		t.Fatalf("AddNode(%s): %v", name, err)
	}
}

func addPod(t *testing.T, s *Scaler, id, node string, pc, pm int64, kind string) {
	t.Helper()
	if err := s.AddPod(Pod{ID: id, Node: node, PC: pc, PM: pm, Kind: kind}); err != nil {
		t.Fatalf("AddPod(%s): %v", id, err)
	}
}

// 利用率恰等于阈值不算低利用（严格小于）。
func TestUtilizationEqualThresholdNotLow(t *testing.T) {
	s := mustNew(t, 50, 1, 0)
	addNode(t, s, "a", 100, 100)
	addNode(t, s, "b", 100, 100)
	addPod(t, s, "p1", "a", 50, 1, KindNormal)
	if _, err := s.Tick(10); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Since()["a"]; ok {
		t.Fatalf("a should not be removable at exact threshold: %v", s.Since())
	}
	if err := s.RemovePod("p1"); err != nil {
		t.Fatal(err)
	}
	addPod(t, s, "p1", "a", 49, 1, KindNormal)
	if _, err := s.Tick(11); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Since()["a"]; !ok {
		t.Fatalf("a should be removable below threshold: %v", s.Since())
	}
}

// cpu 与内存任一维度不低即不可移除。
func TestEitherDimensionNotLow(t *testing.T) {
	s := mustNew(t, 50, 10, 0)
	addNode(t, s, "a", 100, 100)
	addNode(t, s, "b", 100, 100)
	addPod(t, s, "p1", "a", 40, 50, KindNormal)
	if _, err := s.Tick(1); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Since()["a"]; ok {
		t.Fatalf("mem at threshold must block a: %v", s.Since())
	}
}

// daemon 不计入利用率但占用目标空间；无 normal 的节点可直接移除。
func TestDaemonAndEmptyNode(t *testing.T) {
	s := mustNew(t, 50, 1, 0)
	addNode(t, s, "a", 100, 100)
	addNode(t, s, "b", 100, 100)
	addPod(t, s, "d1", "a", 60, 60, KindDaemon)
	if _, err := s.Tick(5); err != nil {
		t.Fatal(err)
	}
	res, err := s.Tick(6)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != "a" || len(res.Migrations) != 0 {
		t.Fatalf("empty-of-normal node should be removed: %+v", res)
	}

	s2 := mustNew(t, 50, 1, 0)
	addNode(t, s2, "a", 100, 100)
	addNode(t, s2, "b", 100, 100)
	addPod(t, s2, "p1", "a", 40, 40, KindNormal)
	addPod(t, s2, "d2", "b", 90, 90, KindDaemon)
	if _, err := s2.Tick(1); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Since()["a"]; ok {
		t.Fatalf("a must not be removable: daemon consumes target space")
	}
}

// 前一候选的模拟迁入占掉空间使后一候选放不下。
func TestIncomingReservationBlocksLaterCandidate(t *testing.T) {
	s := mustNew(t, 50, 100, 1)
	addNode(t, s, "a", 100, 100)
	addNode(t, s, "b", 100, 100)
	addNode(t, s, "c", 100, 100)
	addPod(t, s, "pa", "a", 40, 40, KindNormal)
	addPod(t, s, "pb", "b", 40, 40, KindNormal)
	addPod(t, s, "pc0", "c", 30, 30, KindNormal)
	// 评估 a: pa 目标 b(used80,+40=120 否) => c(used30,+40=70 是)，a 可移除，c 收迁入。
	// 评估 b: pb 目标 a(已 removable 排除)；c used30+inc40+40=110 否 => b 失败。
	if _, err := s.Tick(1); err != nil {
		t.Fatal(err)
	}
	since := s.Since()
	if _, ok := since["a"]; !ok {
		t.Fatalf("a removable: %v", since)
	}
	if _, ok := since["b"]; ok {
		t.Fatalf("b blocked by a's reservation: %v", since)
	}
	if _, ok := since["c"]; ok {
		t.Fatalf("c received incoming, must not be removable: %v", since)
	}
}

// 候选失败时其临时落点被撤销，不污染状态。
func TestFailedCandidateRollback(t *testing.T) {
	s := mustNew(t, 50, 100, 1)
	addNode(t, s, "a", 100, 100)
	addNode(t, s, "b", 100, 100)
	addNode(t, s, "c", 100, 100)
	addPod(t, s, "a1", "a", 20, 20, KindNormal)
	addPod(t, s, "a2", "a", 20, 20, KindNormal)
	addPod(t, s, "b1", "b", 90, 90, KindNormal)
	addPod(t, s, "c1", "c", 70, 70, KindNormal)
	// a1: b used90 否；c used70+20=90 是(临时)。a2: b 否；c 70+20+20=110 否 => 失败撤销。
	if _, err := s.Tick(1); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Since()["a"]; ok {
		t.Fatalf("a should fail placement and get no since")
	}
	if _, err := s.Tick(2); err != nil {
		t.Fatal(err)
	}
	if len(s.Since()) != 0 {
		t.Fatalf("rollback must leave no residue: %v", s.Since())
	}
}

// 本轮接收过迁入的节点不可移除并清除 since。
func TestIncomingNodeClearsSince(t *testing.T) {
	s := mustNew(t, 50, 100, 1)
	addNode(t, s, "a", 100, 100)
	addNode(t, s, "b", 100, 100)
	if _, err := s.Tick(1); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Since()["b"]; !ok {
		t.Fatalf("b should have since")
	}
	addPod(t, s, "pa", "a", 10, 10, KindNormal)
	if _, err := s.Tick(2); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Since()["b"]; ok {
		t.Fatalf("b must lose since after receiving incoming: %v", s.Since())
	}
	if _, ok := s.Since()["a"]; !ok {
		t.Fatalf("a should be removable")
	}
}

// since 的保持与清除。
func TestSinceHeldAndCleared(t *testing.T) {
	s := mustNew(t, 50, 100, 1)
	addNode(t, s, "a", 100, 100)
	addNode(t, s, "b", 100, 100)
	if _, err := s.Tick(7); err != nil {
		t.Fatal(err)
	}
	if got := s.Since()["a"]; got != 7 {
		t.Fatalf("since want 7, got %d", got)
	}
	if _, err := s.Tick(20); err != nil {
		t.Fatal(err)
	}
	if got := s.Since()["a"]; got != 7 {
		t.Fatalf("since must hold at 7, got %d", got)
	}
	addPod(t, s, "pa", "a", 90, 90, KindNormal)
	addPod(t, s, "pb", "b", 90, 90, KindNormal)
	if _, err := s.Tick(21); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Since()["a"]; ok {
		t.Fatalf("since must be cleared")
	}
}

// now-since 恰等于 T 执行；差 1 不执行。
func TestDurationBoundary(t *testing.T) {
	s := mustNew(t, 50, 10, 1)
	addNode(t, s, "a", 100, 100)
	addNode(t, s, "b", 100, 100)
	if _, err := s.Tick(5); err != nil {
		t.Fatal(err)
	}
	res, err := s.Tick(14)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != "" {
		t.Fatalf("9ms must not remove: %+v", res)
	}
	res, err = s.Tick(15)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != "a" {
		t.Fatalf("10ms must remove a: %+v", res)
	}
}

// 节点总数恰等于 MinNodes 时不移除。
func TestAtMinNodesNoRemoval(t *testing.T) {
	s := mustNew(t, 50, 1, 2)
	addNode(t, s, "a", 100, 100)
	addNode(t, s, "b", 100, 100)
	res, err := s.Tick(1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != "" {
		t.Fatalf("must not remove at MinNodes: %+v", res)
	}
}

// since 并列取名字节序小者。
func TestTieBreakByName(t *testing.T) {
	s := mustNew(t, 50, 10, 0)
	addNode(t, s, "b", 100, 100)
	addNode(t, s, "a", 100, 100)
	// 空 a/b 均可移除；Tick1 建立 since=1；Tick11 时两者都到期，取 a。
	if _, err := s.Tick(1); err != nil {
		t.Fatal(err)
	}
	res, err := s.Tick(11)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != "a" {
		t.Fatalf("tie must pick a, got %+v", res)
	}
}

// pinned 节点不可移除。
func TestPinnedBlocks(t *testing.T) {
	s := mustNew(t, 50, 1, 0)
	addNode(t, s, "a", 100, 100)
	addNode(t, s, "b", 100, 100)
	addPod(t, s, "x", "a", 1, 1, KindPinned)
	if _, err := s.Tick(1); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Since()["a"]; ok {
		t.Fatalf("pinned node must not be removable")
	}
}

// 移除后 normal Pod 真实迁移并可再次管理。
func TestRemovalMigratesPods(t *testing.T) {
	s := mustNew(t, 50, 10, 0)
	addNode(t, s, "a", 100, 100)
	addNode(t, s, "b", 100, 100)
	addPod(t, s, "p1", "a", 10, 10, KindNormal)
	addPod(t, s, "d1", "a", 5, 5, KindDaemon)
	if _, err := s.Tick(0); err != nil {
		t.Fatal(err)
	}
	res, err := s.Tick(10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != "a" || len(res.Migrations) != 1 ||
		res.Migrations[0].PodID != "p1" || res.Migrations[0].Target != "b" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if err := s.RemovePod("p1"); err != nil {
		t.Fatalf("migrated pod must be removable: %v", err)
	}
	if err := s.RemovePod("d1"); !errors.Is(err, ErrPodNotFound) {
		t.Fatalf("daemon must be deleted with node, got %v", err)
	}
}

// 各拒绝原因的先后与被拒绝不改状态。
func TestRejectionReasonsOrder(t *testing.T) {
	if _, err := New(0, 1, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("P=0: %v", err)
	}
	if _, err := New(101, 1, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("P=101: %v", err)
	}
	if _, err := New(1, 0, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("T=0: %v", err)
	}
	if _, err := New(1, 1, -1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("MinNodes=-1: %v", err)
	}
	if _, err := New(1, 1, 1_000_001); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("MinNodes too big: %v", err)
	}

	s := mustNew(t, 50, 1, 0)
	if err := s.AddNode("", 1, 1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("empty node name: %v", err)
	}
	if err := s.AddNode("a", 0, 1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("ca=0: %v", err)
	}
	if err := s.AddNode("a", 1, 1_000_000_000_001); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("ma too big: %v", err)
	}
	addNode(t, s, "a", 10, 10)
	if err := s.AddNode("a", 10, 10); !errors.Is(err, ErrNodeExists) {
		t.Fatalf("dup node: %v", err)
	}

	// AddPod 顺序：参数非法 -> Pod 已存在 -> 节点不存在 -> 容量不足。
	if err := s.AddPod(Pod{ID: "", Node: "a", PC: 1, PM: 1, Kind: KindNormal}); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("empty id: %v", err)
	}
	if err := s.AddPod(Pod{ID: "p", Node: "a", PC: 1, PM: 1, Kind: "weird"}); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("bad kind: %v", err)
	}
	if err := s.AddPod(Pod{ID: "p", Node: "a", PC: -1, PM: 1, Kind: KindNormal}); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("pc<0: %v", err)
	}
	if err := s.AddPod(Pod{ID: "p", Node: "a", PC: 0, PM: 0, Kind: KindNormal}); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("both zero: %v", err)
	}
	// 参数非法优先于“已存在”：用已存在 ID 但非法请求。
	addPod(t, s, "p", "a", 1, 1, KindNormal)
	if err := s.AddPod(Pod{ID: "p", Node: "a", PC: 0, PM: 0, Kind: KindNormal}); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("invalid must precede exists: %v", err)
	}
	if err := s.AddPod(Pod{ID: "p", Node: "a", PC: 1, PM: 1, Kind: KindNormal}); !errors.Is(err, ErrPodExists) {
		t.Fatalf("dup pod: %v", err)
	}
	// 已存在优先于节点不存在。
	if err := s.AddPod(Pod{ID: "p", Node: "ghost", PC: 1, PM: 1, Kind: KindNormal}); !errors.Is(err, ErrPodExists) {
		t.Fatalf("exists must precede node missing: %v", err)
	}
	// 节点不存在优先于容量不足。
	if err := s.AddPod(Pod{ID: "q", Node: "ghost", PC: 1_000_000, PM: 1, Kind: KindNormal}); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("node missing must precede capacity: %v", err)
	}
	if err := s.AddPod(Pod{ID: "q", Node: "a", PC: 10, PM: 10, Kind: KindNormal}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity: %v", err)
	}

	if err := s.RemovePod("nope"); !errors.Is(err, ErrPodNotFound) {
		t.Fatalf("remove missing: %v", err)
	}
	if _, err := s.Tick(-1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("tick negative: %v", err)
	}
	if _, err := s.Tick(5); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tick(4); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("clock rewind: %v", err)
	}

	// 被拒绝操作不改状态：仍只有节点 a 与 Pod p。
	if len(s.Since()) != 0 {
		// Tick(-1) 被拒；Tick4 回退被拒；Tick5 中 a 利用率10%低但没有其它节点承接 p，不可移除。
		t.Fatalf("a should not be removable without target: %v", s.Since())
	}
	if err := s.RemovePod("p"); err != nil {
		t.Fatalf("p must still exist: %v", err)
	}
}

// AddNode/AddPod/RemovePod 不改变 since。
func TestMutationsDoNotTouchSince(t *testing.T) {
	s := mustNew(t, 50, 100, 1)
	addNode(t, s, "a", 100, 100)
	addNode(t, s, "b", 100, 100)
	if _, err := s.Tick(3); err != nil {
		t.Fatal(err)
	}
	before := s.Since()
	addNode(t, s, "c", 100, 100)
	addPod(t, s, "p", "c", 5, 5, KindNormal)
	if err := s.RemovePod("p"); err != nil {
		t.Fatal(err)
	}
	after := s.Since()
	if len(before) != len(after) {
		t.Fatalf("since changed by mutation: before=%v after=%v", before, after)
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("since[%s] changed %d->%d", k, v, after[k])
		}
	}
}

// 容量不变量：任意时刻节点上全部 Pod 请求之和不超过可分配量。
func TestCapacityInvariantAfterMigration(t *testing.T) {
	s := mustNew(t, 50, 1, 0)
	addNode(t, s, "a", 100, 100)
	addNode(t, s, "b", 100, 100)
	addPod(t, s, "p1", "a", 40, 40, KindNormal)
	addPod(t, s, "p2", "a", 9, 9, KindNormal)
	addPod(t, s, "pb", "b", 60, 60, KindNormal)
	// a: sc=49 低；排序 p1(40) 先落 b(used60,+40=100 是)；p2(9)：b inc40 => 109 否 => a 失败。
	if _, err := s.Tick(1); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Since()["a"]; ok {
		t.Fatal("a must fail: reservation leaves no room for p2")
	}
}

// 迁移列表顺序：normal Pod 按（cpu 降序，内存降序，ID 字节序）输出。
func TestMigrationOrder(t *testing.T) {
	s := mustNew(t, 50, 10, 0)
	addNode(t, s, "a", 1000, 1000)
	addNode(t, s, "b", 1000, 1000)
	addPod(t, s, "z", "a", 10, 10, KindNormal)
	addPod(t, s, "m", "a", 30, 5, KindNormal)
	addPod(t, s, "e", "a", 30, 9, KindNormal)
	// 排序：e(30,9) > m(30,5) > z(10,10)
	if _, err := s.Tick(0); err != nil {
		t.Fatal(err)
	}
	res, err := s.Tick(10)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"e", "m", "z"}
	if len(res.Migrations) != len(want) {
		t.Fatalf("migrations=%+v", res.Migrations)
	}
	for i, id := range want {
		if res.Migrations[i].PodID != id || res.Migrations[i].Target != "b" {
			t.Fatalf("migration[%d]=%+v want %s->b; full=%+v", i, res.Migrations[i], id, res.Migrations)
		}
	}
}
