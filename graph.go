package incremental

import "sort"

type DependencyGraph struct {
	signatureDeps         edgeSet
	signatureReverse      edgeSet
	implementationDeps    edgeSet
	implementationReverse edgeSet
}

func NewDependencyGraph() *DependencyGraph {
	return &DependencyGraph{
		signatureDeps:         edgeSet{},
		signatureReverse:      edgeSet{},
		implementationDeps:    edgeSet{},
		implementationReverse: edgeSet{},
	}
}

type sccGroup struct {
	members []DeclID
}

type edgeSet map[DeclID]map[DeclID]struct{}

func (g *DependencyGraph) replaceSignatureDependencies(owner DeclID, deps map[DeclID]bool) {
	g.replaceEdges(g.signatureDeps, g.signatureReverse, owner, deps)
}

func (g *DependencyGraph) replaceImplementationDependencies(owner DeclID, deps map[DeclID]bool) {
	g.replaceEdges(g.implementationDeps, g.implementationReverse, owner, deps)
}

func (g *DependencyGraph) signatureDependents(id DeclID) []DeclID {
	return sortedKeys(g.signatureReverse[id])
}

func (g *DependencyGraph) implementationDependents(id DeclID) []DeclID {
	return sortedKeys(g.implementationReverse[id])
}

func (g *DependencyGraph) signatureClosure(seeds []DeclID) []DeclID {
	return closure(seeds, func(id DeclID) []DeclID { return sortedKeys(g.signatureReverse[id]) })
}

func (g *DependencyGraph) topologicalOrder(ids []DeclID) []DeclID {
	candidates := map[DeclID]bool{}
	for _, id := range ids {
		candidates[id] = true
	}
	components := g.componentsWith(candidates, nil, false)
	componentByID := map[DeclID]int{}
	componentEdges := make([]map[int]bool, len(components))
	inDegree := make([]int, len(components))
	for index, component := range components {
		componentEdges[index] = map[int]bool{}
		for _, id := range component {
			componentByID[id] = index
		}
	}
	for _, component := range components {
		from := componentByID[component[0]]
		for _, id := range component {
			for dep := range g.signatureDeps[id] {
				to, ok := componentByID[dep]
				if !ok || to == from || componentEdges[to][from] {
					continue
				}
				componentEdges[to][from] = true
				inDegree[from]++
			}
		}
	}
	readyComponents := []int{}
	for index, degree := range inDegree {
		if degree == 0 {
			readyComponents = append(readyComponents, index)
		}
	}
	sort.Slice(readyComponents, func(i, j int) bool {
		return components[readyComponents[i]][0] < components[readyComponents[j]][0]
	})
	ordered := []DeclID{}
	for len(readyComponents) > 0 {
		index := readyComponents[0]
		readyComponents = readyComponents[1:]
		ordered = append(ordered, components[index]...)
		nextComponents := []int{}
		for dependent := range componentEdges[index] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				nextComponents = append(nextComponents, dependent)
			}
		}
		sort.Slice(nextComponents, func(i, j int) bool {
			return components[nextComponents[i]][0] < components[nextComponents[j]][0]
		})
		readyComponents = append(readyComponents, nextComponents...)
		sort.Slice(readyComponents, func(i, j int) bool {
			return components[readyComponents[i]][0] < components[readyComponents[j]][0]
		})
	}
	return ordered
}

func (g *DependencyGraph) component(seeds []DeclID, allowed map[DeclID]bool) sccGroup {
	return g.componentWith(seeds, allowed, nil)
}

func (g *DependencyGraph) exactComponent(seed DeclID, allowed map[DeclID]bool) sccGroup {
	for _, component := range g.componentsWith(allowed, nil, false) {
		for _, id := range component {
			if id == seed {
				return sccGroup{members: component}
			}
		}
	}
	return sccGroup{members: []DeclID{seed}}
}

