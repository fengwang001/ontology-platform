package instance

// BatchWrite applies a mixed set of Create/Update/Delete operations
// atomically. Every entry is validated before anything is committed:
// duplicate primary keys within the batch, version mismatches, and attribute
// constraint violations all reject the whole batch and leave versions and
// write times untouched.
//
// On success the returned instances are ordered like ops for Create and
// Update entries; Delete entries contribute nil.
func (s *Store) BatchWrite(ops []Op) ([]*Instance, error) {
	type planned struct {
		rec  *record
		kind OpKind
		// created means rec was not previously present and must be inserted.
		created bool
		attrs   map[string]any
	}

	// Clone outside the lock; a bad value fails the batch before it starts.
	cloned := make([]map[string]any, len(ops))
	for i, op := range ops {
		if op.Kind != OpCreate && op.Kind != OpUpdate && op.Kind != OpDelete {
			return nil, batchErr(i, op, ReasonConstraint, 0, 0, errBadOpKind)
		}
		if op.ObjectType == "" || op.Key == "" {
			return nil, batchErr(i, op, ReasonConstraint, 0, 0, errEmptyIdentity)
		}
		if op.Kind == OpDelete {
			continue
		}
		attrs, err := cloneAttributes(op.Attributes)
		if err != nil {
			return nil, batchErr(i, op, ReasonConstraint, op.ExpectedVersion, 0, err)
		}
		cloned[i] = attrs
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[recordKey]int, len(ops))
	plans := make([]planned, len(ops))

	// Validate everything against a transactional view of the data.
	for i, op := range ops {
		rk := recordKey{op.ObjectType, op.Key}
		if first, dup := seen[rk]; dup {
			return nil, batchErr(i, op, ReasonDuplicateInBatch, op.ExpectedVersion, 0,
				duplicateErr(first))
		}
		seen[rk] = i

		rec := s.records[rk]
		switch op.Kind {
		case OpCreate:
			if rec != nil && !rec.instance.Deleted {
				return nil, batchErr(i, op, ReasonAlreadyExists, 0, rec.instance.Version, nil)
			}
		case OpUpdate, OpDelete:
			if rec == nil {
				return nil, batchErr(i, op, ReasonNotFound, op.ExpectedVersion, 0, nil)
			}
			if rec.instance.Deleted {
				return nil, batchErr(i, op, ReasonDeleted, op.ExpectedVersion, rec.instance.Version, nil)
			}
			if rec.instance.Version != op.ExpectedVersion {
				return nil, batchErr(i, op, ReasonVersionConflict, op.ExpectedVersion, rec.instance.Version, nil)
			}
		}

		plans[i] = planned{rec: rec, kind: op.Kind, attrs: cloned[i], created: rec == nil}
	}

	// All checks passed; commit in request order. No validation remains that
	// can fail here, so this sequence cannot produce a partial state.
	now := s.now()
	results := make([]*Instance, len(ops))
	for i, op := range ops {
		p := &plans[i]
		if p.created {
			p.rec = &record{instance: Instance{ObjectType: op.ObjectType, Key: op.Key}}
			s.records[recordKey{op.ObjectType, op.Key}] = p.rec
		}
		inst := &p.rec.instance
		inst.Version++
		inst.LastWriteTime = now
		switch op.Kind {
		case OpCreate:
			inst.ObjectType = op.ObjectType
			inst.Key = op.Key
			inst.Attributes = p.attrs
			inst.Deleted = false
			results[i] = snapshotInstance(inst)
		case OpUpdate:
			inst.Attributes = p.attrs
			results[i] = snapshotInstance(inst)
		case OpDelete:
			inst.Attributes = nil
			inst.Deleted = true
			results[i] = nil
		}
	}
	return results, nil
}
