package slotting

import (
	"errors"
	"testing"
)

func mustCoord(t *testing.T, svc *Service, p Pallet) Coord {
	t.Helper()
	c, err := svc.AutoPutaway(p)
	if err != nil {
		t.Fatalf("AutoPutaway(%s): %v", p.ID, err)
	}
	return c
}

func TestWeightBoundaryEqualAndOver(t *testing.T) {
	svc := newSvcWith(locCfg(1, 1, 1, 100, 500, false, cats(CategoryNormal), true))
	if err := svc.PutawayTo(pallet("P1", "G", "B", CategoryNormal, 100, 10), Coord{1, 1, 1}); err != nil {
		t.Fatalf("equal weight should pass: %v", err)
	}
	svc2 := newSvcWith(locCfg(1, 1, 1, 100, 500, false, cats(CategoryNormal), true))
	err := svc2.PutawayTo(pallet("P1", "G", "B", CategoryNormal, 101, 10), Coord{1, 1, 1})
	if !errors.Is(err, ErrWeight) {
		t.Fatalf("want weight_exceeded, got %v", err)
	}
}

func TestHeightBoundaryEqualAndOver(t *testing.T) {
	svc := newSvcWith(locCfg(1, 1, 1, 500, 200, false, cats(CategoryNormal), true))
	if err := svc.PutawayTo(pallet("P1", "G", "B", CategoryNormal, 10, 200), Coord{1, 1, 1}); err != nil {
		t.Fatalf("equal height should pass: %v", err)
	}
	svc2 := newSvcWith(locCfg(1, 1, 1, 500, 200, false, cats(CategoryNormal), true))
	err := svc2.PutawayTo(pallet("P1", "G", "B", CategoryNormal, 10, 201), Coord{1, 1, 1})
	if !errors.Is(err, ErrHeight) {
		t.Fatalf("want height_exceeded, got %v", err)
	}
}

func TestCapacityTwoMergeAndFull(t *testing.T) {
	svc := newSvcWith(locCfg(1, 1, 1, 500, 200, true, cats(CategoryNormal), true))
	c1 := mustCoord(t, svc, pallet("P1", "G", "B1", CategoryNormal, 50, 10))
	c2 := mustCoord(t, svc, pallet("P2", "G", "B2", CategoryNormal, 50, 10))
	if c1 != c2 {
		t.Fatalf("same product should merge, got %v %v", c1, c2)
	}
	if _, err := svc.AutoPutaway(pallet("P3", "G", "B3", CategoryNormal, 50, 10)); !errors.Is(err, ErrNoLocation) {
		t.Fatalf("want no_location, got %v", err)
	}
	if err := svc.PutawayTo(pallet("P4", "G", "B4", CategoryNormal, 50, 10), c1); !errors.Is(err, ErrCapacity) {
		t.Fatalf("want capacity_full, got %v", err)
	}
}

func TestMixedBatchForbidden(t *testing.T) {
	svc := newSvcWith(locCfg(1, 1, 1, 500, 200, true, cats(CategoryNormal), false))
	c := mustCoord(t, svc, pallet("P1", "G", "B1", CategoryNormal, 50, 10))
	if err := svc.PutawayTo(pallet("P2", "G", "B2", CategoryNormal, 50, 10), c); !errors.Is(err, ErrMixConflict) {
		t.Fatalf("want mix_conflict, got %v", err)
	}
	if err := svc.PutawayTo(pallet("P3", "G", "B1", CategoryNormal, 50, 10), c); err != nil {
		t.Fatalf("same batch should pass: %v", err)
	}
}

func TestDifferentProductMixConflict(t *testing.T) {
	svc := newSvcWith(locCfg(1, 1, 1, 500, 200, true, cats(CategoryNormal), true))
	c := mustCoord(t, svc, pallet("P1", "G1", "B1", CategoryNormal, 50, 10))
	if err := svc.PutawayTo(pallet("P2", "G2", "B1", CategoryNormal, 50, 10), c); !errors.Is(err, ErrMixConflict) {
		t.Fatalf("want mix_conflict for different product, got %v", err)
	}
}

