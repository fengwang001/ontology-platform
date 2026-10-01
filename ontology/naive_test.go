package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"testing"
)

var (
	errNaiveInvalid       = errors.New("naive invalid predicate")
	errNaiveContradiction = errors.New("naive contradiction")
	errNaiveUnknownColumn = errors.New("naive unknown column")
)

type naiveClass struct {
	members  map[string]bool
	lo       int64
	hi       int64
	excluded map[int64]struct{}
}

type naiveDeriver struct {
	accepted []Predicate
}

type naiveSnapshotClass struct {
	members  []string
	lo       int64
	hi       int64
	excluded []int64
}

type naiveSnapshot struct {
	columns []string
	classes map[string]naiveSnapshotClass
}

func newNaiveDeriver() *naiveDeriver {
	return &naiveDeriver{}
}

func naiveEdge(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

func (n *naiveDeriver) rebuild() (map[string]*naiveClass, map[[2]string]bool, error) {
	classes := make(map[string]*naiveClass)
	different := make(map[[2]string]bool)

	ensure := func(column string) *naiveClass {
		for _, current := range classes {
			if current.members[column] {
				return current
			}
		}
		current := &naiveClass{
			members:  map[string]bool{column: true},
			lo:       MinInt64,
			hi:       MaxInt64,
			excluded: make(map[int64]struct{}),
		}
		classes[column] = current
		return current
	}
	root := func(column string) string {
		for representative, current := range classes {
			if current.members[column] {
				return representative
			}
		}
		return ""
	}

	for _, pred := range n.accepted {
		switch pred.Kind {
		case Cmp:
			current := ensure(pred.Column)
			switch pred.Operator {
			case OpEqual:
				current.lo = maxInt64(current.lo, pred.Value)
				current.hi = minInt64(current.hi, pred.Value)
			case OpNotEqual:
				current.excluded[pred.Value] = struct{}{}
			case OpLess:
				if pred.Value == MinInt64 {
					return nil, nil, errNaiveContradiction
				}
				current.hi = minInt64(current.hi, pred.Value-1)
			case OpLessEqual:
				current.hi = minInt64(current.hi, pred.Value)
			case OpGreater:
				if pred.Value == MaxInt64 {
					return nil, nil, errNaiveContradiction
				}
				current.lo = maxInt64(current.lo, pred.Value+1)
			case OpGreaterEqual:
				current.lo = maxInt64(current.lo, pred.Value)
			}
		case Eq, Ne:
			classA := ensure(pred.ColumnA)
			classB := ensure(pred.ColumnB)
			rootA := root(pred.ColumnA)
			rootB := root(pred.ColumnB)
			if pred.Kind == Ne {
				if rootA == rootB {
					return nil, nil, errNaiveContradiction
				}
				different[naiveEdge(rootA, rootB)] = true
			} else {
				if rootA == rootB {
					continue
				}
				if different[naiveEdge(rootA, rootB)] {
					return nil, nil, errNaiveContradiction
				}
				for column := range classB.members {
					classA.members[column] = true
				}
				classA.lo = maxInt64(classA.lo, classB.lo)
				classA.hi = minInt64(classA.hi, classB.hi)
				for value := range classB.excluded {
					classA.excluded[value] = struct{}{}
				}
				for edge := range different {
					if edge[0] == rootB || edge[1] == rootB {
						delete(different, edge)
						other := edge[0]
						if other == rootB {
							other = edge[1]
						}
						if other != rootA {
							different[naiveEdge(rootA, other)] = true
						}
					}
				}
				delete(classes, rootB)
			}
		}

		if err := naiveClose(classes, different); err != nil {
			return nil, nil, err
		}
	}
	return classes, different, nil
}

func naiveClose(classes map[string]*naiveClass, different map[[2]string]bool) error {
	for {
		changed := false
		for edge := range different {
			classA := classes[edge[0]]
			classB := classes[edge[1]]
			if classA == nil || classB == nil {
				delete(different, edge)
				changed = true
				continue
			}
			if classA.lo == classA.hi && classB.lo == classB.hi && classA.lo == classB.lo {
				return errNaiveContradiction
			}
			changed = changed || propagatePinned(classA, classB) || propagatePinned(classB, classA)
		}
		for _, current := range classes {
			nextChanged, err := current.normalize()
			if err != nil {
				return err
			}
			changed = changed || nextChanged
		}
		if !changed {
			return nil
		}
	}
}

func propagatePinned(pinned, other *naiveClass) bool {
	if pinned.lo != pinned.hi {
		return false
	}
	value := pinned.lo
	if value < other.lo || value > other.hi {
		return false
	}
	if _, exists := other.excluded[value]; exists {
		return false
	}
	other.excluded[value] = struct{}{}
	return true
}

func (c *naiveClass) normalize() (bool, error) {
	changed := false
	for value := range c.excluded {
		if value < c.lo || value > c.hi {
			delete(c.excluded, value)
			changed = true
		}
	}
	for {
		if c.lo > c.hi {
			return changed, errNaiveContradiction
		}
		if _, ok := c.excluded[c.lo]; ok {
			if c.lo == MaxInt64 {
				return changed, errNaiveContradiction
			}
			c.lo++
			changed = true
			continue
		}
		if _, ok := c.excluded[c.hi]; ok {
			if c.hi == MinInt64 {
				return changed, errNaiveContradiction
			}
			c.hi--
			changed = true
			continue
		}
		break
	}
	for value := range c.excluded {
		if value < c.lo || value > c.hi {
			delete(c.excluded, value)
			changed = true
		}
	}
	if c.lo > c.hi {
		return changed, errNaiveContradiction
	}
	return changed, nil
}

func validateNaivePredicate(pred Predicate, implication bool) error {
	switch pred.Kind {
	case Cmp:
		if pred.Column == "" || !validOperator(pred.Operator) {
			return errNaiveInvalid
		}
	case Eq:
		if pred.ColumnA == "" || pred.ColumnB == "" {
			return errNaiveInvalid
		}
	case Ne:
		if implication || pred.ColumnA == "" || pred.ColumnB == "" {
			return errNaiveInvalid
		}
	default:
		return errNaiveInvalid
	}
	return nil
}

func (n *naiveDeriver) Add(pred Predicate) error {
	if err := validateNaivePredicate(pred, false); err != nil {
		return err
	}
	n.accepted = append(n.accepted, pred)
	if _, _, err := n.rebuild(); err != nil {
		n.accepted = n.accepted[:len(n.accepted)-1]
		return err
	}
	return nil
}

func (n *naiveDeriver) snapshot() (naiveSnapshot, error) {
	classes, _, err := n.rebuild()
	if err != nil {
		return naiveSnapshot{}, err
	}
	result := naiveSnapshot{classes: make(map[string]naiveSnapshotClass)}
	for _, current := range classes {
		members := make([]string, 0, len(current.members))
		for member := range current.members {
			members = append(members, member)
		}
		slices.Sort(members)

		excluded := make([]int64, 0, len(current.excluded))
		for value := range current.excluded {
			excluded = append(excluded, value)
		}
		slices.Sort(excluded)

		classSnapshot := naiveSnapshotClass{members: members, lo: current.lo, hi: current.hi, excluded: excluded}
		for _, member := range members {
			result.columns = append(result.columns, member)
			result.classes[member] = classSnapshot
		}
	}
	slices.Sort(result.columns)
	return result, nil
}

type actualSnapshotClass struct {
	members  []string
	lo       int64
	hi       int64
	excluded []int64
}

func actualSnapshot(t *testing.T, d *Deriver) map[string]actualSnapshotClass {
	t.Helper()
	d.mu.RLock()
	defer d.mu.RUnlock()

	byRoot := make(map[string][]string)
	for column := range d.state.parent {
		root := d.state.find(column)
		byRoot[root] = append(byRoot[root], column)
	}

	result := make(map[string]actualSnapshotClass)
	for root, members := range byRoot {
		slices.Sort(members)
		excluded := make([]int64, 0, len(d.state.classes[root].excluded))
		for value := range d.state.classes[root].excluded {
			excluded = append(excluded, value)
		}
		slices.Sort(excluded)
		classSnapshot := actualSnapshotClass{
			members:  members,
			lo:       d.state.classes[root].lo,
			hi:       d.state.classes[root].hi,
			excluded: excluded,
		}
		for _, member := range members {
			result[member] = classSnapshot
		}
	}
	return result
}

func assertSnapshotsEqual(t *testing.T, d *Deriver, n *naiveDeriver) {
	t.Helper()
	actual := actualSnapshot(t, d)
	expected, err := n.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != len(expected.classes) {
		t.Fatalf("column count: actual %d, expected %d", len(actual), len(expected.classes))
	}
	for column, expectedClass := range expected.classes {
		actualClass, ok := actual[column]
		if !ok {
			t.Fatalf("actual snapshot missing %q", column)
		}
		if !slices.Equal(actualClass.members, expectedClass.members) ||
			actualClass.lo != expectedClass.lo ||
			actualClass.hi != expectedClass.hi ||
			!slices.Equal(actualClass.excluded, expectedClass.excluded) {
			t.Fatalf("class for %q: actual %+v, expected %+v", column, actualClass, expectedClass)
		}
	}
}

func errorCategory(err error) string {
	switch {
	case errors.Is(err, ErrInvalidPredicate), errors.Is(err, errNaiveInvalid):
		return "invalid"
	case errors.Is(err, ErrContradiction), errors.Is(err, errNaiveContradiction):
		return "contradiction"
	case errors.Is(err, ErrUnknownColumn), errors.Is(err, errNaiveUnknownColumn):
		return "unknown"
	case err == nil:
		return "accepted"
	default:
		return err.Error()
	}
}

func assignmentSatisfies(pred Predicate, assignment map[string]int64) bool {
	if pred.Kind == Cmp {
		value := assignment[pred.Column]
		switch pred.Operator {
		case OpEqual:
			return value == pred.Value
		case OpNotEqual:
			return value != pred.Value
		case OpLess:
			return value < pred.Value
		case OpLessEqual:
			return value <= pred.Value
		case OpGreater:
			return value > pred.Value
		case OpGreaterEqual:
			return value >= pred.Value
		}
		return false
	}

	if pred.Kind == Eq {
		return assignment[pred.ColumnA] == assignment[pred.ColumnB]
	}
	return assignment[pred.ColumnA] != assignment[pred.ColumnB]
}

func bruteForce(t *testing.T, accepted []Predicate, extra []Predicate, columns []string) bool {
	t.Helper()
	assignment := make(map[string]int64, len(columns))
	var search func(int) bool
	search = func(index int) bool {
		if index == len(columns) {
			for _, pred := range accepted {
				if !assignmentSatisfies(pred, assignment) {
					return false
				}
			}
			for _, pred := range extra {
				if !assignmentSatisfies(pred, assignment) {
					return false
				}
			}
			return true
		}
		for value := int64(-9); value <= 9; value++ {
			assignment[columns[index]] = value
			if search(index + 1) {
				return true
			}
		}
		return false
	}
	return search(0)
}

func assertBrutePinned(t *testing.T, accepted []Predicate, d *Deriver, columns []string) {
	t.Helper()
	for _, column := range columns {
		value, pinned, err := d.Pinned(column)
		if err != nil || !pinned {
			continue
		}
		assignment := make(map[string]int64, len(columns))
		for _, other := range columns {
			assignment[other] = MinInt64
		}
		for assignedValue := int64(-9); assignedValue <= 9; assignedValue++ {
			assignment[column] = assignedValue
			var fillAndCheck func(int) bool
			fillAndCheck = func(index int) bool {
				if index == len(columns) {
					for _, pred := range accepted {
						if !assignmentSatisfies(pred, assignment) {
							return false
						}
					}
					return true
				}
				other := columns[index]
				if other == column {
					return fillAndCheck(index + 1)
				}
				for candidate := int64(-9); candidate <= 9; candidate++ {
					assignment[other] = candidate
					if fillAndCheck(index + 1) {
						return true
					}
				}
				return false
			}
			exists := fillAndCheck(0)
			t.Logf("brute pinned check column=%s assigned=%d solutionExists=%t", column, assignedValue, exists)
			if exists != (assignedValue == value) {
				t.Fatalf("pinned column %q has solution at %d = %t, pinned value is %d", column, assignedValue, exists, value)
			}
		}
	}
}

func randomPredicate(r *rand.Rand, columns []string, allowInvalid bool) Predicate {
	if allowInvalid && r.Intn(5) == 0 {
		if r.Intn(2) == 0 {
			return NewCmp("", Operator("?"), int64(r.Intn(7)-3))
		}
		return NewEq("", columns[0])
	}

	column := columns[r.Intn(len(columns))]
	switch r.Intn(10) {
	case 0:
		return NewEq(column, columns[r.Intn(len(columns))])
	case 1:
		return NewNe(column, columns[r.Intn(len(columns))])
	default:
		ops := []Operator{OpEqual, OpNotEqual, OpLess, OpLessEqual, OpGreater, OpGreaterEqual}
		return NewCmp(column, ops[r.Intn(len(ops))], int64(r.Intn(7)-3))
	}
}

func replaySequence(t *testing.T, predicates []Predicate, columns []string) (*Deriver, *naiveDeriver, []Predicate) {
	t.Helper()
	d := NewDeriver()
	n := newNaiveDeriver()
	accepted := make([]Predicate, 0, len(predicates))
	for step, pred := range predicates {
		actualErr := d.Add(pred)
		naiveErr := n.Add(pred)
		t.Logf("input step=%d pred=%v => actual=%s naive=%s", step, pred, errorCategory(actualErr), errorCategory(naiveErr))
		if errorCategory(actualErr) != errorCategory(naiveErr) {
			t.Fatalf("Add(%v): actual %v, naive %v", pred, actualErr, naiveErr)
		}
		assertSnapshotsEqual(t, d, n)
		if actualErr == nil {
			accepted = append(accepted, pred)
			continue
		}
		if validateInputPredicate(pred) == nil {
			hasSolution := bruteForce(t, n.accepted, []Predicate{pred}, columns)
			t.Logf("brute rejected decision pred=%v hasSolution=%t (want false)", pred, hasSolution)
			if hasSolution {
				t.Fatalf("brute force found solution for rejected predicate %v", pred)
			}
		}
	}
	return d, n, accepted
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	r := rand.New(rand.NewSource(1072))
	for iteration := 0; iteration < 2000; iteration++ {
		t.Run(fmt.Sprintf("sequence_%04d", iteration), func(t *testing.T) {
			columns := []string{"a", "b", "c"}[:1+r.Intn(3)]
			predicates := make([]Predicate, 1+r.Intn(12))
			for step := range predicates {
				predicates[step] = randomPredicate(r, columns, true)
			}

			d, n, accepted := replaySequence(t, predicates, columns)
			assertBrutePinned(t, n.accepted, d, columns)

			shuffled := slices.Clone(accepted)
			r.Shuffle(len(shuffled), func(i, j int) {
				shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
			})
			replayD := NewDeriver()
			replayN := newNaiveDeriver()
			for _, pred := range shuffled {
				if err := replayD.Add(pred); err != nil {
					t.Fatalf("replay accepted predicate %v: %v", pred, err)
				}
				if err := replayN.Add(pred); err != nil {
					t.Fatalf("naive replay accepted predicate %v: %v", pred, err)
				}
			}
			replayActual := actualSnapshot(t, replayD)
			replayExpected, err := replayN.snapshot()
			if err != nil {
				t.Fatal(err)
			}
			originalActual := actualSnapshot(t, d)
			for column, expectedClass := range replayExpected.classes {
				got := replayActual[column]
				original := originalActual[column]
				t.Logf("commutativity column=%s originalMembers=%v replayMembers=%v range=[%d,%d] excluded=%v", column, original.members, got.members, got.lo, got.hi, got.excluded)
				if !slices.Equal(got.members, original.members) ||
					got.lo != original.lo || got.hi != original.hi ||
					!slices.Equal(got.excluded, original.excluded) ||
					!slices.Equal(got.members, expectedClass.members) ||
					got.lo != expectedClass.lo || got.hi != expectedClass.hi ||
					!slices.Equal(got.excluded, expectedClass.excluded) {
					t.Fatalf("permutation mismatch for %q: replay %+v, original %+v, naive %+v", column, got, original, expectedClass)
				}
			}
		})
	}
}
