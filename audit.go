package ontology

// AuditEntry records one placement decision: its inputs, the timezone
// definition version it relied on, and the conclusion, so every decision can
// be re-checked after the fact.
type AuditEntry struct {
	Seq         uint64  // view-local decision sequence
	EventLogSeq uint64  // delivery-log position of the triggering event
	EventKind   string  // write / link / unlink / type-changed
	ObjID       string  // object the decision is about
	TypeID      string  // its object type
	Prop        string  // the grouping time property
	WriteSeq    uint64  // write sequence of the value used
	TZVersion   int     // timezone definition version the decision relied on
	Wall        string  // civil value as written
	Group       string  // group the object was placed into (if placed)
	Normalized  string  // RFC3339 instant in the base timezone (if placed)
	Decision    string  // placed / removed / quarantined / ignored-stale
	ErrCode     ErrCode // error category, when quarantined
	Err         string  // human-readable error detail
}
