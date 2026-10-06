package aml

import "errors"

var (
	ErrInvalidArgument      = errors.New("invalid argument")
	ErrClockRollback        = errors.New("clock rollback")
	ErrAccountNotFound      = errors.New("account not found")
	ErrAccountAlreadyExists = errors.New("account already exists")
	ErrDuplicateTransaction = errors.New("duplicate transaction")
	ErrTransactionNotFound  = errors.New("transaction not found")
	ErrAlreadyReversed      = errors.New("transaction already reversed")
	ErrAlreadyLinked        = errors.New("accounts already linked")
)

type Config struct {
	L int64
	H int64
	K int64
	D int64
}

type ReportKind string

const (
	LargeReport      ReportKind = "large"
	StructuredReport ReportKind = "structured"
)

type OperationKind string

const (
	OpenAccountOperation OperationKind = "open_account"
	DepositOperation     OperationKind = "deposit"
	LinkOperation        OperationKind = "link"
	ReverseOperation     OperationKind = "reverse"
)

type OperationRef struct {
	Kind OperationKind
	ID   string
}

type Report struct {
	Number         int64
	Kind           ReportKind
	Trigger        OperationRef
	At             int64
	GroupID        int64
	Accounts       []string
	TransactionIDs []string
	Total          int64
}

type WindowSummary struct {
	Count int64
	Total int64
}

type deposit struct {
	txID      string
	accountID string
	amount    int64
	date      int64
	reversed  bool
	covered   bool
}

type account struct {
	id      string
	groupID int64
}

type group struct {
	id        int64
	hasWindow bool
	lastNow   int64
	accounts  map[string]*account
	buckets   map[int64]map[string]*deposit
}
