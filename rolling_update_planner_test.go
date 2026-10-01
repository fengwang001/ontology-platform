package ontology

import (
	"errors"
	"testing"
)

func TestRoundingAndZeroTolerance(t *testing.T) {
	planner, err := NewRollingUpdatePlanner(Config{DesiredReplicas: 10, MaxSurgePercent: 25, MaxUnavailablePercent: 25, StallSteps: 1})
	if err != nil {
		t.Fatalf("NewRollingUpdatePlanner() error = %v", err)
	}
	if planner.maxSurge != 3 || planner.maxUnavailable != 2 {
		t.Fatalf("limits = (%d,%d), want (3,2)", planner.maxSurge, planner.maxUnavailable)
	}

	planner, err = NewRollingUpdatePlanner(Config{DesiredReplicas: 1, MaxSurgePercent: 50, MaxUnavailablePercent: 50, StallSteps: 1})
	if err != nil {
		t.Fatalf("NewRollingUpdatePlanner() error = %v", err)
	}
	if planner.maxSurge != 1 || planner.maxUnavailable != 0 {
		t.Fatalf("limits = (%d,%d), want (1,0)", planner.maxSurge, planner.maxUnavailable)
	}
	assertStep(t, planner, 0, StepResult{Created: 1})

	planner, err = NewRollingUpdatePlanner(Config{DesiredReplicas: 3, MaxSurgePercent: 33, MaxUnavailablePercent: 33, StallSteps: 1})
	if err != nil {
		t.Fatalf("NewRollingUpdatePlanner() error = %v", err)
	}
	if planner.maxSurge != 1 || planner.maxUnavailable != 0 {
		t.Fatalf("limits = (%d,%d), want (1,0)", planner.maxSurge, planner.maxUnavailable)
	}
	assertStep(t, planner, 0, StepResult{Created: 1})

	planner, err = NewRollingUpdatePlanner(Config{DesiredReplicas: 10, MaxSurgePercent: 0, MaxUnavailablePercent: 0, StallSteps: 1})
	if err != nil {
		t.Fatalf("NewRollingUpdatePlanner() error = %v", err)
	}
	if planner.maxSurge != 0 || planner.maxUnavailable != 1 {
		t.Fatalf("limits = (%d,%d), want (0,1)", planner.maxSurge, planner.maxUnavailable)
	}

	planner, _ = NewRollingUpdatePlanner(Config{DesiredReplicas: 1, StallSteps: 1})
	assertStep(t, planner, 0, StepResult{Reduced: 1})
}

func TestExampleSequence(t *testing.T) {
	planner, _ := NewRollingUpdatePlanner(Config{
		DesiredReplicas:       10,
		MaxSurgePercent:       25,
		MaxUnavailablePercent: 25,
		MinReadyMillis:        1000,
		StallSteps:            3,
	})

	assertStep(t, planner, 0, StepResult{Created: 3, Cleaned: 0, Reduced: 2})
	if err := planner.NewReady(3, 100); err != nil {
		t.Fatalf("NewReady() error = %v", err)
	}
	assertStep(t, planner, 500, StepResult{Created: 2, Cleaned: 0, Reduced: 0})
	assertStep(t, planner, 1100, StepResult{Created: 0, Cleaned: 0, Reduced: 3})
}

func TestOldUnreadyCleanupUsesNoUnavailableBudget(t *testing.T) {
	planner, _ := NewRollingUpdatePlanner(Config{
		DesiredReplicas:       10,
		MaxSurgePercent:       25,
		MaxUnavailablePercent: 25,
		StallSteps:            3,
	})

	if err := planner.OldUnready(3, 0); err != nil {
		t.Fatalf("OldUnready() error = %v", err)
	}
	assertStep(t, planner, 1, StepResult{Created: 3, Cleaned: 3, Reduced: 0})

	snapshot := planner.Snapshot()
	if snapshot.OldReady != 7 || snapshot.OldUnready != 0 || snapshot.NewUnready != 3 {
		t.Fatalf("snapshot = %+v", snapshot)
	}

	zeroSurgePlanner, _ := NewRollingUpdatePlanner(Config{
		DesiredReplicas:       10,
		MaxSurgePercent:       0,
		MaxUnavailablePercent: 0,
		StallSteps:            1,
	})
	if err := zeroSurgePlanner.OldUnready(1, 0); err != nil {
		t.Fatalf("OldUnready() error = %v", err)
	}
	assertStep(t, zeroSurgePlanner, 0, StepResult{Cleaned: 1})
}

