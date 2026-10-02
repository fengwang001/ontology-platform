package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

type naiveStatement struct {
	kind StmtKind
	d, s int
	g    string
	args []int
}

type naiveFunction struct {
	name       string
	params     int
	variables  int
	statements []naiveStatement
}

type naiveSummaryResult struct {
	summary Summary
	sites   []AllocationSite
}

func nsetAdd(set map[int]bool, value int) bool {
	if set[value] {
		return false
	}
	set[value] = true
	return true
}

func nsetUnion(dst, src map[int]bool) bool {
	changed := false
	for value := range src {
		if nsetAdd(dst, value) {
			changed = true
		}
	}
	return changed
}

func nreachable(seeds map[int]bool, heap map[int]map[int]bool, includeSeeds bool) map[int]bool {
	seen := make(map[int]bool)
	queue := make([]int, 0)
	for value := range seeds {
		if includeSeeds {
			seen[value] = true
		}
		queue = append(queue, value)
	}
	for len(queue) > 0 {
		value := queue[0]
		queue = queue[1:]
		for next := range heap[value] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return seen
}

func nparameterReachable(params int, heap map[int]map[int]bool) map[int]bool {
	seen := make(map[int]bool)
	for parameter := 0; parameter < params; parameter++ {
		queue := []int{parameter}
		for len(queue) > 0 {
			value := queue[0]
			queue = queue[1:]
			for next := range heap[value] {
				if !seen[next] {
					seen[next] = true
					queue = append(queue, next)
				}
			}
		}
	}
	return seen
}

func nemptySummary(k int) Summary {
	summary := Summary{Ret: make([]bool, k), Glob: make([]bool, k), E: make([][]bool, k)}
	for i := range summary.E {
		summary.E[i] = make([]bool, k)
	}
	return summary
}

func nsummaryContains(outer, inner Summary) bool {
	if len(outer.Ret) != len(inner.Ret) {
		return false
	}
	for i := range inner.Ret {
		if inner.Ret[i] && !outer.Ret[i] {
			return false
		}
		if inner.Glob[i] && !outer.Glob[i] {
			return false
		}
		for j := range inner.E[i] {
			if inner.E[i][j] && !outer.E[i][j] {
				return false
			}
		}
	}
	return !inner.Fresh || outer.Fresh
}

func nrun(fn naiveFunction, known map[string]naiveSummaryResult, current Summary) (map[int]bool, map[int]map[int]bool, map[int]bool, int) {
	allocCount := 0
	for _, stmt := range fn.statements {
		if stmt.kind == NewStmt {
			allocCount++
		}
	}
	xIndex := make(map[int]int)
	xCount := 0
	for i, stmt := range fn.statements {
		if stmt.kind != CallStmt || stmt.d == -1 {
			continue
		}
		xIndex[i] = xCount
		xCount++
	}

	allocStart := fn.params
	xStart := allocStart + allocCount
	pts := make([]map[int]bool, fn.variables)
	for i := range pts {
		pts[i] = make(map[int]bool)
	}
	for i := 0; i < fn.params; i++ {
		pts[i][i] = true
	}
	heap := make(map[int]map[int]bool)
	global := make(map[int]bool)
	ret := make(map[int]bool)
	for i, stmt := range fn.statements {
		if stmt.kind == CallStmt && stmt.d != -1 {
			fresh := current.Fresh
			if stmt.g != fn.name {
				fresh = known[stmt.g].summary.Fresh
			}
			if fresh {
				global[xStart+xIndex[i]] = true
			}
		}
	}

	allocObject := make([]int, len(fn.statements))
	nextAlloc := 0
	for i, stmt := range fn.statements {
		if stmt.kind == NewStmt {
			allocObject[i] = allocStart + nextAlloc
			nextAlloc++
		}
	}

	for {
		changed := false
		for i, stmt := range fn.statements {
			switch stmt.kind {
			case NewStmt:
				changed = nsetAdd(pts[stmt.d], allocObject[i]) || changed
			case CopyStmt:
				changed = nsetUnion(pts[stmt.d], pts[stmt.s]) || changed
			case StoreStmt:
				for object := range pts[stmt.d] {
					if heap[object] == nil {
						heap[object] = make(map[int]bool)
					}
					changed = nsetUnion(heap[object], pts[stmt.s]) || changed
				}
			case LoadStmt:
				for object := range pts[stmt.s] {
					changed = nsetUnion(pts[stmt.d], heap[object]) || changed
				}
			case RetStmt:
				changed = nsetUnion(ret, pts[stmt.s]) || changed
			case GlobalStmt:
				for object := range pts[stmt.s] {
					changed = nsetAdd(global, object) || changed
				}
			case CallStmt:
				callee := current
				if stmt.g == fn.name {
					callee = current
				} else {
					callee = known[stmt.g].summary
				}
				for argIndex := range stmt.args {
					if callee.Glob[argIndex] {
						for object := range pts[stmt.args[argIndex]] {
							changed = nsetAdd(global, object) || changed
						}
					}
					if callee.Ret[argIndex] && stmt.d != -1 {
						changed = nsetUnion(pts[stmt.d], pts[stmt.args[argIndex]]) || changed
					}
					for other := range stmt.args {
						if !callee.E[argIndex][other] {
							continue
						}
						for object := range pts[stmt.args[argIndex]] {
							if heap[object] == nil {
								heap[object] = make(map[int]bool)
							}
							changed = nsetUnion(heap[object], pts[stmt.args[other]]) || changed
						}
					}
				}
				if callee.Fresh && stmt.d != -1 {
					object := xStart + xIndex[i]
					changed = nsetAdd(pts[stmt.d], object) || changed
					changed = nsetAdd(global, object) || changed
				}
			}
		}
		if !changed {
			return global, heap, ret, allocStart
		}
	}
}

func nanalyze(fn naiveFunction, known map[string]naiveSummaryResult, firstSiteID int) naiveSummaryResult {
	current := nemptySummary(fn.params)
	selfCall := false
	for _, stmt := range fn.statements {
		if stmt.kind == CallStmt && stmt.g == fn.name {
			selfCall = true
		}
	}

	rounds := 0
	var global map[int]bool
	var heap map[int]map[int]bool
	var ret map[int]bool
	var allocStart int
	for {
		rounds++
		global, heap, ret, allocStart = nrun(fn, known, current)
		next := nemptySummary(fn.params)
		glb := nreachable(global, heap, true)
		returned := nreachable(ret, heap, true)
		for i := 0; i < fn.params; i++ {
			next.Glob[i] = glb[i]
			next.Ret[i] = returned[i]
			for j := 0; j < fn.params; j++ {
				next.E[i][j] = heap[i][j]
			}
		}
		for object := range ret {
			if object >= allocStart {
				next.Fresh = true
			}
		}
		if !selfCall {
			current = next
			break
		}
		if rounds > 1 && !nsummaryContains(next, current) {
			panic(fmt.Sprintf("self-recursive summary lost a bit: previous=%#v next=%#v", current, next))
		}
		if rounds > 2*fn.params+fn.params*fn.params+2 {
			panic(fmt.Sprintf("self-recursive rounds exceeded bound: %d", rounds))
		}
		if fmt.Sprint(current) == fmt.Sprint(next) {
			break
		}
		current = next
	}

	current.Rounds = rounds
	glb := nreachable(global, heap, true)
	returned := nreachable(ret, heap, true)
	parameters := nparameterReachable(fn.params, heap)
	allocations := 0
	sites := make([]AllocationSite, 0)
	for _, stmt := range fn.statements {
		if stmt.kind != NewStmt {
			continue
		}
		object := allocStart + allocations
		class := StackEscape
		if parameters[object] {
			class = ParameterEscape
		}
		if returned[object] {
			class = ReturnEscape
		}
		if glb[object] {
			class = GlobalEscape
		}
		sites = append(sites, AllocationSite{ID: firstSiteID + allocations, Class: class})
		allocations++
	}
	return naiveSummaryResult{summary: current, sites: sites}
}

func productionStatements(stmts []naiveStatement) []Statement {
	result := make([]Statement, len(stmts))
	for i, stmt := range stmts {
		result[i] = Statement{Kind: stmt.kind, D: stmt.d, S: stmt.s, G: stmt.g, Args: stmt.args}
	}
	return result
}

func generateNaiveFunction(rng *rand.Rand, index int, previous []naiveFunction) naiveFunction {
	params := rng.Intn(4)
	variables := params + rng.Intn(5)
	if variables > 8 {
		variables = 8
	}
	name := fmt.Sprintf("f%02d", index)
	statementCount := 0
	if variables > 0 {
		statementCount = rng.Intn(17)
	}
	stmts := make([]naiveStatement, 0, statementCount)

	for len(stmts) < statementCount {
		switch rng.Intn(7) {
		case 0:
			stmts = append(stmts, naiveStatement{kind: NewStmt, d: rng.Intn(variables)})
		case 1:
			stmts = append(stmts, naiveStatement{kind: CopyStmt, d: rng.Intn(variables), s: rng.Intn(variables)})
		case 2:
			stmts = append(stmts, naiveStatement{kind: StoreStmt, d: rng.Intn(variables), s: rng.Intn(variables)})
		case 3:
			stmts = append(stmts, naiveStatement{kind: LoadStmt, d: rng.Intn(variables), s: rng.Intn(variables)})
		case 4:
			stmts = append(stmts, naiveStatement{kind: RetStmt, s: rng.Intn(variables)})
		case 5:
			stmts = append(stmts, naiveStatement{kind: GlobalStmt, s: rng.Intn(variables)})
		default:
			candidates := make([]naiveFunction, 0, len(previous)+1)
			candidates = append(candidates, naiveFunction{name: name, params: params})
			for _, candidate := range previous {
				if candidate.params <= variables {
					candidates = append(candidates, candidate)
				}
			}
			callee := candidates[rng.Intn(len(candidates))]
			args := make([]int, callee.params)
			for i := range args {
				args[i] = rng.Intn(variables)
			}
			d := -1
			if rng.Intn(2) == 0 {
				d = rng.Intn(variables)
			}
			stmts = append(stmts, naiveStatement{kind: CallStmt, d: d, g: callee.name, args: args})
		}
	}
	return naiveFunction{name: name, params: params, variables: variables, statements: stmts}
}

func TestRandomNaiveOracle2000(t *testing.T) {
	if !testing.Verbose() {
		t.Log("use -v to inspect every random input, output, and naive fixed-point basis")
	}
	rng := rand.New(rand.NewSource(1164))
	registry := NewRegistry()
	known := make(map[string]naiveSummaryResult)
	previous := make([]naiveFunction, 0)
	siteCursor := 0

	for iteration := 0; iteration < 2000; iteration++ {
		if iteration%64 == 0 {
			registry = NewRegistry()
			known = make(map[string]naiveSummaryResult)
			previous = previous[:0]
			siteCursor = 0
		}
		fn := generateNaiveFunction(rng, iteration, previous)
		expected := nanalyze(fn, known, siteCursor+1)
		err := registry.Register(fn.name, fn.params, fn.variables, productionStatements(fn.statements))
		if err != nil {
			t.Fatalf("iteration %d valid input rejected: %v\ninput=%#v", iteration, err, fn)
		}
		actualSummary, err := registry.Summary(fn.name)
		if err != nil {
			t.Fatal(err)
		}
		actualSites, err := registry.Sites(fn.name)
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("case=%d input=%#v actual_summary=%#v actual_sites=%#v naive_summary=%#v naive_sites=%#v basis=independent repeated scan over New/Copy/Store/Load/Ret/Global/Call constraints, then GLB/RR heap closure and one-step PR classification",
			iteration, fn, actualSummary, actualSites, expected.summary, expected.sites)

		if fmt.Sprint(actualSummary) != fmt.Sprint(expected.summary) {
			t.Fatalf("iteration %d summary mismatch\ninput=%#v\nactual=%#v\nnaive=%#v",
				iteration, fn, actualSummary, expected.summary)
		}
		if len(actualSites) != len(expected.sites) {
			t.Fatalf("iteration %d site count mismatch\ninput=%#v\nactual=%#v\nnaive=%#v",
				iteration, fn, actualSites, expected.sites)
		}
		for i := range actualSites {
			if actualSites[i] != expected.sites[i] {
				t.Fatalf("iteration %d site %d mismatch\ninput=%#v\nactual=%#v\nnaive=%#v",
					iteration, i, fn, actualSites, expected.sites)
			}
		}

		allocCount := 0
		for _, stmt := range fn.statements {
			if stmt.kind == NewStmt {
				allocCount++
			}
		}
		siteCursor += allocCount
		known[fn.name] = expected
		previous = append(previous, fn)
	}
}
