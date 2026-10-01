package scaler

import (
	"errors"
	"testing"
)

type testState struct {
	nodes     map[string]testNode
	pods      map[string]Pod
	since     map[string]*int64
	removable map[string]bool
}

type testNode struct {
	cpu int64
	mem int64
}

func mustNewScaler(t *testing.T, p, duration, minNodes int64) *Scaler {
	t.Helper()
	s, err := New(p, duration, minNodes)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return s
}

func addTestNode(t *testing.T, s *Scaler, name string, cpu, mem int64) {
	t.Helper()
	if err := s.AddNode(name, cpu, mem); err != nil {
		t.Fatalf("AddNode(%q) error = %v", name, err)
	}
}

func addTestPod(t *testing.T, s *Scaler, id, node string, cpu, mem int64, kind PodKind) {
	t.Helper()
	if err := s.AddPod(Pod{ID: id, Node: node, CPU: cpu, Memory: mem, Kind: kind}); err != nil {
		t.Fatalf("AddPod(%q) error = %v", id, err)
	}
}

func snapshotScaler(s *Scaler) testState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := testState{nodes: make(map[string]testNode), pods: make(map[string]Pod), since: make(map[string]*int64), removable: s.lastTickRemovable}
	for name, node := range s.nodes {
		state.nodes[name] = testNode{cpu: node.cpuUsed, mem: node.memUsed}
		if node.since != nil {
			value := *node.since
			state.since[name] = &value
		}
	}
	for id, pod := range s.pods {
		state.pods[id] = Pod{ID: pod.id, Node: pod.node, CPU: pod.cpu, Memory: pod.memory, Kind: pod.kind}
	}
	return state
}

func assertSince(t *testing.T, s *Scaler, want map[string]*int64) {
	t.Helper()
	state := snapshotScaler(s)
	if len(state.since) != len(want) {
		t.Fatalf("since len = %d (%v), want %d (%v)", len(state.since), state.since, len(want), want)
	}
	for name, wanted := range want {
		got := state.since[name]
		if !state.removable[name] {
			t.Fatalf("%s was not evaluated removable, since = %v", name, got)
		}
		if wanted == nil {
			if got != nil {
				t.Fatalf("since[%s] = %d, want absent", name, *got)
			}
			continue
		}
		if got == nil || *got != *wanted {
			t.Fatalf("since[%s] = %v, want %d", name, got, *wanted)
		}
	}
}

func intPtr(v int64) *int64 { return &v }

func TestUtilizationThresholdIsStrict(t *testing.T) {
	s := mustNewScaler(t, 10, 10, 1)
	addTestNode(t, s, "a", 100, 100)
	addTestPod(t, s, "p", "a", 10, 9, PodNormal)
	result, err := s.Tick(10)
	if err != nil || result.RemovedNode != "" || len(result.Migrations) != 0 {
		t.Fatalf("Tick() = %+v, %v", result, err)
	}
	assertSince(t, s, map[string]*int64{})
}

func TestEitherDimensionAboveThresholdBlocksRemoval(t *testing.T) {
	for _, request := range []struct{ cpu, mem int64 }{{11, 9}, {9, 11}} {
		t.Run("", func(t *testing.T) {
			s := mustNewScaler(t, 10, 10, 1)
			addTestNode(t, s, "a", 100, 100)
			addTestPod(t, s, "p", "a", request.cpu, request.mem, PodNormal)
			if _, err := s.Tick(1); err != nil {
				t.Fatal(err)
			}
			assertSince(t, s, map[string]*int64{})
		})
	}
}

func TestDaemonIgnoredByUtilizationButConsumesTarget(t *testing.T) {
	s := mustNewScaler(t, 50, 10, 2)
	addTestNode(t, s, "source", 10, 10)
	addTestNode(t, s, "target", 10, 10)
	addTestPod(t, s, "work", "source", 2, 2, PodNormal)
	addTestPod(t, s, "agent", "target", 9, 9, PodDaemon)
	if _, err := s.Tick(1); err != nil {
		t.Fatal(err)
	}
	assertSince(t, s, map[string]*int64{"target": intPtr(1)})
	if since := snapshotScaler(s).since; since["source"] != nil {
		t.Fatalf("source since = %v", since["source"])
	}
}

func TestSuccessfulSimulationOccupiesTargetAndFailureRollsBack(t *testing.T) {
	s := mustNewScaler(t, 50, 10, 4)
	for _, name := range []string{"a", "b", "c", "d"} {
		addTestNode(t, s, name, 10, 10)
	}
	addTestPod(t, s, "a1", "a", 1, 1, PodNormal)
	addTestPod(t, s, "b1", "b", 1, 1, PodNormal)
	if _, err := s.Tick(1); err != nil {
		t.Fatal(err)
	}
	assertSince(t, s, map[string]*int64{"a": intPtr(1), "c": intPtr(1), "d": intPtr(1)})
	if since := snapshotScaler(s).since; since["b"] != nil {
		t.Fatalf("b since = %v", since["b"])
	}
}