func TestMinReadyBoundary(t *testing.T) {
	planner, _ := NewRollingUpdatePlanner(Config{
		DesiredReplicas:       1,
		MaxSurgePercent:       100,
		MaxUnavailablePercent: 0,
		MinReadyMillis:        1000,
		StallSteps:            3,
	})

	assertStep(t, planner, 0, StepResult{Created: 1, Cleaned: 0, Reduced: 0})
	if err := planner.NewReady(1, 0); err != nil {
		t.Fatalf("NewReady() error = %v", err)
	}
	assertStep(t, planner, 999, StepResult{Created: 0, Cleaned: 0, Reduced: 0})
	if planner.Done() {
		t.Fatal("Done at 999 = true, want false because new replica is not stable")
	}
	assertStep(t, planner, 1000, StepResult{Created: 0, Cleaned: 0, Reduced: 1})

	snapshot := planner.Snapshot()
	if snapshot.NewReady != 1 || snapshot.StableReady != 1 || !planner.Done() {
		t.Fatalf("snapshot = %+v, Done = %v", snapshot, planner.Done())
	}
}

func TestNewFailConsumesNewestBatchesAndReducesScaleDown(t *testing.T) {
	planner, _ := NewRollingUpdatePlanner(Config{
		DesiredReplicas:       10,
		MaxSurgePercent:       100,
		MaxUnavailablePercent: 0,
		MinReadyMillis:        1000,
		StallSteps:            3,
	})

	assertStep(t, planner, 0, StepResult{Created: 10})
	if err := planner.NewReady(4, 0); err != nil {
		t.Fatalf("NewReady() error = %v", err)
	}
	if err := planner.NewReady(3, 1000); err != nil {
		t.Fatalf("NewReady() error = %v", err)
	}
	if err := planner.NewFail(2, 2000); err != nil {
		t.Fatalf("NewFail() error = %v", err)
	}

	batches := planner.Snapshot().ReadyBatches
	if len(batches) != 2 || batches[0].Count != 4 || batches[1].Count != 1 {
		t.Fatalf("batches = %+v, want [4 1]", batches)
	}
	assertStep(t, planner, 2000, StepResult{Created: 0, Cleaned: 0, Reduced: 5})
}

func TestNewFailDoesNotResetStallCount(t *testing.T) {
	planner, _ := NewRollingUpdatePlanner(Config{
		DesiredReplicas:       10,
		MaxSurgePercent:       100,
		MaxUnavailablePercent: 0,
		MinReadyMillis:        1000,
		StallSteps:            3,
	})

	assertStep(t, planner, 0, StepResult{Created: 10})
	if err := planner.NewReady(10, 0); err != nil {
		t.Fatalf("NewReady() error = %v", err)
	}
	assertStep(t, planner, 1, StepResult{})
	assertStep(t, planner, 2, StepResult{})
	if err := planner.NewFail(1, 2); err != nil {
		t.Fatalf("NewFail() error = %v", err)
	}
	if snapshot := planner.Snapshot(); snapshot.StallCount != 2 {
		t.Fatalf("stall count after NewFail = %d, want 2", snapshot.StallCount)
	}
}

func TestNewReadyResetsStallCount(t *testing.T) {
	planner, _ := NewRollingUpdatePlanner(Config{
		DesiredReplicas:       10,
		MaxSurgePercent:       100,
		MaxUnavailablePercent: 0,
		MinReadyMillis:        0,
		StallSteps:            2,
	})

	assertStep(t, planner, 0, StepResult{Created: 10})
	assertStep(t, planner, 1, StepResult{})
	assertStep(t, planner, 2, StepResult{Stalled: true})
	if err := planner.NewReady(1, 2); err != nil {
		t.Fatalf("NewReady() error = %v", err)
	}
	if snapshot := planner.Snapshot(); snapshot.StallCount != 0 {
		t.Fatalf("stall count after NewReady = %d, want 0", snapshot.StallCount)
	}
	assertStep(t, planner, 3, StepResult{Reduced: 1})
}

