package vanload

import (
	"errors"
	"testing"
)

func rejectKind(t *testing.T, err error) RejectKind {
	t.Helper()
	var r *Reject
	if !errors.As(err, &r) {
		t.Fatalf("期望 *Reject, 实际 %v", err)
	}
	return r.Kind
}

func mustLoad(t *testing.T, sys *System, c Cargo) int {
	t.Helper()
	res, err := sys.Load(c)
	if err != nil {
		t.Fatalf("装货 id=%d 意外失败: %v", c.ID, err)
	}
	return res.Compartment
}

func TestBoundaryEqualAndExceed(t *testing.T) {
	// 单分区：载重 1000、容积 2000。
	sys, err := New([]Compartment{{MaxWeight: 1000, MaxVolume: 2000}})
	if err != nil {
		t.Fatal(err)
	}

	// 恰好等于上限：允许。
	if k := mustLoad(t, sys, Cargo{ID: 1, Weight: 500, Volume: 1000, Stop: 1}); k != 1 {
		t.Fatalf("期望分区1, 实际 %d", k)
	}
	// 再放一件恰好把载重/容积用满：允许。
	if k := mustLoad(t, sys, Cargo{ID: 2, Weight: 500, Volume: 1000, Stop: 1}); k != 1 {
		t.Fatalf("期望分区1, 实际 %d", k)
	}
	caps := sys.RemainingCapacities()
	if caps[0].RemainWeight != 0 || caps[0].RemainVolume != 0 {
		t.Fatalf("期望剩余均为0, 实际 %+v", caps[0])
	}

	// 超一：超容（容积0、载重0 都超；按严重度应报超容，因为超重严重度更高?
	// 同分区首个失败取超重：重量超 1 先命中超重）。
	_, err = sys.Load(Cargo{ID: 3, Weight: 1, Volume: 1, Stop: 1})
	if got := rejectKind(t, err); got != KindOverweight {
		t.Fatalf("重量与容积同超时期望超重（严重度更高），实际 %v", got)
	}

	// 卸载后仅超容一单位：构造载重剩余足够、容积为 0 的场景。
	sys2, _ := New([]Compartment{{MaxWeight: 1000, MaxVolume: 10}})
	mustLoad(t, sys2, Cargo{ID: 1, Weight: 1, Volume: 10, Stop: 1})
	_, err = sys2.Load(Cargo{ID: 2, Weight: 1, Volume: 1, Stop: 1})
	if got := rejectKind(t, err); got != KindOvervolume {
		t.Fatalf("期望超容, 实际 %v", got)
	}

	// 仅超重一单位。
	sys3, _ := New([]Compartment{{MaxWeight: 10, MaxVolume: 1000}})
	mustLoad(t, sys3, Cargo{ID: 1, Weight: 10, Volume: 1, Stop: 1})
	_, err = sys3.Load(Cargo{ID: 2, Weight: 1, Volume: 1, Stop: 1})
	if got := rejectKind(t, err); got != KindOverweight {
		t.Fatalf("期望超重, 实际 %v", got)
	}
}

func TestIsolationCombinations(t *testing.T) {
	categories := []Category{CategoryGeneral, CategoryFood, CategoryFlammable, CategoryOxidizing}
	// allowed[a][b] 表示类别 a,b 能否同区（对称）。
	allowed := [4][4]bool{
		{true, true, true, true},
		{true, true, false, false},
		{true, false, true, false},
		{true, false, false, true},
	}
	id := 1
	for ai, a := range categories {
		for bi, b := range categories {
			sys, _ := New([]Compartment{{MaxWeight: 1_000_000, MaxVolume: 1_000_000}})
			mustLoad(t, sys, Cargo{ID: id, Weight: 1, Volume: 1, Stop: 1, Category: a})
			id++
			_, err := sys.Load(Cargo{ID: id, Weight: 1, Volume: 1, Stop: 1, Category: b})
			id++
			ok := err == nil
			if ok != allowed[ai][bi] {
				t.Fatalf("类别组合 a=%v b=%v 期望可同区=%v，实际 err=%v", a, b, allowed[ai][bi], err)
			}
		}
	}
}

func TestOrderConstraintLaterMakesEarlierChoiceInfeasible(t *testing.T) {
	// 两个分区：分区1容量小，分区2容量大。
	sys, _ := New([]Compartment{
		{MaxWeight: 10, MaxVolume: 1_000_000},
		{MaxWeight: 1_000_000, MaxVolume: 1_000_000},
	})

	// 先把两件停靠点5的货物分别放到分区1（占满载重）与分区2：
	mustLoad(t, sys, Cargo{ID: 1, Weight: 10, Volume: 1, Stop: 5})
	mustLoad(t, sys, Cargo{ID: 2, Weight: 1, Volume: 1, Stop: 5})
	if k, _ := sys.Locate(2); k != 2 {
		t.Fatalf("第二件应在分区2, 实际 %d", k)
	}
	// 后来停靠点1（更早卸）货物：
	//  - 分区1：后方分区2有更晚卸的 stop5 → 顺序冲突（同时超重，但顺序严重度更高）；
	//  - 分区2：与 stop5 同区混装允许，隔离/容量均满足 → 可行。
	res, err := sys.Load(Cargo{ID: 3, Weight: 1, Volume: 1, Stop: 1})
	if err != nil {
		t.Fatalf("停靠点1货物应能放入分区2, 实际 %v", err)
	}
	if res.Compartment != 2 {
		t.Fatalf("期望分区2, 实际 %d", res.Compartment)
	}

	// 更早时分区1本是编号最小的可行选择；正是后放入分区2的晚卸货物使它因顺序约束失效。
	// 再把分区2也用隔离封死（放入食品，阻止易燃进入），stop1 易燃件将无处可放，整体顺序冲突。
	sys2, _ := New([]Compartment{
		{MaxWeight: 10, MaxVolume: 1_000_000},
		{MaxWeight: 1_000_000, MaxVolume: 1_000_000},
	})
	mustLoad(t, sys2, Cargo{ID: 11, Weight: 10, Volume: 1, Stop: 5})
	mustLoad(t, sys2, Cargo{ID: 12, Weight: 1, Volume: 1, Stop: 5, Category: CategoryFood})
	_, err = sys2.Load(Cargo{ID: 13, Weight: 1, Volume: 1, Stop: 1, Category: CategoryFlammable})
	if rejectKind(t, err) != KindIsolationConflict {
		t.Fatalf("分区1顺序冲突、分区2隔离冲突时整体应取严重度更低的隔离冲突, 实际 %v", err)
	}
}