func (g *DependencyGraph) componentIn(seeds []DeclID, allowed map[DeclID]bool, extra map[DeclID]map[DeclID]bool) sccGroup {
	indices := map[DeclID]int{}
	low := map[DeclID]int{}
	onStack := map[DeclID]bool{}
	stack := []DeclID{}
	nextIndex := 0
	seedSet := map[DeclID]bool{}
	for _, id := range seeds {
		seedSet[id] = true
	}

	var visit func(DeclID)
	visit = func(id DeclID) {
		indices[id] = nextIndex
		low[id] = nextIndex
		nextIndex++
		stack = append(stack, id)
		onStack[id] = true

		dependencies := map[DeclID]bool{}
		if allowed[id] {
			for dep := range g.signatureDeps[id] {
				dependencies[dep] = true
			}
			for dep := range extra[id] {
				dependencies[dep] = true
			}
		}
		for dep := range dependencies {
			if !allowed[dep] {
				continue
			}
			if _, seen := indices[dep]; !seen {
				visit(dep)
				if low[dep] < low[id] {
					low[id] = low[dep]
				}
			} else if onStack[dep] && indices[dep] < low[id] {
				low[id] = indices[dep]
			}
		}

		if low[id] == indices[id] {
			component := map[DeclID]bool{}
			for {
				node := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[node] = false
				component[node] = true
				if node == id {
					break
				}
			}
			for seed := range seedSet {
				if component[seed] {
					return
				}
			}
		}
	}

	for _, seed := range sortedBoolKeys(seedSet) {
		if _, seen := indices[seed]; !seen {
			visit(seed)
		}
	}
	result := map[DeclID]bool{}
	for _, seed := range seeds {
		if component := g.componentWith([]DeclID{seed}, allowed, extra); len(component.members) > 0 {
			for _, id := range component.members {
				result[id] = true
			}
		}
	}
	return sccGroup{members: sortedBoolKeys(result)}
}

func (g *DependencyGraph) componentWith(seeds []DeclID, allowed map[DeclID]bool, extra map[DeclID]map[DeclID]bool) sccGroup {
	found := map[DeclID]bool{}
	allowedSet := cloneIDSet(allowed)
	for _, id := range seeds {
		allowedSet[id] = true
	}
	components := g.componentsWith(allowedSet, extra, false)
	for _, component := range components {
		componentSet := map[DeclID]bool{}
		for _, id := range component {
			componentSet[id] = true
		}
		for _, seed := range seeds {
			if componentSet[seed] {
				found[seed] = true
			}
		}
	}
	return sccGroup{members: sortedBoolKeys(found)}
}

func (g *DependencyGraph) componentsWith(allowed map[DeclID]bool, extra map[DeclID]map[DeclID]bool, trialOnly bool) [][]DeclID {
	indices := map[DeclID]int{}
	low := map[DeclID]int{}
	onStack := map[DeclID]bool{}
	stack := []DeclID{}
	next := 0
	components := [][]DeclID{}

	var visit func(DeclID)
	visit = func(id DeclID) {
		indices[id] = next
		low[id] = next
		next++
		stack = append(stack, id)
		onStack[id] = true

		dependencies := map[DeclID]bool{}
		if !trialOnly {
			for dep := range g.signatureDeps[id] {
				dependencies[dep] = true
			}
		}
		for dep := range extra[id] {
			dependencies[dep] = true
		}
		for dep := range dependencies {
			if !allowed[dep] {
				continue
			}
			if _, seen := indices[dep]; !seen {
				visit(dep)
				if low[dep] < low[id] {
					low[id] = low[dep]
				}
			} else if onStack[dep] && indices[dep] < low[id] {
				low[id] = indices[dep]
			}
		}

		if low[id] == indices[id] {
			component := map[DeclID]bool{}
			for {
				node := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[node] = false
				component[node] = true
				if node == id {
					break
				}
			}
			components = append(components, sortedBoolKeys(component))
		}
	}

	for _, seed := range sortedBoolKeys(allowed) {
		if _, seen := indices[seed]; !seen {
			visit(seed)
		}
	}
	return components
}

func (g *DependencyGraph) componentContaining(seeds []DeclID, allowed map[DeclID]bool, extra map[DeclID]map[DeclID]bool) sccGroup {
	return g.componentContainingWithOptions(seeds, allowed, extra, false)
}

func (g *DependencyGraph) componentContainingTrial(seeds []DeclID, allowed map[DeclID]bool, extra map[DeclID]map[DeclID]bool) sccGroup {
	return g.componentContainingWithOptions(seeds, allowed, extra, true)
}

