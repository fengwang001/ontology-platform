package ontology

import "sync/atomic"

type analysisResult struct {
	pts        []map[int]bool
	heap       map[int]map[int]bool
	ret        map[int]bool
	global     map[int]bool
	allocStart int
	xStart     int
}

func emptySummary(k int) Summary {
	summary := Summary{
		Ret:  make([]bool, k),
		Glob: make([]bool, k),
		E:    make([][]bool, k),
	}
	for i := range summary.E {
		summary.E[i] = make([]bool, k)
	}
	return summary
}

func cloneSummary(summary Summary) Summary {
	cloned := emptySummary(len(summary.Ret))
	copy(cloned.Ret, summary.Ret)
	copy(cloned.Glob, summary.Glob)
	for i := range cloned.E {
		copy(cloned.E[i], summary.E[i])
	}
	cloned.Fresh = summary.Fresh
	cloned.Rounds = summary.Rounds
	return cloned
}

func addObject(target map[int]bool, object int) bool {
	if target[object] {
		return false
	}
	target[object] = true
	return true
}

func addAllObjects(target map[int]bool, source map[int]bool) bool {
	changed := false
	for object := range source {
		if addObject(target, object) {
			changed = true
		}
	}
	return changed
}

func runAnalysis(fn *function, registered map[string]*function, selfSummary Summary) analysisResult {
	allocCount := len(fn.siteIDs)
	callCount := 0
	for _, stmt := range fn.statements {
		if stmt.Kind == CallStmt && stmt.D != -1 {
			callCount++
		}
	}

	allocStart := fn.params
	xStart := allocStart + allocCount
	objectCount := xStart + callCount

	pts := make([]map[int]bool, fn.variables)
	for i := range pts {
		pts[i] = make(map[int]bool)
	}
	for i := 0; i < fn.params; i++ {
		pts[i][i] = true
	}

	heap := make(map[int]map[int]bool, objectCount)
	for object := 0; object < objectCount; object++ {
		heap[object] = make(map[int]bool)
	}
	ret := make(map[int]bool)
	global := make(map[int]bool)

	allocObject := make([]int, len(fn.statements))
	xObject := make([]int, len(fn.statements))
	nextAlloc := 0
	nextX := 0
	for i, stmt := range fn.statements {
		if stmt.Kind == NewStmt {
			allocObject[i] = allocStart + nextAlloc
			nextAlloc++
		}
		if stmt.Kind == CallStmt && stmt.D != -1 {
			xObject[i] = xStart + nextX
			if selfSummary.Fresh && stmt.G == fn.name {
				global[xObject[i]] = true
			}
			nextX++
		}
	}

	for {
		changed := false

		for stmtIndex, stmt := range fn.statements {
			switch stmt.Kind {
			case NewStmt:
				changed = addObject(pts[stmt.D], allocObject[stmtIndex]) || changed
			case CopyStmt:
				changed = addAllObjects(pts[stmt.D], pts[stmt.S]) || changed
			case StoreStmt:
				for object := range pts[stmt.D] {
					changed = addAllObjects(heap[object], pts[stmt.S]) || changed
				}
			case LoadStmt:
				for object := range pts[stmt.S] {
					changed = addAllObjects(pts[stmt.D], heap[object]) || changed
				}
			case RetStmt:
				changed = addAllObjects(ret, pts[stmt.S]) || changed
			case GlobalStmt:
				for object := range pts[stmt.S] {
					changed = addObject(global, object) || changed
				}
			case CallStmt:
				calleeSummary := selfSummary
				if stmt.G != fn.name {
					calleeSummary = registered[stmt.G].summary
				}

				for i := range stmt.Args {
					if calleeSummary.Glob[i] {
						for object := range pts[stmt.Args[i]] {
							changed = addObject(global, object) || changed
						}
					}
					if calleeSummary.Ret[i] && stmt.D != -1 {
						changed = addAllObjects(pts[stmt.D], pts[stmt.Args[i]]) || changed
					}
					for j := range stmt.Args {
						if !calleeSummary.E[i][j] {
							continue
						}
						for object := range pts[stmt.Args[i]] {
							changed = addAllObjects(heap[object], pts[stmt.Args[j]]) || changed
						}
					}
				}

				if calleeSummary.Fresh && stmt.D != -1 {
					changed = addObject(pts[stmt.D], xObject[stmtIndex]) || changed
					changed = addObject(global, xObject[stmtIndex]) || changed
				}
			}
		}

		if !changed {
			return analysisResult{
				pts:        pts,
				heap:       heap,
				ret:        ret,
				global:     global,
				allocStart: allocStart,
				xStart:     xStart,
			}
		}
	}
}

