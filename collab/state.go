package collab

// field is the mutable per-field state.
type field struct {
	kind    Kind
	value   int64
	version int64 // 0 iff a KindSet field is absent
	present bool  // false only for an absent KindSet field
}

// doc is the full state of one document.
type doc struct {
	schema   map[string]Kind
	fields   map[string]*field
	revision int64
}

// resultRing retains results of the most recent RetainWindow operations for a
// (client, document) pair. Lookup of a retained result is O(1):
// ring[seq % RetainWindow] and it stores at most one result per slot, so both
// time and space are bounded by RetainWindow independent of the client's full
// history length.
type resultRing struct {
	entries [RetainWindow]ringEntry
}

type ringEntry struct {
	seq int64 // 0 marks an unused slot
	res OpResult
}

func (r *resultRing) put(res OpResult) {
	slot := &r.entries[(res.Seq-1)%RetainWindow]
	slot.seq = res.Seq
	slot.res = res
}

// get returns (result, true) for a retained sequence, or (_, false) when the
// sequence has aged out or was never processed.
func (r *resultRing) get(seq int64) (OpResult, bool) {
	slot := &r.entries[(seq-1)%RetainWindow]
	if slot.seq == seq {
		return slot.res, true
	}
	return OpResult{}, false
}

// clientState is per (client, document) replay bookkeeping.
type clientState struct {
	maxSeq  int64 // largest processed sequence number
	lastNow int64 // now of the latest accepted batch
	ring    resultRing
}

// state is the lock-protected mutable server state.
type state struct {
	docs    map[string]*doc
	clients map[[2]string]*clientState // key: {client, docID}
}

func newState() *state {
	return &state{
		docs:    make(map[string]*doc),
		clients: make(map[[2]string]*clientState),
	}
}

// Create creates a document with a validated schema.
func (s *Server) Create(docID string, schema map[string]Kind) error {
	if docID == "" {
		return reject(ErrInvalidParam, "docId is empty")
	}
	if len(schema) == 0 {
		return reject(ErrInvalidParam, "schema is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.state.docs[docID]; ok {
		return reject(ErrInvalidParam, "document already exists")
	}
	d := &doc{
		schema: make(map[string]Kind, len(schema)),
		fields: make(map[string]*field, len(schema)),
	}
	for name, kind := range schema {
		if name == "" || (kind != KindSet && kind != KindAdd) {
			return reject(ErrInvalidParam, "illegal field declaration: "+name)
		}
		d.schema[name] = kind
		f := &field{kind: kind}
		if kind == KindAdd {
			f.present = true
			f.value = 0
		}
		d.fields[name] = f
	}
	s.state.docs[docID] = d
	return nil
}

// Get returns an isolated copy of the document state.
func (s *Server) Get(docID string) (*DocSnapshot, error) {
	if docID == "" {
		return nil, reject(ErrInvalidParam, "docId is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.state.docs[docID]
	if !ok {
		return nil, reject(ErrUnknownDoc, docID)
	}
	snap := &DocSnapshot{
		DocID:    docID,
		Schema:   make(map[string]Kind, len(d.schema)),
		Revision: d.revision,
		Fields:   make(map[string]FieldState, len(d.fields)),
	}
	for name, kind := range d.schema {
		snap.Schema[name] = kind
	}
	for name, f := range d.fields {
		snap.Fields[name] = FieldState{
			Kind:    f.kind,
			Value:   f.value,
			Version: f.version,
			Present: f.present,
		}
	}
	return snap, nil
}

// Pending returns the largest processed sequence for a (client, document).
func (s *Server) Pending(client, docID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs := s.state.clients[[2]string{client, docID}]
	if cs == nil {
		return 0
	}
	return cs.maxSeq
}

func reject(sentinel error, detail string) *RejectError {
	return &RejectError{Reason: sentinel.Error(), Detail: detail}
}
