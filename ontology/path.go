package ontology

import (
	"container/heap"
	"fmt"
)

type PathStatus int

const (
	PathUnreachable PathStatus = iota
	PathReachable
	PathAmbiguous
)

type CoverageBasis int

const (
	CoverageDefaultDeny CoverageBasis = iota
	CoverageLinkLayer
	CoverageObjectLayer
)

type CoverageTrace struct {
	LinkID          string
	LinkTypeID      string
	SourceID        string
	SourceTypeID    string
	TargetID        string
	TargetTypeID    string
	Decision        Decision
	Basis           CoverageBasis
	WinningPriority int
}

type QueryMetrics struct {
	EvaluatedLinks       int
	EvaluatedLinkTypes   int
	EvaluatedObjectTypes int
}

type PathResult struct {
	Status    PathStatus
	TotalCost int
	ObjectIDs []string
	LinkIDs   []string
	traces    []CoverageTrace
	metrics   QueryMetrics
}

type traversalEvaluator struct {
	subject     subjectSnapshot
	linkTypes   map[string]struct{}
	objectTypes map[string]struct{}
	evaluated   int
}

func (a *Authorizer) EvaluateTraversal(graph *Graph, subjectID string, linkID string) (CoverageTrace, error) {
	if !validID(subjectID) {
		return CoverageTrace{}, ErrInvalidSubject
	}
	if graph == nil || !validID(linkID) {
		return CoverageTrace{}, fmt.Errorf("ontology: invalid traversal evaluation")
	}
	edge, exists := graph.links[linkID]
	if !exists {
		return CoverageTrace{}, fmt.Errorf("ontology: link %q does not exist", linkID)
	}
	return evaluateCoverage(edge, a.currentSnapshot().subjects[subjectID]), nil
}

func (a *Authorizer) ShortestPath(graph *Graph, subjectID string, sourceID string, targetID string) (*PathResult, error) {
	if !validID(subjectID) {
		return nil, ErrInvalidSubject
	}
	if graph == nil || !validID(sourceID) || !validID(targetID) {
		return nil, fmt.Errorf("ontology: invalid shortest path query")
	}
	if _, exists := graph.objects[sourceID]; !exists {
		return nil, fmt.Errorf("ontology: source object %q does not exist", sourceID)
	}
	if _, exists := graph.objects[targetID]; !exists {
		return nil, fmt.Errorf("ontology: target object %q does not exist", targetID)
	}
	if sourceID == targetID {
		return &PathResult{Status: PathReachable, ObjectIDs: []string{sourceID}}, nil
	}

	return shortestPathWithSnapshot(a.currentSnapshot(), graph, subjectID, sourceID, targetID)
}

func shortestPathWithSnapshot(snapshot *policySnapshot, graph *Graph, subjectID string, sourceID string, targetID string) (*PathResult, error) {
	evaluator := &traversalEvaluator{
		subject:     snapshot.subjects[subjectID],
		linkTypes:   make(map[string]struct{}),
		objectTypes: make(map[string]struct{}),
	}
	distance := map[string]int{sourceID: 0}
	best := map[string]*searchNode{sourceID: {objectIDs: []string{sourceID}}}
	queue := &pathQueue{{objectID: sourceID, objectIDs: []string{sourceID}}}
	heap.Init(queue)

	for queue.Len() > 0 {
		item := heap.Pop(queue).(*pathQueueItem)
		current := best[item.objectID]
		if current == nil || item.cost > distance[item.objectID] {
			continue
		}
		if item.objectID == targetID {
			return current.result(evaluator), nil
		}

		for _, linkID := range graph.out[item.objectID] {
			edge := graph.links[linkID]
			trace := evaluator.evaluate(edge)
			if trace.Decision != DecisionAllow {
				continue
			}
			nextID := edge.link.TargetID
			candidate := current.extend(edge, trace)
			oldCost, seen := distance[nextID]
			if !seen || candidate.totalCost < oldCost ||
				(candidate.totalCost == oldCost && lexLess(candidate.objectIDs, best[nextID].objectIDs)) {
				distance[nextID] = candidate.totalCost
				best[nextID] = candidate
				heap.Push(queue, &pathQueueItem{objectID: nextID, cost: candidate.totalCost, objectIDs: append([]string(nil), candidate.objectIDs...)})
			}
		}
	}

	return &PathResult{Status: PathUnreachable, metrics: evaluator.metrics()}, nil
}

