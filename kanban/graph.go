package kanban

import "sort"

type dependencyGraph struct {
	prerequisites map[string]map[string]struct{}
	dependents    map[string]map[string]struct{}
}

func newDependencyGraph() *dependencyGraph {
	return &dependencyGraph{
		prerequisites: make(map[string]map[string]struct{}),
		dependents:    make(map[string]map[string]struct{}),
	}
}

func (g *dependencyGraph) addCard(id string) {
	if _, ok := g.prerequisites[id]; !ok {
		g.prerequisites[id] = make(map[string]struct{})
	}
	if _, ok := g.dependents[id]; !ok {
		g.dependents[id] = make(map[string]struct{})
	}
}

func (g *dependencyGraph) hasEdge(cardID, prerequisiteID string) bool {
	prerequisites, ok := g.prerequisites[cardID]
	if !ok {
		return false
	}
	_, ok = prerequisites[prerequisiteID]
	return ok
}

func (g *dependencyGraph) addEdge(cardID, prerequisiteID string) {
	g.addCard(cardID)
	g.addCard(prerequisiteID)
	g.prerequisites[cardID][prerequisiteID] = struct{}{}
	g.dependents[prerequisiteID][cardID] = struct{}{}
}

func (g *dependencyGraph) removeEdge(cardID, prerequisiteID string) {
	delete(g.prerequisites[cardID], prerequisiteID)
	delete(g.dependents[prerequisiteID], cardID)
}

func (g *dependencyGraph) createsCycle(cardID, prerequisiteID string) bool {
	if cardID == prerequisiteID {
		return true
	}
	visited := make(map[string]bool)
	stack := []string{prerequisiteID}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current == cardID {
			return true
		}
		if visited[current] {
			continue
		}
		visited[current] = true
		for prerequisite := range g.prerequisites[current] {
			stack = append(stack, prerequisite)
		}
	}
	return false
}

func (g *dependencyGraph) incompletePrerequisites(cardID string, completed func(string) bool) []string {
	var incomplete []string
	for prerequisiteID := range g.prerequisites[cardID] {
		if !completed(prerequisiteID) {
			incomplete = append(incomplete, prerequisiteID)
		}
	}
	sort.Strings(incomplete)
	return incomplete
}

func (g *dependencyGraph) hasActiveDependent(cardID string, active func(string) bool) bool {
	for dependentID := range g.dependents[cardID] {
		if active(dependentID) {
			return true
		}
	}
	return false
}
