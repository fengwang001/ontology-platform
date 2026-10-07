package ontology

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type naiveCandidate struct {
	decision Decision
	priority int
}

type naiveCoverageResult struct {
	decision Decision
	basis    CoverageBasis
	priority int
}

func TestRandomNaiveOracleComparison(t *testing.T) {
	random := rand.New(rand.NewSource(2727))
	for iteration := 0; iteration < 160; iteration++ {
		objects, links, graph := randomGraph(t, random)
		groups := []Group{{ID: "g1", Priority: random.Intn(3)}, {ID: "g2", Priority: random.Intn(3)}}
		memberships := []Membership{{SubjectID: "user", GroupID: "g1"}}
		if random.Intn(2) == 0 {
			memberships = append(memberships, Membership{SubjectID: "user", GroupID: "g2"})
		}
		objectDeclarations := randomObjectDeclarations(random, groups)
		linkDeclarations := randomLinkDeclarations(random, groups)
		policy := mustPolicy(t, groups, memberships, objectDeclarations, linkDeclarations)
		authorizer, _ := NewAuthorizer(policy)

		for _, link := range links {
			got, err := authorizer.EvaluateTraversal(graph, "user", link.ID)
			if err != nil {
				t.Fatalf("iteration %d link %s: %v", iteration, link.ID, err)
			}
			want := naiveEvaluate(groups, memberships, objectDeclarations, linkDeclarations, graph, link.ID)
			if got.Decision != want.decision || got.Basis != want.basis || got.WinningPriority != want.priority {
				t.Fatalf("iteration %d link %s got=(%s,%d,%d) want=(%s,%d,%d)",
					iteration, link.ID, got.Decision, got.Basis, got.WinningPriority,
					want.decision, want.basis, want.priority)
			}
		}

		source := objects[0].ID
		target := objects[len(objects)-1].ID
		got, err := authorizer.ShortestPath(graph, "user", source, target)
		if err != nil {
			t.Fatalf("iteration %d path: %v", iteration, err)
		}
		wantObjects, wantCost := naiveShortestPath(groups, memberships, objectDeclarations, linkDeclarations, graph, source, target)
		if (wantObjects == nil) != (got.Status == PathUnreachable) {
			t.Fatalf("iteration %d status got=%+v want=%v", iteration, got, wantObjects)
		}
		if wantObjects != nil {
			if got.TotalCost != wantCost || !reflect.DeepEqual(got.ObjectIDs, wantObjects) {
				t.Fatalf("iteration %d path got=%v/%d want=%v/%d", iteration, got.ObjectIDs, got.TotalCost, wantObjects, wantCost)
			}
			for index, linkID := range got.LinkIDs {
				trace := got.traces[index]
				want := naiveEvaluate(groups, memberships, objectDeclarations, linkDeclarations, graph, linkID)
				t.Logf("iteration=%d input subject=user link=%s source=%s target=%s output decision=%s basis=%d priority=%d",
					iteration, linkID, trace.SourceID, trace.TargetID, trace.Decision, trace.Basis, trace.WinningPriority)
				if trace.Decision != want.decision || trace.Basis != want.basis || trace.WinningPriority != want.priority {
					t.Fatalf("iteration %d trace mismatch link=%s", iteration, linkID)
				}
			}
		}
		t.Logf("iteration=%d input objects=%v links=%v groups=%+v memberships=%+v output objects=%v cost=%d",
			iteration, objects, links, groups, memberships, got.ObjectIDs, got.TotalCost)
	}
}

func randomGraph(t *testing.T, random *rand.Rand) ([]Object, []Link, *Graph) {
	t.Helper()
	objectCount := 2 + random.Intn(5)
	objects := make([]Object, objectCount)
	for i := range objects {
		objects[i] = Object{ID: "o" + string(rune('a'+i)), TypeID: "t" + string(rune('0'+random.Intn(3)))}
	}
	var links []Link
	for source := range objects {
		for target := range objects {
			if source != target && random.Intn(3) == 0 {
				links = append(links, Link{
					ID:       "l" + string(rune('a'+source)) + string(rune('a'+target)),
					TypeID:   "lt" + string(rune('0'+random.Intn(3))),
					SourceID: objects[source].ID, TargetID: objects[target].ID,
					Cost: random.Intn(4),
				})
			}
		}
	}
	graph, err := NewGraph(objects, links)
	if err != nil {
		t.Fatal(err)
	}
	return objects, links, graph
}

func randomObjectDeclarations(random *rand.Rand, groups []Group) []ObjectDeclaration {
	seen := make(map[string]bool)
	var result []ObjectDeclaration
	for _, group := range groups {
		for typeIndex := 0; typeIndex < 3; typeIndex++ {
			if random.Intn(2) == 0 {
				typeID := "t" + string(rune('0'+typeIndex))
				key := group.ID + typeID
				if seen[key] {
					continue
				}
				seen[key] = true
				result = append(result, ObjectDeclaration{GroupID: group.ID, ObjectTypeID: typeID, Decision: Decision(random.Intn(2) + 1)})
			}
		}
	}
	return result
}

