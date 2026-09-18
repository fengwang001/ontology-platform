package instance

// Update replaces the attributes of a live instance. expectedVersion must
// equal the version returned by the caller's last Get; otherwise the update is
// rejected with ReasonVersionConflict carrying both expected and actual.
func (s *Store) Update(objectType, key string, expectedVersion int64, attributes map[string]any) (*Instance, error) {
	attrs, err := cloneAttributes(attributes)
	if err != nil {
		return nil, &WriteError{
			Reason:     ReasonConstraint,
			ObjectType: objectType,
			Key:        key,
			Expected:   expectedVersion,
			Err:        err,
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rk := recordKey{objectType, key}
	rec := s.records[rk]
	if rec == nil {
		return nil, &WriteError{Reason: ReasonNotFound, ObjectType: objectType, Key: key, Expected: expectedVersion}
	}
	if rec.instance.Deleted {
		return nil, &WriteError{
			Reason:     ReasonDeleted,
			ObjectType: objectType,
			Key:        key,
			Expected:   expectedVersion,
			Actual:     rec.instance.Version,
		}
	}
	if rec.instance.Version != expectedVersion {
		return nil, &WriteError{
			Reason:     ReasonVersionConflict,
			ObjectType: objectType,
			Key:        key,
			Expected:   expectedVersion,
			Actual:     rec.instance.Version,
		}
	}

	rec.instance.Version++
	rec.instance.Attributes = attrs
	rec.instance.LastWriteTime = s.now()
	return snapshotInstance(&rec.instance), nil
}

// Delete logically deletes a live instance. The record and its version history
// are retained, so a later Create resurrects with a continuing version.
func (s *Store) Delete(objectType, key string, expectedVersion int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rk := recordKey{objectType, key}
	rec := s.records[rk]
	if rec == nil {
		return &WriteError{Reason: ReasonNotFound, ObjectType: objectType, Key: key, Expected: expectedVersion}
	}
	if rec.instance.Deleted {
		return &WriteError{
			Reason:     ReasonDeleted,
			ObjectType: objectType,
			Key:        key,
			Expected:   expectedVersion,
			Actual:     rec.instance.Version,
		}
	}
	if rec.instance.Version != expectedVersion {
		return &WriteError{
			Reason:     ReasonVersionConflict,
			ObjectType: objectType,
			Key:        key,
			Expected:   expectedVersion,
			Actual:     rec.instance.Version,
		}
	}

	rec.instance.Version++
	rec.instance.Deleted = true
	rec.instance.Attributes = nil
	rec.instance.LastWriteTime = s.now()
	return nil
}

func snapshotInstance(in *Instance) *Instance {
	attrs, err := cloneAttributes(in.Attributes)
	if err != nil {
		panic("instance: stored attributes failed clone: " + err.Error())
	}
	return &Instance{
		ObjectType:    in.ObjectType,
		Key:           in.Key,
		Version:       in.Version,
		Attributes:    attrs,
		LastWriteTime: in.LastWriteTime,
		Deleted:       in.Deleted,
	}
}
