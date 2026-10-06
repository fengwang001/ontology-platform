package compaction

import (
	"errors"
	"math/big"
	"slices"
	"testing"
)

func testService(t *testing.T, layers int, trigger, target, multiplier uint64) *Service {
	t.Helper()
	service, err := NewService(Config{Layers: layers, ZeroTrigger: trigger, FirstNonZeroTarget: target, TargetMultiplier: multiplier}, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func register(t *testing.T, service *Service, files ...File) {
	t.Helper()
	for _, file := range files {
		if err := service.RegisterFile(file); err != nil {
			t.Fatalf("RegisterFile(%+v) error = %v", file, err)
		}
	}
}

func TestScoreBoundaryAndTieSelectsLowerLayer(t *testing.T) {
	service := testService(t, 3, 2, 10, 2)
	register(t, service,
		File{ID: 1, Layer: 0, MinKey: []byte("a"), MaxKey: []byte("a"), Bytes: 1},
		File{ID: 2, Layer: 0, MinKey: []byte("z"), MaxKey: []byte("z"), Bytes: 1},
		File{ID: 3, Layer: 1, MinKey: []byte("z"), MaxKey: []byte("z"), Bytes: 10},
	)
	result := service.CreatePlan()
	if result.Plan == nil || result.Plan.SourceLayer != 0 {
		t.Fatalf("expected tie to select layer 0, got %+v", result.Plan)
	}
	if got := result.RankedScores[0].Score.Cmp(big.NewRat(1, 1)); got != 0 {
		t.Fatalf("score = %s, want exactly 1", result.RankedScores[0].Score.RatString())
	}
	if err := service.CancelPlan(result.Plan.ID); err != nil {
		t.Fatalf("CancelPlan() error = %v", err)
	}

	undersized := testService(t, 2, 100, 100, 2)
	register(t, undersized, File{ID: 10, Layer: 0, MinKey: []byte{1}, MaxKey: []byte{1}, Bytes: 1})
	undersizedResult := undersized.CreatePlan()
	if undersizedResult.Plan != nil {
		t.Fatalf("score below one created plan %+v", undersizedResult.Plan)
	}
}

func TestZeroLayerIncludesEqualEndpoint(t *testing.T) {
	service := testService(t, 2, 1, 10, 2)
	register(t, service,
		File{ID: 2, Layer: 0, MinKey: []byte("c"), MaxKey: []byte("e"), Bytes: 1},
		File{ID: 3, Layer: 0, MinKey: []byte("e"), MaxKey: []byte("g"), Bytes: 1},
		File{ID: 1, Layer: 0, MinKey: []byte("a"), MaxKey: []byte("c"), Bytes: 1},
	)
	result := service.CreatePlan()
	if result.Plan == nil {
		t.Fatalf("CreatePlan() = %+v", result)
	}
	if got := result.Plan.InputIDs; !slices.Equal(got, []uint64{1, 2, 3}) {
		t.Fatalf("InputIDs = %v, want [1 2 3]", got)
	}
}

func TestOccupiedCandidateFallsBack(t *testing.T) {
	service := testService(t, 3, 2, 10, 10)
	register(t, service,
		File{ID: 1, Layer: 0, MinKey: []byte("a"), MaxKey: []byte("a"), Bytes: 1},
		File{ID: 2, Layer: 0, MinKey: []byte("a"), MaxKey: []byte("a"), Bytes: 1},
		File{ID: 3, Layer: 1, MinKey: []byte("z"), MaxKey: []byte("z"), Bytes: 10},
	)
	first := service.CreatePlan()
	if first.Plan == nil || first.Plan.SourceLayer != 0 {
		t.Fatalf("first plan = %+v", first.Plan)
	}
	second := service.CreatePlan()
	if second.Plan == nil || second.Plan.SourceLayer != 1 {
		t.Fatalf("second plan = %+v, want layer 1 fallback", second.Plan)
	}
	third := service.CreatePlan()
	if third.Plan != nil || third.Skips[0] != SkipOccupied || third.Skips[1] != SkipOccupied {
		t.Fatalf("third result = %+v", third)
	}
}

func TestErrorsKeepPriority(t *testing.T) {
	service := testService(t, 2, 1, 10, 2)
	if !errors.Is(service.InstallPlan(0, nil), ErrInvalidArgument) {
		t.Fatal("missing plan id must be invalid argument")
	}
	if !errors.Is(service.InstallPlan(1, nil), ErrInvalidArgument) {
		t.Fatal("missing outputs must precede missing plan")
	}
	if !errors.Is(service.CancelPlan(99), ErrPlanNotFound) {
		t.Fatal("unknown cancel must return plan not found")
	}

	occupied := testService(t, 2, 1, 10, 2)
	register(t, occupied, File{ID: 1, Layer: 0, MinKey: []byte("a"), MaxKey: []byte("b"), Bytes: 1})
	plan := occupied.CreatePlan()
	if plan.Plan == nil {
		t.Fatalf("CreatePlan() = %+v", plan)
	}
	output := File{ID: 1, Layer: 1, MinKey: []byte("a"), MaxKey: []byte("b"), Bytes: 1}
	if err := occupied.InstallPlan(999, []File{output}); !errors.Is(err, ErrFileOccupied) {
		t.Fatalf("InstallPlan(unknown, occupied output) error = %v, want occupied", err)
	}

	other := testService(t, 3, 1, 10, 2)
	register(t, other,
		File{ID: 11, Layer: 0, MinKey: []byte("a"), MaxKey: []byte("b"), Bytes: 1},
		File{ID: 21, Layer: 1, MinKey: []byte("z"), MaxKey: []byte("z"), Bytes: 10},
	)
	otherPlan := other.CreatePlan()
	if otherPlan.Plan == nil {
		t.Fatalf("other CreatePlan() = %+v", otherPlan)
	}
	invalidButOccupied := File{ID: 11, Layer: 2, MinKey: []byte("a"), MaxKey: []byte("b"), Bytes: 1}
	if err := other.InstallPlan(otherPlan.Plan.ID, []File{invalidButOccupied}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("InstallPlan(existing, invalid and occupied) error = %v, want invalid argument", err)
	}
}

func TestMoveDownVersusEndpointTouch(t *testing.T) {
	service := testService(t, 3, 100, 1, 10)
	register(t, service, File{ID: 1, Layer: 1, MinKey: []byte("a"), MaxKey: []byte("b"), Bytes: 5})
	plan := service.CreatePlan()
	if plan.Plan == nil || plan.Plan.Type != PlanMoveDown {
		t.Fatalf("plan = %+v, want direct move-down", plan.Plan)
	}

	touching := testService(t, 3, 100, 1, 10)
	register(t, touching,
		File{ID: 1, Layer: 1, MinKey: []byte("a"), MaxKey: []byte("b"), Bytes: 5},
		File{ID: 2, Layer: 2, MinKey: []byte("b"), MaxKey: []byte("c"), Bytes: 1},
	)
	touchPlan := touching.CreatePlan()
	if touchPlan.Plan == nil || touchPlan.Plan.Type != PlanRewrite || !slices.Equal(touchPlan.Plan.InputIDs, []uint64{1, 2}) {
		t.Fatalf("touch plan = %+v", touchPlan.Plan)
	}
}

func TestFailedInstallLeavesStateAndOccupancy(t *testing.T) {
	service := testService(t, 2, 1, 10, 2)
	register(t, service,
		File{ID: 1, Layer: 0, MinKey: []byte("a"), MaxKey: []byte("b"), Bytes: 1},
		File{ID: 3, Layer: 0, MinKey: []byte("a"), MaxKey: []byte("b"), Bytes: 1},
		File{ID: 2, Layer: 1, MinKey: []byte("b"), MaxKey: []byte("c"), Bytes: 1},
		File{ID: 4, Layer: 1, MinKey: []byte("d"), MaxKey: []byte("e"), Bytes: 1},
	)
	plan := service.CreatePlan()
	if plan.Plan == nil {
		t.Fatalf("CreatePlan() = %+v", plan)
	}
	before := service.Snapshot()
	err := service.InstallPlan(plan.Plan.ID, []File{{ID: 5, Layer: 1, MinKey: []byte("d"), MaxKey: []byte("e"), Bytes: 2}})
	if !errors.Is(err, ErrLayerInvariant) {
		t.Fatalf("InstallPlan() error = %v, want layer invariant", err)
	}
	after := service.Snapshot()
	if len(after.Files) != len(before.Files) || after.Plans[plan.Plan.ID].ID != plan.Plan.ID {
		t.Fatalf("failed install changed state: before=%+v after=%+v", before, after)
	}
	if after.LastEnds[0] != nil || len(after.Occupied) != 3 {
		t.Fatalf("failed install updated last-end or released occupancy: %+v", after)
	}
}

func TestNonZeroBoundaryClosureWrapAndCancel(t *testing.T) {
	service := testService(t, 3, 100, 3, 10)
	register(t, service,
		File{ID: 11, Layer: 1, MinKey: []byte("a"), MaxKey: []byte("c"), Bytes: 1},
		File{ID: 12, Layer: 1, MinKey: []byte("c"), MaxKey: []byte("d"), Bytes: 1},
		File{ID: 13, Layer: 1, MinKey: []byte("d"), MaxKey: []byte("f"), Bytes: 1},
		File{ID: 14, Layer: 1, MinKey: []byte("m"), MaxKey: []byte("p"), Bytes: 1},
		File{ID: 21, Layer: 2, MinKey: []byte("f"), MaxKey: []byte("g"), Bytes: 1},
	)
	first := service.CreatePlan()
	if first.Plan == nil {
		t.Fatalf("CreatePlan() = %+v", first)
	}
	if got := first.Plan.InputIDs; !slices.Equal(got, []uint64{11, 12, 13, 21}) {
		t.Fatalf("chain inputs = %v", got)
	}
	if err := service.InstallPlan(first.Plan.ID, []File{{ID: 31, Layer: 2, MinKey: []byte("a"), MaxKey: []byte("g"), Bytes: 4}}); err != nil {
		t.Fatalf("InstallPlan() error = %v", err)
	}
	if got := service.Snapshot().LastEnds[1]; string(got) != "g" {
		t.Fatalf("lastEnd = %q, want g", got)
	}

	register(t, service,
		File{ID: 15, Layer: 1, MinKey: []byte("a"), MaxKey: []byte("c"), Bytes: 1},
		File{ID: 16, Layer: 1, MinKey: []byte("c"), MaxKey: []byte("d"), Bytes: 1},
		File{ID: 17, Layer: 1, MinKey: []byte("d"), MaxKey: []byte("f"), Bytes: 1},
	)
	afterEnd := service.CreatePlan()
	if afterEnd.Plan == nil || !slices.Equal(afterEnd.Plan.InputIDs, []uint64{14}) {
		t.Fatalf("after-end plan = %+v", afterEnd.Plan)
	}
	if err := service.CancelPlan(afterEnd.Plan.ID); err != nil {
		t.Fatalf("CancelPlan() error = %v", err)
	}
	if got := service.Snapshot().LastEnds[1]; string(got) != "g" {
		t.Fatalf("lastEnd after cancel = %q", got)
	}
	afterEnd = service.CreatePlan()
	if err := service.InstallPlan(afterEnd.Plan.ID, []File{{ID: 32, Layer: 2, MinKey: []byte("m"), MaxKey: []byte("p"), Bytes: 1}}); err != nil {
		t.Fatalf("InstallPlan(afterEnd) error = %v", err)
	}

	wrapped := service.CreatePlan()
	if wrapped.Plan == nil || !slices.Equal(wrapped.Plan.InputIDs, []uint64{15, 16, 17, 31}) {
		t.Fatalf("wrapped plan = %+v", wrapped.Plan)
	}
}
