package slotting

import "testing"

func TestSkylineSuffixSemantics(t *testing.T) {
	// 块内两个空货位：(h=60,w=60) 与 (h=100,w=1000)。
	// 查询 (H=50,W=500) 必须判定可行（后者满足），
	// 查询 (H=50,W=2000) 必须判定不可行。
	s := NewSystem(nil)
	mustLoc(t, s, Location{ID: LocationID{1, 1, 1}, MaxWeight: 60, ClearHeight: 60,
		Allowed: cats(CatGeneral), Capacity: 1})
	mustLoc(t, s, Location{ID: LocationID{1, 1, 2}, MaxWeight: 1000, ClearHeight: 100,
		Allowed: cats(CatGeneral), Capacity: 1})
	mustPal(t, s, Pallet{ID: "ok", Product: "P", Batch: "b", Category: CatGeneral, Weight: 500, Height: 50})
	id, err := s.AutoPlace("ok")
	if err != nil {
		t.Fatalf("后缀 skyline 误判不可行: %v", err)
	}
	if id != (LocationID{1, 1, 2}) {
		t.Fatalf("应选中 (1,1,2), got %v", id)
	}

	s2 := NewSystem(nil)
	mustLoc(t, s2, Location{ID: LocationID{1, 1, 1}, MaxWeight: 60, ClearHeight: 60,
		Allowed: cats(CatGeneral), Capacity: 1})
	mustPal(t, s2, Pallet{ID: "no", Product: "P", Batch: "b", Category: CatGeneral, Weight: 2000, Height: 50})
	if _, err := s2.AutoPlace("no"); reasonOf(err) != ReasonNoAvailableLocation {
		t.Fatalf("应无可用货位, got %v", err)
	}
}

func TestPlacedPalletMissingTargetPriority(t *testing.T) {
	// 已上架托盘 + 不存在的目标货位：对象不存在优先于重复。
	s := NewSystem(nil)
	mustLoc(t, s, Location{ID: LocationID{1, 1, 1}, MaxWeight: 1000, ClearHeight: 300,
		Allowed: cats(CatGeneral), Capacity: 1})
	mustPal(t, s, Pallet{ID: "dup", Product: "P", Batch: "b", Category: CatGeneral, Weight: 1, Height: 1})
	if _, err := s.AutoPlace("dup"); err != nil {
		t.Fatal(err)
	}
	if err := s.PlaceTo("dup", LocationID{9, 9, 9}); reasonOf(err) != ReasonLocationNotFound {
		t.Fatalf("对象不存在应优先于重复, got %v", err)
	}
}