func TestSameStopAcrossCompartments(t *testing.T) {
	sys, _ := New([]Compartment{
		{MaxWeight: 100, MaxVolume: 100},
		{MaxWeight: 100, MaxVolume: 100},
	})
	// 相同停靠点可跨任意分区（相等不违反严格不等式）。
	mustLoad(t, sys, Cargo{ID: 1, Weight: 60, Volume: 1, Stop: 2})
	mustLoad(t, sys, Cargo{ID: 2, Weight: 60, Volume: 1, Stop: 2})
	if k, _ := sys.Locate(2); k != 2 {
		t.Fatalf("第二件因载重应去分区2, 实际 %d", k)
	}
}

func TestSeverityMerge(t *testing.T) {
	// 场景：分区1 隔离冲突（易燃已在区），分区2 仅超容。整体报严重度最低的超容。
	sys2, _ := New([]Compartment{
		{MaxWeight: 1_000_000, MaxVolume: 1_000_000},
		{MaxWeight: 10, MaxVolume: 10},
	})
	// 让食品货物对分区1产生顺序冲突也可；这里用隔离冲突 + 分区2超容。
	mustLoad(t, sys2, Cargo{ID: 1, Weight: 1, Volume: 1, Stop: 1, Category: CategoryFlammable})
	_, err := sys2.Load(Cargo{ID: 2, Weight: 1, Volume: 20, Stop: 1, Category: CategoryFood})
	if got := rejectKind(t, err); got != KindOvervolume {
		t.Fatalf("分区1隔离冲突、分区2超容时应归并为超容, 实际 %v", got)
	}

	// 分区1顺序冲突（后方有更晚货），分区2超重 → 报超重（超重严重度更低）。
	sys3, _ := New([]Compartment{
		{MaxWeight: 1_000_000, MaxVolume: 1_000_000},
		{MaxWeight: 100, MaxVolume: 1_000_000},
	})
	// 用同停靠点货物把分区1载重占满，使 stop1 货物落入分区2。
	mustLoad(t, sys3, Cargo{ID: 1, Weight: 1_000_000, Volume: 1, Stop: 1})
	mustLoad(t, sys3, Cargo{ID: 2, Weight: 90, Volume: 1, Stop: 1})
	if k, _ := sys3.Locate(2); k != 2 {
		t.Fatalf("第二件早卸货物应在分区2, 实际 %d", k)
	}
	// stop5 货物：分区1 后方有 stop1 → 顺序冲突；分区2 超重 → 归并为超重。
	_, err = sys3.Load(Cargo{ID: 3, Weight: 20, Volume: 5, Stop: 5})
	if got := rejectKind(t, err); got != KindOverweight {
		t.Fatalf("分区1顺序冲突、分区2超重时整体应取超重, 实际 %v", got)
	}
}

func TestDuplicateAndInvalid(t *testing.T) {
	sys, _ := New([]Compartment{{MaxWeight: 100, MaxVolume: 100}})
	mustLoad(t, sys, Cargo{ID: 1, Weight: 1, Volume: 1, Stop: 1})

	_, err := sys.Load(Cargo{ID: 1, Weight: 1, Volume: 1, Stop: 1})
	if rejectKind(t, err) != KindDuplicateID {
		t.Fatalf("期望编号重复")
	}

	bad := []Cargo{
		{ID: 0, Weight: 1, Volume: 1, Stop: 1},
		{ID: 2, Weight: 0, Volume: 1, Stop: 1},
		{ID: 2, Weight: 1, Volume: 0, Stop: 1},
		{ID: 2, Weight: 1, Volume: 1, Stop: 0},
		{ID: 2, Weight: 1, Volume: 1, Stop: 1, Category: Category(99)},
	}
	for i, c := range bad {
		if _, err := sys.Load(c); rejectKind(t, err) != KindInvalidArgument {
			t.Fatalf("用例 %d 期望参数非法, 实际 %v", i, err)
		}
	}

	if _, err := New(nil); rejectKind(t, err) != KindInvalidArgument {
		t.Fatalf("空分区期望参数非法")
	}
	if _, err := New([]Compartment{{MaxWeight: 0, MaxVolume: 1}}); rejectKind(t, err) != KindInvalidArgument {
		t.Fatalf("零载重期望参数非法")
	}
}
