package ontology

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

const (
	MinInt64 = math.MinInt64
	MaxInt64 = math.MaxInt64
)

type Operator string

const (
	OpEqual        Operator = "="
	OpNotEqual     Operator = "!="
	OpLess         Operator = "<"
	OpLessEqual    Operator = "<="
	OpGreater      Operator = ">"
	OpGreaterEqual Operator = ">="
)

type PredicateKind int

const (
	Cmp PredicateKind = iota
	Eq
	Ne
)

type Predicate struct {
	Kind     PredicateKind
	Column   string
	Operator Operator
	Value    int64
	ColumnA  string
	ColumnB  string
}

type Deriver struct {
	mu    sync.RWMutex
	state *deriverState
}

func NewDeriver() *Deriver {
	return &Deriver{state: newState()}
}

func NewCmp(column string, op Operator, value int64) Predicate {
	return Predicate{Kind: Cmp, Column: column, Operator: op, Value: value}
}

func NewEq(a, b string) Predicate {
	return Predicate{Kind: Eq, ColumnA: a, ColumnB: b}
}

func NewNe(a, b string) Predicate {
	return Predicate{Kind: Ne, ColumnA: a, ColumnB: b}
}

func (d *Deriver) Add(pred Predicate) error {
	if err := validateInputPredicate(pred); err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	candidate := d.state.clone()
	if pred.Kind == Cmp {
		candidate.ensureColumn(pred.Column)
	} else {
		candidate.ensureColumn(pred.ColumnA)
		candidate.ensureColumn(pred.ColumnB)
	}

	switch pred.Kind {
	case Cmp:
		root := candidate.find(pred.Column)
		current := candidate.classes[root]
		switch pred.Operator {
		case OpEqual:
			current.lo = maxInt64(current.lo, pred.Value)
			current.hi = minInt64(current.hi, pred.Value)
		case OpNotEqual:
			current.excluded[pred.Value] = struct{}{}
		case OpLess:
			if pred.Value == MinInt64 {
				return ErrContradiction
			}
			current.hi = minInt64(current.hi, pred.Value-1)
		case OpLessEqual:
			current.hi = minInt64(current.hi, pred.Value)
		case OpGreater:
			if pred.Value == MaxInt64 {
				return ErrContradiction
			}
			current.lo = maxInt64(current.lo, pred.Value+1)
		case OpGreaterEqual:
			current.lo = maxInt64(current.lo, pred.Value)
		}
	case Eq:
		rootA := candidate.find(pred.ColumnA)
		rootB := candidate.find(pred.ColumnB)
		if rootA != rootB {
			if neighbors := candidate.different[rootA]; neighbors != nil {
				if _, different := neighbors[rootB]; different {
					return ErrContradiction
				}
			}
		}
		candidate.unionColumns(pred.ColumnA, pred.ColumnB)
	case Ne:
		rootA := candidate.find(pred.ColumnA)
		rootB := candidate.find(pred.ColumnB)
		if rootA == rootB {
			return ErrContradiction
		}
		candidate.addDifferent(rootA, rootB)
	}

	if err := candidate.close(); err != nil {
		return err
	}
	d.state = candidate
	return nil
}

