package ontology

import (
	"fmt"
	"sort"
)

type naiveClass struct {
	lo       int64
	hi       int64
	excluded map[int64]struct{}
}

type naiveEngine struct {
	columns map[string]int
	parent  map[int]int
	classes map[int]naiveClass
	nePairs map[[2]int]struct{}
	nextID  int
}

func newNaiveEngine() *naiveEngine {
	return &naiveEngine{
		columns: map[string]int{},
		parent:  map[int]int{},
		classes: map[int]naiveClass{},
		nePairs: map[[2]int]struct{}{},
	}
}

func (engine *naiveEngine) find(id int) int {
	root := id
	for engine.parent[root] != root {
		root = engine.parent[root]
	}
	for engine.parent[id] != id {
		next := engine.parent[id]
		engine.parent[id] = root
		id = next
	}
	return root
}

func (engine *naiveEngine) rootFor(column string) int {
	if id, ok := engine.columns[column]; ok {
		return engine.find(id)
	}
	id := engine.nextID
	engine.nextID++
	engine.columns[column] = id
	engine.parent[id] = id
	engine.classes[id] = naiveClass{lo: minInt64, hi: maxInt64, excluded: map[int64]struct{}{}}
	return id
}

func cloneNaiveExcluded(values map[int64]struct{}) map[int64]struct{} {
	copied := make(map[int64]struct{}, len(values))
	for value := range values {
		copied[value] = struct{}{}
	}
	return copied
}

func naiveNormalize(class *naiveClass) error {
	for {
		if class.lo > class.hi {
			return ErrContradiction
		}
		for value := range class.excluded {
			if value < class.lo || value > class.hi {
				delete(class.excluded, value)
			}
		}
		if _, excluded := class.excluded[class.lo]; excluded {
			if class.lo == maxInt64 {
				return ErrContradiction
			}
			class.lo++
			continue
		}
		if _, excluded := class.excluded[class.hi]; excluded {
			if class.hi == minInt64 {
				return ErrContradiction
			}
			class.hi--
			continue
		}
		return nil
	}
}

func naiveApplyCmp(class *naiveClass, op CmpOp, value int64) error {
	switch op {
	case OpEq:
		if value < class.lo || value > class.hi {
			return ErrContradiction
		}
		class.lo = value
		class.hi = value
	case OpNe:
		if value >= class.lo && value <= class.hi {
			class.excluded[value] = struct{}{}
		}
	case OpLt:
		if value == minInt64 {
			return ErrContradiction
		}
		class.hi = minInt64Value(class.hi, value-1)
	case OpLe:
		class.hi = minInt64Value(class.hi, value)
	case OpGt:
		if value == maxInt64 {
			return ErrContradiction
		}
		class.lo = maxInt64Value(class.lo, value+1)
	case OpGe:
		class.lo = maxInt64Value(class.lo, value)
	}
	return naiveNormalize(class)
}

func naivePair(left, right int) [2]int {
	if left > right {
		left, right = right, left
	}
	return [2]int{left, right}

}

func (engine *naiveEngine) merge(rootA, rootB int) error {
	classA := engine.classes[rootA]
	classB := engine.classes[rootB]
	merged := naiveClass{
		lo:       maxInt64Value(classA.lo, classB.lo),
		hi:       minInt64Value(classA.hi, classB.hi),
		excluded: cloneNaiveExcluded(classA.excluded),
	}
	for value := range classB.excluded {
		merged.excluded[value] = struct{}{}
	}
	if err := naiveNormalize(&merged); err != nil {
		return err
	}
	parent, child := rootA, rootB
	if rootB < rootA {
		parent, child = rootB, rootA
	}
	engine.parent[child] = parent
	delete(engine.classes, child)
	engine.classes[parent] = merged

	rewritten := make(map[[2]int]struct{})
	for pair := range engine.nePairs {
		left := engine.find(pair[0])
		right := engine.find(pair[1])
		if left == right {
			return ErrContradiction
		}
		rewritten[naivePair(left, right)] = struct{}{}
	}
	engine.nePairs = rewritten
	return engine.propagate()
}