func reachable(seeds map[int]bool, heap map[int]map[int]bool, includeSeeds bool) map[int]bool {
	result := make(map[int]bool)
	queue := make([]int, 0, len(seeds))
	for object := range seeds {
		if includeSeeds {
			result[object] = true
		}
		queue = append(queue, object)
	}

	for len(queue) > 0 {
		object := queue[0]
		queue = queue[1:]
		for next := range heap[object] {
			if result[next] {
				continue
			}
			result[next] = true
			queue = append(queue, next)
		}
	}
	return result
}

func parameterReachable(fn *function, heap map[int]map[int]bool) map[int]bool {
	result := make(map[int]bool)
	for parameter := 0; parameter < fn.params; parameter++ {
		queue := append([]int(nil), parameter)
		for len(queue) > 0 {
			object := queue[0]
			queue = queue[1:]
			for next := range heap[object] {
				if result[next] {
					continue
				}
				result[next] = true
				queue = append(queue, next)
			}
		}
	}
	return result
}

func buildSummary(fn *function, result analysisResult) Summary {
	summary := emptySummary(fn.params)
	glb := reachable(result.global, result.heap, true)
	returned := reachable(result.ret, result.heap, true)

	for i := 0; i < fn.params; i++ {
		summary.Ret[i] = returned[i]
		summary.Glob[i] = glb[i]
		for j := 0; j < fn.params; j++ {
			summary.E[i][j] = result.heap[i][j]
		}
	}

	for object := range result.ret {
		if object >= result.allocStart {
			summary.Fresh = true
		}
	}
	return summary
}

func summariesEqual(left, right Summary) bool {
	if left.Fresh != right.Fresh || len(left.Ret) != len(right.Ret) {
		return false
	}
	for i := range left.Ret {
		if left.Ret[i] != right.Ret[i] || left.Glob[i] != right.Glob[i] {
			return false
		}
		for j := range left.E[i] {
			if left.E[i][j] != right.E[i][j] {
				return false
			}
		}
	}
	return true
}

func siteClasses(fn *function, result analysisResult) []AllocationSite {
	glb := reachable(result.global, result.heap, true)
	returned := reachable(result.ret, result.heap, true)
	parameterEscaped := parameterReachable(fn, result.heap)

	sites := make([]AllocationSite, 0, len(fn.siteIDs))
	for i, id := range fn.siteIDs {
		object := result.allocStart + i
		class := StackEscape
		if parameterEscaped[object] {
			class = ParameterEscape
		}
		if returned[object] {
			class = ReturnEscape
		}
		if glb[object] {
			class = GlobalEscape
		}
		sites = append(sites, AllocationSite{ID: id, Class: class})
	}
	return sites
}

func analyzeFunction(fn *function, registered map[string]*function, registry *Registry) (Summary, []AllocationSite) {
	current := emptySummary(fn.params)
	hasSelfCall := false
	for _, stmt := range fn.statements {
		if stmt.Kind == CallStmt && stmt.G == fn.name {
			hasSelfCall = true
		}
	}

	rounds := 0
	var result analysisResult
	for {
		rounds++
		atomic.AddInt64(&registry.analysisRuns, 1)
		result = runAnalysis(fn, registered, current)
		next := buildSummary(fn, result)
		if !hasSelfCall || summariesEqual(current, next) {
			next.Rounds = rounds
			return next, siteClasses(fn, result)
		}
		current = next
	}
}
