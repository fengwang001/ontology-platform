// Package ontology implements an SMS segmenting and billing meter.
package ontology

import (
	"errors"
	"sync"
	"unicode/utf8"
)

// ErrCode identifies the reason a call was rejected.
type ErrCode string

const (
	// ErrInvalidArgument covers out-of-range construction parameters, empty
	// account ids, empty text, invalid UTF-8 and out-of-range timestamps.
	ErrInvalidArgument ErrCode = "invalid_argument"
	// ErrAccountNotFound means Send or Quote referenced an unknown account.
	ErrAccountNotFound ErrCode = "account_not_found"
	// ErrClockSkew means now is earlier than a previously accepted Send.
	ErrClockSkew ErrCode = "clock_skew"
	// ErrTooManySegments means a message needs more than 10 segments.
	ErrTooManySegments ErrCode = "too_many_segments"
	// ErrInsufficientBalance means the fee exceeds the account balance.
	ErrInsufficientBalance ErrCode = "insufficient_balance"
)

// Error is the typed error returned by every rejected operation.
type Error struct {
	Code ErrCode
	Op   string
	Msg  string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Msg }

func newError(code ErrCode, op, msg string) *Error {
	return &Error{Code: code, Op: op, Msg: msg}
}

// AsError reports whether err carries the given code.
func AsError(err error, code ErrCode) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Code == code
	}
	return false
}

// Segment is a half-open [Start, End) range of code-point indices.
type Segment struct {
	Start int
	End   int
}

// SplitResult is the read-only result of encoding and segmenting text.
type SplitResult struct {
	Encoding string
	Segments []Segment
}

// SendResult reports the encoding, segment count and total fee in li.
type SendResult struct {
	Encoding string
	Segments int
	Fee      int64
}

// account holds one account's billing state.
type account struct {
	balance int64
	k       int64 // current period number
	u       int64 // segments already used in period k
	t       int64 // first-tier capacity of period k
	active  bool  // whether the account has ever been rolled into a period
}

// Meter segments SMS text and bills accounts per billing period.
// All exported methods are safe for concurrent use.
type Meter struct {
	mu sync.Mutex

	p  int64
	t0 int64
	p1 int64
	p2 int64
	mi int64

	// maxNow is the largest now of every accepted Send; initially 0.
	maxNow   int64
	accounts map[string]*account
}

// NewMeter constructs a Meter. Skeleton: always returns an empty meter.
func NewMeter(P, T0, p1, p2, MI int64) (*Meter, error) {
	const op = "NewMeter"
	if P < 1 || P > 1_000_000_000 {
		return nil, newError(ErrInvalidArgument, op, "P out of range [1,1e9]")
	}
	if T0 < 0 || T0 > 1_000_000 {
		return nil, newError(ErrInvalidArgument, op, "T0 out of range [0,1e6]")
	}
	if p1 < 0 || p1 > 1_000_000 || p2 < 0 || p2 > 1_000_000 {
		return nil, newError(ErrInvalidArgument, op, "unit price out of range [0,1e6]")
	}
	if MI < 100 || MI > 1000 {
		return nil, newError(ErrInvalidArgument, op, "MI out of range [100,1000]")
	}
	return &Meter{
		p:        P,
		t0:       T0,
		p1:       p1,
		p2:       p2,
		mi:       MI,
		accounts: make(map[string]*account),
	}, nil
}