func (g *DependencyGraph) trialCycleComponent(seeds []DeclID, extra map[DeclID]map[DeclID]bool) sccGroup {
	allowed := cloneIDSet(nil)
	for _, id := range seeds {
		allowed[id] = true
	}
	cycle := map[DeclID]bool{}
	for _, component := range g.componentsWith(allowed, extra, true) {
		if len(component) > 1 {
			for _, id := range component {
				cycle[id] = true
			}
		}
		if len(component) == 1 {
			id := component[0]
			if extra[id] != nil && extra[id][id] {
				cycle[id] = true
			}
		}
	}
	return sccGroup{members: sortedBoolKeys(cycle)}
}

func (g *DependencyGraph) componentContainingWithOptions(seeds []DeclID, allowed map[DeclID]bool, extra map[DeclID]map[DeclID]bool, trialOnly bool) sccGroup {
	allowedSet := cloneIDSet(allowed)
	for _, id := range seeds {
		allowedSet[id] = true
	}
	seedSet := map[DeclID]bool{}
	for _, id := range seeds {
		seedSet[id] = true
	}
	groupEdges := map[DeclID]map[DeclID]bool{}
	for _, id := range seeds {
		groupEdges[id] = map[DeclID]bool{}
		if dependencies, ok := extra[id]; ok {
			for dep := range dependencies {
				groupEdges[id][dep] = true
			}
		}
		if !trialOnly {
			for dep := range g.signatureDeps[id] {
				groupEdges[id][dep] = true
			}
		}
	}
	for id, dependencies := range extra {
		if groupEdges[id] != nil {
			continue
		}
		groupEdges[id] = map[DeclID]bool{}
		for dep := range dependencies {
			groupEdges[id][dep] = true
		}
	}
	for _, component := range g.componentsWith(allowedSet, groupEdges, true) {
		componentSet := map[DeclID]bool{}
		for _, id := range component {
			componentSet[id] = true
		}
		containsAll := true
		for _, seed := range seeds {
			if !componentSet[seed] {
				containsAll = false
			}
		}
		if containsAll {
			return sccGroup{members: component}
		}
	}
	return sccGroup{members: append([]DeclID(nil), seeds...)}
}

func (g *DependencyGraph) trialClosureContaining(seeds []DeclID, allowed map[DeclID]bool, direct map[DeclID]bool, extra map[DeclID]map[DeclID]bool) sccGroup {
	allowedSet := cloneIDSet(allowed)
	for _, id := range seeds {
		allowedSet[id] = true
	}
	frontier := append([]DeclID(nil), seeds...)
	reachable := map[DeclID]bool{}
	for _, id := range frontier {
		reachable[id] = true
	}
	for len(frontier) > 0 {
		id := frontier[0]
		frontier = frontier[1:]
		dependencies := map[DeclID]bool{}
		if dependenciesForTrial, tried := extra[id]; tried {
			dependencies = dependenciesForTrial
		} else {
			for dep := range g.signatureDeps[id] {
				dependencies[dep] = true
			}
		}
		for dep := range dependencies {
			if allowedSet[dep] && direct[dep] && !reachable[dep] {
				reachable[dep] = true
				frontier = append(frontier, dep)
			}
		}
	}
	return sccGroup{members: sortedBoolKeys(reachable)}
}

func (g *DependencyGraph) replaceEdges(forward edgeSet, reverse edgeSet, owner DeclID, deps map[DeclID]bool) {
	for dep := range forward[owner] {
		delete(reverse[dep], owner)
	}
	delete(forward, owner)
	for dep := range deps {
		addEdge(forward, owner, dep)
		addEdge(reverse, dep, owner)
	}
}

func addEdge(set edgeSet, from, to DeclID) {
	if set[from] == nil {
		set[from] = map[DeclID]struct{}{}
	}
	if set[to] == nil {
		set[to] = map[DeclID]struct{}{}
	}
	set[from][to] = struct{}{}
}

func closure(seeds []DeclID, next func(DeclID) []DeclID) []DeclID {
	seen := map[DeclID]bool{}
	pending := append([]DeclID(nil), seeds...)
	for len(pending) > 0 {
		id := pending[0]
		pending = pending[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		pending = append(pending, next(id)...)
	}
	return sortedBoolKeys(seen)
}

func sortedBoolKeys(set map[DeclID]bool) []DeclID {
	ids := make([]DeclID, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func sortedKeys(set map[DeclID]struct{}) []DeclID {
	ids := make([]DeclID, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func cloneIDSet(set map[DeclID]bool) map[DeclID]bool {
	cloned := make(map[DeclID]bool, len(set))
	for id, value := range set {
		cloned[id] = value
	}
	return cloned
}
