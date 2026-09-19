package ontology

// Stats describes how a traversal has observed the store change since the
// traversal's snapshot was taken.
type Stats struct {
	// MutatedByInsert reports that at least one new key was inserted into
	// the store during this traversal.
	MutatedByInsert bool
	// MutatedByDelete reports that at least one key was deleted from the
	// store during this traversal.
	MutatedByDelete bool
	// SkippedInserted counts elements inserted during the traversal whose
	// sort position falls before the traversal's current position. They are
	// skipped because they are not part of the snapshot.
	SkippedInserted int
	// SkippedDeleted counts snapshot elements in the already-scanned region
	// that were deleted before being reached (dropped rather than truncated).
	SkippedDeleted int
}

// TraversalStats returns the mutation flags and cumulative skip counters of
// the traversal identified by cursor. The cursor may be any cursor issued
// for that traversal, including the most recent Page.Next.
func (s *Store) TraversalStats(cursor string) (Stats, error) {
	sess, err := s.cursorSession(cursor)
	if err != nil {
		return Stats{}, err
	}
	st := Stats{
		MutatedByInsert: s.insCnt.Load() != sess.baseIns,
		MutatedByDelete: s.delCnt.Load() != sess.baseDel,
	}
	hwm := int(sess.hwm.Load())

	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := 0; i < hwm && i < len(sess.keys); i++ {
		if _, ok := s.values[sess.keys[i]]; !ok {
			st.SkippedDeleted++
		}
	}
	bounded := hwm < len(sess.keys)
	bound := ""
	if bounded {
		bound = sess.keys[hwm]
	}
	for _, k := range s.keys {
		if bounded && k >= bound {
			break
		}
		if _, ok := sess.set[k]; !ok {
			st.SkippedInserted++
		}
	}
	return st, nil
}

// Invalidate explicitly ends the traversal session identified by cursor.
// Any later Scan or TraversalStats with a cursor of that session fails with
// ErrInvalidSession, which is distinct from ErrInvalidCursor.
func (s *Store) Invalidate(cursor string) error {
	id, _, err := s.decodeCursor(cursor)
	if err != nil {
		return err
	}
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	if _, ok := s.sessions[id]; !ok {
		return ErrInvalidSession
	}
	delete(s.sessions, id)
	return nil
}

func (s *Store) cursorSession(cursor string) (*session, error) {
	id, _, err := s.decodeCursor(cursor)
	if err != nil {
		return nil, err
	}
	sess, ok := s.findSession(id)
	if !ok {
		return nil, ErrInvalidSession
	}
	return sess, nil
}
