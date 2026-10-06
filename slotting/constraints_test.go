package slotting

import "testing"

// 承重与净高：恰等于允许，超一拒绝；被拒绝操作不改变状态。
func TestWeightHeightBoundary(t *testing.T) {
	s := NewSystem(nil)
	mustLoc(t, s, locOf(LocationID{1, 1, 1}, 100, 200, 1, false, CatGeneral))
	mustPal(t, s, pallet("p-eq", "P", "b", CatGeneral, 100, 200))
	mustPal(t, s, pallet("p-h", "P", "b", CatGeneral, 50, 201))
	mustPal(t, s, pallet("p-w", "P", "b", CatGeneral, 101, 10))

	if err := s.PlaceTo("p-eq", LocationID{1, 1, 1}); err != nil {
		t.Fatalf("恰等于上限应允许: %v", err)
	}
	s2 := NewSystem(nil)
	mustLoc(t, s2, locOf(LocationID{1, 1, 1}, 100, 200, 1, false, CatGeneral))
	mustPal(t, s2, pallet("h1", "P", "b", CatGeneral, 1, 201))
	if err := s2.PlaceTo("h1", LocationID{1, 1, 1}); reasonOf(err) != ReasonHeightInsufficient {
		t.Fatalf("高超 1 应报净高不足, got %v", err)
	}
	v, _ := s2.GetLocation(LocationID{1, 1, 1})
	if len(v.PalletIDs) != 0 || v.UsedWeight != 0 {
		t.Fatalf("被拒绝操作不得改变状态, got %+v", v)
	}
	mustPal(t, s2, pallet("w1", "P", "b", CatGeneral, 101, 10))
	if err := s2.PlaceTo("w1", LocationID{1, 1, 1}); reasonOf(err) != ReasonWeightInsufficient {
		t.Fatalf("重超 1 应报承重不足, got %v", err)
	}
	_ = s
}

// 容量 2 的货位合并存放。
func TestCapacityTwoMerging(t *testing.T) {
	s := NewSystem(nil)
	mustLoc(t, s, locOf(LocationID{2, 1, 3}, 500, 200, 2, false, CatGeneral))
	mustPal(t, s, pallet("a", "X", "b1", CatGeneral, 100, 50))
	mustPal(t, s, pallet("b", "X", "b1", CatGeneral, 200, 60))
	mustPal(t, s, pallet("c", "X", "b1", CatGeneral, 300, 60))
	if _, err := s.AutoPlace("a"); err != nil {
		t.Fatal(err)
	}
	id, err := s.AutoPlace("b")
	if err != nil || id != (LocationID{2, 1, 3}) {
		t.Fatalf("同商品应合并, id=%v err=%v", id, err)
	}
	v, _ := s.GetLocation(LocationID{2, 1, 3})
	if v.UsedCapacity != 2 || v.RemainCapacity != 0 || v.UsedWeight != 300 || v.RemainWeight != 200 {
		t.Fatalf("账目错误: %+v", v)
	}
	if err := s.PlaceTo("c", LocationID{2, 1, 3}); reasonOf(err) != ReasonWeightInsufficient {
		t.Fatalf("300+300>500 应报承重不足, got %v", err)
	}
}

// 不允许混批。
func TestNoMixBatch(t *testing.T) {
	s := NewSystem(nil)
	mustLoc(t, s, locOf(LocationID{1, 1, 1}, 500, 200, 2, false, CatGeneral))
	mustLoc(t, s, locOf(LocationID{1, 1, 2}, 500, 200, 2, true, CatGeneral))
	mustPal(t, s, pallet("a", "X", "b1", CatGeneral, 10, 10))
	mustPal(t, s, pallet("b", "X", "b2", CatGeneral, 10, 10))
	if _, err := s.AutoPlace("a"); err != nil {
		t.Fatal(err)
	}
	if err := s.PlaceTo("b", LocationID{1, 1, 1}); reasonOf(err) != ReasonMixConflict {
		t.Fatalf("不允许混批应报混放冲突, got %v", err)
	}
	if err := s.PlaceTo("b", LocationID{1, 1, 2}); err != nil {
		t.Fatalf("允许混批货位应接受: %v", err)
	}
}

// 相邻隔离双向检查。
func TestAdjacencyBothDirections(t *testing.T) {
	s := NewSystem(nil)
	for _, id := range []LocationID{{1, 1, 1}, {1, 1, 2}, {1, 1, 3}} {
		mustLoc(t, s, locOf(id, 500, 200, 1, true, CatGeneral, CatFood, CatFlammable))
	}
	mustPal(t, s, pallet("f", "F", "b", CatFlammable, 10, 10))
	mustPal(t, s, pallet("g1", "G", "b", CatFood, 10, 10))
	mustPal(t, s, pallet("g2", "G", "b", CatFood, 10, 10))
	if _, err := s.AutoPlace("f"); err != nil {
		t.Fatal(err)
	}
	if err := s.PlaceTo("g1", LocationID{1, 1, 2}); reasonOf(err) != ReasonAdjacencyConflict {
		t.Fatalf("食品不得放入易燃相邻, got %v", err)
	}
	if err := s.PlaceTo("g2", LocationID{1, 1, 3}); err != nil {
		t.Fatalf("位序号差 2 不相邻, got %v", err)
	}

	s2 := NewSystem(nil)
	for _, id := range []LocationID{{1, 1, 1}, {1, 1, 2}} {
		mustLoc(t, s2, locOf(id, 500, 200, 1, true, CatGeneral, CatFood, CatFlammable))
	}
	mustPal(t, s2, pallet("food", "G", "b", CatFood, 10, 10))
	mustPal(t, s2, pallet("fire", "F", "b", CatFlammable, 10, 10))
	if _, err := s2.AutoPlace("food"); err != nil {
		t.Fatal(err)
	}
	if err := s2.PlaceTo("fire", LocationID{1, 1, 2}); reasonOf(err) != ReasonAdjacencyConflict {
		t.Fatalf("易燃不得放入食品相邻, got %v", err)
	}
}

