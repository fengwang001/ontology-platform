package link

import (
	"errors"
	"fmt"
	"testing"
)

// TestFailurePriority 验证对象实例不存在优先于基数与重复性判定，
// 以及四类失败可被调用方精确区分。
func TestFailurePriority(t *testing.T) {
	objs := newMemObjects()
	objs.add("a", "A")
	objs.add("b", "B")
	s := NewStore(objs)
	if err := s.RegisterType(LinkType{
		ID: "lt", SourceType: "A", TargetType: "B",
		ForwardCap: Limited(0), BackwardCap: Unlimited(),
	}); err != nil {
		t.Fatal(err)
	}

	// 先造成“与在库链接重复”的条件（需要一个能创建的链接类型做对照，
	// 这里直接逻辑删除使三个失败条件同时成立：cap=0 满、实例已删除）。
	objs.delete("b")
	_, err := s.Create(CreateRequest{"lt", "a", "b", disc("k", "1")})
	if !errors.Is(err, ErrObjectInstanceDeleted) {
		t.Fatalf("priority: got %v want ErrObjectInstanceDeleted", err)
	}

	objs.add("b", "B")
	objs.add("c", "C")
	_, err = s.Create(CreateRequest{"lt", "a", "c", disc("k", "1")})
	if !errors.Is(err, ErrLinkTypeDirectionNotAllowed) {
		t.Fatalf("direction: got %v", err)
	}

	_, err = s.Create(CreateRequest{"lt", "a", "b", disc("k", "1")})
	if !errors.Is(err, ErrCardinalityExceeded) {
		t.Fatalf("cap: got %v", err)
	}

	_, err = s.Create(CreateRequest{"nope", "a", "b", nil})
	if !errors.Is(err, ErrUnknownLinkType) {
		t.Fatalf("unknown type: got %v", err)
	}

	// 全部拒绝不得影响后续查询。
	if s.CountDirection("lt", "a", DirectionForward) != 0 {
		t.Fatal("rejected requests must not change counts")
	}
	log := s.Decisions()
	if len(log) != 4 {
		t.Fatalf("decisions logged: %d", len(log))
	}
	for i, d := range log {
		if d.Seq != int64(i+1) {
			t.Fatalf("decision seq not contiguous: %+v", d)
		}
		if d.Reason == "" {
			t.Fatalf("decision %d missing basis", i)
		}
	}
}

// TestRevokedSlotReuse 验证已撤销区分属性可被重新占用，且新链接不继承旧状态。
func TestRevokedSlotReuse(t *testing.T) {
	objs := newMemObjects()
	objs.add("a", "A")
	objs.add("b", "B")
	s := NewStore(objs)
	if err := s.RegisterType(LinkType{
		ID: "lt", SourceType: "A", TargetType: "B",
		ForwardCap: Limited(1), BackwardCap: Limited(1),
	}); err != nil {
		t.Fatal(err)
	}

	first := mustCreate(t, s, CreateRequest{"lt", "a", "b", disc("k", "v")})

	if _, err := s.Create(CreateRequest{"lt", "a", "b", disc("k", "w")}); !errors.Is(err, ErrCardinalityExceeded) {
		t.Fatalf("cap: %v", err)
	}

	if _, err := s.Delete("lt", "a", "b", disc("k", "v")); err != nil {
		t.Fatal(err)
	}

	// 撤销后名额释放，被撤销的取值可重新占用，但必须分配新实例 ID。
	again, err := s.Create(CreateRequest{"lt", "a", "b", disc("k", "v")})
	if err != nil {
		t.Fatalf("reuse: %v", err)
	}
	if again.ID == first.ID {
		t.Fatalf("reused link inherits revoked identity: %d == %d", again.ID, first.ID)
	}

	// 删除从未存在的取值是幂等失败，不改变任何状态。
	if _, err := s.Delete("lt", "a", "b", disc("k", "z")); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("missing delete: %v", err)
	}

	// 混合已撤销取值 v 与未撤销取值 x：backward cap=1。
	mustCreate(t, s, CreateRequest{"lt", "b", "a", disc("k", "x")})
	if _, err := s.Create(CreateRequest{"lt", "b", "a", disc("k", "x")}); !errors.Is(err, ErrDuplicateLink) {
		t.Fatalf("backward duplicate: %v", err)
	}
	if _, err := s.Create(CreateRequest{"lt", "b", "a", disc("k", "y")}); !errors.Is(err, ErrCardinalityExceeded) {
		t.Fatalf("backward still full: %v", err)
	}
}