func TestFailedCandidateTemporaryPlacementsRollBack(t *testing.T) {
	s := mustNewScaler(t, 50, 10, 3)
	addTestNode(t, s, "source", 100, 100)
	addTestNode(t, s, "target", 10, 10)
	addTestPod(t, s, "small", "source", 1, 1, PodNormal)
	addTestPod(t, s, "large", "source", 10, 10, PodNormal)
	if _, err := s.Tick(1); err != nil {
		t.Fatal(err)
	}
	state := snapshotScaler(s)
	if state.since["source"] != nil || state.since["target"] == nil || *state.since["target"] != 1 {
		t.Fatalf("unexpected since: source=%v target=%v", state.since["source"], state.since["target"])
	}
	if state.nodes["target"].cpu != 0 || state.nodes["target"].mem != 0 {
		t.Fatalf("rolled-back placement changed target usage: %+v", state.nodes["target"])
	}
}

func TestReceivingSimulationClearsSince(t *testing.T) {
	s := mustNewScaler(t, 50, 10, 3)
	for _, name := range []string{"a-s", "b-t"} {
		addTestNode(t, s, name, 10, 10)
	}
	if _, err := s.Tick(0); err != nil {
		t.Fatal(err)
	}
	assertSince(t, s, map[string]*int64{"a-s": intPtr(0), "b-t": intPtr(0)})
	addTestPod(t, s, "work", "a-s", 1, 1, PodNormal)
	if _, err := s.Tick(1); err != nil {
		t.Fatal(err)
	}
	state := snapshotScaler(s)
	if state.since["a-s"] == nil || *state.since["a-s"] != 0 || state.since["b-t"] != nil {
		t.Fatalf("unexpected since: a-s=%v b-t=%v", state.since["a-s"], state.since["b-t"])
	}
}

func TestSinceRetainedThenCleared(t *testing.T) {
	s := mustNewScaler(t, 50, 100, 2)
	addTestNode(t, s, "a", 10, 10)
	addTestNode(t, s, "b", 10, 10)
	if _, err := s.Tick(5); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tick(8); err != nil {
		t.Fatal(err)
	}
	assertSince(t, s, map[string]*int64{"a": intPtr(5), "b": intPtr(5)})
	addTestPod(t, s, "pinned", "a", 1, 1, PodPinned)
	if _, err := s.Tick(9); err != nil {
		t.Fatal(err)
	}
	assertSince(t, s, map[string]*int64{"b": intPtr(5)})
	if since := snapshotScaler(s).since; since["a"] != nil {
		t.Fatalf("a since = %v", since["a"])
	}
}

func TestDurationBoundary(t *testing.T) {
	setup := func(t *testing.T) *Scaler {
		s := mustNewScaler(t, 50, 10, 2)
		addTestNode(t, s, "a", 10, 10)
		addTestNode(t, s, "b", 10, 10)
		if _, err := s.Tick(0); err != nil {
			t.Fatal(err)
		}
		return s
	}
	t.Run("oneBefore", func(t *testing.T) {
		s := setup(t)
		result, err := s.Tick(9)
		if err != nil || result.RemovedNode != "" || len(result.Migrations) != 0 {
			t.Fatalf("Tick() = %+v, %v", result, err)
		}
	})
	t.Run("equal", func(t *testing.T) {
		s := setup(t)
		s.minNodes = 1
		result, err := s.Tick(10)
		if err != nil {
			t.Fatal(err)
		}
		if result.RemovedNode != "a" || len(result.Migrations) != 0 {
			t.Fatalf("Tick() = %+v", result)
		}
	})
}

func TestMinNodesBoundaryAndSinceTie(t *testing.T) {
	setup := func(t *testing.T, minNodes int64) *Scaler {
		s := mustNewScaler(t, 50, 10, minNodes)
		addTestNode(t, s, "a", 10, 10)
		addTestNode(t, s, "b", 10, 10)
		if _, err := s.Tick(0); err != nil {
			t.Fatal(err)
		}
		return s
	}
	t.Run("equalDoesNotRemove", func(t *testing.T) {
		s := setup(t, 2)
		result, err := s.Tick(10)
		if err != nil || result.RemovedNode != "" || len(result.Migrations) != 0 {
			t.Fatalf("Tick() = %+v, %v", result, err)
		}
	})
	t.Run("tieChoosesSmallerName", func(t *testing.T) {
		s := setup(t, 1)
		result, err := s.Tick(10)
		if err != nil {
			t.Fatal(err)
		}
		if result.RemovedNode != "a" {
			t.Fatalf("RemovedNode = %q", result.RemovedNode)
		}
	})
}

