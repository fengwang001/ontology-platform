package ontology

// Stats holds the unexported operation counters described by the spec.
type Stats struct {
	MacCalls          int64 // MAC invocations billed to this service
	RevocationLookups int64 // revocation table queries (live scan stops on hit)
	HeapPops          int64 // heap-top pops during reclamation
	ActiveRecords     int   // records with bound > current clock
	Clock             int64
	ClockSet          bool
}

// Snapshot returns the current counters and state under the service lock.
func (s *Service) Snapshot() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		MacCalls:          s.macCalls,
		RevocationLookups: s.revQueries,
		HeapPops:          s.heapPops,
		ActiveRecords:     s.active,
		Clock:             s.clock,
		ClockSet:          s.clockSet,
	}
}

// ResetCounters zeroes the per-service counters (table and clock untouched).
func (s *Service) ResetCounters() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.macCalls, s.revQueries, s.heapPops = 0, 0, 0
}

// AttenuateMacCalls returns the process-wide count of MAC calls made by
// Attenuate (the only state-free entry point).
func AttenuateMacCalls() int64 {
	countersMu.Lock()
	defer countersMu.Unlock()
	return attenuateMacCalls
}

// ResetAttenuateCounter zeroes the process-wide Attenuate MAC counter.
func ResetAttenuateCounter() {
	countersMu.Lock()
	defer countersMu.Unlock()
	attenuateMacCalls = 0
}