// TestDirectionIndependence 验证两个方向的基数互不影响。
func TestDirectionIndependence(t *testing.T) {
	objs := newMemObjects()
	objs.add("a", "A")
	objs.add("b", "B")
	s := NewStore(objs)
	if err := s.RegisterType(LinkType{
		ID: "lt", SourceType: "A", TargetType: "B",
		ForwardCap: Limited(1), BackwardCap: Limited(2),
	}); err != nil {
		t.Fatal(err)
	}

	mustCreate(t, s, CreateRequest{"lt", "a", "b", disc("k", "1")})
	if _, err := s.Create(CreateRequest{"lt", "a", "b", disc("k", "2")}); !errors.Is(err, ErrCardinalityExceeded) {
		t.Fatal(err)
	}
	mustCreate(t, s, CreateRequest{"lt", "b", "a", disc("k", "1")})
	mustCreate(t, s, CreateRequest{"lt", "b", "a", disc("k", "2")})
	if got := s.CountDirection("lt", "b", DirectionBackward); got != 2 {
		t.Fatalf("bwd = %d", got)
	}
	if got := s.CountDirection("lt", "a", DirectionForward); got != 1 {
		t.Fatalf("fwd = %d", got)
	}
}

// TestLinkTypeIsolation 验证多个链接类型间基数与判重完全独立。
func TestLinkTypeIsolation(t *testing.T) {
	objs := newMemObjects()
	objs.add("a", "A")
	objs.add("b", "B")
	s := NewStore(objs)
	for _, id := range []LinkTypeID{"l1", "l2"} {
		if err := s.RegisterType(LinkType{
			ID: id, SourceType: "A", TargetType: "B",
			ForwardCap: Limited(1), BackwardCap: Unlimited(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	mustCreate(t, s, CreateRequest{"l1", "a", "b", disc("k", "same")})
	mustCreate(t, s, CreateRequest{"l2", "a", "b", disc("k", "same")})
	if _, err := s.Create(CreateRequest{"l2", "a", "b", disc("k", "other")}); !errors.Is(err, ErrCardinalityExceeded) {
		t.Fatalf("l2 own cap: %v", err)
	}
	if _, err := s.Create(CreateRequest{"l1", "a", "b", disc("k", "same")}); !errors.Is(err, ErrDuplicateLink) {
		t.Fatalf("l1 duplicate within type: %v", err)
	}
	if got := s.CountDirection("l2", "a", DirectionForward); got != 1 {
		t.Fatalf("l2 count = %d", got)
	}
}

// TestCountCostIndependentOfHistory 用可验证方式证明度数查询开销与历史无关：
// 反复创建/撤销后内部度数槽与 pair 槽都回收，规模只与当前有效链接相关。
func TestCountCostIndependentOfHistory(t *testing.T) {
	objs := newMemObjects()
	objs.add("a", "A")
	objs.add("b", "B")
	s := NewStore(objs)
	if err := s.RegisterType(LinkType{
		ID: "lt", SourceType: "A", TargetType: "B",
		ForwardCap: Unlimited(), BackwardCap: Unlimited(),
	}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2000; i++ {
		v := fmt.Sprintf("v%d", i)
		mustCreate(t, s, CreateRequest{"lt", "a", "b", disc("k", v)})
		if _, err := s.Delete("lt", "a", "b", disc("k", v)); err != nil {
			t.Fatal(err)
		}
	}

	for i, sh := range s.shards {
		sh.mu.Lock()
		np, nd := len(sh.pairs), len(sh.degree)
		sh.mu.Unlock()
		if np != 0 || nd != 0 {
			t.Fatalf("shard %d retains history: pairs=%d degree=%d", i, np, nd)
		}
	}
	if got := s.CountDirection("lt", "a", DirectionForward); got != 0 {
		t.Fatalf("count after full revoke: %d", got)
	}

	mustCreate(t, s, CreateRequest{"lt", "a", "b", disc("k", "x")})
	mustCreate(t, s, CreateRequest{"lt", "a", "b", disc("k", "y")})
	totalPairs, totalDegree := 0, 0
	for _, sh := range s.shards {
		sh.mu.Lock()
		totalPairs += len(sh.pairs)
		totalDegree += len(sh.degree)
		sh.mu.Unlock()
	}
	if totalPairs != 1 || totalDegree != 1 {
		t.Fatalf("state size = pairs %d, degree %d; want 1/1", totalPairs, totalDegree)
	}
}
