package store

type OperationType uint8

const (
	OpPut OperationType = iota + 1
	OpDelete
)

type Row struct {
	Primary   string
	Secondary *string
}

type Record struct {
	LSN          int64
	Operation    OperationType
	Primary      string
	Secondary    *string
	OldSecondary *string
}

type IndexEntry struct {
	Primary string
	LSN     int64
}

type MismatchType uint8

const (
	MismatchExtra MismatchType = iota + 1
	MismatchMissing
	MismatchWrongPrimary
)

type Mismatch struct {
	Secondary string
	Type      MismatchType
	Expected  string
	Actual    string
}

type CheckReport struct {
	Mismatches []Mismatch
}
