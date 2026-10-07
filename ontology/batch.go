package ontology

// BatchItem is one change declaration inside a batch. It writes exactly one
// instance (identified by ID) and may additionally mutate link edges that
// touch that instance.
//
// Create declares a brand-new instance and must use Baseline 0. Update (or
// delete-less write) of an existing instance must carry the version last
// observed by the caller; the store checks it item by item.
type BatchItem struct {
	ID       InstanceID
	Type     ObjectTypeName
	Create   bool
	Baseline Version
	// Properties is the desired full property set after commit. nil clears
	// the properties of an existing instance.
	Properties Property
	// LinkDeltas are edge adds/removes carried by this item.
	LinkDeltas []EdgeDelta
}

// BatchInput is one atomic batch update request.
type BatchInput struct {
	// ClientID is an optional caller-supplied correlation label copied into
	// the journal; it has no semantic effect.
	ClientID string
	Items    []BatchItem
}

// BatchView exposes the post-batch (shadow) image of every instance in the
// batch working set to validation hooks.
type BatchView interface {
	// Get returns the post-batch image of id, or nil if id is not part of
	// the working set.
	Get(id InstanceID) *Instance
	// Instances lists every instance visible in the view in ID order.
	Instances() []*Instance
	// HasEdge reports whether the edge exists in the post-batch image.
	HasEdge(edge Edge) bool
}

// BatchResult is returned by a successful ApplyBatch call.
type BatchResult struct {
	// CommitSeq is the store-wide logical commit clock value; it advances by
	// exactly one per committed batch and never advances for rejected
	// batches.
	CommitSeq int64
	// Versions maps every written instance to its new version.
	Versions map[InstanceID]Version
	// Record is the fully populated journal record for this attempt.
	Record *JournalRecord
}
