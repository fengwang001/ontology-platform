// Package naive is an independently written reference model used solely by
// differential tests against chargeback.System.
package naive

import "errors"

// Reason/Outcome/Status numeric values mirror chargeback.* so generated ops
// can be fed to both implementations.
type Reason int

const (
	ReasonFraud Reason = iota
	ReasonNotReceived
	ReasonDuplicate
)

type Outcome int

const (
	OutcomeNone Outcome = iota
	OutcomeMerchantWin
	OutcomeIssuerWin
)

type Status int

const (
	StatusAwaitingDefense Status = iota
	StatusAwaitingReview
	StatusPreArbitration
	StatusClosed
)

type Config struct {
	FraudWindowDays       int
	NotReceivedWindowDays int
	DuplicateWindowDays   int
	DuplicateRangeDays    int
	DefenseDays           int
	ReviewDays            int
	ArbitrationFee        int64
}

var (
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrClockRollback       = errors.New("clock rollback")
	ErrTransactionNotFound = errors.New("transaction not found")
	ErrWindowExpired       = errors.New("filing window expired")
	ErrDuplicateFiling     = errors.New("duplicate filing")
	ErrAmountExceeded      = errors.New("disputable amount exceeded")
	ErrNoBasis             = errors.New("no basis transaction for duplicate chargeback")
	ErrCaseNotFound        = errors.New("case not found")
	ErrStatusNotAllowed    = errors.New("operation not allowed in current status")
)

type Transaction struct {
	ID            string
	CardID        string
	MerchantID    string
	Amount        int64
	SettlementDay int
}

// Model keeps only the log of accepted operations. Every answer is derived
// from scratch by replaying the log at the queried time; it shares no
// incremental data structure or algorithm with the production engine.
type Model struct {
	cfg    Config
	ops    []op
	last   int
	txnIDs map[string]bool
}

type opKind int

const (
	opRegister opKind = iota
	opCredit
	opFile
	opDefend
	opAccept
	opPreArb
	opRule
)

type op struct {
	kind   opKind
	now    int
	txnID  string
	card   string
	merch  string
	amount int64
	settle int
	caseID string
	reason Reason
	basis  string
	winner Outcome
}

func New(cfg Config) *Model {
	return &Model{cfg: cfg, txnIDs: map[string]bool{}}
}
