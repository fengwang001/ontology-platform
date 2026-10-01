package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func neTaint(key, value string) Taint {
	return Taint{Key: key, Value: value, Effect: NoExecute}
}

func nsTaint(key, value string) Taint {
	return Taint{Key: key, Value: value, Effect: NoSchedule}
}

func preferTaint(key, value string) Taint {
	return Taint{Key: key, Value: value, Effect: PreferNoSchedule}
}

func toleration(key string, op TolerationOperator, value string, effect Effect, seconds int64) Toleration {
	return Toleration{Key: key, Operator: op, Value: value, Effect: effect, Seconds: seconds}
}

func equalNE(key, value string, seconds int64) Toleration {
	return toleration(key, EqualOperator, value, NoExecute, seconds)
}

func existsNE(key string, seconds int64) Toleration {
	return toleration(key, ExistsOperator, "", NoExecute, seconds)
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var operationErr *OperationError
	if !errors.As(err, &operationErr) {
		t.Fatalf("got %v, want error code %s", err, code)
	}
	if operationErr.Code != code {
		t.Fatalf("got code %s, want %s", operationErr.Code, code)
	}
}

func wantEvictions(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestEvictionDeadlineBoundaryAndLateBinding(t *testing.T) {
	m, err := NewManager(0, 2)
	mustOK(t, err)
	mustOK(t, m.AddNode("n", 2))
	mustOK(t, m.Taint("n", neTaint("k", "v"), 1000))
	mustOK(t, m.Schedule("p", "n", []Toleration{equalNE("k", "v", 1)}, 1500))

	got, err := m.Tick(1999)
	mustOK(t, err)
	wantEvictions(t, got)

	got, err = m.Tick(2000)
	mustOK(t, err)
	wantEvictions(t, got, "p")
}

func TestTolerationDeclarationOrderWins(t *testing.T) {
	cases := []struct {
		name        string
		tolerations []Toleration
		evicted     bool
	}{
		{
			name: "finite before forever",
			tolerations: []Toleration{
				equalNE("k", "v", 30),
				equalNE("k", "v", -1),
			},
			evicted: true,
		},
		{
			name: "forever before finite",
			tolerations: []Toleration{
				equalNE("k", "v", -1),
				equalNE("k", "v", 30),
			},
			evicted: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := NewManager(0, 2)
			mustOK(t, m.AddNode("n", 1))
			mustOK(t, m.Taint("n", neTaint("k", "v"), 0))
			mustOK(t, m.Schedule("p", "n", tc.tolerations, 100))
			got, err := m.Tick(30000)
			mustOK(t, err)
			if tc.evicted {
				wantEvictions(t, got, "p")
			} else {
				wantEvictions(t, got)
			}
		})
	}
}

func TestMinimumDeadlineAndUntoleratedNoExecute(t *testing.T) {
	m, _ := NewManager(0, 4)
	mustOK(t, m.AddNode("n", 1))
	mustOK(t, m.Taint("n", neTaint("a", "x"), 0))
	mustOK(t, m.Taint("n", neTaint("b", "y"), 0))
	mustOK(t, m.Schedule("p", "n", []Toleration{
		equalNE("a", "x", 10),
		equalNE("b", "y", 30),
	}, 0))

	got, err := m.Tick(9999)
	mustOK(t, err)
	wantEvictions(t, got)
	got, err = m.Tick(10000)
	mustOK(t, err)
	wantEvictions(t, got, "p")

	mustOK(t, m.AddNode("u", 1))
	mustOK(t, m.Schedule("q", "u", nil, 10000))
	got, err = m.Tick(10499)
	mustOK(t, err)
	wantEvictions(t, got)
	mustOK(t, m.Taint("u", neTaint("k", "v"), 10500))
	got, err = m.Tick(10500)
	mustOK(t, err)
	wantEvictions(t, got, "q")
}

func TestReplacingTaintValueDoesNotResetAddedAt(t *testing.T) {
	m, _ := NewManager(0, 2)
	mustOK(t, m.AddNode("n", 1))
	mustOK(t, m.Taint("n", neTaint("k", "old"), 100000))
	mustOK(t, m.Schedule("p", "n", []Toleration{equalNE("k", "old", 10)}, 200000))
	mustOK(t, m.Taint("n", neTaint("k", "new"), 500000))

	got, err := m.Tick(500000)
	mustOK(t, err)
	wantEvictions(t, got, "p")
}