// Deposit adds x to the balance of account.
func (m *Meter) Deposit(a string, x int64) error {
	const op = "Deposit"
	if a == "" {
		return newError(ErrInvalidArgument, op, "empty account id")
	}
	if x < 1 || x > 1_000_000_000_000 {
		return newError(ErrInvalidArgument, op, "x out of range [1,1e12]")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	acc := m.accounts[a]
	if acc != nil && acc.balance+x > 1_000_000_000_000_000 {
		return newError(ErrInvalidArgument, op, "balance would exceed 1e15")
	}
	if acc == nil {
		acc = &account{}
		m.accounts[a] = acc
	}
	acc.balance += x
	return nil
}

// Send segments text, bills account and mutates its state.
func (m *Meter) Send(a, text string, international bool, now int64) (*SendResult, error) {
	const op = "Send"
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := validateArguments(op, a, text, now); err != nil {
		return nil, err
	}
	acc := m.accounts[a]
	if acc == nil {
		return nil, newError(ErrAccountNotFound, op, "account does not exist")
	}
	if now < m.maxNow {
		return nil, newError(ErrClockSkew, op, "now is before a previously accepted Send")
	}

	split, err := splitText(text)
	if err != nil {
		return nil, rewriteOp(err, op)
	}

	kNext := now / m.p
	used, capacity := rollOver(m, acc, kNext)

	var s int64
	for j := int64(1); j <= int64(len(split.Segments)); j++ {
		if used+j <= capacity {
			s += m.p1
		} else {
			s += m.p2
		}
	}
	fee := s
	if international {
		fee = ceilDiv(s*m.mi, 100)
	}
	if fee > acc.balance {
		return nil, newError(ErrInsufficientBalance, op, "fee exceeds balance")
	}

	acc.balance -= fee
	acc.u = used + int64(len(split.Segments))
	acc.k = kNext
	acc.t = capacity
	acc.active = true
	if now > m.maxNow {
		m.maxNow = now
	}
	return &SendResult{Encoding: split.Encoding, Segments: len(split.Segments), Fee: fee}, nil
}

// Quote is a read-only Send: identical result, no state change.
func (m *Meter) Quote(a, text string, international bool, now int64) (*SendResult, error) {
	const op = "Quote"
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := validateArguments(op, a, text, now); err != nil {
		return nil, err
	}
	acc := m.accounts[a]
	if acc == nil {
		return nil, newError(ErrAccountNotFound, op, "account does not exist")
	}
	if now < m.maxNow {
		return nil, newError(ErrClockSkew, op, "now is before a previously accepted Send")
	}

	split, err := splitText(text)
	if err != nil {
		return nil, rewriteOp(err, op)
	}

	used, capacity := projectedState(m, acc, now/m.p)
	var s int64
	for j := int64(1); j <= int64(len(split.Segments)); j++ {
		if used+j <= capacity {
			s += m.p1
		} else {
			s += m.p2
		}
	}
	fee := s
	if international {
		fee = ceilDiv(s*m.mi, 100)
	}
	if fee > acc.balance {
		return nil, newError(ErrInsufficientBalance, op, "fee exceeds balance")
	}
	return &SendResult{Encoding: split.Encoding, Segments: len(split.Segments), Fee: fee}, nil
}

// Split returns the encoding and [start,end) code-point ranges of text.
func (m *Meter) Split(text string) (*SplitResult, error) { return splitText(text) }

// validateArguments performs the argument checks shared by Send and Quote.
// It reports only invalid-argument errors; length (>10 segments) is checked
// later so clock skew keeps precedence over it.
func validateArguments(op, a, text string, now int64) error {
	if a == "" {
		return newError(ErrInvalidArgument, op, "empty account id")
	}
	if text == "" {
		return newError(ErrInvalidArgument, op, "empty text")
	}
	if !utf8.ValidString(text) {
		return newError(ErrInvalidArgument, op, "invalid UTF-8 in text")
	}
	if now < 0 || now > 1_000_000_000_000 {
		return newError(ErrInvalidArgument, op, "now out of range [0,1e12]")
	}
	return nil
}

// rollOver applies the period transition to acc and returns the (used,
// capacity) in effect for period kNext.
func rollOver(m *Meter, acc *account, kNext int64) (used, capacity int64) {
	if !acc.active {
		return 0, m.t0
	}
	if kNext == acc.k {
		return acc.u, acc.t
	}
	previousUsed := int64(0)
	if kNext == acc.k+1 {
		previousUsed = acc.u
	}
	return 0, m.t0 + previousUsed/4
}

// projectedState returns the (used, capacity) a Send at kNext would see,
// without mutating acc.
func projectedState(m *Meter, acc *account, kNext int64) (used, capacity int64) {
	if !acc.active {
		return 0, m.t0
	}
	if kNext == acc.k {
		return acc.u, acc.t
	}
	if kNext == acc.k+1 {
		return 0, m.t0 + acc.u/4
	}
	return 0, m.t0
}

// ceilDiv returns ceil(a/b) for b > 0.
func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

func rewriteOp(err error, op string) error {
	var e *Error
	if errors.As(err, &e) {
		cp := *e
		cp.Op = op
		return &cp
	}
	return err
}
