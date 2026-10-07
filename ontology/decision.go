package ontology

// Decision is the complete, replayable record of one conflict adjudication.
// Every attempt that reaches adjudication is appended under the target
// instance lock, including rejected ones; validator rejections are recorded
// as well with Kind == ConflictNone and RejectedByValidator set.
type Decision struct {
	Sequence       uint64
	Object         ObjectID
	Type           TypeName
	IdempotencyKey string

	DeclaredBase Version
	HeadAtCommit Version
	NewVersion   Version

	Accepted            bool
	RejectedByValidator bool
	Kind                ConflictKind
	Reason              string
	ValidatorErr        string

	// Full inputs of the relevant-set calculation.
	WriteSet  []Property
	ReadScope []PropertyRef
	Observed  map[ObjectID]Version

	// The adjudicated relevant set, split local vs linked instances.
	RelevantLocal    []Property
	RelevantExternal map[ObjectID][]Property

	// Evidence for the verdict: each entry is one marker comparison. The
	// number of comparisons equals the relevant-set size and is therefore
	// independent of the instance's version-history length.
	Evidence []ConflictEvidence
}

// ConflictEvidence records one "marker vs baseline" comparison performed
// during adjudication.
type ConflictEvidence struct {
	Object    ObjectID
	Property  Property
	Via       string
	Observed  Version
	Committed Version
	Hit       bool
}

func (s *Store) Decisions(id ObjectID) []Decision {
	st := s.stateFor(id)
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]Decision, len(st.decisions))
	copy(out, st.decisions)
	return out
}

func (k ConflictKind) String() string {
	switch k {
	case ConflictDeleted:
		return "deleted"
	case ConflictStaleBase:
		return "stale-base"
	case ConflictProperty:
		return "property-conflict"
	default:
		return "none"
	}
}
