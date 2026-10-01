package placement

import "testing"

func TestRelabelRejectionAllowedAndExemptionChange(t *testing.T) {
	s := setupZones()
	gate := &Pod{ID: "gate", Labels: testLabels("role", "gate"),
		AntiAffinity: []AntiAffinityTerm{{Selector: testLabels("app", "db"), Topology: "zone"}}}
	if err := s.Place(gate, "n1"); err != nil {
		t.Fatal(err)
	}
	target := &Pod{ID: "target", Labels: testLabels("app", "other")}
	if err := s.Place(target, "n1"); err != nil {
		t.Fatal(err)
	}

	// Invalid args precede existence, which precedes the repulsion check.
	mustReject(t, s.Relabel("target", map[string]string{"": "v"}), ReasonInvalidArgs)
	mustReject(t, s.Relabel("missing", map[string]string{}), ReasonPodNotFound)
	rj := mustReject(t, s.Relabel("target", testLabels("app", "db")), ReasonRepelled)
	if rj.BlockingPod != "gate" {
		t.Fatalf("blocker = %q", rj.BlockingPod)
	}
	// A rejected relabel changes no state: the old labels remain and a pod that
	// only conflicts with the rejected labels can still be placed alongside it.
	if err := s.Place(&Pod{ID: "probe", Labels: testLabels("app", "db")}, "n3"); err != nil {
		t.Fatal(err)
	}

	// Moving the conflicting label to a pod in another zone is allowed because
	// gate's zone domain only covers n1/n2.
	if err := s.Relabel("probe", testLabels("app", "other2")); err != nil {
		t.Fatalf("relabel in another zone must be allowed: %v", err)
	}

	// Reservation status does not exempt a pod from relabel checks.
	if err := s.Reserve(&Pod{ID: "res", Labels: testLabels("app", "other")}, "n1"); err != nil {
		t.Fatal(err)
	}
	mustReject(t, s.Relabel("res", testLabels("app", "db")), ReasonRepelled)

	// Exemption state changes after relabel. While the sole cluster-wide pod
	// carrying marker=1 exists, no new pod gets the first-member exemption.
	// Relabeling that pod away removes every cluster matcher, so exemption
	// returns without removing the pod from its node.
	sole := &Pod{ID: "sole", Labels: testLabels("marker", "1")}
	if err := s.Place(sole, "n3"); err != nil {
		t.Fatal(err)
	}
	join := &Pod{ID: "join", Labels: testLabels("marker", "1"),
		Affinity: []AffinityTerm{{Selector: testLabels("marker", "1"), Topology: "node", MinMatching: 2}}}
	if got, _ := s.Feasible(join); len(got) != 0 {
		t.Fatalf("cluster matcher exists, no exemption: %v", got)
	}
	if err := s.Relabel("sole", testLabels("marker", "2")); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Feasible(join); len(got) != 3 {
		t.Fatalf("exemption must return after the only matcher relabels away: %v", got)
	}

	// Affinity is not re-checked: relabel the sole worker away after a pod
	// already relied on it; the dependent pod remains placed.
	worker := &Pod{ID: "worker", Labels: testLabels("tier", "worker")}
	if err := s.Place(worker, "n3"); err != nil {
		t.Fatal(err)
	}
	if err := s.Place(&Pod{ID: "need", Labels: testLabels("tier", "need"),
		Affinity: []AffinityTerm{{Selector: testLabels("tier", "worker"), Topology: "node", MinMatching: 1}}}, "n3"); err != nil {
		t.Fatal(err)
	}
	if err := s.Relabel("worker", testLabels("tier", "gone")); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleErrors(t *testing.T) {
	s := setupZones()
	mustReject(t, s.Commit("nope"), ReasonPodNotFound)
	mustReject(t, s.Cancel("nope"), ReasonPodNotFound)
	mustReject(t, s.Remove("nope"), ReasonPodNotFound)

	if err := s.Place(&Pod{ID: "p", Labels: map[string]string{}}, "n1"); err != nil {
		t.Fatal(err)
	}
	mustReject(t, s.Commit("p"), ReasonNotReserved)
	mustReject(t, s.Cancel("p"), ReasonNotReserved)

	if err := s.Reserve(&Pod{ID: "r", Labels: map[string]string{}}, "n2"); err != nil {
		t.Fatal(err)
	}
	mustReject(t, s.Remove("r"), ReasonStillReserved)
	if err := s.Cancel("r"); err != nil {
		t.Fatal(err)
	}
	mustReject(t, s.Commit("r"), ReasonPodNotFound)

	mustReject(t, s.AddNode("", "z"), ReasonInvalidArgs)
	mustReject(t, s.AddNode("n1", ""), ReasonInvalidArgs)
	mustReject(t, s.AddNode("n1", "c"), ReasonNodeExists)
	mustReject(t, s.RemoveNode("nope"), ReasonNodeNotFound)
	mustReject(t, s.RemoveNode("n1"), ReasonNodeNotEmpty)
	if err := s.Remove("p"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveNode("n1"); err != nil {
		t.Fatalf("empty node must be removable: %v", err)
	}
}

func TestFeasibleRejections(t *testing.T) {
	s := setupZones()
	if err := s.Place(&Pod{ID: "p", Labels: map[string]string{}}, "n1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Feasible(nil); err.(*Reject).Code != ReasonInvalidArgs {
		t.Fatalf("nil pod = %v", err)
	}
	if _, err := s.Feasible(&Pod{ID: "", Labels: map[string]string{}}); err.(*Reject).Code != ReasonInvalidArgs {
		t.Fatalf("empty id = %v", err)
	}
	if _, err := s.Feasible(&Pod{ID: "p", Labels: map[string]string{}}); err.(*Reject).Code != ReasonPodExists {
		t.Fatalf("existing id = %v", err)
	}
}