func TestAdjacencyBidirectional(t *testing.T) {
	all := cats(CategoryFood, CategoryFlammable, CategoryNormal)
	svc := newSvcWith(
		locCfg(1, 1, 1, 500, 200, false, all, true),
		locCfg(1, 1, 2, 500, 200, false, all, true),
	)
	if err := svc.PutawayTo(pallet("F1", "Fuel", "B", CategoryFlammable, 10, 10), Coord{1, 1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutawayTo(pallet("E1", "Eat", "B", CategoryFood, 10, 10), Coord{1, 1, 1}); !errors.Is(err, ErrAdjacency) {
		t.Fatalf("food next to flammable: want adjacency, got %v", err)
	}

	svc2 := newSvcWith(
		locCfg(1, 1, 1, 500, 200, false, all, true),
		locCfg(1, 1, 2, 500, 200, false, all, true),
	)
	if err := svc2.PutawayTo(pallet("E1", "Eat", "B", CategoryFood, 10, 10), Coord{1, 1, 1}); err != nil {
		t.Fatal(err)
	}
	if err := svc2.PutawayTo(pallet("F1", "Fuel", "B", CategoryFlammable, 10, 10), Coord{1, 1, 2}); !errors.Is(err, ErrAdjacency) {
		t.Fatalf("flammable next to food: want adjacency, got %v", err)
	}

	svc3 := newSvcWith(
		locCfg(1, 1, 1, 500, 200, false, all, true),
		locCfg(1, 2, 2, 500, 200, false, all, true),
	)
	if err := svc3.PutawayTo(pallet("E1", "Eat", "B", CategoryFood, 10, 10), Coord{1, 1, 1}); err != nil {
		t.Fatal(err)
	}
	if err := svc3.PutawayTo(pallet("F1", "Fuel", "B", CategoryFlammable, 10, 10), Coord{1, 2, 2}); err != nil {
		t.Fatalf("different level should be allowed: %v", err)
	}
}

func TestFrozenLocation(t *testing.T) {
	svc := newSvcWith(
		locCfg(1, 1, 1, 500, 200, false, cats(CategoryNormal), true),
		locCfg(1, 1, 2, 500, 200, false, cats(CategoryNormal), true),
	)
	if err := svc.Freeze(Coord{1, 1, 1}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutawayTo(pallet("P1", "G", "B", CategoryNormal, 10, 10), Coord{1, 1, 1}); !errors.Is(err, ErrFrozen) {
		t.Fatalf("want frozen, got %v", err)
	}
	c := mustCoord(t, svc, pallet("P1", "G", "B", CategoryNormal, 10, 10))
	if c != (Coord{1, 1, 2}) {
		t.Fatalf("frozen loc skipped, got %v", c)
	}
	if err := svc.Unfreeze(Coord{1, 1, 1}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutawayTo(pallet("P2", "H", "B", CategoryNormal, 10, 10), Coord{1, 1, 1}); err != nil {
		t.Fatalf("unfrozen loc should accept: %v", err)
	}
}

func TestFrozenKeepsPalletAndAdjacency(t *testing.T) {
	all := cats(CategoryFood, CategoryFlammable, CategoryNormal)
	svc := newSvcWith(
		locCfg(1, 1, 1, 500, 200, false, all, true),
		locCfg(1, 1, 2, 500, 200, false, all, true),
	)
	if err := svc.PutawayTo(pallet("F1", "Fuel", "B", CategoryFlammable, 10, 10), Coord{1, 1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Freeze(Coord{1, 1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutawayTo(pallet("E1", "Eat", "B", CategoryFood, 10, 10), Coord{1, 1, 1}); !errors.Is(err, ErrAdjacency) {
		t.Fatalf("frozen flammable should still block food: %v", err)
	}
	if err := svc.Retrieve("F1"); err != nil {
		t.Fatalf("retrieve from frozen: %v", err)
	}
	if err := svc.PutawayTo(pallet("E1", "Eat", "B", CategoryFood, 10, 10), Coord{1, 1, 1}); err != nil {
		t.Fatalf("after remove adjacency clears: %v", err)
	}
}
