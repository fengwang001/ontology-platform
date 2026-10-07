package ontology

import (
	"strings"
	"testing"
)

func testGraph(t *testing.T) *Graph {
	t.Helper()
	graph, err := NewGraph(
		[]Object{{ID: "s", TypeID: "S"}, {ID: "a", TypeID: "A"}, {ID: "b", TypeID: "B"}, {ID: "t", TypeID: "T"}},
		[]Link{
			{ID: "allow", TypeID: "ExplicitAllow", SourceID: "s", TargetID: "a", Cost: 1},
			{ID: "deny", TypeID: "ExplicitDeny", SourceID: "s", TargetID: "b", Cost: 1},
			{ID: "fallback", TypeID: "Fallback", SourceID: "s", TargetID: "t", Cost: 1},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func TestLinkLayerOverridesObjectLayer(t *testing.T) {
	graph := testGraph(t)
	policy := mustPolicy(t,
		[]Group{{ID: "g", Priority: 1}},
		[]Membership{{SubjectID: "user", GroupID: "g"}},
		[]ObjectDeclaration{
			{GroupID: "g", ObjectTypeID: "S", Decision: DecisionAllow},
			{GroupID: "g", ObjectTypeID: "A", Decision: DecisionDeny},
			{GroupID: "g", ObjectTypeID: "B", Decision: DecisionAllow},
			{GroupID: "g", ObjectTypeID: "T", Decision: DecisionAllow},
		},
		[]LinkDeclaration{
			{GroupID: "g", LinkTypeID: "ExplicitAllow", Decision: DecisionAllow},
			{GroupID: "g", LinkTypeID: "ExplicitDeny", Decision: DecisionDeny},
		},
	)
	authorizer, err := NewAuthorizer(policy)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		linkID   string
		decision Decision
		basis    CoverageBasis
	}{
		{linkID: "allow", decision: DecisionAllow, basis: CoverageLinkLayer},
		{linkID: "deny", decision: DecisionDeny, basis: CoverageLinkLayer},
		{linkID: "fallback", decision: DecisionAllow, basis: CoverageObjectLayer},
	}
	for _, tt := range tests {
		trace, err := authorizer.EvaluateTraversal(graph, "user", tt.linkID)
		if err != nil {
			t.Fatal(err)
		}
		if trace.Decision != tt.decision || trace.Basis != tt.basis {
			t.Fatalf("%s: trace=%+v, want %s/%d", tt.linkID, trace, tt.decision, tt.basis)
		}
	}
}

func TestEqualPriorityConflictUsesDeny(t *testing.T) {
	graph, _ := NewGraph(
		[]Object{{ID: "s", TypeID: "S"}, {ID: "o", TypeID: "O"}},
		[]Link{{ID: "edge", TypeID: "L", SourceID: "s", TargetID: "o", Cost: 1}},
	)
	policy := mustPolicy(t,
		[]Group{{ID: "allow", Priority: 5}, {ID: "deny", Priority: 5}},
		[]Membership{{SubjectID: "user", GroupID: "allow"}, {SubjectID: "user", GroupID: "deny"}},
		[]ObjectDeclaration{
			{GroupID: "allow", ObjectTypeID: "S", Decision: DecisionAllow},
			{GroupID: "allow", ObjectTypeID: "O", Decision: DecisionAllow},
			{GroupID: "deny", ObjectTypeID: "O", Decision: DecisionDeny},
		},
		[]LinkDeclaration{
			{GroupID: "allow", LinkTypeID: "L", Decision: DecisionAllow},
			{GroupID: "deny", LinkTypeID: "L", Decision: DecisionDeny},
		},
	)
	authorizer, _ := NewAuthorizer(policy)
	trace, err := authorizer.EvaluateTraversal(graph, "user", "edge")
	if err != nil {
		t.Fatal(err)
	}
	if trace.Decision != DecisionDeny || trace.Basis != CoverageLinkLayer || trace.WinningPriority != 5 {
		t.Fatalf("trace=%+v, want link-layer deny priority 5", trace)
	}
}

func TestObjectEqualPriorityConflictUsesDeny(t *testing.T) {
	graph, _ := NewGraph(
		[]Object{{ID: "s", TypeID: "S"}, {ID: "o", TypeID: "O"}},
		[]Link{{ID: "edge", TypeID: "L", SourceID: "s", TargetID: "o", Cost: 1}},
	)
	policy := mustPolicy(t,
		[]Group{{ID: "allow", Priority: 5}, {ID: "deny", Priority: 5}},
		[]Membership{{SubjectID: "user", GroupID: "allow"}, {SubjectID: "user", GroupID: "deny"}},
		[]ObjectDeclaration{
			{GroupID: "allow", ObjectTypeID: "S", Decision: DecisionAllow},
			{GroupID: "allow", ObjectTypeID: "O", Decision: DecisionAllow},
			{GroupID: "deny", ObjectTypeID: "O", Decision: DecisionDeny},
		},
		nil,
	)
	authorizer, _ := NewAuthorizer(policy)
	trace, err := authorizer.EvaluateTraversal(graph, "user", "edge")
	if err != nil {
		t.Fatal(err)
	}
	if trace.Decision != DecisionDeny || trace.Basis != CoverageObjectLayer {
		t.Fatalf("trace=%+v, want object deny-priority merge", trace)
	}
}

func TestHigherPriorityWins(t *testing.T) {
	graph, _ := NewGraph(
		[]Object{{ID: "s", TypeID: "S"}, {ID: "o", TypeID: "O"}},
		[]Link{{ID: "edge", TypeID: "L", SourceID: "s", TargetID: "o", Cost: 1}},
	)
	policy := mustPolicy(t,
		[]Group{{ID: "low", Priority: 1}, {ID: "high", Priority: 9}},
		[]Membership{{SubjectID: "user", GroupID: "low"}, {SubjectID: "user", GroupID: "high"}},
		[]ObjectDeclaration{
			{GroupID: "low", ObjectTypeID: "S", Decision: DecisionAllow},
			{GroupID: "low", ObjectTypeID: "O", Decision: DecisionAllow},
			{GroupID: "high", ObjectTypeID: "O", Decision: DecisionDeny},
		},
		nil,
	)
	authorizer, _ := NewAuthorizer(policy)
	trace, err := authorizer.EvaluateTraversal(graph, "user", "edge")
	if err != nil {
		t.Fatal(err)
	}
	if trace.Decision != DecisionDeny || trace.WinningPriority != 9 {
		t.Fatalf("trace=%+v, want high-priority deny", trace)
	}
}

func TestDefaultDeny(t *testing.T) {
	graph := testGraph(t)
	authorizer, _ := NewAuthorizer(mustPolicy(t, nil, nil, nil, nil))
	trace, err := authorizer.EvaluateTraversal(graph, "user", "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if trace.Decision != DecisionDeny || trace.Basis != CoverageDefaultDeny {
		t.Fatalf("trace=%+v, want default deny", trace)
	}
}

func TestInvalidSubject(t *testing.T) {
	graph := testGraph(t)
	authorizer, _ := NewAuthorizer(mustPolicy(t, nil, nil, nil, nil))
	if _, err := authorizer.ShortestPath(graph, "", "s", "t"); err != ErrInvalidSubject {
		t.Fatalf("err=%v, want ErrInvalidSubject", err)
	}
}

func TestShortestPathCostAndLexicographicTieBreak(t *testing.T) {
	graph, err := NewGraph(
		[]Object{{ID: "s", TypeID: "S"}, {ID: "a", TypeID: "X"}, {ID: "b", TypeID: "X"}, {ID: "t", TypeID: "T"}},
		[]Link{
			{ID: "direct", TypeID: "L", SourceID: "s", TargetID: "t", Cost: 5},
			{ID: "via-a", TypeID: "L", SourceID: "s", TargetID: "a", Cost: 2},
			{ID: "via-b", TypeID: "L", SourceID: "s", TargetID: "b", Cost: 2},
			{ID: "a-t", TypeID: "L", SourceID: "a", TargetID: "t", Cost: 2},
			{ID: "b-t", TypeID: "L", SourceID: "b", TargetID: "t", Cost: 2},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	policy := mustPolicy(t,
		[]Group{{ID: "g", Priority: 1}},
		[]Membership{{SubjectID: "user", GroupID: "g"}},
		nil,
		[]LinkDeclaration{{GroupID: "g", LinkTypeID: "L", Decision: DecisionAllow}},
	)
	authorizer, _ := NewAuthorizer(policy)
	result, err := authorizer.ShortestPath(graph, "user", "s", "t")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"s", "a", "t"}
	if result.Status != PathReachable || result.TotalCost != 4 || strings.Join(result.ObjectIDs, ",") != strings.Join(want, ",") {
		t.Fatalf("result=%+v, want cost 4 path %v", result, want)
	}
}

func TestUnreachableWhenDenied(t *testing.T) {
	graph, _ := NewGraph(
		[]Object{{ID: "s", TypeID: "S"}, {ID: "t", TypeID: "T"}},
		[]Link{{ID: "edge", TypeID: "L", SourceID: "s", TargetID: "t", Cost: 1}},
	)
	authorizer, _ := NewAuthorizer(mustPolicy(t, nil, nil, nil, nil))
	result, err := authorizer.ShortestPath(graph, "user", "s", "t")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != PathUnreachable {
		t.Fatalf("status=%d, want unreachable", result.Status)
	}
}

func TestUnreachableAndAmbiguousAreDistinctResults(t *testing.T) {
	if PathUnreachable == PathAmbiguous {
		t.Fatal("unreachable and ambiguous coverage must use distinct statuses")
	}
}

func mustPolicy(t *testing.T, groups []Group, memberships []Membership, objects []ObjectDeclaration, links []LinkDeclaration) *Policy {
	t.Helper()
	policy, err := NewPolicy(groups, memberships, objects, links)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}
