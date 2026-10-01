package placement

import (
	"sync"
	"testing"
)

func TestZoneAffinityExample(t *testing.T) {
	s := setupZones()
	if err := s.Place(&Pod{ID: "db1", Labels: testLabels("app", "db")}, "n1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Place(&Pod{ID: "db2", Labels: testLabels("app", "db")}, "n2"); err != nil {
		t.Fatal(err)
	}
	x := &Pod{
		ID:       "x",
		Labels:   testLabels("app", "db"),
		Affinity: []AffinityTerm{{Selector: testLabels("app", "db"), Topology: "zone", MinMatching: 2}},
	}
	got, err := s.Feasible(x)
	if err != nil || joinSorted(got) != "n1,n2" {
		t.Fatalf("got %v, %v", got, err)
	}

	if err := s.Remove("db2"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Feasible(x)
	if len(got) != 0 {
		t.Fatalf("cluster matcher elsewhere must disable exemption, got %v", got)
	}
	if err := s.Remove("db1"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Feasible(x)
	if len(got) != 3 {
		t.Fatalf("exemption must return after all matchers removed, got %v", got)
	}
}

func TestMinMatchingEqualityAndTopologies(t *testing.T) {
	s := setupZones()
	for _, id := range []string{"p0", "p1", "p2"} {
		if err := s.Place(&Pod{ID: id, Labels: testLabels("k", "v")}, "n1"); err != nil {
			t.Fatal(err)
		}
	}
	x := &Pod{ID: "x", Labels: testLabels("k", "v"),
		Affinity: []AffinityTerm{{Selector: testLabels("k", "v"), Topology: "node", MinMatching: 3}}}
	got, _ := s.Feasible(x)
	if joinSorted(got) != "n1" {
		t.Fatalf("node topology m=3 got %v", got)
	}

	z := *x
	z.ID = "z"
	z.Affinity[0].Topology = "zone"
	got, _ = s.Feasible(&z)
	if joinSorted(got) != "n1,n2" {
		t.Fatalf("zone topology got %v", got)
	}

	z4 := z
	z4.ID = "z4"
	z4.Affinity[0].MinMatching = 4
	got, _ = s.Feasible(&z4)
	if len(got) != 0 {
		t.Fatalf("m=4 not satisfiable with 3 pods, got %v", got)
	}
}

func TestExemptionClusterMatchOtherDomain(t *testing.T) {
	s := setupZones()
	if err := s.Place(&Pod{ID: "b", Labels: testLabels("app", "db")}, "n3"); err != nil {
		t.Fatal(err)
	}
	x := &Pod{ID: "x", Labels: testLabels("app", "db"),
		Affinity: []AffinityTerm{{Selector: testLabels("app", "db"), Topology: "zone", MinMatching: 2}}}
	got, _ := s.Feasible(x)
	if len(got) != 0 {
		t.Fatalf("a matcher in another zone prevents exemption, got %v", got)
	}
}

func TestIndependentAffinityExemptions(t *testing.T) {
	s := setupZones()
	if err := s.Place(&Pod{ID: "w1", Labels: testLabels("role", "web")}, "n3"); err != nil {
		t.Fatal(err)
	}
	x := &Pod{
		ID:     "x",
		Labels: testLabels("role", "web", "app", "db"),
		Affinity: []AffinityTerm{
			{Selector: testLabels("role", "web"), Topology: "zone", MinMatching: 2},
			{Selector: testLabels("app", "db"), Topology: "zone", MinMatching: 1},
		},
	}
	got, _ := s.Feasible(x)
	if len(got) != 0 {
		t.Fatalf("first term cannot be exempt, got %v", got)
	}
	if err := s.Remove("w1"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Feasible(x)
	if len(got) != 3 {
		t.Fatalf("both terms should be independently exempt, got %v", got)
	}
}

func TestReservationVisibilityCancelQuotaAndPlace(t *testing.T) {
	s := NewScheduler(1)
	if err := s.AddNode("n1", "a"); err != nil {
		t.Fatal(err)
	}
	r := &Pod{ID: "r", Labels: testLabels("app", "db"),
		AntiAffinity: []AntiAffinityTerm{{Selector: testLabels("app", "db"), Topology: "node"}}}
	if err := s.Reserve(r, "n1"); err != nil {
		t.Fatal(err)
	}
	if s.Reservations() != 1 {
		t.Fatalf("reservation count = %d", s.Reservations())
	}
	got, _ := s.Feasible(&Pod{ID: "x", Labels: testLabels("app", "db")})
	if len(got) != 0 {
		t.Fatalf("reservation must be visible to Feasible, got %v", got)
	}
	mustReject(t, s.Reserve(&Pod{ID: "overflow", Labels: map[string]string{}}, "n1"), ReasonReservationsFull)

	if err := s.Place(&Pod{ID: "c", Labels: map[string]string{}}, "n1"); err != nil {
		t.Fatalf("Place must not consume reservation quota: %v", err)
	}
	if s.Reservations() != 1 {
		t.Fatalf("Place changed reservation count to %d", s.Reservations())
	}

	if err := s.Cancel("r"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Feasible(&Pod{ID: "x2", Labels: testLabels("app", "db")})
	if joinSorted(got) != "n1" {
		t.Fatalf("feasibility should recover after Cancel, got %v", got)
	}

	// Commit releases the quota and leaves the pod in place.
	if err := s.Reserve(&Pod{ID: "r2", Labels: map[string]string{}}, "n1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit("r2"); err != nil {
		t.Fatal(err)
	}
	if s.Reservations() != 0 {
		t.Fatalf("commit must release quota, got %d", s.Reservations())
	}
	if err := s.Remove("r2"); err != nil {
		t.Fatalf("committed pod can be removed: %v", err)
	}
}

func TestEmptySelectorMatchesAll(t *testing.T) {
	s := setupZones()
	if err := s.Place(&Pod{ID: "a", Labels: testLabels("x", "y")}, "n1"); err != nil {
		t.Fatal(err)
	}
	x := &Pod{ID: "x", Labels: testLabels("z", "1"),
		AntiAffinity: []AntiAffinityTerm{{Selector: Selector{}, Topology: "zone"}}}
	got, _ := s.Feasible(x)
	if joinSorted(got) != "n3" {
		t.Fatalf("empty selector should match all pods, got %v", got)
	}
}

func TestSymmetricRepulsionAndNoSelfMatch(t *testing.T) {
	s := setupZones()
	q := &Pod{ID: "q", Labels: testLabels("role", "gate"),
		AntiAffinity: []AntiAffinityTerm{{Selector: testLabels("app", "db"), Topology: "node"}}}
	if err := s.Place(q, "n1"); err != nil {
		t.Fatal(err)
	}
	rj := mustReject(t, s.Place(&Pod{ID: "x", Labels: testLabels("app", "db")}, "n1"), ReasonRepelled)
	if rj.BlockingPod != "q" {
		t.Fatalf("blocker = %q", rj.BlockingPod)
	}
	if err := s.Place(&Pod{ID: "qz", Labels: testLabels("role", "gate"),
		AntiAffinity: []AntiAffinityTerm{{Selector: testLabels("app", "cache"), Topology: "zone"}}}, "n1"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Feasible(&Pod{ID: "x2", Labels: testLabels("app", "cache")})
	if joinSorted(got) != "n3" {
		t.Fatalf("zone-symmetric repulsion got %v", got)
	}

	aff := &Pod{ID: "aff", Labels: testLabels("role", "gate"),
		Affinity: []AffinityTerm{{Selector: testLabels("role", "gate"), Topology: "node", MinMatching: 1}}}
	if err := s.Place(aff, "n1"); err != nil {
		t.Fatalf("affinity is one-directional: %v", err)
	}

	self := &Pod{ID: "self", Labels: testLabels("app", "db"),
		AntiAffinity: []AntiAffinityTerm{{Selector: Selector{}, Topology: "node"}}}
	if err := s.Place(self, "n3"); err != nil {
		t.Fatalf("a pod must not repel itself: %v", err)
	}
}

func TestConcurrentSameIDWinsOnce(t *testing.T) {
	s := setupZones()
	var wg sync.WaitGroup
	results := make(chan error, 64)
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.Place(&Pod{ID: "same", Labels: map[string]string{}}, "n3")
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if err.(*Reject).Code != ReasonPodExists {
			t.Fatalf("unexpected error %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("exactly one Place must succeed, got %d", successes)
	}
	assertInvariants(t, s)
}

func TestConcurrentReservesRespectQuota(t *testing.T) {
	s := NewScheduler(7)
	if err := s.AddNode("n1", "a"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "p" + itoa(i)
			err := s.Reserve(&Pod{ID: id, Labels: map[string]string{}}, "n1")
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			} else if err.(*Reject).Code != ReasonReservationsFull &&
				err.(*Reject).Code != ReasonRepelled &&
				err.(*Reject).Code != ReasonAntiAffinityConflict {
				t.Errorf("unexpected error %v", err)
			}
		}(i)
	}
	wg.Wait()
	if successes != 7 || s.Reservations() != 7 {
		t.Fatalf("successes=%d reservations=%d", successes, s.Reservations())
	}
	assertInvariants(t, s)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [12]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func assertInvariants(t *testing.T, s *Scheduler) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	reserved := 0
	ids := make([]string, 0, len(s.pods))
	for id, st := range s.pods {
		if st.reserved {
			reserved++
		}
		if _, ok := s.nodes[st.node]; !ok {
			t.Fatalf("pod %s on unknown node %s", id, st.node)
		}
		ids = append(ids, id)
	}
	if reserved > s.q {
		t.Fatalf("reservations %d exceed quota %d", reserved, s.q)
	}
	for _, id := range ids {
		st := s.pods[id]
		labels := st.pod.Labels
		if blocker := repellerLocked(s.pods, s.nodes, id, st.node, labels); blocker != "" {
			t.Fatalf("pod %s is repelled by %s after concurrent ops", id, blocker)
		}
		for _, term := range st.pod.AntiAffinity {
			if blocker := firstInDomain(s.pods, s.nodes, id, st.node,
				parseTopology(term.Topology),
				func(labels map[string]string) bool { return matches(term.Selector, labels) }); blocker != "" {
				t.Fatalf("pod %s anti-affinity blocked by %s", id, blocker)
			}
		}
	}
}
