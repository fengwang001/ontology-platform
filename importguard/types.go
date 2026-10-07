package importguard

// Mode is the atomicity mode declared once for the whole batch.
type Mode string

const (
	// ModeAtomic rejects an entire record if any one field is not writable.
	ModeAtomic Mode = "atomic"
	// ModeLenient skips non-writable fields, writes the rest, and re-evaluates
	// required-property constraints after skipping.
	ModeLenient Mode = "lenient"
)

// Semantic is the per-record declared intent.
type Semantic string

const (
	SemanticCreate Semantic = "create"
	SemanticUpdate Semantic = "update"
)

// PropertyValue is an opaque property value; the gatekeeper treats values as
// opaque payload and never inspects them.
type PropertyValue any

// ObjectType describes a type of object and its required property names.
type ObjectType struct {
	Name     string
	Required map[string]bool
}

// Object is an existing object instance.
type Object struct {
	ID         string
	Type       string
	Properties map[string]PropertyValue
	Version    int64
}

// Entry is one record of a batch import.
type Entry struct {
	ObjectID string
	Type     string
	Semantic Semantic
	Fields   map[string]PropertyValue
}

// RecordStatus is the per-record outcome.
type RecordStatus string

const (
	StatusSuccess RecordStatus = "success"
	StatusPartial RecordStatus = "partial_success"
	StatusFailed  RecordStatus = "failed"
)

// RecordResult is the outcome of a single record.
type RecordResult struct {
	ObjectID    string
	Index       int
	Status      RecordStatus
	Skipped     []string
	Failure     *Failure
	WrittenKeys []string
}

// BatchResult is the overall outcome of a batch import.
type BatchResult struct {
	Rejected bool
	Failure  *Failure
	Records  []RecordResult
}
