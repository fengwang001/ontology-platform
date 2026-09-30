package autoscaler

import (
	"errors"
	"sync"
	"testing"
)

func TestToleranceBoundary(t *testing.T) {
	controller := mustNewController(t, Config{MinReplicas: 2, MaxReplicas: 10, Target: 10})

	assertEvaluation(t, controller, 0, 9, 2, DecisionMaintain)
	assertEvaluation(t, controller, 1, 11, 2, DecisionMaintain)

	result, err := controller.Evaluate(2, 8)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if result.Replicas != 2 || result.Decision != DecisionMaintain {
		t.Fatalf("metric 8 with two current replicas = %+v, want maintained replicas 2", result)
	}

	assertEvaluation(t, controller, 3, 12, 3, DecisionScaleUp)
}

func TestScaleUpIsLimitedToDouble(t *testing.T) {
	controller := mustNewController(t, Config{MinReplicas: 1, MaxReplicas: 100, Target: 10})

	assertEvaluation(t, controller, 0, 100, 2, DecisionScaleUp)
	assertEvaluation(t, controller, 1, 100, 4, DecisionScaleUp)
	assertEvaluation(t, controller, 2, 100, 8, DecisionScaleUp)
}

func TestScaleDownWindowBoundary(t *testing.T) {
	t.Run("difference equal to W stays in window", func(t *testing.T) {
		controller := mustNewController(t, Config{MinReplicas: 1, MaxReplicas: 10, Target: 10, WindowMS: 3})

		assertEvaluation(t, controller, 0, 12, 2, DecisionScaleUp)
		assertEvaluation(t, controller, 1, 5, 2, DecisionWindowBlocked)
		assertEvaluation(t, controller, 3, 5, 2, DecisionWindowBlocked)
		assertEvaluation(t, controller, 4, 5, 1, DecisionScaleDown)
	})

	t.Run("difference W plus one leaves window", func(t *testing.T) {
		controller := mustNewController(t, Config{MinReplicas: 1, MaxReplicas: 10, Target: 10, WindowMS: 10})

		assertEvaluation(t, controller, 0, 12, 2, DecisionScaleUp)
		assertEvaluation(t, controller, 2, 5, 2, DecisionWindowBlocked)
		assertEvaluation(t, controller, 13, 5, 1, DecisionScaleDown)
	})
}

func TestCooldownBoundary(t *testing.T) {
	controller := mustNewController(t, Config{
		MinReplicas: 1,
		MaxReplicas: 10,
		Target:      10,
		WindowMS:    1,
		CooldownMS:  10,
	})

	assertEvaluation(t, controller, 0, 12, 2, DecisionScaleUp)
	assertEvaluation(t, controller, 2, 5, 1, DecisionScaleDown)
	assertEvaluation(t, controller, 3, 12, 2, DecisionScaleUp)
	assertEvaluation(t, controller, 12, 5, 1, DecisionScaleDown)

	assertEvaluation(t, controller, 13, 12, 2, DecisionScaleUp)
	assertEvaluation(t, controller, 22, 5, 1, DecisionScaleDown)
	assertEvaluation(t, controller, 23, 12, 2, DecisionScaleUp)
	assertEvaluation(t, controller, 31, 5, 2, DecisionCooldownBlocked)
	assertEvaluation(t, controller, 32, 5, 1, DecisionScaleDown)
}

func TestHighHistoryRecommendationBlocksScaleDown(t *testing.T) {
	controller := mustNewController(t, Config{MinReplicas: 1, MaxReplicas: 10, Target: 10, WindowMS: 5})

	assertEvaluation(t, controller, 0, 50, 2, DecisionScaleUp)
	assertEvaluation(t, controller, 1, 100, 4, DecisionScaleUp)
	assertEvaluation(t, controller, 2, 1, 4, DecisionWindowBlocked)
	assertEvaluation(t, controller, 5, 1, 4, DecisionWindowBlocked)
	assertEvaluation(t, controller, 6, 1, 4, DecisionWindowBlocked)
	assertEvaluation(t, controller, 7, 1, 1, DecisionScaleDown)
}

func TestRecommendationIsClampedToBounds(t *testing.T) {
	controller := mustNewController(t, Config{MinReplicas: 3, MaxReplicas: 5, Target: 10, WindowMS: 10})

	assertEvaluation(t, controller, 0, 0, 3, DecisionMaintain)
	assertEvaluation(t, controller, 1, 100, 5, DecisionScaleUp)
	assertEvaluation(t, controller, 2, 0, 5, DecisionWindowBlocked)
	assertEvaluation(t, controller, 13, 0, 3, DecisionScaleDown)
}

