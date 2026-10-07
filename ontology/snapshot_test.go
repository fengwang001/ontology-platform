package ontology

import "testing"

func TestQuerySnapshotIgnoresPolicyChangesAfterStart(t *testing.T) {
	graph, _ := NewGraph(
		[]Object{{ID: "s", TypeID: "S"}, {ID: "t", TypeID: "T"}},
		[]Link{{ID: "edge", TypeID: "L", SourceID: "s", TargetID: "t", Cost: 1}},
	)
	oldPolicy := mustPolicy(t,
		[]Group{{ID: "g", Priority: 1}},
		[]Membership{{SubjectID: "user", GroupID: "g"}},
		nil,
		[]LinkDeclaration{{GroupID: "g", LinkTypeID: "L", Decision: DecisionAllow}},
	)
	authorizer, _ := NewAuthorizer(oldPolicy)
	snapshot := authorizer.currentSnapshot()

	newPolicy := mustPolicy(t,
		[]Group{{ID: "g", Priority: 1}},
		[]Membership{{SubjectID: "user", GroupID: "g"}},
		nil,
		[]LinkDeclaration{{GroupID: "g", LinkTypeID: "L", Decision: DecisionDeny}},
	)
	if err := authorizer.UpdatePolicy(newPolicy); err != nil {
		t.Fatal(err)
	}

	fixed, err := shortestPathWithSnapshot(snapshot, graph, "user", "s", "t")
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Status != PathReachable {
		t.Fatalf("fixed status=%d, want snapshot allow", fixed.Status)
	}
	current, err := authorizer.ShortestPath(graph, "user", "s", "t")
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != PathUnreachable {
		t.Fatalf("current status=%d, want updated deny", current.Status)
	}
}

func TestMetricsDoNotGrowWithUnrelatedGraphOrGroups(t *testing.T) {
	graph, _ := NewGraph(
		[]Object{{ID: "s", TypeID: "S"}, {ID: "t", TypeID: "T"}},
		[]Link{{ID: "edge", TypeID: "L", SourceID: "s", TargetID: "t", Cost: 1}},
	)
	groups := []Group{{ID: "g", Priority: 1}}
	linkDeclarations := []LinkDeclaration{{GroupID: "g", LinkTypeID: "L", Decision: DecisionAllow}}
	for i := 0; i < 32; i++ {
		groupID := "noise-" + string(rune('a'+i))
		groups = append(groups, Group{ID: groupID, Priority: i})
		linkDeclarations = append(linkDeclarations, LinkDeclaration{
			GroupID: groupID, LinkTypeID: "UnrelatedLink", Decision: DecisionDeny,
		})
	}
	policy := mustPolicy(t, groups, []Membership{{SubjectID: "user", GroupID: "g"}}, nil, linkDeclarations)
	authorizer, _ := NewAuthorizer(policy)
	result, err := authorizer.ShortestPath(graph, "user", "s", "t")
	if err != nil {
		t.Fatal(err)
	}
	want := QueryMetrics{EvaluatedLinks: 1, EvaluatedLinkTypes: 1, EvaluatedObjectTypes: 0}
	if result.metrics != want {
		t.Fatalf("metrics=%+v, want %+v", result.metrics, want)
	}
}

func TestConcurrentQueriesAndPolicyUpdates(t *testing.T) {
	graph, _ := NewGraph(
		[]Object{{ID: "s", TypeID: "S"}, {ID: "t", TypeID: "T"}},
		[]Link{{ID: "edge", TypeID: "L", SourceID: "s", TargetID: "t", Cost: 1}},
	)
	allowPolicy := mustPolicy(t,
		[]Group{{ID: "g", Priority: 1}},
		[]Membership{{SubjectID: "user", GroupID: "g"}},
		nil,
		[]LinkDeclaration{{GroupID: "g", LinkTypeID: "L", Decision: DecisionAllow}},
	)
	denyPolicy := mustPolicy(t,
		[]Group{{ID: "g", Priority: 1}},
		[]Membership{{SubjectID: "user", GroupID: "g"}},
		nil,
		[]LinkDeclaration{{GroupID: "g", LinkTypeID: "L", Decision: DecisionDeny}},
	)
	authorizer, _ := NewAuthorizer(allowPolicy)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			if i%2 == 0 {
				_ = authorizer.UpdatePolicy(denyPolicy)
			} else {
				_ = authorizer.UpdatePolicy(allowPolicy)
			}
		}
	}()

	for i := 0; i < 100; i++ {
		result, err := authorizer.ShortestPath(graph, "user", "s", "t")
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != PathReachable && result.Status != PathUnreachable {
			t.Fatalf("status=%d, want a serializable reachable/unreachable result", result.Status)
		}
	}
	<-done
}
