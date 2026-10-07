// Package ontology implements an in-memory ontology platform with
// cross-instance atomic batch updates guarded by joint version preconditions.
package ontology

import "encoding/json"

// ID is the identifier of an object instance.
type ID string

// Attr is an attribute name.
type Attr string

// Value is an opaque attribute value.
type Value = any

// LinkType is a named, directed association kind with per-endpoint
// cardinality bounds.
type LinkType struct {
	Name       string
	SourceType string
	TargetType string
	MinSource  int
	MaxSource  int // -1 = unbounded
	MinTarget  int
	MaxTarget  int // -1 = unbounded
}

// ObjectType declares an instance kind.
type ObjectType struct {
	Name string
}

// Instance is a versioned object instance.
type Instance struct {
	ID      ID
	Type    string
	Version uint64
	Step    uint64
	Attrs   map[Attr]Value
	Out     map[string]map[ID]struct{}
	In      map[string]map[ID]struct{}
	History []AttrVersion
}

// AttrVersion records one attribute value together with the version
// at which it was written.
type AttrVersion struct {
	Attr    Attr
	Value   Value
	Version uint64
}

// Precondition requires an instance to be at exactly ExpectedVersion.
type Precondition struct {
	Instance        ID
	ExpectedVersion uint64
}

// OpKind enumerates the mutating operations of a batch.
type OpKind int

const (
	OpSetAttr OpKind = iota
	OpAddLink
	OpRemoveLink
)

// MarshalJSON renders the op kind as a stable string token.
func (k OpKind) MarshalJSON() ([]byte, error) {
	switch k {
	case OpSetAttr:
		return json.Marshal("set_attr")
	case OpAddLink:
		return json.Marshal("add_link")
	case OpRemoveLink:
		return json.Marshal("remove_link")
	default:
		return json.Marshal("unknown")
	}
}

// UnmarshalJSON parses the stable string token.
func (k *OpKind) UnmarshalJSON(raw []byte) error {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	switch s {
	case "set_attr":
		*k = OpSetAttr
	case "add_link":
		*k = OpAddLink
	case "remove_link":
		*k = OpRemoveLink
	default:
		*k = -1
	}
	return nil
}

// Op is one mutating step inside a batch.
type Op struct {
	Kind     OpKind
	Instance ID // target instance for SetAttr; source instance for links
	Attr     Attr
	Value    Value
	LinkType string
	Other    ID // link endpoint
}

// Batch is a joint multi-instance update.
type Batch struct {
	ID            string
	Preconditions []Precondition
	Ops           []Op
}

// Status is the mutually exclusive outcome classification.
type Status string

const (
	StatusDuplicatePrecondition Status = "duplicate_precondition"
	StatusVersionMismatch       Status = "version_mismatch"
	StatusCardinalityViolation  Status = "cardinality_violation"
	StatusCommitted             Status = "committed"
)

// Mismatch records one failed precondition.
type Mismatch struct {
	Instance ID     `json:"instance"`
	Expected uint64 `json:"expected"`
	Actual   uint64 `json:"actual"`
	Missing  bool   `json:"missing,omitempty"`
}

// CardinalityFailure records one violated link-cardinality bound.
type CardinalityFailure struct {
	LinkType string `json:"link_type"`
	Endpoint string `json:"endpoint"`
	Instance ID     `json:"instance"`
	Count    int    `json:"count"`
	Min      int    `json:"min"`
	Max      int    `json:"max"`
}

// Result is the outcome of applying one batch.
type Result struct {
	BatchID     string               `json:"batch_id"`
	Status      Status               `json:"status"`
	Duplicate   ID                   `json:"duplicate,omitempty"`
	Mismatches  []Mismatch           `json:"mismatches,omitempty"`
	Cardinality []CardinalityFailure `json:"cardinality,omitempty"`
	Versions    map[ID]VersionChange `json:"versions,omitempty"`
	Order       uint64               `json:"order"`
	// AcquireOrder is the sequence number assigned at the instant the batch
	// finished acquiring its locks; it is the linearisation order of the
	// decision and the order in which a journal must be replayed.
	AcquireOrder uint64        `json:"acquire_order"`
	Observed     map[ID]uint64 `json:"observed,omitempty"`
}

// VersionChange records the version transition of one instance.
type VersionChange struct {
	From uint64 `json:"from"`
	To   uint64 `json:"to"`
	Step uint64 `json:"step"`
}

// Snapshot is a point-in-time immutable view of one instance.
type Snapshot struct {
	ID      ID
	Type    string
	Version uint64
	Step    uint64
	Attrs   map[Attr]Value
}
