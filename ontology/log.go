package ontology

func (s *Store) recordFailureLocked(root string, err error, p *plan) {
	s.nextRequestID++
	entry := LogEntry{
		RequestID:    s.nextRequestID,
		RootObjectID: root,
		Error:        err.Error(),
		Committed:    false,
	}
	if p != nil {
		entry.Steps = append([]DeleteStep(nil), p.steps...)
		entry.DedupProbes = p.dedupProbes
	}
	s.logs = append(s.logs, entry)
}

func (s *Store) recordSuccessLocked(root string, p *plan, result DeleteResult) {
	s.nextRequestID++
	s.logs = append(s.logs, LogEntry{
		RequestID:      s.nextRequestID,
		RootObjectID:   root,
		DeletedObjects: append([]string(nil), result.DeletedObjects...),
		RemovedLinks:   append([]Link(nil), result.RemovedLinks...),
		NullifiedLinks: append([]Link(nil), result.NullifiedLinks...),
		Steps:          append([]DeleteStep(nil), p.steps...),
		DedupProbes:    p.dedupProbes,
		Committed:      true,
	})
}
