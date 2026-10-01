package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

func TestRandomNaiveDifferentialAndBruteForce(t *testing.T) {
	random := rand.New(rand.NewSource(1072000))
	for iteration := 0; iteration < 2000; iteration++ {
		engine := NewEngine()
		oracle := newNaiveEngine()
		acceptedInputs := make([]Predicate, 0, 8)
		allInputs := make([]Predicate, 0, 8)
		outputs := make([]string, 0, 8)
		sequenceLength := random.Intn(9)

		for step := 0; step < sequenceLength; step++ {
			predicate := randomSmallPredicate(random)
			allInputs = append(allInputs, predicate)
			actualErr := engine.Add(predicate)
			expectedErr := oracle.add(predicate)
			if errorCategory(actualErr) != errorCategory(expectedErr) {
				t.Fatalf("iteration=%d step=%d input=%s actual=%v expected=%v",
					iteration, step, predicateString(predicate), actualErr, expectedErr)
			}

			if actualErr == nil {
				acceptedInputs = append(acceptedInputs, predicate)
				actualSnapshot := productionDigest(engine)
				expectedSnapshot := oracle.digest()
				if actualSnapshot != expectedSnapshot {
					t.Fatalf("iteration=%d step=%d input=%s actual=%s expected=%s",
						iteration, step, predicateString(predicate), actualSnapshot, expectedSnapshot)
				}
				result := bruteForceState(oracle)
				basis := "accepted:" + actualSnapshot
				if result.satisfiable {
					assertPinnedMatchesAllSolutions(t, engine, result)
				} else {
					basis += ":bruteforce-unsat-without-required-pigeonhole-derivation"
				}
				outputs = append(outputs, basis)
			} else {
				outputs = append(outputs, "rejected:"+errorCategory(actualErr))
				if errorCategory(actualErr) == "contradiction" {
					result := bruteForceWithNext(acceptedInputs, predicate)
					if result.satisfiable {
						t.Fatalf("iteration=%d rejected input=%s but brute-force solution=%v exists",
							iteration, predicateString(predicate), result.solution)
					}
					outputs[len(outputs)-1] += ":bruteforce-unsat"
				}
			}
		}

		if iteration < 20 {
			t.Logf("iteration=%d inputs=%v outputs=%v basis=naive replay plus exhaustive assignments in [-9,9]",
				iteration, allInputs, outputs)
		}
	}
	t.Logf("completed 2000 random sequences; basis=all accepted states matched naive replay, all rejected contradictions had no [-9,9] assignment, and pinned roots had one brute-force value")
}

func randomSmallPredicate(random *rand.Rand) Predicate {
	columns := []string{"a", "b", "c"}
	values := []int64{-3, -2, -1, 0, 1, 2, 3}
	left := columns[random.Intn(3)]
	right := columns[random.Intn(3)]
	switch random.Intn(3) {
	case 1:
		return Eq(left, right)
	case 2:
		return Ne(left, right)
	default:
		ops := []CmpOp{OpEq, OpNe, OpLt, OpLe, OpGt, OpGe}
		return Cmp(left, ops[random.Intn(len(ops))], values[random.Intn(len(values))])
	}
}

func errorCategory(err error) string {
	switch err {
	case nil:
		return ""
	case ErrInvalidPredicate:
		return "invalid"
	case ErrContradiction:
		return "contradiction"
	case ErrColumnNotFound:
		return "unknown-column"
	default:
		return "other"
	}
}

func productionDigest(engine *Engine) string {
	groups := make(map[int][]string)
	for column, id := range engine.state.columns {
		root := engine.state.find(id)
		groups[root] = append(groups[root], column)
	}
	parts := make([]string, 0, len(groups))
	for root, columns := range groups {
		sort.Strings(columns)
		class := engine.state.classes[root]
		parts = append(parts, fmt.Sprintf("[%s]:%d..%d:%v",
			joinSortedStrings(columns), class.lo, class.hi, sortedMapValues(class.excluded)))
	}
	sort.Strings(parts)
	return fmt.Sprint(parts)
}

