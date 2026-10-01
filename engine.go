package ontology

import "sync"

type Engine struct {
	mu    sync.RWMutex
	state *engineState
}

func NewEngine() *Engine {
	return &Engine{state: newEngineState()}
}

func (e *Engine) Add(predicate Predicate) error {
	if err := validateInsertPredicate(predicate); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	candidate := e.state.clone()
	switch predicate.Kind {
	case KindCmp:
		root := candidate.rootForColumn(predicate.Column)
		if err := applyCmp(candidate.classes[root], predicate.Op, predicate.Value); err != nil {
			return err
		}
		if err := candidate.propagateNe(); err != nil {
			return err
		}
	case KindEq:
		rootA := candidate.rootForColumn(predicate.Column)
		rootB := candidate.rootForColumn(predicate.Column2)
		if rootA != rootB {
			if _, err := candidate.merge(rootA, rootB); err != nil {
				return err
			}
		}
	case KindNe:
		rootA := candidate.rootForColumn(predicate.Column)
		rootB := candidate.rootForColumn(predicate.Column2)
		if err := candidate.addNe(rootA, rootB); err != nil {
			return err
		}
	}
	e.state = candidate
	return nil
}

func (e *Engine) Range(column string) (int64, int64, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	class, err := e.lookupClass(column)
	if err != nil {
		return 0, 0, err
	}
	return class.lo, class.hi, nil
}

func (e *Engine) Pinned(column string) (int64, bool, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	class, err := e.lookupClass(column)
	if err != nil {
		return 0, false, err
	}
	value, pinned := class.pinned()
	return value, pinned, nil
}

func (e *Engine) Excluded(column string) ([]int64, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	class, err := e.lookupClass(column)
	if err != nil {
		return nil, err
	}
	values := make([]int64, 0, len(class.excluded))
	for value := range class.excluded {
		values = append(values, value)
	}
	sortInt64s(values)
	return values, nil
}

func (e *Engine) Same(left, right string) (bool, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	idA, ok := e.state.columns[left]
	if !ok {
		return false, ErrColumnNotFound
	}
	idB, ok := e.state.columns[right]
	if !ok {
		return false, ErrColumnNotFound
	}
	return e.state.findReadOnly(idA) == e.state.findReadOnly(idB), nil
}

func (e *Engine) Implies(predicate Predicate) (bool, error) {
	if err := validateImpliesPredicate(predicate); err != nil {
		return false, err
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	class, err := e.lookupClass(predicate.Column)
	if err != nil {
		return false, err
	}
	if predicate.Kind == KindEq {
		other, err := e.lookupClass(predicate.Column2)
		if err != nil {
			return false, err
		}
		if class == other {
			return true, nil
		}
		valueA, pinnedA := class.pinned()
		valueB, pinnedB := other.pinned()
		return pinnedA && pinnedB && valueA == valueB, nil
	}

	value := predicate.Value
	switch predicate.Op {
	case OpEq:
		pinnedValue, pinned := class.pinned()
		return pinned && pinnedValue == value, nil
	case OpNe:
		if value < class.lo || value > class.hi {
			return true, nil
		}
		_, excluded := class.excluded[value]
		return excluded, nil
	case OpLt:
		return class.hi < value, nil
	case OpLe:
		return class.hi <= value, nil
	case OpGt:
		return class.lo > value, nil
	case OpGe:
		return class.lo >= value, nil
	default:
		return false, ErrInvalidPredicate
	}
}

func validateInsertPredicate(predicate Predicate) error {
	switch predicate.Kind {
	case KindCmp:
		if predicate.Column == "" || !validCmpOp(predicate.Op) {
			return ErrInvalidPredicate
		}
	case KindEq, KindNe:
		if predicate.Column == "" || predicate.Column2 == "" {
			return ErrInvalidPredicate
		}
	default:
		return ErrInvalidPredicate
	}
	return nil
}

func validateImpliesPredicate(predicate Predicate) error {
	switch predicate.Kind {
	case KindCmp:
		if predicate.Column == "" || !validCmpOp(predicate.Op) {
			return ErrInvalidPredicate
		}
	case KindEq:
		if predicate.Column == "" || predicate.Column2 == "" {
			return ErrInvalidPredicate
		}
	default:
		return ErrInvalidPredicate
	}
	return nil
}

func validCmpOp(op CmpOp) bool {
	switch op {
	case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
		return true
	default:
		return false
	}
}

func (e *Engine) lookupClass(column string) (*eqClass, error) {
	id, ok := e.state.columns[column]
	if !ok {
		return nil, ErrColumnNotFound
	}
	return e.state.classes[e.state.findReadOnly(id)], nil
}

func sortInt64s(values []int64) {
	for i := 1; i < len(values); i++ {
		value := values[i]
		j := i - 1
		for j >= 0 && values[j] > value {
			values[j+1] = values[j]
			j--
		}
		values[j+1] = value
	}
}