func (d *Deriver) Range(column string) (int64, int64, error) {
	if column == "" {
		return 0, 0, invalidPredicatef("empty column name")
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	root, ok := d.state.registeredRoot(column)
	if !ok {
		return 0, 0, unknownColumnf(column)
	}
	current := d.state.classes[root]
	return current.lo, current.hi, nil
}

func (d *Deriver) Pinned(column string) (int64, bool, error) {
	if column == "" {
		return 0, false, invalidPredicatef("empty column name")
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	root, ok := d.state.registeredRoot(column)
	if !ok {
		return 0, false, unknownColumnf(column)
	}
	current := d.state.classes[root]
	return current.lo, current.lo == current.hi, nil
}

func (d *Deriver) Excluded(column string) ([]int64, error) {
	if column == "" {
		return nil, invalidPredicatef("empty column name")
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	root, ok := d.state.registeredRoot(column)
	if !ok {
		return nil, unknownColumnf(column)
	}
	values := make([]int64, 0, len(d.state.classes[root].excluded))
	for value := range d.state.classes[root].excluded {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values, nil
}

func (d *Deriver) Same(a, b string) (bool, error) {
	if a == "" || b == "" {
		column := a
		if column == "" {
			column = b
		}
		return false, invalidPredicatef("empty column name %q", column)
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	rootA, ok := d.state.registeredRoot(a)
	if !ok {
		return false, unknownColumnf(a)
	}
	rootB, ok := d.state.registeredRoot(b)
	if !ok {
		return false, unknownColumnf(b)
	}
	return rootA == rootB, nil
}

func (d *Deriver) Implies(pred Predicate) (bool, error) {
	if err := validateImplicationPredicate(pred); err != nil {
		return false, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	if pred.Kind == Cmp {
		root, ok := d.state.registeredRoot(pred.Column)
		if !ok {
			return false, unknownColumnf(pred.Column)
		}
		current := d.state.classes[root]
		_, excluded := current.excluded[pred.Value]
		switch pred.Operator {
		case OpEqual:
			return current.lo == current.hi && current.lo == pred.Value, nil
		case OpNotEqual:
			return pred.Value < current.lo || pred.Value > current.hi || excluded, nil
		case OpLess:
			return current.hi < pred.Value, nil
		case OpLessEqual:
			return current.hi <= pred.Value, nil
		case OpGreater:
			return current.lo > pred.Value, nil
		case OpGreaterEqual:
			return current.lo >= pred.Value, nil
		}
	}

	rootA, ok := d.state.registeredRoot(pred.ColumnA)
	if !ok {
		return false, unknownColumnf(pred.ColumnA)
	}
	rootB, ok := d.state.registeredRoot(pred.ColumnB)
	if !ok {
		return false, unknownColumnf(pred.ColumnB)
	}
	if rootA == rootB {
		return true, nil
	}
	classA := d.state.classes[rootA]
	classB := d.state.classes[rootB]
	return classA.lo == classA.hi && classB.lo == classB.hi && classA.lo == classB.lo, nil
}

func validateInputPredicate(pred Predicate) error {
	switch pred.Kind {
	case Cmp:
		if pred.Column == "" || !validOperator(pred.Operator) {
			return invalidPredicatef("Cmp requires a non-empty column and one of =, !=, <, <=, >, >=")
		}
	case Eq, Ne:
		if pred.ColumnA == "" || pred.ColumnB == "" {
			return invalidPredicatef("%s requires non-empty column names", kindName(pred.Kind))
		}
	default:
		return invalidPredicatef("unknown predicate kind")
	}
	return nil
}

func validateImplicationPredicate(pred Predicate) error {
	switch pred.Kind {
	case Cmp:
		if pred.Column == "" || !validOperator(pred.Operator) {
			return invalidPredicatef("Cmp requires a non-empty column and one of =, !=, <, <=, >, >=")
		}
	case Eq:
		if pred.ColumnA == "" || pred.ColumnB == "" {
			return invalidPredicatef("Eq requires non-empty column names")
		}
	case Ne:
		return invalidPredicatef("Ne is not accepted by Implies")
	default:
		return invalidPredicatef("unknown predicate kind")
	}
	return nil
}

func validOperator(op Operator) bool {
	switch op {
	case OpEqual, OpNotEqual, OpLess, OpLessEqual, OpGreater, OpGreaterEqual:
		return true
	default:
		return false
	}
}

func kindName(kind PredicateKind) string {
	switch kind {
	case Eq:
		return "Eq"
	case Ne:
		return "Ne"
	default:
		return "predicate"
	}
}

func unknownColumnf(column string) error {
	return fmt.Errorf("%w: %q", ErrUnknownColumn, column)
}