func TestStallAndResetRules(t *testing.T) {
	planner, _ := NewRollingUpdatePlanner(Config{
		DesiredReplicas:       10,
		MaxSurgePercent:       100,
		MaxUnavailablePercent: 0,
		MinReadyMillis:        1000,
		StallSteps:            2,
	})

	assertStep(t, planner, 0, StepResult{Created: 10})
	if err := planner.NewReady(10, 0); err != nil {
		t.Fatalf("NewReady() error = %v", err)
	}
	assertStep(t, planner, 1, StepResult{})
	assertStep(t, planner, 2, StepResult{Stalled: true})

	if err := planner.OldUnready(1, 2); err != nil {
		t.Fatalf("OldUnready() error = %v", err)
	}
	if planner.Snapshot().StallCount != 2 {
		t.Fatalf("stall count after OldUnready = %d, want 2", planner.Snapshot().StallCount)
	}
	assertStep(t, planner, 3, StepResult{Cleaned: 1, Stalled: false})

	if err := planner.NewReady(0, 4); err == nil {
		t.Fatal("NewReady(0) succeeded")
	}
	if err := planner.NewReady(0, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewReady() error = %v, want ErrInvalidArgument", err)
	}

	planner2, _ := NewRollingUpdatePlanner(Config{DesiredReplicas: 1, MaxSurgePercent: 1, StallSteps: 2})
	assertStep(t, planner2, 0, StepResult{Created: 1})
	if err := planner2.NewReady(1, 1); err != nil {
		t.Fatalf("NewReady() error = %v", err)
	}
	assertStep(t, planner2, 2, StepResult{Reduced: 1})
	if !planner2.Done() {
		t.Fatal("Done = false, want true")
	}
}

func TestRejectionsDoNotChangeStateAndReasonsAreDistinguishable(t *testing.T) {
	planner, _ := NewRollingUpdatePlanner(Config{DesiredReplicas: 2, MaxSurgePercent: 1, StallSteps: 2})
	assertStep(t, planner, 5, StepResult{Created: 1, Reduced: 0})
	before := planner.Snapshot()

	if _, err := planner.Step(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Step() error = %v, want ErrInvalidArgument", err)
	}
	if _, err := planner.Step(4); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("Step() error = %v, want ErrClockRegression", err)
	}
	if err := planner.NewReady(0, 5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewReady() error = %v, want ErrInvalidArgument", err)
	}
	if err := planner.NewReady(2, 5); !errors.Is(err, ErrNewReadyOutOfRange) {
		t.Fatalf("NewReady() error = %v, want ErrNewReadyOutOfRange", err)
	}
	if err := planner.NewReady(1, 4); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("NewReady() error = %v, want ErrClockRegression", err)
	}
	if err := planner.NewFail(1, 5); !errors.Is(err, ErrNewFailOutOfRange) {
		t.Fatalf("NewFail() error = %v, want ErrNewFailOutOfRange", err)
	}
	if err := planner.OldUnready(3, 5); !errors.Is(err, ErrOldUnreadyOutOfRange) {
		t.Fatalf("OldUnready() error = %v, want ErrOldUnreadyOutOfRange", err)
	}

	after := planner.Snapshot()
	if !snapshotsEqual(before, after) {
		t.Fatalf("state changed after rejection: before %+v, after %+v", before, after)
	}
}

func TestInvalidConfig(t *testing.T) {
	invalidConfigs := []Config{
		{DesiredReplicas: 0},
		{DesiredReplicas: 1_000_001},
		{DesiredReplicas: 1, MaxSurgePercent: -1},
		{DesiredReplicas: 1, MaxSurgePercent: 101},
		{DesiredReplicas: 1, MaxUnavailablePercent: -1},
		{DesiredReplicas: 1, MaxUnavailablePercent: 101},
		{DesiredReplicas: 1, MinReadyMillis: -1},
		{DesiredReplicas: 1, MinReadyMillis: 1_000_000_000_001},
		{DesiredReplicas: 1, StallSteps: 0},
		{DesiredReplicas: 1, StallSteps: 1001},
	}

	for index, config := range invalidConfigs {
		if _, err := NewRollingUpdatePlanner(config); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("config[%d] error = %v, want ErrInvalidConfig", index, err)
		}
	}
}

func assertStep(t *testing.T, planner *RollingUpdatePlanner, now int64, want StepResult) {
	t.Helper()

	got, err := planner.Step(now)
	if err != nil {
		t.Fatalf("Step(%d) error = %v", now, err)
	}
	if got != want {
		t.Fatalf("Step(%d) = %+v, want %+v", now, got, want)
	}
}

func snapshotsEqual(left, right PlannerSnapshot) bool {
	if left.OldReady != right.OldReady || left.OldUnready != right.OldUnready ||
		left.NewUnready != right.NewUnready || left.NewReady != right.NewReady ||
		left.StableReady != right.StableReady || left.Available != right.Available ||
		left.StallCount != right.StallCount || left.AcceptedNow != right.AcceptedNow ||
		left.HasAcceptedOp != right.HasAcceptedOp || len(left.ReadyBatches) != len(right.ReadyBatches) {
		return false
	}

	for index := range left.ReadyBatches {
		if left.ReadyBatches[index] != right.ReadyBatches[index] {
			return false
		}
	}
	return true
}
