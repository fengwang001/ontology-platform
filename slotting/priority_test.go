package slotting

import "testing"

// 拒绝原因优先级：不存在 > 冻结 > 品类 > 净高 > 承重 > 混放 > 相邻 > 容量。
func TestRejectPriority(t *testing.T) {
	type tc struct {
		name   string
		setup  func(s *System)
		pid    string
		target LocationID
		want   Reason
	}
	cases := []tc{
		{"不存在的货位", func(s *System) {}, "p", LocationID{9, 9, 9}, ReasonLocationNotFound},
		{"冻结优先于品类", func(s *System) {
			mustLoc(t, s, locOf(LocationID{1, 1, 1}, 1, 1, 1, false, CatGeneral))
			if err := s.Freeze(LocationID{1, 1, 1}); err != nil {
				t.Fatal(err)
			}
		}, "food", LocationID{1, 1, 1}, ReasonFrozen},
		{"品类优先于净高", func(s *System) {
			mustLoc(t, s, locOf(LocationID{1, 1, 1}, 500, 5, 1, false, CatGeneral))
		}, "food", LocationID{1, 1, 1}, ReasonCategoryNotAllowed},
		{"净高优先于承重", func(s *System) {
			mustLoc(t, s, locOf(LocationID{1, 1, 1}, 1, 5, 1, false, CatGeneral))
		}, "p", LocationID{1, 1, 1}, ReasonHeightInsufficient},
		{"承重优先于容量", func(s *System) {
			mustLoc(t, s, locOf(LocationID{1, 1, 1}, 10, 500, 2, false, CatGeneral))
			mustPal(t, s, pallet("z", "P", "b", CatGeneral, 9, 10))
			if _, err := s.AutoPlace("z"); err != nil {
				t.Fatal(err)
			}
		}, "heavy", LocationID{1, 1, 1}, ReasonWeightInsufficient},
		{"容量为最后原因", func(s *System) {
			mustLoc(t, s, locOf(LocationID{1, 1, 1}, 500, 500, 1, false, CatGeneral))
			mustPal(t, s, pallet("z2", "P", "b", CatGeneral, 1, 1))
			if _, err := s.AutoPlace("z2"); err != nil {
				t.Fatal(err)
			}
		}, "p", LocationID{1, 1, 1}, ReasonCapacityFull},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewSystem(nil)
			mustPal(t, s, pallet("p", "P", "b", CatGeneral, 50, 10))
			mustPal(t, s, pallet("food", "F", "b", CatFood, 50, 10))
			mustPal(t, s, pallet("heavy", "P", "b", CatGeneral, 50, 10))
			c.setup(s)
			if got := reasonOf(s.PlaceTo(c.pid, c.target)); got != c.want {
				t.Fatalf("want %s got %s", c.want, got)
			}
		})
	}
}

// 相邻优先于容量：空货位本身容量充足，但因相邻冲突被拒。
func TestAdjacencyBeforeCapacity(t *testing.T) {
	s := NewSystem(nil)
	mustLoc(t, s, locOf(LocationID{1, 1, 1}, 500, 500, 1, true, CatFood, CatFlammable))
	mustLoc(t, s, locOf(LocationID{1, 1, 2}, 500, 500, 1, true, CatFood, CatFlammable))
	mustPal(t, s, pallet("fire", "F", "b", CatFlammable, 1, 1))
	mustPal(t, s, pallet("food", "G", "b", CatFood, 1, 1))
	if _, err := s.AutoPlace("fire"); err != nil {
		t.Fatal(err)
	}
	if err := s.PlaceTo("food", LocationID{1, 1, 2}); reasonOf(err) != ReasonAdjacencyConflict {
		t.Fatalf("相邻冲突应先于容量报出, got %v", err)
	}
}

// 自动上架次序：同商品有容量优先，其次最小空货位。
func TestAutoOrdering(t *testing.T) {
	s := NewSystem(nil)
	mustLoc(t, s, locOf(LocationID{1, 1, 1}, 500, 500, 1, false, CatGeneral))
	mustLoc(t, s, locOf(LocationID{2, 2, 2}, 500, 500, 2, false, CatGeneral))
	mustLoc(t, s, locOf(LocationID{3, 1, 1}, 500, 500, 1, false, CatGeneral))
	mustPal(t, s, pallet("a", "X", "b", CatGeneral, 1, 1))
	mustPal(t, s, pallet("b", "X", "b", CatGeneral, 1, 1))
	mustPal(t, s, pallet("c", "Y", "b", CatGeneral, 1, 1))
	if id, _ := s.AutoPlace("a"); id != (LocationID{1, 1, 1}) {
		t.Fatalf("首个托盘取最小空货位, got %s", id)
	}
	if err := s.Move("a", LocationID{2, 2, 2}); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.AutoPlace("b"); id != (LocationID{2, 2, 2}) {
		t.Fatalf("同商品剩余容量优先, got %s", id)
	}
	if id, _ := s.AutoPlace("c"); id != (LocationID{1, 1, 1}) {
		t.Fatalf("无同商品候选时取最小空货位, got %s", id)
	}
}

// 参数非法优先于一切。
func TestInvalidArgumentsFirst(t *testing.T) {
	s := NewSystem(nil)
	if reasonOf(s.AddLocation(locOf(LocationID{0, 1, 1}, 1, 1, 1, true, CatGeneral))) != ReasonInvalidArgument {
		t.Fatal("通道号非正应参数非法")
	}
	if reasonOf(s.AddLocation(locOf(LocationID{1, 1, 1}, 0, 1, 1, true, CatGeneral))) != ReasonInvalidArgument {
		t.Fatal("承重非正应参数非法")
	}
	if reasonOf(s.AddLocation(locOf(LocationID{1, 1, 1}, 1, 1, 3, true, CatGeneral))) != ReasonInvalidArgument {
		t.Fatal("容量必须 1 或 2")
	}
	if reasonOf(s.AddLocation(locOf(LocationID{1, 1, 1}, 1, 1, 1, true))) != ReasonInvalidArgument {
		t.Fatal("品类集合为空应参数非法")
	}
	if reasonOf(s.RegisterPallet(Pallet{ID: "x", Product: "P", Batch: "b", Category: CatGeneral, Weight: 0, Height: 1})) != ReasonInvalidArgument {
		t.Fatal("重量非正应参数非法")
	}
	if reasonOf(s.RegisterPallet(Pallet{ID: "x", Product: "P", Batch: "b", Category: "外星品类", Weight: 1, Height: 1})) != ReasonInvalidArgument {
		t.Fatal("未知品类应参数非法")
	}
	if _, err := s.BatchAutoPlace(nil); reasonOf(err) != ReasonInvalidArgument {
		t.Fatalf("空批量应参数非法, got %v", err)
	}
}
