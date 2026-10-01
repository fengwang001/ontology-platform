package ontology

type eqClass struct {
	lo       int64
	hi       int64
	excluded map[int64]struct{}
}

type engineState struct {
	columns map[string]int
	parent  map[int]int
	classes map[int]*eqClass
	nePairs map[[2]int]struct{}
	nextID  int
}

func newEngineState() *engineState {
	return &engineState{
		columns: make(map[string]int),
		parent:  make(map[int]int),
		classes: make(map[int]*eqClass),
		nePairs: make(map[[2]int]struct{}),
	}
}

func (s *engineState) clone() *engineState {
	copied := &engineState{
		columns: make(map[string]int, len(s.columns)),
		parent:  make(map[int]int, len(s.parent)),
		classes: make(map[int]*eqClass, len(s.classes)),
		nePairs: make(map[[2]int]struct{}, len(s.nePairs)),
		nextID:  s.nextID,
	}
	for column, id := range s.columns {
		copied.columns[column] = id
	}
	for id, parent := range s.parent {
		copied.parent[id] = parent
	}
	for id, class := range s.classes {
		excluded := make(map[int64]struct{}, len(class.excluded))
		for value := range class.excluded {
			excluded[value] = struct{}{}
		}
		copied.classes[id] = &eqClass{lo: class.lo, hi: class.hi, excluded: excluded}
	}
	for pair := range s.nePairs {
		copied.nePairs[pair] = struct{}{}
	}
	return copied
}

func (s *engineState) find(id int) int {
	root := id
	for s.parent[root] != root {
		root = s.parent[root]
	}
	for s.parent[id] != id {
		next := s.parent[id]
		s.parent[id] = root
		id = next
	}
	return root
}

func (s *engineState) findReadOnly(id int) int {
	for s.parent[id] != id {
		id = s.parent[id]
	}
	return id
}

func (s *engineState) rootForColumn(column string) int {
	if id, ok := s.columns[column]; ok {
		return s.find(id)
	}
	id := s.nextID
	s.nextID++
	s.columns[column] = id
	s.parent[id] = id
	s.classes[id] = &eqClass{
		lo:       minInt64,
		hi:       maxInt64,
		excluded: make(map[int64]struct{}),
	}
	return id
}

func (s *engineState) classForColumn(column string) *eqClass {
	return s.classes[s.find(s.columns[column])]
}

func (class *eqClass) normalize() error {
	for {
		if class.lo > class.hi {
			return ErrContradiction
		}
		for value := range class.excluded {
			if value < class.lo || value > class.hi {
				delete(class.excluded, value)
			}
		}
		if _, ok := class.excluded[class.lo]; ok {
			if class.lo == maxInt64 {
				return ErrContradiction
			}
			class.lo++
			continue
		}
		if _, ok := class.excluded[class.hi]; ok {
			if class.hi == minInt64 {
				return ErrContradiction
			}
			class.hi--
			continue
		}
		return nil
	}
}

func (class *eqClass) pinned() (int64, bool) {
	return class.lo, class.lo == class.hi
}
