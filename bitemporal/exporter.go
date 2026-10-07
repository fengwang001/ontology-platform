package bitemporal

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Cutoff is the frozen (T, arrival-sequence) binding for one logical export.
// It is shared by every batch so the whole export reflects exactly one
// consistent read at one point of the global serial order.
type Cutoff struct {
	T   Tick   `json:"t"`
	Seq uint64 `json:"seq"`
}

// ObjectRef names one object within a typed export request.
type ObjectRef struct {
	ID string `json:"id"`
}

// Segment is one constant-value run of an object snapshot. Value == nil means
// "unknown": no record covered that valid-time run at the cutoff. Unknown runs
// are emitted explicitly and must never be default-filled.
type Segment struct {
	Start Tick   `json:"start"`
	End   Tick   `json:"end"`
	Value *Value `json:"value,omitempty"`
}

// Unknown reports whether this segment is an explicit unknown gap.
func (g Segment) Unknown() bool { return g.Value == nil }

// Snapshot is one object's frozen, timeline-complete slice.
type Snapshot struct {
	ObjectID string    `json:"object_id"`
	TypeName string    `json:"type_name"`
	Cutoff   Cutoff    `json:"cutoff"`
	Segments []Segment `json:"segments"`
}

// Canonical serializes the snapshot deterministically. Repeated exports of an
// unchanged object at equal T produce byte-identical output.
func (s *Snapshot) Canonical() []byte {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return b
}

// Exporter executes frozen exports against a Store.
type Exporter struct {
	store *Store
	log   DecisionLogger
}

// NewExporter constructs an exporter with the given decision logger.
func NewExporter(s *Store, l DecisionLogger) *Exporter {
	if l == nil {
		l = NopLogger{}
	}
	return &Exporter{store: s, log: l}
}

// Freeze binds the current transaction time and the global arrival sequence in
// one linearized critical section. Writes arriving after this binding — even
// those stamped with the same transaction tick — are excluded from the whole
// export; the binding is applied uniformly to every object and batch.
func (e *Exporter) Freeze(t Tick) (Cutoff, error) {
	if t < e.store.RetainedFrom() {
		err := &ExportError{Code: CodeRetention, Msg: fmt.Sprintf("T=%d predates retained horizon %d", t, e.store.RetainedFrom())}
		e.log.Log("reject", fmt.Sprintf("freeze T=%d -> %s", t, err))
		return Cutoff{}, err
	}
	c := e.store.freeze()
	c.T = t // freeze at the requested T; Seq marks the arrival binding
	e.log.Log("freeze", fmt.Sprintf("T=%d bound seq=%d (same-tick arrivals after binding excluded)", c.T, c.Seq))
	return c, nil
}

// Prepare validates one typed/ranged export and returns its frozen binding.
// Rejection priority is fixed and enforced in order:
//
//	retention > schema undefined at T > invalid export range.
//
// Rejection happens before any partial result is produced and reserves no
// retained resource.
func (e *Exporter) Prepare(t Tick, typeName string, rng *Interval) (Cutoff, error) {
	if t < e.store.RetainedFrom() {
		err := &ExportError{Code: CodeRetention, Msg: fmt.Sprintf("T=%d predates retained horizon %d", t, e.store.RetainedFrom())}
		e.log.Log("reject", fmt.Sprintf("prepare T=%d type=%s -> %s", t, typeName, err))
		return Cutoff{}, err
	}
	if _, ok := e.store.schemaAt(typeName, t); !ok {
		err := &ExportError{Code: CodeSchemaUndefined, Msg: fmt.Sprintf("type %q not defined at T=%d", typeName, t)}
		e.log.Log("reject", fmt.Sprintf("prepare T=%d type=%s -> %s", t, typeName, err))
		return Cutoff{}, err
	}
	if rng != nil && (rng.Start >= rng.End || rng.Start < MinTick || rng.End > MaxTick) {
		err := &ExportError{Code: CodeInvalidRange, Msg: "export range must be a non-empty half-open interval inside the time domain"}
		e.log.Log("reject", fmt.Sprintf("prepare T=%d type=%s -> %s", t, typeName, err))
		return Cutoff{}, err
	}
	return e.Freeze(t)
}

