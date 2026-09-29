package rebalance

import "encoding/json"

type snapshotData struct {
	N          int                 `json:"n"`
	Target     int                 `json:"target"`
	Phase      Phase               `json:"phase"`
	Cursor     int                 `json:"cursor"`
	MoveKeys   []string            `json:"move_keys"`
	Partitions []map[string]string `json:"partitions"`
}

// Snapshot serializes the complete store state, including the migration
// cursor, so a crashed process can resume without re-migrating or skipping
// any record.
func (s *Store) Snapshot() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data := snapshotData{
		N:          s.n,
		Target:     s.target,
		Phase:      s.phase,
		Cursor:     s.cursor,
		MoveKeys:   append([]string(nil), s.moveKeys...),
		Partitions: make([]map[string]string, len(s.partitions)),
	}
	for i, p := range s.partitions {
		cp := make(map[string]string, len(p))
		for k, v := range p {
			cp[k] = v
		}
		data.Partitions[i] = cp
	}
	return json.Marshal(data)
}

// Restore rebuilds a store from a snapshot. The cursor is authoritative:
// records before it are only looked up in the target layout, records at or
// after it only in the old layout.
func (s *Store) Restore(data []byte) error {
	var d snapshotData
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	if d.N < 1 || d.Target < 1 || len(d.Partitions) < d.N {
		return ErrInvalidSnapshot
	}
	if d.Phase != PhaseIdle && d.Phase != PhaseMigrating {
		return ErrInvalidSnapshot
	}
	if d.Phase == PhaseMigrating {
		if d.Target != len(d.Partitions) || d.Cursor < 0 || d.Cursor > len(d.MoveKeys) {
			return ErrInvalidSnapshot
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.n = d.N
	s.target = d.Target
	s.phase = d.Phase
	s.cursor = d.Cursor
	s.moveKeys = append([]string(nil), d.MoveKeys...)
	s.partitions = make([]map[string]string, len(d.Partitions))
	for i, p := range d.Partitions {
		cp := make(map[string]string, len(p))
		for k, v := range p {
			cp[k] = v
		}
		s.partitions[i] = cp
	}
	s.log("RESTORE %s", s.debugDumpLocked("restore"))
	return nil
}