func (e *traversalEvaluator) evaluate(edge graphLink) CoverageTrace {
	trace := evaluateCoverage(edge, e.subject)
	e.evaluated++
	e.linkTypes[trace.LinkTypeID] = struct{}{}
	if trace.Basis == CoverageObjectLayer {
		e.objectTypes[trace.SourceTypeID] = struct{}{}
		e.objectTypes[trace.TargetTypeID] = struct{}{}
	}
	return trace
}

func (e *traversalEvaluator) metrics() QueryMetrics {
	return QueryMetrics{
		EvaluatedLinks:       e.evaluated,
		EvaluatedLinkTypes:   len(e.linkTypes),
		EvaluatedObjectTypes: len(e.objectTypes),
	}
}

func evaluateCoverage(edge graphLink, subject subjectSnapshot) CoverageTrace {
	trace := CoverageTrace{
		LinkID: edge.link.ID, LinkTypeID: edge.link.TypeID,
		SourceID: edge.link.SourceID, TargetID: edge.link.TargetID,
		SourceTypeID: edge.source, TargetTypeID: edge.target,
		Decision: DecisionDeny, Basis: CoverageDefaultDeny,
	}

	if rule, exists := subject.links[edge.link.TypeID]; exists {
		trace.Decision, trace.Basis, trace.WinningPriority = rule.decision, CoverageLinkLayer, rule.priority
		return trace
	}

	sourceRule, sourceExists := subject.objects[edge.source]
	targetRule, targetExists := subject.objects[edge.target]
	switch {
	case sourceExists && targetExists:
		if sourceRule.priority > targetRule.priority {
			trace.Decision, trace.WinningPriority = sourceRule.decision, sourceRule.priority
		} else if targetRule.priority > sourceRule.priority {
			trace.Decision, trace.WinningPriority = targetRule.decision, targetRule.priority
		} else {
			trace.Decision, trace.WinningPriority = mergeDecision(sourceRule.decision, targetRule.decision), sourceRule.priority
		}
	case sourceExists:
		trace.Decision, trace.WinningPriority = sourceRule.decision, sourceRule.priority
	case targetExists:
		trace.Decision, trace.WinningPriority = targetRule.decision, targetRule.priority
	}
	if sourceExists || targetExists {
		trace.Basis = CoverageObjectLayer
	}
	return trace
}

func mergeDecision(left Decision, right Decision) Decision {
	if left == DecisionDeny || right == DecisionDeny {
		return DecisionDeny
	}
	return DecisionAllow
}

type searchNode struct {
	totalCost int
	objectIDs []string
	linkIDs   []string
	traces    []CoverageTrace
}

func (n *searchNode) extend(edge graphLink, trace CoverageTrace) *searchNode {
	return &searchNode{
		totalCost: n.totalCost + edge.link.Cost,
		objectIDs: append(append([]string(nil), n.objectIDs...), edge.link.TargetID),
		linkIDs:   append(append([]string(nil), n.linkIDs...), edge.link.ID),
		traces:    append(append([]CoverageTrace(nil), n.traces...), trace),
	}
}

func (n *searchNode) result(evaluator *traversalEvaluator) *PathResult {
	return &PathResult{
		Status:    PathReachable,
		TotalCost: n.totalCost,
		ObjectIDs: append([]string(nil), n.objectIDs...),
		LinkIDs:   append([]string(nil), n.linkIDs...),
		traces:    append([]CoverageTrace(nil), n.traces...),
		metrics:   evaluator.metrics(),
	}
}

type pathQueueItem struct {
	objectID  string
	cost      int
	objectIDs []string
	index     int
}

type pathQueue []*pathQueueItem

func (q pathQueue) Len() int { return len(q) }
func (q pathQueue) Less(i, j int) bool {
	if q[i].cost != q[j].cost {
		return q[i].cost < q[j].cost
	}
	return lexLess(q[i].objectIDs, q[j].objectIDs)
}
func (q pathQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index = i
	q[j].index = j
}
func (q *pathQueue) Push(value any) {
	item := value.(*pathQueueItem)
	item.index = len(*q)
	*q = append(*q, item)
}
func (q *pathQueue) Pop() any {
	old := *q
	item := old[len(old)-1]
	*q = old[:len(old)-1]
	return item
}

func lexLess(left []string, right []string) bool {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for i := 0; i < limit; i++ {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	return len(left) < len(right)
}
