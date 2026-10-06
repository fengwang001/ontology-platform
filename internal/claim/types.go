package claim

import "errors"

// ClauseType identifies the policy clause kind.
type ClauseType int

const (
	// Ordinary is the normal (primary) clause.
	Ordinary ClauseType = iota
	// Excess only pays after ordinary policies.
	Excess
)

func (c ClauseType) valid() bool { return c == Ordinary || c == Excess }

// Policy is the immutable input description of an insurance policy.
type Policy struct {
	ID         string
	Subject    string
	Limit      int64
	Deductible int64
	StartDay   int
	EndDay     int
	Insurer    string
	Clause     ClauseType
}

// Accident is the input description of a registered accident.
type Accident struct {
	ID      string
	Subject string
	Day     int
	Loss    int64
}

// Payment is one policy's payout for one accident.
type Payment struct {
	PolicyID string
	Amount   int64
}

// AccidentResult is the outcome of one accident.
type AccidentResult struct {
	AccidentID string
	Seq        int
	TotalPaid  int64
	Payments   []Payment
}

// AddPolicyInput is the input of AddPolicy.
type AddPolicyInput = Policy

// RegisterAccidentInput is the input of RegisterAccident.
type RegisterAccidentInput = Accident

// CorrectAccidentInput changes the loss of a registered accident.
type CorrectAccidentInput struct {
	AccidentID string
	NewLoss    int64
}

// CancelPolicyInput cancels a policy effective on a day.
type CancelPolicyInput struct {
	PolicyID  string
	CancelDay int
}

// Sentinel errors, compared with errors.Is.
var (
	ErrInvalidArg      = errors.New("claim: invalid argument")
	ErrDuplicateID     = errors.New("claim: duplicate id")
	ErrNotFound        = errors.New("claim: not found")
	ErrPolicyCancelled = errors.New("claim: policy already cancelled")
)
