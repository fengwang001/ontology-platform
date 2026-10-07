package snapshot

type RecordKind string

const (
	KindObject RecordKind = "object"
	KindLink   RecordKind = "link"
	KindAction RecordKind = "action"
)

type Operation string

const (
	OpUpsert Operation = "upsert"
	OpDelete Operation = "delete"
)

type EventType string

const (
	EventPrepare EventType = "prepare"
	EventCommit  EventType = "commit"
	EventAbort   EventType = "abort"
)

type Record struct {
	Kind       RecordKind        `json:"kind"`
	Operation  Operation         `json:"operation"`
	ID         string            `json:"id"`
	TypeID     string            `json:"typeId,omitempty"`
	SourceID   string            `json:"sourceId,omitempty"`
	TargetID   string            `json:"targetId,omitempty"`
	ActionID   string            `json:"actionId,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
}

type Event struct {
	Type      EventType `json:"type"`
	TxID      string    `json:"txId"`
	CommitLSN int64     `json:"commitLsn,omitempty"`
	Record    *Record   `json:"record,omitempty"`
}

type Snapshot struct {
	Boundary int64             `json:"boundary"`
	Objects  map[string]Record `json:"objects"`
	Links    map[string]Record `json:"links"`
	Actions  map[string]Record `json:"actions"`
}

type Delta struct {
	CommitLSN int64    `json:"commitLsn"`
	TxID      string   `json:"txId"`
	Records   []Record `json:"records"`
}

type ExportFrame struct {
	Snapshot *Snapshot    `json:"snapshot,omitempty"`
	Deltas   []Delta      `json:"deltas,omitempty"`
	Error    *ExportError `json:"error,omitempty"`
	Done     bool         `json:"done,omitempty"`
}
