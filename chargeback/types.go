package chargeback

// Reason is the reason code a chargeback is filed under.
type Reason int

const (
	ReasonFraud Reason = iota
	ReasonNotReceived
	ReasonDuplicate
)

// Outcome is the terminal outcome of a case.
type Outcome int

const (
	OutcomeNone Outcome = iota
	OutcomeMerchantWin
	OutcomeIssuerWin
)

// Status is the externally visible lifecycle status of a case.
type Status int

const (
	StatusAwaitingDefense Status = iota
	StatusAwaitingReview
	StatusPreArbitration
	StatusClosed
)

// Config holds the configurable time windows. All values are whole days.
type Config struct {
	FraudWindowDays       int
	NotReceivedWindowDays int
	DuplicateWindowDays   int
	DuplicateRangeDays    int
	DefenseDays           int
	ReviewDays            int
	ArbitrationFee        int64
}

// Transaction is a settled card transaction.
type Transaction struct {
	ID            string
	CardID        string
	MerchantID    string
	Amount        int64
	SettlementDay int
}

// Case is the exposed snapshot of a chargeback case.
type Case struct {
	ID            string
	TransactionID string
	MerchantID    string
	Reason        Reason
	Amount        int64
	FileDay       int
	DefenseDay    int
	BasisTxnID    string
	Status        Status
	Outcome       Outcome
	TerminalDay   int
}

// manualTerm records an explicit terminating operation (accept defense or
// arbitration award). Auto determinations produce no such record.
type manualTerm struct {
	day     int
	outcome Outcome
}

type caseRec struct {
	id         string
	txnID      string
	merchantID string
	reason     Reason
	amount     int64
	fileDay    int
	defenseDay int // -1 until a defense is accepted
	basisTxnID string
	preArb     bool
	term       *manualTerm // nil while not manually terminated
}

// phase is derived purely from the operation history plus queried time.
type phase int

const (
	phDefense phase = iota
	phReview
	phArb
	phMerchantWin
	phIssuerWin
)

// derive is a pure function: it never mutates the record.
func (c *caseRec) derive(cfg Config, now int) (phase, int) {
	if c.term != nil {
		if c.term.outcome == OutcomeMerchantWin {
			return phMerchantWin, c.term.day
		}
		return phIssuerWin, c.term.day
	}
	if c.preArb {
		return phArb, -1
	}
	if c.defenseDay < 0 {
		limit := c.fileDay + cfg.DefenseDays + 1
		if now >= limit {
			return phIssuerWin, limit
		}
		return phDefense, -1
	}
	limit := c.defenseDay + cfg.ReviewDays + 1
	if now >= limit {
		return phMerchantWin, limit
	}
	return phReview, -1
}

func phaseStatus(p phase) Status {
	switch p {
	case phDefense:
		return StatusAwaitingDefense
	case phReview:
		return StatusAwaitingReview
	case phArb:
		return StatusPreArbitration
	default:
		return StatusClosed
	}
}

func phaseOutcome(p phase) Outcome {
	switch p {
	case phMerchantWin:
		return OutcomeMerchantWin
	case phIssuerWin:
		return OutcomeIssuerWin
	default:
		return OutcomeNone
	}
}
