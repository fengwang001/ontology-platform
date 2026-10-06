package hemo

// ExportedChairIDs returns all registered chair IDs, sorted. It exists for
// the differential test harness; normal clients enumerate via queries.
func ExportedChairIDs(s *System) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortedChairIDs(s.chairs)
}

// ExportedPatientIDs returns all registered patient IDs, sorted.
func ExportedPatientIDs(s *System) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.patients))
	for id := range s.patients {
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
	return ids
}

// ResetProbeStats zeroes the structural-probe counters and returns the
// engine so benchmark callers can bracket measured sections.
func (s *System) ResetProbeStats() ProbeStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.probes
	s.probes = ProbeStats{}
	return old
}

// ProbeStatsValue returns the current structural-probe counters.
func (s *System) ProbeStatsValue() ProbeStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.probes
}