func TestEmptyNodeRemoval(t *testing.T) {
	s := mustNewScaler(t, 50, 1, 1)
	addTestNode(t, s, "a", 10, 10)
	addTestNode(t, s, "b", 10, 10)
	if _, err := s.Tick(0); err != nil {
		t.Fatal(err)
	}
	result, err := s.Tick(1)
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedNode != "a" {
		t.Fatalf("RemovedNode = %q, migrations = %+v", result.RemovedNode, result.Migrations)
	}
}

func TestRejectionOrderingAndAtomicity(t *testing.T) {
	t.Run("config", func(t *testing.T) {
		cases := []struct{ p, duration, min int64 }{{0, 1, 0}, {101, 1, 0}, {1, 0, 0}, {1, 1, -1}}
		for _, tc := range cases {
			if _, err := New(tc.p, tc.duration, tc.min); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("New(%+v) error = %v", tc, err)
			}
		}
	})
	t.Run("nodeInvalidBeforeDuplicate", func(t *testing.T) {
		s := mustNewScaler(t, 50, 1, 0)
		addTestNode(t, s, "a", 1, 1)
		if err := s.AddNode("a", 0, 1); !errors.Is(err, ErrInvalidNode) {
			t.Fatalf("error = %v", err)
		}
		if err := s.AddNode("a", 1, 1); !errors.Is(err, ErrNodeExists) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("podInvalidBeforeExists", func(t *testing.T) {
		s := mustNewScaler(t, 50, 1, 0)
		addTestNode(t, s, "a", 10, 10)
		addTestPod(t, s, "p", "a", 1, 1, PodNormal)
		err := s.AddPod(Pod{ID: "p", Node: "missing", CPU: 0, Memory: 0, Kind: "bad"})
		if !errors.Is(err, ErrInvalidPod) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("podExistsBeforeMissingNode", func(t *testing.T) {
		s := mustNewScaler(t, 50, 1, 0)
		addTestNode(t, s, "a", 10, 10)
		addTestPod(t, s, "p", "a", 1, 1, PodNormal)
		err := s.AddPod(Pod{ID: "p", Node: "missing", CPU: 1, Memory: 1, Kind: PodNormal})
		if !errors.Is(err, ErrPodExists) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("missingNodeBeforeCapacity", func(t *testing.T) {
		s := mustNewScaler(t, 50, 1, 0)
		err := s.AddPod(Pod{ID: "p", Node: "missing", CPU: 100, Memory: 100, Kind: PodNormal})
		if !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("capacity", func(t *testing.T) {
		s := mustNewScaler(t, 50, 1, 0)
		addTestNode(t, s, "a", 1, 1)
		err := s.AddPod(Pod{ID: "p", Node: "a", CPU: 2, Memory: 1, Kind: PodNormal})
		if !errors.Is(err, ErrInsufficientSpace) {
			t.Fatalf("error = %v", err)
		}
		if len(snapshotScaler(s).pods) != 0 {
			t.Fatal("rejected AddPod changed state")
		}
	})
	t.Run("removeMissing", func(t *testing.T) {
		s := mustNewScaler(t, 50, 1, 0)
		if err := s.RemovePod("missing"); !errors.Is(err, ErrPodNotFound) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("tickInvalidAndRollback", func(t *testing.T) {
		s := mustNewScaler(t, 50, 1, 0)
		if _, err := s.Tick(-1); !errors.Is(err, ErrInvalidTime) {
			t.Fatalf("error = %v", err)
		}
		if _, err := s.Tick(5); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Tick(4); !errors.Is(err, ErrClockRollback) {
			t.Fatalf("error = %v", err)
		}
		if s.lastNow != 5 {
			t.Fatalf("lastNow = %d", s.lastNow)
		}
	})
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	s := mustNewScaler(t, 50, 3, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_ = s.AddNode("shared", 100, 100)
		}
	}()
	for i := 0; i < 100; i++ {
		_, _ = s.Tick(int64(i))
	}
	<-done
	state := snapshotScaler(s)
	for nodeName, usage := range state.nodes {
		node := s.nodes[nodeName]
		if usage.cpu < 0 || usage.cpu > node.cpuAllocatable || usage.mem < 0 || usage.mem > node.memAllocatable {
			t.Fatalf("invalid usage for %s: %+v", nodeName, usage)
		}
	}
}