// ExportObject exports one object under a previously frozen binding. Records
// with TxTime > c.T or Seq > c.Seq are invisible; corrections collapse to the
// newest eligible record; uncovered runs are explicit unknown segments.
func (e *Exporter) ExportObject(c Cutoff, typeName, objectID string, rng *Interval) (*Snapshot, error) {
	if _, ok := e.store.schemaAt(typeName, c.T); !ok {
		return nil, &ExportError{Code: CodeSchemaUndefined, Msg: fmt.Sprintf("type %q not defined at frozen T=%d", typeName, c.T)}
	}
	span := Interval{Start: MinTick, End: MaxTick}
	if rng != nil {
		if rng.Start >= rng.End || rng.Start < MinTick || rng.End > MaxTick {
			return nil, &ExportError{Code: CodeInvalidRange, Msg: "export range must be a non-empty half-open interval inside the time domain"}
		}
		span = *rng
	}
	recs, err := e.store.objectRecords(objectID, c)
	if err != nil {
		e.log.Log("inconsistency", fmt.Sprintf("object=%s %v", objectID, err))
		return nil, err
	}
	segs := e.buildSegments(c, objectID, span, recs)
	snap := &Snapshot{ObjectID: objectID, TypeName: typeName, Cutoff: c, Segments: segs}
	e.log.Log("export", fmt.Sprintf("object=%s cutoff=%s segments=%d", objectID, describeCutoff(c), len(segs)))
	return snap, nil
}

// ExportBatch exports many objects under one shared frozen binding. Object IDs
// are sorted so output order is deterministic; all snapshots share the same
// cutoff regardless of batch scheduling or interleaved concurrent writes.
func (e *Exporter) ExportBatch(c Cutoff, typeName string, objs []ObjectRef, rng *Interval) ([]*Snapshot, error) {
	if _, ok := e.store.schemaAt(typeName, c.T); !ok {
		return nil, &ExportError{Code: CodeSchemaUndefined, Msg: fmt.Sprintf("type %q not defined at frozen T=%d", typeName, c.T)}
	}
	ids := make([]string, 0, len(objs))
	for _, o := range objs {
		ids = append(ids, o.ID)
	}
	sort.Strings(ids)
	out := make([]*Snapshot, 0, len(ids))
	for _, id := range ids {
		snap, err := e.ExportObject(c, typeName, id, rng)
		if err != nil {
			return nil, err // no partial results
		}
		out = append(out, snap)
	}
	return out, nil
}

// VisibleAt answers one point query: the record visible for objectID at valid
// time v under cutoff c, or nil for unknown. inspected reports how many index
// chain entries were compared, giving a verifiable scale-independent bound.
func (e *Exporter) VisibleAt(c Cutoff, objectID string, v Tick) (rec *Record, inspected int) {
	idx := e.store.objectIndex(objectID)
	if idx == nil {
		e.log.Log("point", fmt.Sprintf("object=%s v=%d -> unknown (no index) input{%s}", objectID, v, describeCutoff(c)))
		return nil, 0
	}
	seq, tx, n := idx.visibleSeq(v, c)
	inspected = n
	if seq == 0 {
		e.log.Log("point", fmt.Sprintf("object=%s v=%d -> unknown; compared=%d chains=%d input{%s}", objectID, v, inspected, treeDepth, describeCutoff(c)))
		return nil, inspected
	}
	r := e.store.recordBySeq(seq)
	e.log.Log("point", fmt.Sprintf("object=%s v=%d -> seq=%d tx=%d (newest tx<=%d covering); compared=%d input{%s}", objectID, v, seq, tx, c.T, inspected, describeCutoff(c)))
	return r, inspected
}

func describeCutoff(c Cutoff) string {
	return fmt.Sprintf("T=%d,seq<=%d", c.T, c.Seq)
}