// 冻结货位：不可放入，可移出；其中托盘持续约束相邻。
func TestFrozenAdjacencyAndMoveOut(t *testing.T) {
	s := NewSystem(nil)
	mustLoc(t, s, locOf(LocationID{1, 1, 1}, 500, 200, 1, true, CatFlammable))
	mustLoc(t, s, locOf(LocationID{1, 1, 2}, 500, 200, 1, true, CatFood))
	mustLoc(t, s, locOf(LocationID{2, 1, 2}, 500, 200, 1, true, CatFood))
	mustLoc(t, s, locOf(LocationID{3, 1, 1}, 500, 200, 1, true, CatFlammable))
	mustPal(t, s, pallet("fire", "F", "b", CatFlammable, 10, 10))
	mustPal(t, s, pallet("food", "G", "b", CatFood, 10, 10))
	mustPal(t, s, pallet("food2", "G", "b", CatFood, 10, 10))
	if _, err := s.AutoPlace("fire"); err != nil {
		t.Fatal(err)
	}
	if err := s.Freeze(LocationID{1, 1, 1}); err != nil {
		t.Fatal(err)
	}
	id, err := s.AutoPlace("food")
	if err != nil {
		t.Fatalf("应跳过冻结货位选其他食品货位: %v", err)
	}
	if id != (LocationID{2, 1, 2}) {
		t.Fatalf("got %s", id)
	}
	// (1,1,2) 仍被冻结货位内的易燃托盘隔离。
	if err := s.PlaceTo("food2", LocationID{1, 1, 2}); reasonOf(err) != ReasonAdjacencyConflict {
		t.Fatalf("冻结货位内托盘应持续约束相邻, got %v", err)
	}
	// 冻结货位中的托盘可以移出。
	if err := s.Move("fire", LocationID{3, 1, 1}); err != nil {
		t.Fatalf("冻结货位中的托盘应可移出: %v", err)
	}
	if err := s.PlaceTo("food2", LocationID{1, 1, 2}); err != nil {
		t.Fatalf("易燃移走后食品可入 (1,1,2): %v", err)
	}
	// 解冻后空货位恢复参与自动上架。
	if err := s.Unfreeze(LocationID{1, 1, 1}); err != nil {
		t.Fatal(err)
	}
}

// 移库失败两侧不变。
func TestMoveFailureAtomicity(t *testing.T) {
	s := NewSystem(nil)
	mustLoc(t, s, locOf(LocationID{1, 1, 1}, 100, 200, 1, false, CatGeneral))
	mustLoc(t, s, locOf(LocationID{1, 1, 2}, 100, 200, 1, false, CatGeneral))
	mustLoc(t, s, locOf(LocationID{1, 1, 3}, 100, 200, 1, false, CatGeneral))
	mustPal(t, s, pallet("heavy", "P", "b", CatGeneral, 60, 10))
	mustPal(t, s, pallet("q", "Q", "b", CatGeneral, 90, 10))
	if _, err := s.AutoPlace("heavy"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AutoPlace("q"); err != nil {
		t.Fatal(err)
	}
	err := s.Move("heavy", LocationID{1, 1, 2})
	if reasonOf(err) != ReasonWeightInsufficient {
		t.Fatalf("应承重不足, got %v", err)
	}
	if id, _ := s.PalletLocation("heavy"); id != (LocationID{1, 1, 1}) {
		t.Fatalf("移库失败后原货位改变: %s", id)
	}
	v, _ := s.GetLocation(LocationID{1, 1, 1})
	if len(v.PalletIDs) != 1 || v.PalletIDs[0] != "heavy" || v.UsedWeight != 60 {
		t.Fatalf("原货位账目被破坏: %+v", v)
	}
	v2, _ := s.GetLocation(LocationID{1, 1, 2})
	if len(v2.PalletIDs) != 1 || v2.PalletIDs[0] != "q" {
		t.Fatalf("目标货位账目被破坏: %+v", v2)
	}
	if err := s.Move("heavy", LocationID{1, 1, 1}); reasonOf(err) != ReasonSelfLocation {
		t.Fatalf("应报目标即原位, got %v", err)
	}
	if err := s.Move("heavy", LocationID{1, 1, 3}); err != nil {
		t.Fatalf("移库成功路径: %v", err)
	}
	if id, _ := s.PalletLocation("heavy"); id != (LocationID{1, 1, 3}) {
		t.Fatalf("移库结果错误: %s", id)
	}
}
