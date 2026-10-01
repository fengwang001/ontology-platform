package aries

type RecordType string

const (
	RecordUpdate    RecordType = "Update"
	RecordCommit    RecordType = "Commit"
	RecordAbort     RecordType = "Abort"
	RecordEnd       RecordType = "End"
	RecordBeginCkpt RecordType = "BeginCkpt"
	RecordEndCkpt   RecordType = "EndCkpt"
	RecordPageFlush RecordType = "PageFlush"
)

type TxnStatus string

const (
	StatusRunning   TxnStatus = "running"
	StatusCommitted TxnStatus = "committed"
	StatusAborting  TxnStatus = "aborting"
)

type Record struct {
	LSN                int64
	Type               RecordType
	Txn                string
	Page               string
	BeginLSN           int64
	DirtyPages         map[string]int64
	ActiveTransactions map[string]TxnState
}

type TxnState struct {
	Status  TxnStatus
	LastLSN int64
}

type DirtyPage struct {
	Page   string
	RecLSN int64
}

type ActiveTransaction struct {
	Txn     string
	Status  TxnStatus
	LastLSN int64
}

type Analysis struct {
	RedoLSN            *int64
	DirtyPages         []DirtyPage
	ActiveTransactions []ActiveTransaction
	FailedTxns         []string
}
