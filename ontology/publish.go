package ontology

// publish installs the shadow image into committed state. It runs entirely
// inside the caller's tabMu write section while all instance locks are held:
// there is exactly one commit point (the subsequent tabMu unlock), so no
// reader can ever observe a partially published batch.
func (s *Store) publish(in BatchInput, proposed []*Instance, view *shadowView, edgeOrder []edgeKey) map[InstanceID]Version {
	for i, item := range in.Items {
		s.instances[item.ID] = proposed[i]
	}

	for _, key := range edgeOrder {
		present := view.edges[key]
		_, exists := s.edges[key]
		if present && !exists {
			s.edges[key] = struct{}{}
			addAdj(s.outAdj, key.source, key.link, key.target)
			addAdj(s.inAdj, key.target, key.link, key.source)
		}
		if !present && exists {
			delete(s.edges, key)
			removeAdj(s.outAdj, key.source, key.link, key.target)
			removeAdj(s.inAdj, key.target, key.link, key.source)
		}
	}

	s.commitClock++

	versions := make(map[InstanceID]Version, len(in.Items))
	for i, item := range in.Items {
		versions[item.ID] = proposed[i].Version
	}
	return versions
}

func addAdj(m map[InstanceID]map[LinkTypeName]map[InstanceID]struct{}, endpoint InstanceID, link LinkTypeName, other InstanceID) {
	byLink := m[endpoint]
	if byLink == nil {
		byLink = map[LinkTypeName]map[InstanceID]struct{}{}
		m[endpoint] = byLink
	}
	neighbors := byLink[link]
	if neighbors == nil {
		neighbors = map[InstanceID]struct{}{}
		byLink[link] = neighbors
	}
	neighbors[other] = struct{}{}
}

func removeAdj(m map[InstanceID]map[LinkTypeName]map[InstanceID]struct{}, endpoint InstanceID, link LinkTypeName, other InstanceID) {
	byLink := m[endpoint]
	if byLink == nil {
		return
	}
	neighbors := byLink[link]
	if neighbors == nil {
		return
	}
	delete(neighbors, other)
	if len(neighbors) == 0 {
		delete(byLink, link)
	}
	if len(byLink) == 0 {
		delete(m, endpoint)
	}
}

// finishCommit appends the successful attempt record. Called after tabMu has
// been released; seq must be captured while tabMu is still held.
func (s *Store) finishCommit(rec *JournalRecord, seq int64) {
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	s.attemptSeq++
	rec.Seq = seq
	rec.Committed = true
	rec.Phase = "commit"
	s.journal = append(s.journal, cloneRecord(rec))
}

// finishReject appends the rejected attempt record without advancing any
// business clock: rejected batches leave versions, links and the commit
// clock byte-for-byte unchanged. seq must be captured while tabMu is held.
func (s *Store) finishReject(rec *JournalRecord, phase string, err *BatchError, seq int64) {
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	s.attemptSeq++
	rec.Seq = seq
	rec.Committed = false
	rec.Phase = phase
	rec.Failure = err
	s.journal = append(s.journal, cloneRecord(rec))
}

// abortLocked captures the commit clock under the held write lock, releases
// tabMu and records a rejection. It returns the same error for convenience.
func (s *Store) abortLocked(rec *JournalRecord, phase string, err *BatchError) *BatchError {
	seq := s.commitClock
	s.tabMu.Unlock()
	s.finishReject(rec, phase, err, seq)
	return err
}

// abortBeforeLock records a rejection decided before tabMu was acquired
// (phase 0 duplicates). It reads the clock under a short read lock.
func (s *Store) abortBeforeLock(rec *JournalRecord, phase string, err *BatchError) *BatchError {
	s.tabMu.RLock()
	seq := s.commitClock
	s.tabMu.RUnlock()
	s.finishReject(rec, phase, err, seq)
	return err
}

func cloneInput(in BatchInput) BatchInput {
	cp := BatchInput{ClientID: in.ClientID}
	cp.Items = make([]BatchItem, len(in.Items))
	for i, item := range in.Items {
		cp.Items[i] = item
		cp.Items[i].Properties = cloneProperties(item.Properties)
		cp.Items[i].LinkDeltas = append([]EdgeDelta(nil), item.LinkDeltas...)
	}
	return cp
}