func sortedMapValues(values map[int64]struct{}) []int64 {
	result := make([]int64, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func joinSortedStrings(values []string) string {
	result := ""
	for i, value := range values {
		if i > 0 {
			result += ","
		}
		result += value
	}
	return result
}

type bruteForceResult struct {
	satisfiable  bool
	solution     map[int]int64
	pinnedValues map[int]map[int64]struct{}
	stopAtFirst  bool
}

func bruteForceState(engine *naiveEngine) bruteForceResult {
	roots := make([]int, 0, len(engine.classes))
	for root := range engine.classes {
		roots = append(roots, root)
	}
	sort.Ints(roots)
	assignment := make(map[int]int64, len(roots))
	result := bruteForceResult{pinnedValues: make(map[int]map[int64]struct{})}
	enumerateRootValues(roots, 0, engine, nil, assignment, &result)
	if result.satisfiable {
		result.satisfiable = true
	}
	return result
}

func bruteForceWithNext(accepted []Predicate, next Predicate) bruteForceResult {
	engine := newNaiveEngine()
	for _, predicate := range accepted {
		if err := engine.add(predicate); err != nil {
			return bruteForceResult{}
		}
	}
	roots := make([]int, 0, len(engine.classes))
	for root := range engine.classes {
		roots = append(roots, root)
	}
	for _, column := range []string{next.Column, next.Column2} {
		if column != "" {
			engine.rootFor(column)
		}
	}
	roots = roots[:0]
	for root := range engine.classes {
		roots = append(roots, root)
	}
	sort.Ints(roots)
	assignment := make(map[int]int64, len(roots))
	result := bruteForceResult{pinnedValues: make(map[int]map[int64]struct{}), stopAtFirst: true}
	enumerateRootValues(roots, 0, engine, &next, assignment, &result)
	if result.satisfiable {
		result.satisfiable = true
	}
	return result
}

func enumerateRootValues(roots []int, index int, engine *naiveEngine, next *Predicate, assignment map[int]int64, result *bruteForceResult) {
	if result.stopAtFirst && result.satisfiable {
		return
	}
	if index == len(roots) {
		if nextSatisfied(engine, next, assignment) && nePairsSatisfied(engine, assignment) {
			result.satisfiable = true
			if result.solution == nil {
				result.solution = make(map[int]int64, len(assignment))
				for root, value := range assignment {
					result.solution[root] = value
				}
			}
			for root, class := range engine.classes {
				if class.lo != class.hi {
					continue
				}
				if result.pinnedValues[root] == nil {
					result.pinnedValues[root] = make(map[int64]struct{})
				}
				result.pinnedValues[root][assignment[root]] = struct{}{}
			}
		}
		return
	}
	root := roots[index]
	class := engine.classes[root]
	for value := int64(-9); value <= 9; value++ {
		if value < class.lo || value > class.hi {
			continue
		}
		if _, excluded := class.excluded[value]; excluded {
			continue
		}
		assignment[root] = value
		enumerateRootValues(roots, index+1, engine, next, assignment, result)
		delete(assignment, root)
	}
}

func nePairsSatisfied(engine *naiveEngine, assignment map[int]int64) bool {
	for pair := range engine.nePairs {
		left := engine.find(pair[0])
		right := engine.find(pair[1])
		if assignment[left] == assignment[right] {
			return false
		}
	}
	return true
}

func nextSatisfied(engine *naiveEngine, next *Predicate, assignment map[int]int64) bool {
	if next == nil {
		return true
	}
	rootA := engine.find(engine.columns[next.Column])
	valueA := assignment[rootA]
	switch next.Kind {
	case KindCmp:
		switch next.Op {
		case OpEq:
			return valueA == next.Value
		case OpNe:
			return valueA != next.Value
		case OpLt:
			return valueA < next.Value
		case OpLe:
			return valueA <= next.Value
		case OpGt:
			return valueA > next.Value
		case OpGe:
			return valueA >= next.Value
		}
	case KindEq:
		rootB := engine.find(engine.columns[next.Column2])
		return rootA == rootB || valueA == assignment[rootB]
	case KindNe:
		rootB := engine.find(engine.columns[next.Column2])
		return rootA != rootB && valueA != assignment[rootB]
	}
	return false
}

func assertPinnedMatchesAllSolutions(t *testing.T, engine *Engine, result bruteForceResult) {
	t.Helper()
	for column, id := range engine.state.columns {
		root := engine.state.find(id)
		class := engine.state.classes[root]
		if class.lo != class.hi {
			continue
		}
		values := result.pinnedValues[root]
		if len(values) != 1 {
			t.Fatalf("column %s brute-force pinned value set=%v, want exactly one value", column, sortedMapValues(values))
		}
		if _, ok := values[class.lo]; !ok {
			t.Fatalf("column %s pinned to %d but brute-force allows %v", column, class.lo, sortedMapValues(values))
		}
	}
}
