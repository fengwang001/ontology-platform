package slotting

import (
	"errors"
	"testing"
)

func TestRejectPriority(t *testing.T) {
	// 容量2、限普通、承重10、净高5、不混批；先放 G/B1 重9。
	svc := newSvcWith(
		locCfg(2, 1, 1, 10, 5, true, cats(CategoryNormal), false),
		locCfg(2, 1, 2, 10, 5, true, cats(CategoryNormal), false),
	)
	if err := svc.PutawayTo(pallet("X1", "G", "B1", CategoryNormal, 9, 5), Coord{2, 1, 1}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutawayTo(pallet("Q0", "G", "B1", CategoryNormal, 1, 1), Coord{9, 9, 9}); !errors.Is(err, ErrLocationAbsent) {
		t.Fatalf("absent: got %v", err)
	}
	// 冻结优先于品类等其余原因。
	if err := svc.Freeze(Coord{2, 1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutawayTo(pallet("Qf", "G", "B1", CategoryFood, 99, 99), Coord{2, 1, 2}); !errors.Is(err, ErrFrozen) {
		t.Fatalf("frozen first: got %v", err)
	}
	// 品类优先于高度、重量。
	if err := svc.PutawayTo(pallet("Q1", "G", "B1", CategoryFood, 99, 99), Coord{2, 1, 1}); !errors.Is(err, ErrCategoryDenied) {
		t.Fatalf("category first: got %v", err)
	}
	// 高度优先于重量。
	if err := svc.PutawayTo(pallet("Q2", "G", "B1", CategoryNormal, 99, 99), Coord{2, 1, 1}); !errors.Is(err, ErrHeight) {
		t.Fatalf("height>weight: got %v", err)
	}
	// 重量优先于混放。
	if err := svc.PutawayTo(pallet("Q3", "H", "B1", CategoryNormal, 99, 1), Coord{2, 1, 1}); !errors.Is(err, ErrWeight) {
		t.Fatalf("weight>mix: got %v", err)
	}
	// 混放（异商品、其余通过）。
	if err := svc.PutawayTo(pallet("Q4", "H", "B1", CategoryNormal, 1, 1), Coord{2, 1, 1}); !errors.Is(err, ErrMixConflict) {
		t.Fatalf("mix: got %v", err)
	}
	// 同商品同批次第二个 -> 占满。
	if err := svc.PutawayTo(pallet("Q5", "G", "B1", CategoryNormal, 1, 1), Coord{2, 1, 1}); err != nil {
		t.Fatalf("second slot: %v", err)
	}
	// 相邻隔离优先于容量。
	all := cats(CategoryFood, CategoryFlammable)
	svc2 := newSvcWith(
		locCfg(3, 1, 1, 500, 200, true, all, true),
		locCfg(3, 1, 2, 500, 200, false, cats(CategoryFood), true),
	)
	if err := svc2.PutawayTo(pallet("A1", "G", "B", CategoryFood, 10, 10), Coord{3, 1, 1}); err != nil {
		t.Fatal(err)
	}
	if err := svc2.PutawayTo(pallet("A2", "G", "B", CategoryFood, 10, 10), Coord{3, 1, 1}); err != nil {
		t.Fatal(err)
	}
	if err := svc2.PutawayTo(pallet("E2", "Eat", "B", CategoryFood, 10, 10), Coord{3, 1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := svc2.PutawayTo(pallet("Z1", "G", "B2", CategoryFlammable, 10, 10), Coord{3, 1, 1}); !errors.Is(err, ErrAdjacency) {
		t.Fatalf("adjacency>capacity: got %v", err)
	}
	// 满员且承重仍富余、无其他冲突 -> 容量已满。
	if err := svc.Store().AddLocation(locCfg(4, 1, 1, 500, 200, true, cats(CategoryNormal), false)); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutawayTo(pallet("C1", "K", "B", CategoryNormal, 10, 10), Coord{4, 1, 1}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutawayTo(pallet("C2", "K", "B", CategoryNormal, 10, 10), Coord{4, 1, 1}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutawayTo(pallet("C3", "K", "B", CategoryNormal, 10, 10), Coord{4, 1, 1}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity: got %v", err)
	}
}

func TestMoveSuccessAndFailure(t *testing.T) {
	all := cats(CategoryNormal, CategoryFood, CategoryFlammable)
	svc := newSvcWith(
		locCfg(1, 1, 1, 500, 200, false, all, true),
		locCfg(1, 1, 2, 500, 200, false, cats(CategoryNormal), true),
	)
	if err := svc.PutawayTo(pallet("P1", "G", "B", CategoryNormal, 10, 10), Coord{1, 1, 1}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Move("P1", Coord{1, 1, 1}); !errors.Is(err, ErrSameLocation) {
		t.Fatalf("same location: got %v", err)
	}
	if err := svc.Move("Nope", Coord{1, 1, 2}); !errors.Is(err, ErrPalletMissing) {
		t.Fatalf("missing: got %v", err)
	}
	if err := svc.Move("P1", Coord{1, 1, 2}); err != nil {
		t.Fatalf("move: %v", err)
	}
	if c, _ := svc.PalletLocation("P1"); c != (Coord{1, 1, 2}) {
		t.Fatalf("pallet at %v want P2", c)
	}
	v1, _ := svc.Location(Coord{1, 1, 1})
	if v1.Occupied != 0 {
		t.Fatalf("source not released: %d", v1.Occupied)
	}

	// 移库失败两侧不变：易燃托盘 FL 在位3，食品 FD 在位1，
	// 把 FL 移到位2（与位1相邻）应被隔离拒绝。
	svc2 := newSvcWith(
		locCfg(2, 1, 1, 500, 200, false, cats(CategoryFood, CategoryNormal), true),
		locCfg(2, 1, 2, 500, 200, false, all, true),
	)
	if err := svc2.Store().AddLocation(locCfg(2, 1, 3, 500, 200, false, all, true)); err != nil {
		t.Fatal(err)
	}
	if err := svc2.PutawayTo(pallet("FD", "Eat", "B", CategoryFood, 10, 10), Coord{2, 1, 1}); err != nil {
		t.Fatal(err)
	}
	// FL 直接落位3：其邻居位2为空，不违反隔离。
	if err := svc2.PutawayTo(pallet("FL", "Oil", "B", CategoryFlammable, 10, 10), Coord{2, 1, 3}); err != nil {
		t.Fatal(err)
	}
	err := svc2.Move("FL", Coord{2, 1, 2})
	if !errors.Is(err, ErrAdjacency) {
		t.Fatalf("want adjacency, got %v", err)
	}
	if c, _ := svc2.PalletLocation("FL"); c != (Coord{2, 1, 3}) {
		t.Fatalf("failed move changed source: %v", c)
	}
	v, _ := svc2.Location(Coord{2, 1, 2})
	if v.Occupied != 0 {
		t.Fatalf("failed move left pallet at target: %v", v.PalletIDs)
	}
}

func TestDuplicateAndRetrieve(t *testing.T) {
	svc := newSvcWith(locCfg(1, 1, 1, 500, 200, false, cats(CategoryNormal), true))
	p := pallet("P1", "G", "B", CategoryNormal, 10, 10)
	if _, err := svc.AutoPutaway(p); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AutoPutaway(p); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := svc.Retrieve("P1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Retrieve("P1"); !errors.Is(err, ErrPalletMissing) {
		t.Fatalf("missing after retrieve: %v", err)
	}
	if _, err := svc.AutoPutaway(p); err != nil {
		t.Fatalf("re-putaway: %v", err)
	}
}

func TestBatchAllOrNothing(t *testing.T) {
	svc := newSvcWith(
		locCfg(1, 1, 1, 100, 200, false, cats(CategoryNormal), true),
		locCfg(1, 1, 2, 100, 200, false, cats(CategoryNormal), true),
	)
	batch := []Pallet{
		pallet("B1", "G", "X", CategoryNormal, 10, 10),
		pallet("B2", "H", "X", CategoryNormal, 10, 10),
		pallet("B3", "K", "X", CategoryNormal, 999, 10),
	}
	_, err := svc.BatchPutaway(batch)
	var be *BatchError
	if !errors.As(err, &be) || be.Index != 2 || !errors.Is(be.Err, ErrNoLocation) {
		t.Fatalf("want batch index=2 no_location, got %v", err)
	}
	for _, id := range []string{"B1", "B2", "B3"} {
		if _, err := svc.PalletLocation(id); !errors.Is(err, ErrPalletMissing) {
			t.Fatalf("pallet %s leaked after failed batch", id)
		}
	}
	for _, c := range []Coord{{1, 1, 1}, {1, 1, 2}} {
		v, _ := svc.Location(c)
		if v.Occupied != 0 {
			t.Fatalf("location %v not empty after failed batch: %d", c, v.Occupied)
		}
	}
	res, err := svc.BatchPutaway(batch[:2])
	if err != nil {
		t.Fatal(err)
	}
	if res["B1"] != (Coord{1, 1, 1}) || res["B2"] != (Coord{1, 1, 2}) {
		t.Fatalf("batch allocation order wrong: %v", res)
	}
	dup := []Pallet{
		pallet("D1", "G", "X", CategoryNormal, 10, 10),
		pallet("D1", "G", "X", CategoryNormal, 10, 10),
	}
	_, err = svc.BatchPutaway(dup)
	if !errors.As(err, &be) || !errors.Is(be.Err, ErrInvalidParam) {
		t.Fatalf("duplicate in batch: %v", err)
	}
	if _, err := svc.PalletLocation("D1"); !errors.Is(err, ErrPalletMissing) {
		t.Fatalf("invalid batch must leave no trace")
	}
}

func TestInvalidParams(t *testing.T) {
	svc := newSvcWith()
	if err := svc.Store().AddLocation(locCfg(0, 1, 1, 10, 10, false, cats(CategoryNormal), true)); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("zero aisle: %v", err)
	}
	if _, err := svc.AutoPutaway(pallet("", "G", "B", CategoryNormal, 1, 1)); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("empty id: %v", err)
	}
	if _, err := svc.AutoPutaway(pallet("P", "G", "B", CategoryNormal, 0, 1)); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("zero weight: %v", err)
	}
	if err := svc.PutawayTo(pallet("P", "G", "B", CategoryNormal, 1, 1), Coord{0, 1, 1}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("zero target: %v", err)
	}
}

func TestQueries(t *testing.T) {
	svc := newSvcWith(
		locCfg(1, 1, 1, 100, 200, true, cats(CategoryNormal), true),
		locCfg(2, 1, 1, 100, 200, false, cats(CategoryNormal), true),
	)
	mustCoord(t, svc, pallet("P1", "G", "B", CategoryNormal, 30, 10))
	mustCoord(t, svc, pallet("P2", "G", "B", CategoryNormal, 40, 10))
	mustCoord(t, svc, pallet("P3", "H", "B", CategoryNormal, 10, 10))

	v, err := svc.Location(Coord{1, 1, 1})
	if err != nil || v.Occupied != 2 || v.RemainingWeight != 30 {
		t.Fatalf("location view: %+v err=%v", v, err)
	}
	if cs := svc.ProductLocations("G"); len(cs) != 1 || cs[0] != (Coord{1, 1, 1}) {
		t.Fatalf("product G locations: %v", cs)
	}
	if err := svc.Freeze(Coord{1, 1, 1}); err != nil {
		t.Fatal(err)
	}
	if cs := svc.ProductLocations("G"); len(cs) != 1 || cs[0] != (Coord{1, 1, 1}) {
		t.Fatalf("frozen loc missing from product query: %v", cs)
	}
	if _, err := svc.Location(Coord{9, 9, 9}); !errors.Is(err, ErrLocationAbsent) {
		t.Fatalf("query absent: %v", err)
	}
}

func TestAutoSelectOrder(t *testing.T) {
	// 同商品合并优先于空货位；字典序最小。
	svc := newSvcWith(
		locCfg(1, 1, 1, 500, 200, true, cats(CategoryNormal), true),
		locCfg(1, 1, 2, 500, 200, true, cats(CategoryNormal), true),
		locCfg(1, 2, 1, 500, 200, true, cats(CategoryNormal), true),
	)
	// 先把 G 放到 (1,1,2)：用指定上架。
	if err := svc.PutawayTo(pallet("G1", "G", "B", CategoryNormal, 10, 10), Coord{1, 1, 2}); err != nil {
		t.Fatal(err)
	}
	c := mustCoord(t, svc, pallet("G2", "G", "B", CategoryNormal, 10, 10))
	if c != (Coord{1, 1, 2}) {
		t.Fatalf("same-product merge preferred over empties: %v", c)
	}
	// 没有同商品候选时取空货位字典序最小 (1,1,1)。
	c = mustCoord(t, svc, pallet("H1", "H", "B", CategoryNormal, 10, 10))
	if c != (Coord{1, 1, 1}) {
		t.Fatalf("empty lexicographic min: %v", c)
	}
	c = mustCoord(t, svc, pallet("H2", "H", "B", CategoryNormal, 10, 10))
	if c != (Coord{1, 1, 1}) {
		t.Fatalf("merge again: %v", c)
	}
	c = mustCoord(t, svc, pallet("K1", "K", "B", CategoryNormal, 10, 10))
	if c != (Coord{1, 2, 1}) {
		t.Fatalf("next empty: %v", c)
	}
}

func TestBatchSeesEarlierPallets(t *testing.T) {
	// 容量2、同商品同批次的两个托盘应在同一批中合并进同一货位。
	svc := newSvcWith(
		locCfg(1, 1, 1, 500, 200, true, cats(CategoryNormal), false),
		locCfg(2, 1, 1, 500, 200, false, cats(CategoryNormal), false),
	)
	res, err := svc.BatchPutaway([]Pallet{
		pallet("S1", "G", "B1", CategoryNormal, 10, 10),
		pallet("S2", "G", "B1", CategoryNormal, 10, 10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res["S1"] != (Coord{1, 1, 1}) || res["S2"] != (Coord{1, 1, 1}) {
		t.Fatalf("batch pallets should merge in same loc: %v", res)
	}

	// 批量中第三个托盘无货位 -> 下标 2；同批次第二个托盘不同批次先违规时，
	// 失败下标为 1，且整批回滚。
	svc2 := newSvcWith(locCfg(1, 1, 1, 500, 200, true, cats(CategoryNormal), false))
	_, err = svc2.BatchPutaway([]Pallet{
		pallet("S1", "G", "B1", CategoryNormal, 10, 10),
		pallet("S2", "G", "B2", CategoryNormal, 10, 10),
		pallet("S3", "G", "B1", CategoryNormal, 10, 10),
	})
	var be *BatchError
	if !errors.As(err, &be) || be.Index != 1 {
		t.Fatalf("want first failing index 1, got %v", err)
	}
	for _, id := range []string{"S1", "S2", "S3"} {
		if _, err := svc2.PalletLocation(id); !errors.Is(err, ErrPalletMissing) {
			t.Fatalf("pallet %s leaked", id)
		}
	}
}