func TestUntaintAndReaddRestartsFromNewAddedAt(t *testing.T) {
	m, _ := NewManager(0, 2)
	mustOK(t, m.AddNode("n", 1))
	mustOK(t, m.Schedule("p", "n", nil, 0))
	mustOK(t, m.Taint("n", neTaint("k", "v"), 100))
	mustOK(t, m.Untaint("n", "k", NoExecute, 100))
	got, err := m.Tick(499)
	mustOK(t, err)
	wantEvictions(t, got)
	mustOK(t, m.Taint("n", neTaint("k", "v"), 500))
	got, err = m.Tick(500)
	mustOK(t, err)
	wantEvictions(t, got, "p")
}

func TestRateLimitAndInterveningUntaint(t *testing.T) {
	m, _ := NewManager(0, 1)
	mustOK(t, m.AddNode("n", 3))
	for _, id := range []string{"p1", "p2", "p3"} {
		mustOK(t, m.Schedule(id, "n", nil, 0))
	}
	mustOK(t, m.Taint("n", neTaint("k", "v"), 0))

	got, err := m.Tick(10)
	mustOK(t, err)
	wantEvictions(t, got, "p1")
	mustOK(t, m.Untaint("n", "k", NoExecute, 11))
	got, err = m.Tick(20)
	mustOK(t, err)
	wantEvictions(t, got)
}

func TestGracePeriodBoundaryAndTerminatingOccupancy(t *testing.T) {
	m, _ := NewManager(1000, 1)
	mustOK(t, m.AddNode("n", 2))
	mustOK(t, m.Schedule("p1", "n", nil, 0))
	mustOK(t, m.Schedule("p2", "n", nil, 0))
	mustOK(t, m.Taint("n", neTaint("k", "v"), 0))

	got, err := m.Tick(10)
	mustOK(t, err)
	wantEvictions(t, got, "p1")
	wantCode(t, m.Schedule("p1", "n", []Toleration{equalNE("k", "v", 0)}, 1009), ErrPodExists)
	wantCode(t, m.Schedule("p3", "n", []Toleration{equalNE("k", "v", 0)}, 1009), ErrInsufficientCapacity)
	mustOK(t, m.Schedule("p3", "n", []Toleration{equalNE("k", "v", 0)}, 1010))
}

func TestSpecExampleRateLimitGraceAndCapacity(t *testing.T) {
	m, _ := NewManager(1000, 1)
	mustOK(t, m.AddNode("n", 2))
	mustOK(t, m.Taint("n", neTaint("k", "v"), 0))
	mustOK(t, m.Schedule("p1", "n", []Toleration{equalNE("k", "v", 0)}, 5))
	mustOK(t, m.Schedule("p2", "n", []Toleration{equalNE("k", "v", 0)}, 5))

	got, err := m.Tick(10)
	mustOK(t, err)
	wantEvictions(t, got, "p1")
	got, err = m.Tick(20)
	mustOK(t, err)
	wantEvictions(t, got, "p2")
	wantCode(t, m.Schedule("p3", "n", []Toleration{equalNE("k", "v", 0)}, 30), ErrInsufficientCapacity)
	mustOK(t, m.Schedule("p3", "n", []Toleration{equalNE("k", "v", 0)}, 1010))
}

func TestZeroGraceReleasesImmediately(t *testing.T) {
	m, _ := NewManager(0, 1)
	mustOK(t, m.AddNode("n", 1))
	mustOK(t, m.Schedule("p", "n", nil, 0))
	mustOK(t, m.Taint("n", neTaint("k", "v"), 0))
	got, err := m.Tick(10)
	mustOK(t, err)
	wantEvictions(t, got, "p")
	mustOK(t, m.Schedule("p", "n", []Toleration{equalNE("k", "v", -1)}, 10))
}

func TestWildcardAndPreferNoSchedule(t *testing.T) {
	m, _ := NewManager(0, 2)
	mustOK(t, m.AddNode("n", 2))
	mustOK(t, m.Taint("n", neTaint("k", "v"), 0))
	mustOK(t, m.Taint("n", nsTaint("other", "x"), 0))
	mustOK(t, m.Schedule("wild", "n", []Toleration{
		toleration("", ExistsOperator, "", "", -1),
	}, 0))
	wantCode(t, m.Schedule("effect-scoped-wild", "n", []Toleration{
		toleration("", ExistsOperator, "", NoSchedule, -1),
	}, 0), ErrUnschedulableTaint)

	mustOK(t, m.AddNode("prefer-node", 1))
	mustOK(t, m.Taint("prefer-node", preferTaint("pref", "y"), 0))
	mustOK(t, m.Schedule("prefer-only", "prefer-node", nil, 0))
}

