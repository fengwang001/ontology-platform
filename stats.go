package ontology

// OpStats counts the permission checks and cardinality evaluations a
// single operation performed. It is the observable, implementation
// independent proof that per-operation cost depends only on the two
// endpoints involved (and, for cascades, their direct associations),
// never on the total number of link types or links in the system.
type OpStats struct {
	PermissionChecks  int
	CardinalityChecks int
}

func (s *OpStats) add(o OpStats) {
	s.PermissionChecks += o.PermissionChecks
	s.CardinalityChecks += o.CardinalityChecks
}

// OpResult is the outcome of one public operation.
type OpResult struct {
	Op    OpKind
	OK    bool
	Err   *OpError
	Stats OpStats
	// Clock is the logical clock value after the operation. Rejected
	// operations never advance the clock.
	Clock uint64
	// Cleaned, Skipped and Deleted are populated by DeleteObject.
	Cleaned []LinkRef
	Skipped []SkippedLink
	Deleted []ObjectID
}
