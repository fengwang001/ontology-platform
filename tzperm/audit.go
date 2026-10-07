package tzperm

import "sync"

// Record is the internal, access-controlled audit trace of one decision.
//
// It deliberately contains every sensitive datum that the public Outcome
// must never carry: the normalized instants, the chosen baseline zone
// names, the window boundaries and comparison counters. Records go only to
// the configured AuditSink, which is assumed to sit behind stricter
// controls than the view API.
type Record struct {
	RequestID string
	At        int64

	ObjectID string
	AttrName string
	RegionID string

	EntryWall   int64
	EntryZone   string
	Querier     string
	QuerierZone string

	ValueInstant        int64
	ValueWallStatus     WallStatus
	BaselineZoneAtValue string

	BaselineZoneAtQuery string
	QueryNormalizedWall int64

	WindowSetID  string
	Window       WindowRules
	InsideWindow bool

	Decision  Decision
	ErrorCode string

	// EnginePathComparisons counts version-timestamp comparisons made by
	// the engine's binary searches. NaiveComparisons counts the same work
	// done by the linear-scan reference model. Recording both lets an
	// auditor verify, per request, that the production cost did not grow
	// linearly with the region's accumulated definition count.
	EnginePathComparisons int
	NaiveComparisons      int
	ReferenceConsistent   bool
}

// AuditSink receives audit records.
type AuditSink interface {
	Write(Record)
}

// MemorySink retains records in memory.
type MemorySink struct {
	mu      sync.Mutex
	records []Record
}

// NewMemorySink creates an empty memory sink.
func NewMemorySink() *MemorySink { return &MemorySink{} }

// Write appends a record.
func (m *MemorySink) Write(r Record) {
	m.mu.Lock()
	m.records = append(m.records, r)
	m.mu.Unlock()
}

// Records returns a copy of all stored records in arrival order.
func (m *MemorySink) Records() []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Record, len(m.records))
	copy(out, m.records)
	return out
}
