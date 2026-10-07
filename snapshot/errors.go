package snapshot

type ErrorClass string

const (
	ClassBoundary  ErrorClass = "boundary_definition_conflict"
	ClassAtomicity ErrorClass = "atomicity_conflict"
	ClassIntegrity ErrorClass = "referential_integrity_conflict"
	ClassResource  ErrorClass = "resource_exhausted"
)

type ExportError struct {
	Class     ErrorClass `json:"class"`
	Rule      string     `json:"rule"`
	Message   string     `json:"message"`
	CommitLSN int64      `json:"commitLsn,omitempty"`
	TxID      string     `json:"txId,omitempty"`
	RecordID  string     `json:"recordId,omitempty"`
}

func (e *ExportError) Error() string {
	return string(e.Class) + ": " + e.Message
}

type Limits struct {
	MaxTransactions int
	MaxRecords      int
}

var NoLimits = Limits{MaxTransactions: -1, MaxRecords: -1}
