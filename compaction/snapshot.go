package compaction

type FileSnapshot struct {
	ID     uint64
	Layer  int
	MinKey []byte
	MaxKey []byte
	Bytes  uint64
}

type PlanSnapshot struct {
	ID          uint64
	Type        PlanType
	SourceLayer int
	TargetLayer int
	InputIDs    []uint64
	MinKey      []byte
	MaxKey      []byte
}

type Snapshot struct {
	Files      map[uint64]FileSnapshot
	Plans      map[uint64]PlanSnapshot
	LastEnds   map[int][]byte
	Occupied   map[uint64]uint64
	NextPlanID uint64
}

func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := Snapshot{
		Files:      make(map[uint64]FileSnapshot, len(s.files)),
		Plans:      make(map[uint64]PlanSnapshot, len(s.plans)),
		LastEnds:   make(map[int][]byte, len(s.layers)),
		Occupied:   make(map[uint64]uint64, len(s.occupied)),
		NextPlanID: s.nextPlanID,
	}
	for id, file := range s.files {
		snapshot.Files[id] = FileSnapshot{
			ID:     file.ID,
			Layer:  file.Layer,
			MinKey: append([]byte(nil), file.MinKey...),
			MaxKey: append([]byte(nil), file.MaxKey...),
			Bytes:  file.Bytes,
		}
	}
	for id, plan := range s.plans {
		snapshot.Plans[id] = PlanSnapshot{
			ID:          plan.plan.ID,
			Type:        plan.plan.Type,
			SourceLayer: plan.plan.SourceLayer,
			TargetLayer: plan.plan.TargetLayer,
			InputIDs:    append([]uint64(nil), plan.plan.InputIDs...),
			MinKey:      append([]byte(nil), plan.plan.MinKey...),
			MaxKey:      append([]byte(nil), plan.plan.MaxKey...),
		}
	}
	for layer, state := range s.layers {
		if state.lastEnd != nil {
			snapshot.LastEnds[layer] = append([]byte(nil), state.lastEnd...)
		}
	}
	for id, planID := range s.occupied {
		snapshot.Occupied[id] = planID
	}
	return snapshot
}
