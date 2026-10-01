package ontology

func applyCmp(class *eqClass, op CmpOp, value int64) error {
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
	return class.normalize()
}

func minInt64Value(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func maxInt64Value(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func (s *engineState) merge(rootA, rootB int) (*eqClass, error) {
	classA := s.classes[rootA]
	classB := s.classes[rootB]
	merged := &eqClass{
		lo:       maxInt64Value(classA.lo, classB.lo),
		hi:       minInt64Value(classA.hi, classB.hi),
		excluded: make(map[int64]struct{}, len(classA.excluded)+len(classB.excluded)),
	}
	for value := range classA.excluded {
		merged.excluded[value] = struct{}{}
	}
	for value := range classB.excluded {
		merged.excluded[value] = struct{}{}
	}
	if err := merged.normalize(); err != nil {
		return nil, err
	}

	parent, child := rootA, rootB
	if rootB < rootA {
		parent, child = rootB, rootA
	}
	s.parent[child] = parent
	delete(s.classes, child)
	s.classes[parent] = merged

	rewritten := make(map[[2]int]struct{})
	for pair := range s.nePairs {
		left := s.find(pair[0])
		right := s.find(pair[1])
		if left == right {
			return nil, ErrContradiction
		}
		rewritten[canonicalPair(left, right)] = struct{}{}
	}
	s.nePairs = rewritten
	if err := s.propagateNe(); err != nil {
		return nil, err
	}
	return s.classes[parent], nil
}

func (s *engineState) addNe(rootA, rootB int) error {
	if rootA == rootB {
		return ErrContradiction
	}
	pair := canonicalPair(rootA, rootB)
	if _, exists := s.nePairs[pair]; !exists {
		s.nePairs[pair] = struct{}{}
	}
	return s.propagateNe()
}

func canonicalPair(left, right int) [2]int {
	if left > right {
		left, right = right, left
	}
	return [2]int{left, right}
}

func (s *engineState) propagateNe() error {
	for {
		changed := false
		for pair := range s.nePairs {
			rootA := s.find(pair[0])
			rootB := s.find(pair[1])
			if rootA == rootB {
				return ErrContradiction
			}
			classA := s.classes[rootA]
			classB := s.classes[rootB]
			valueA, pinnedA := classA.pinned()
			valueB, pinnedB := classB.pinned()
			if pinnedA && pinnedB && valueA == valueB {
				return ErrContradiction
			}
			if pinnedA {
				if valueA >= classB.lo && valueA <= classB.hi {
					if _, exists := classB.excluded[valueA]; !exists {
						classB.excluded[valueA] = struct{}{}
						if err := classB.normalize(); err != nil {
							return err
						}
						changed = true
					}
				}
			}
			if pinnedB {
				if valueB >= classA.lo && valueB <= classA.hi {
					if _, exists := classA.excluded[valueB]; !exists {
						classA.excluded[valueB] = struct{}{}
						if err := classA.normalize(); err != nil {
							return err
						}
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