func TestInvalidConfigReportsFirstErrorOnly(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		want   error
	}{
		{name: "min below one", config: Config{MinReplicas: 0, MaxReplicas: 0, Target: 0, WindowMS: -1, CooldownMS: -1}, want: ErrInvalidMinReplicas},
		{name: "max below min", config: Config{MinReplicas: 2, MaxReplicas: 1, Target: 0, WindowMS: -1, CooldownMS: -1}, want: ErrInvalidMaxReplicas},
		{name: "nonpositive target", config: Config{MinReplicas: 1, MaxReplicas: 2, Target: 0, WindowMS: -1, CooldownMS: -1}, want: ErrInvalidTarget},
		{name: "negative window", config: Config{MinReplicas: 1, MaxReplicas: 2, Target: 1, WindowMS: -1, CooldownMS: -1}, want: ErrInvalidWindow},
		{name: "negative cooldown", config: Config{MinReplicas: 1, MaxReplicas: 2, Target: 1, WindowMS: 0, CooldownMS: -1}, want: ErrInvalidCooldown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller, err := New(test.config)
			if controller != nil {
				t.Fatalf("New returned controller %v, want nil", controller)
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("New error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestInvalidEvaluationDoesNotChangeState(t *testing.T) {
	controller := mustNewController(t, Config{MinReplicas: 1, MaxReplicas: 10, Target: 10, WindowMS: 10, CooldownMS: 10})

	assertEvaluation(t, controller, 5, 12, 2, DecisionScaleUp)
	historyLength := len(controller.history)

	if _, err := controller.Evaluate(4, 12); !errors.Is(err, ErrEvaluationOrder) {
		t.Fatalf("earlier evaluation error = %v, want %v", err, ErrEvaluationOrder)
	}
	if _, err := controller.Evaluate(5, -1); !errors.Is(err, ErrNegativeMetric) {
		t.Fatalf("negative metric error = %v, want %v", err, ErrNegativeMetric)
	}
	if len(controller.history) != historyLength {
		t.Fatalf("history length = %d, want unchanged %d", len(controller.history), historyLength)
	}

	assertEvaluation(t, controller, 5, 5, 2, DecisionWindowBlocked)

	lastScaleDown, ok := controller.LastScaleDownAt()
	if ok || lastScaleDown != 0 {
		t.Fatalf("last scale-down = (%d, %v), want absent", lastScaleDown, ok)
	}
}

func TestEqualTimestampIsAllowed(t *testing.T) {
	controller := mustNewController(t, Config{MinReplicas: 1, MaxReplicas: 10, Target: 10})

	assertEvaluation(t, controller, 7, 12, 2, DecisionScaleUp)
	assertEvaluation(t, controller, 7, 10, 2, DecisionMaintain)
}

func TestConcurrentEvaluationAndQuery(t *testing.T) {
	controller := mustNewController(t, Config{MinReplicas: 1, MaxReplicas: 100, Target: 10, WindowMS: 100, CooldownMS: 5})

	const goroutines = 16
	var waitGroup sync.WaitGroup
	for worker := 0; worker < goroutines; worker++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for range 50 {
				if _, err := controller.Evaluate(0, 100); err != nil {
					t.Errorf("Evaluate returned error: %v", err)
					return
				}
				replicas := controller.Replicas()
				if replicas < 1 || replicas > 100 {
					t.Errorf("Replicas = %d, want range [1, 100]", replicas)
				}
			}
		}()
	}
	waitGroup.Wait()
}

func mustNewController(t *testing.T, config Config) *Controller {
	t.Helper()
	controller, err := New(config)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return controller
}

func assertEvaluation(t *testing.T, controller *Controller, now int64, metric int64, replicas int64, decision Decision) {
	t.Helper()
	result, err := controller.Evaluate(now, metric)
	if err != nil {
		t.Fatalf("Evaluate(%d, %d) returned error: %v", now, metric, err)
	}
	if result.Replicas != replicas || result.Decision != decision {
		t.Fatalf("Evaluate(%d, %d) = %+v, want replicas %d and decision %s", now, metric, result, replicas, decision)
	}
	if actual := controller.Replicas(); actual != replicas {
		t.Fatalf("Replicas after Evaluate(%d, %d) = %d, want %d", now, metric, actual, replicas)
	}
}
