package ontology

// Op is one write operation inside a batch.
type Op struct {
	Delete bool // true: delete PK; false: insert Props under PK
	PK     string
	Props  map[string]Value
}

// InsertOp builds an insert operation.
func InsertOp(pk string, props map[string]Value) Op {
	return Op{PK: pk, Props: props}
}

// DeleteOp builds a delete operation.
func DeleteOp(pk string) Op { return Op{Delete: true, PK: pk} }

// ApplyBatch applies ops atomically with deferred constraint checking:
// each op sees the effects of earlier ops in the same batch (so deleting
// a record and then inserting one with the same normalized key succeeds),
// and any conflict aborts the whole batch, leaving stored records
// untouched.
func (s *Store) ApplyBatch(ops []Op) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, index := s.cloneLocked()

	// insertedBy tracks which batch op inserted each normalized key, so
	// intra-batch conflicts can name both op positions.
	insertedBy := make([]map[string]int, len(s.constraints))
	for i := range insertedBy {
		insertedBy[i] = make(map[string]int)
	}

	for pos, op := range ops {
		if op.Delete {
			deleteLocked(op.PK, s.constraints, s.norm, index, records)
			continue
		}
		if _, dup := records[op.PK]; dup {
			return &BatchError{OpIndex: pos, OtherOpIndex: -1, Conflict: &ConflictError{
				Constraint: "(primary key)",
				ExistingPK: op.PK,
			}}
		}
		if err := s.checkBatchOp(op, pos, index, records, insertedBy); err != nil {
			return err
		}
		s.putLocked(op.PK, op.Props, index, records)
		for i, c := range s.constraints {
			if key, ok := c.key(s.norm, op.Props); ok {
				insertedBy[i][key] = pos
			}
		}
	}

	s.records = records
	s.index = index
	return nil
}

// checkBatchOp validates one insert op against the batch's working
// state, reporting op positions for intra-batch conflicts.
func (s *Store) checkBatchOp(op Op, pos int,
	index []map[string]string, records map[string]map[string]Value,
	insertedBy []map[string]int) error {
	for i, c := range s.constraints {
		key, ok := c.key(s.norm, op.Props)
		if !ok {
			continue
		}
		holder, taken := index[i][key]
		if !taken || holder == op.PK {
			continue
		}
		other := -1
		if j, ok := insertedBy[i][key]; ok {
			other = j
		}
		return &BatchError{
			OpIndex:      pos,
			OtherOpIndex: other,
			Conflict: &ConflictError{
				Constraint: c.Name,
				Key:        key,
				ExistingPK: holder,
				Incoming:   pickCols(c, op.Props),
				Existing:   pickCols(c, records[holder]),
			},
		}
	}
	return nil
}

// cloneLocked snapshots records and index for speculative batch work.
// Inner property maps are shared but never mutated in place.
func (s *Store) cloneLocked() (map[string]map[string]Value, []map[string]string) {
	records := make(map[string]map[string]Value, len(s.records))
	for pk, props := range s.records {
		records[pk] = props
	}
	index := make([]map[string]string, len(s.index))
	for i := range s.index {
		m := make(map[string]string, len(s.index[i]))
		for k, v := range s.index[i] {
			m[k] = v
		}
		index[i] = m
	}
	return records, index
}