func randomLinkDeclarations(random *rand.Rand, groups []Group) []LinkDeclaration {
	seen := make(map[string]bool)
	var result []LinkDeclaration
	for _, group := range groups {
		for typeIndex := 0; typeIndex < 3; typeIndex++ {
			if random.Intn(2) == 0 {
				typeID := "lt" + string(rune('0'+typeIndex))
				key := group.ID + typeID
				if seen[key] {
					continue
				}
				seen[key] = true
				result = append(result, LinkDeclaration{GroupID: group.ID, LinkTypeID: typeID, Decision: Decision(random.Intn(2) + 1)})
			}
		}
	}
	return result
}

func naiveEvaluate(groups []Group, memberships []Membership, objects []ObjectDeclaration, links []LinkDeclaration, graph *Graph, linkID string) naiveCoverageResult {
	edge := graph.links[linkID]
	priorityByGroup := make(map[string]int)
	for _, group := range groups {
		priorityByGroup[group.ID] = group.Priority
	}
	subjectGroups := make(map[string]bool)
	for _, membership := range memberships {
		if membership.SubjectID == "user" {
			subjectGroups[membership.GroupID] = true
		}
	}

	var linkCandidates []naiveCandidate
	for _, declaration := range links {
		if subjectGroups[declaration.GroupID] && declaration.LinkTypeID == edge.link.TypeID {
			linkCandidates = append(linkCandidates, naiveCandidate{declaration.Decision, priorityByGroup[declaration.GroupID]})
		}
	}
	if decision, priority, ok := highestMerge(linkCandidates); ok {
		return naiveCoverageResult{decision, CoverageLinkLayer, priority}
	}

	sourceCandidates := candidatesForObject(objects, subjectGroups, priorityByGroup, edge.source)
	targetCandidates := candidatesForObject(objects, subjectGroups, priorityByGroup, edge.target)
	sourceDecision, sourcePriority, sourceOK := highestMerge(sourceCandidates)
	targetDecision, targetPriority, targetOK := highestMerge(targetCandidates)
	switch {
	case sourceOK && targetOK:
		if sourcePriority > targetPriority {
			return naiveCoverageResult{sourceDecision, CoverageObjectLayer, sourcePriority}
		}
		if targetPriority > sourcePriority {
			return naiveCoverageResult{targetDecision, CoverageObjectLayer, targetPriority}
		}
		return naiveCoverageResult{mergeDecision(sourceDecision, targetDecision), CoverageObjectLayer, sourcePriority}
	case sourceOK:
		return naiveCoverageResult{sourceDecision, CoverageObjectLayer, sourcePriority}
	case targetOK:
		return naiveCoverageResult{targetDecision, CoverageObjectLayer, targetPriority}
	default:
		return naiveCoverageResult{DecisionDeny, CoverageDefaultDeny, 0}
	}
}

func candidatesForObject(declarations []ObjectDeclaration, groups map[string]bool, priorities map[string]int, typeID string) []naiveCandidate {
	var candidates []naiveCandidate
	for _, declaration := range declarations {
		if groups[declaration.GroupID] && declaration.ObjectTypeID == typeID {
			candidates = append(candidates, naiveCandidate{declaration.Decision, priorities[declaration.GroupID]})
		}
	}
	return candidates
}

func highestMerge(candidates []naiveCandidate) (Decision, int, bool) {
	if len(candidates) == 0 {
		return DecisionDeny, 0, false
	}
	highest := candidates[0].priority
	for _, candidate := range candidates[1:] {
		if candidate.priority > highest {
			highest = candidate.priority
		}
	}
	decision := DecisionAllow
	for _, candidate := range candidates {
		if candidate.priority == highest && candidate.decision == DecisionDeny {
			decision = DecisionDeny
		}
	}
	return decision, highest, true
}

func naiveShortestPath(groups []Group, memberships []Membership, objects []ObjectDeclaration, links []LinkDeclaration, graph *Graph, source string, target string) ([]string, int) {
	visited := map[string]bool{source: true}
	var bestObjects []string
	bestCost := 0
	var dfs func(string, []string, int)
	dfs = func(current string, path []string, cost int) {
		if current == target {
			if bestObjects == nil || cost < bestCost || (cost == bestCost && lexLess(path, bestObjects)) {
				bestObjects = append([]string(nil), path...)
				bestCost = cost
			}
			return
		}
		outgoing := append([]string(nil), graph.out[current]...)
		sort.Strings(outgoing)
		for _, linkID := range outgoing {
			edge := graph.links[linkID]
			if visited[edge.link.TargetID] {
				continue
			}
			result := naiveEvaluate(groups, memberships, objects, links, graph, linkID)
			if result.decision != DecisionAllow {
				continue
			}
			visited[edge.link.TargetID] = true
			dfs(edge.link.TargetID, append(path, edge.link.TargetID), cost+edge.link.Cost)
			visited[edge.link.TargetID] = false
		}
	}
	dfs(source, []string{source}, 0)
	return bestObjects, bestCost
}