func (engine *naiveEngine) propagate() error {
	for {
		changed := false
		for pair := range engine.nePairs {
			rootA := engine.find(pair[0])
			rootB := engine.find(pair[1])
			if rootA == rootB {
				return ErrContradiction
			}
			classA := engine.classes[rootA]
			classB := engine.classes[rootB]
			if classA.lo == classA.hi && classB.lo == classB.hi && classA.lo == classB.lo {
				return ErrContradiction
			}
			if classA.lo == classA.hi {
				value := classA.lo
				if value >= classB.lo && value <= classB.hi {
					if _, exists := classB.excluded[value]; !exists {
						classB.excluded[value] = struct{}{}
						if err := naiveNormalize(&classB); err != nil {
							return err
						}
						engine.classes[rootB] = classB
						changed = true
					}
				}
			}
			if classB.lo == classB.hi {
				value := classB.lo
				if value >= classA.lo && value <= classA.hi {
					if _, exists := classA.excluded[value]; !exists {
						classA.excluded[value] = struct{}{}
						if err := naiveNormalize(&classA); err != nil {
							return err
						}
						engine.classes[rootA] = classA
						changed = true
					}
				}
			}
		}
		if !changed {
			return nil
		}
	}
}

func (engine *naiveEngine) add(predicate Predicate) error {
	if err := validateInsertPredicate(predicate); err != nil {
		return err
	}
	candidate := engine.clone()
	switch predicate.Kind {
	case KindCmp:
		root := candidate.rootFor(predicate.Column)
		class := candidate.classes[root]
		class.excluded = cloneNaiveExcluded(class.excluded)
		if err := naiveApplyCmp(&class, predicate.Op, predicate.Value); err != nil {
			return err
		}
		candidate.classes[root] = class
		if err := candidate.propagate(); err != nil {
			return err
		}
	case KindEq:
		rootA := candidate.rootFor(predicate.Column)
		rootB := candidate.rootFor(predicate.Column2)
		if rootA == rootB {
			*engine = *candidate
			return nil
		}
		if err := candidate.merge(rootA, rootB); err != nil {
			return err
		}
	case KindNe:
		rootA := candidate.rootFor(predicate.Column)
		rootB := candidate.rootFor(predicate.Column2)
		if rootA == rootB {
			return ErrContradiction
		}
		candidate.nePairs[naivePair(rootA, rootB)] = struct{}{}
		if err := candidate.propagate(); err != nil {
			return err
		}
	default:
		return ErrInvalidPredicate
	}
	*engine = *candidate
	return nil
}

func (engine *naiveEngine) clone() *naiveEngine {
	copied := &naiveEngine{
		columns: make(map[string]int, len(engine.columns)),
		parent:  make(map[int]int, len(engine.parent)),
		classes: make(map[int]naiveClass, len(engine.classes)),
		nePairs: make(map[[2]int]struct{}, len(engine.nePairs)),
		nextID:  engine.nextID,
	}
	for column, id := range engine.columns {
		copied.columns[column] = id
	}
	for id, parent := range engine.parent {
		copied.parent[id] = parent
	}
	for root, class := range engine.classes {
		copied.classes[root] = naiveClass{lo: class.lo, hi: class.hi, excluded: cloneNaiveExcluded(class.excluded)}
	}
	for pair := range engine.nePairs {
		copied.nePairs[pair] = struct{}{}
	}
	return copied
}

func sortedExcluded(class naiveClass) []int64 {
	values := make([]int64, 0, len(class.excluded))
	for value := range class.excluded {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}

func (engine *naiveEngine) digest() string {
	columns := make([]string, 0, len(engine.columns))
	for column := range engine.columns {
		columns = append(columns, column)
	}
	sort.Strings(columns)

	groups := make(map[int][]string)
	for _, column := range columns {
		root := engine.find(engine.columns[column])
		groups[root] = append(groups[root], column)
	}
	parts := make([]string, 0, len(groups))
	for _, members := range groups {
		sort.Strings(members)
		root := engine.find(engine.columns[members[0]])
		class := engine.classes[root]
		parts = append(parts, fmt.Sprintf("[%s]:%d..%d:%v", joinNaiveStrings(members), class.lo, class.hi, sortedExcluded(class)))
	}
	sort.Strings(parts)
	return fmt.Sprint(parts)
}

func joinNaiveStrings(values []string) string {
	result := ""
	for i, value := range values {
		if i > 0 {
			result += ","
		}
		result += value
	}
	return result
}