func TestRejectionOrderAndRejectedOperationsDoNotMutate(t *testing.T) {
	_, err := NewManager(-1, 1)
	wantCode(t, err, ErrInvalidConfig)
	_, err = NewManager(0, 0)
	wantCode(t, err, ErrInvalidConfig)

	m, _ := NewManager(0, 1)
	mustOK(t, m.AddNode("n", 1))
	mustOK(t, m.AddNode("blocked", 1))
	mustOK(t, m.Taint("n", neTaint("k", "v"), 0))
	mustOK(t, m.Schedule("p", "n", []Toleration{existsNE("k", -1)}, 0))

	wantCode(t, m.AddNode("", 1), ErrInvalidArgument)
	wantCode(t, m.AddNode("n", 1), ErrNodeExists)
	wantCode(t, m.Taint("n", Taint{Key: "", Effect: NoExecute}, 1), ErrInvalidArgument)
	wantCode(t, m.Taint("missing", neTaint("k", "v"), 1), ErrNodeNotFound)
	wantCode(t, m.Untaint("n", "missing", NoExecute, 1), ErrTaintNotFound)
	wantCode(t, m.Schedule("", "n", nil, -1), ErrInvalidArgument)
	wantCode(t, m.Schedule("p", "missing", nil, 1), ErrPodExists)
	wantCode(t, m.Schedule("q", "missing", nil, 1), ErrNodeNotFound)

	mustOK(t, m.Taint("blocked", nsTaint("a", "x"), 1))
	mustOK(t, m.Taint("blocked", neTaint("a", "x"), 1))
	mustOK(t, m.Taint("blocked", neTaint("b", "x"), 1))
	err = m.Schedule("r", "blocked", nil, 1)
	wantCode(t, err, ErrUnschedulableTaint)
	var operationErr *OperationError
	errors.As(err, &operationErr)
	if operationErr.Key != "a" || operationErr.Effect != NoExecute {
		t.Fatalf("got (%s,%s), want (a,%s)", operationErr.Key, operationErr.Effect, NoExecute)
	}
	wantCode(t, m.Schedule("full", "n", []Toleration{existsNE("k", -1)}, 1), ErrInsufficientCapacity)
	wantCode(t, m.Schedule("p", "n", nil, 0), ErrClockBacktrack)

	if len(m.nodes) != 2 || len(m.pods) != 1 {
		t.Fatalf("rejected operation changed state: nodes=%d pods=%d", len(m.nodes), len(m.pods))
	}
}

func TestInvalidTolerations(t *testing.T) {
	m, _ := NewManager(0, 1)
	mustOK(t, m.AddNode("n", 1))
	cases := []Toleration{
		{Key: "", Operator: EqualOperator, Effect: NoExecute, Seconds: -1},
		{Key: "k", Operator: ExistsOperator, Value: "v", Effect: NoExecute, Seconds: -1},
		{Key: "k", Operator: EqualOperator, Value: "v", Effect: NoSchedule, Seconds: 0},
		{Key: "k", Operator: ExistsOperator, Effect: "", Seconds: 0},
		{Key: "k", Operator: ExistsOperator, Effect: NoExecute, Seconds: -2},
		{Key: "k", Operator: TolerationOperator("bad"), Effect: NoExecute, Seconds: -1},
		{Key: "k", Operator: EqualOperator, Value: "v", Effect: Effect("bad"), Seconds: -1},
	}
	for i, tol := range cases {
		if err := m.Schedule("p", "n", []Toleration{tol}, 0); err == nil {
			t.Fatalf("case %d: expected invalid toleration", i)
		}
	}
}

func TestConcurrentOperations(t *testing.T) {
	m, _ := NewManager(1, 4)
	mustOK(t, m.AddNode("n", 100))
	mustOK(t, m.Taint("n", preferTaint("k", "v"), 0))

	var wg sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				podID := fmt.Sprintf("p-%d-%d", worker, i)
				_ = m.Schedule(podID, "n", nil, 1)
				_, _ = m.Tick(1)
			}
		}(worker)
	}
	wg.Wait()
}
