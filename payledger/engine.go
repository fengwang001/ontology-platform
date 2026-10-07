package payledger

import (
	"fmt"
	"sync"
)

// Engine is the concurrent entry point of the ledger. All methods are safe
// for concurrent use; their combined effect equals some serial order, and
// replaying the same accepted operation sequence yields identical results.
type Engine struct {
	mu           sync.Mutex
	validityDays int64
	toleranceBps int64
	clk          clock
	accounts     map[string]*Account
	auths        map[string]*Authorization
}

// NewEngine builds an empty ledger. validityDays is the authorization
// lifetime E in days (an authorization created on day d stays valid through
// day d+E, inclusive); toleranceBps is the capture tolerance in basis
// points.
func NewEngine(validityDays, toleranceBps int64) (*Engine, error) {
	if validityDays < 0 || toleranceBps < 0 {
		return nil, fmt.Errorf("%w: validityDays and toleranceBps must be >= 0", ErrInvalidParam)
	}
	return &Engine{
		validityDays: validityDays,
		toleranceBps: toleranceBps,
		accounts:     make(map[string]*Account),
		auths:        make(map[string]*Authorization),
	}, nil
}

// checkDay validates the day argument and then the clock, in priority
// order. It never mutates anything.
func (e *Engine) checkDay(now int64) error {
	if now < 0 {
		return fmt.Errorf("%w: negative day %d", ErrInvalidParam, now)
	}
	return e.clk.check(now)
}

// ScanSteps returns the total number of bucket/day scan steps performed so
// far. It exists to empirically prove that query and sweep costs do not
// grow with the number of historical authorizations or accounts.
func (e *Engine) ScanSteps() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	var total int64
	for _, acc := range e.accounts {
		total += acc.scanSteps
	}
	return total
}

// CreateAccount registers a card account with the given credit limit.
func (e *Engine) CreateAccount(accountID string, creditLimit, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if accountID == "" || creditLimit < 0 {
		return fmt.Errorf("%w: CreateAccount(%q, %d)", ErrInvalidParam, accountID, creditLimit)
	}
	if err := e.checkDay(now); err != nil {
		return err
	}
	if _, ok := e.accounts[accountID]; ok {
		return fmt.Errorf("%w: %q", ErrAccountExists, accountID)
	}
	e.accounts[accountID] = newAccount(accountID, creditLimit, now)
	e.clk.advance(now)
	return nil
}

// AdjustCreditLimit sets a new credit limit. Raises take effect
// immediately; reductions are rejected with ErrInsufficientFunds when they
// would push the available credit below zero, leaving every state untouched.
func (e *Engine) AdjustCreditLimit(accountID string, newLimit, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if accountID == "" || newLimit < 0 {
		return fmt.Errorf("%w: AdjustCreditLimit(%q, %d)", ErrInvalidParam, accountID, newLimit)
	}
	if err := e.checkDay(now); err != nil {
		return err
	}
	acc, ok := e.accounts[accountID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrAccountNotFound, accountID)
	}
	if newLimit < acc.creditLimit && newLimit-acc.posted-(acc.totalHolds-acc.expiredBetween(acc.sweepDay, now)) < 0 {
		return fmt.Errorf("%w: limit %d would make available credit negative", ErrInsufficientFunds, newLimit)
	}
	acc.sweep(now)
	acc.creditLimit = newLimit
	e.clk.advance(now)
	return nil
}

// Authorize places a hold of amount on the account. The authorization stays
// valid through day now+validityDays inclusive.
func (e *Engine) Authorize(accountID, authID string, amount, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if accountID == "" || authID == "" || amount <= 0 {
		return fmt.Errorf("%w: Authorize(%q, %q, %d)", ErrInvalidParam, accountID, authID, amount)
	}
	if err := e.checkDay(now); err != nil {
		return err
	}
	if _, dup := e.auths[authID]; dup {
		return fmt.Errorf("%w: %q", ErrDuplicateAuthID, authID)
	}
	acc, ok := e.accounts[accountID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrAccountNotFound, accountID)
	}
	if acc.availableAt(now) < amount {
		return fmt.Errorf("%w: need %d", ErrInsufficientFunds, amount)
	}
	acc.sweep(now)
	auth := &Authorization{
		id:              authID,
		accountID:       accountID,
		totalAuthorized: amount,
		remainingHold:   amount,
		expiryDay:       now + e.validityDays,
	}
	e.auths[authID] = auth
	acc.addHold(auth.expiryDay, amount)
	e.clk.advance(now)
	return nil
}

// Increment adds amount to a live authorization and resets its validity to
// now+validityDays. Only the increment consumes extra available credit; the
// original hold is not re-charged. A failed increment leaves the original
// authorization and its expiry untouched.
func (e *Engine) Increment(authID string, amount, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if authID == "" || amount <= 0 {
		return fmt.Errorf("%w: Increment(%q, %d)", ErrInvalidParam, authID, amount)
	}
	if err := e.checkDay(now); err != nil {
		return err
	}
	auth, ok := e.auths[authID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrAuthNotFound, authID)
	}
	if st := auth.status(now); st != StatusActive {
		return fmt.Errorf("%w: %q is %s", ErrAuthTerminated, authID, st)
	}
	acc := e.accounts[auth.accountID]
	if acc.availableAt(now) < amount {
		return fmt.Errorf("%w: need %d", ErrInsufficientFunds, amount)
	}
	acc.sweep(now)
	acc.removeHold(auth.expiryDay, auth.remainingHold)
	auth.totalAuthorized += amount
	auth.remainingHold += amount
	auth.expiryDay = now + e.validityDays
	acc.addHold(auth.expiryDay, auth.remainingHold)
	e.clk.advance(now)
	return nil
}

// Capture converts held funds into posted balance. The part of amount that
// exceeds the remaining hold must be covered by the current available
// credit. When final is true the authorization terminates and any leftover
// hold is released immediately.
func (e *Engine) Capture(authID string, amount int64, final bool, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if authID == "" || amount <= 0 {
		return fmt.Errorf("%w: Capture(%q, %d)", ErrInvalidParam, authID, amount)
	}
	if err := e.checkDay(now); err != nil {
		return err
	}
	auth, ok := e.auths[authID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrAuthNotFound, authID)
	}
	if st := auth.status(now); st != StatusActive {
		return fmt.Errorf("%w: %q is %s", ErrAuthTerminated, authID, st)
	}
	if auth.totalCaptured+amount > auth.captureCap(e.toleranceBps) {
		return fmt.Errorf("%w: captured %d + %d over cap %d",
			ErrOverTolerance, auth.totalCaptured, amount, auth.captureCap(e.toleranceBps))
	}
	acc := e.accounts[auth.accountID]
	if excess := amount - auth.remainingHold; excess > 0 && acc.availableAt(now) < excess {
		return fmt.Errorf("%w: excess %d over available", ErrInsufficientFunds, excess)
	}
	acc.sweep(now)
	acc.removeHold(auth.expiryDay, auth.remainingHold)
	if !final && amount < auth.remainingHold {
		auth.remainingHold -= amount
		acc.addHold(auth.expiryDay, auth.remainingHold)
	} else {
		auth.remainingHold = 0
	}
	if final {
		auth.terminal = StatusFinalCaptured
	}
	acc.posted += amount
	auth.totalCaptured += amount
	e.clk.advance(now)
	return nil
}

// Reverse releases the remaining hold of a live authorization and
// terminates it. Already captured amounts are unaffected.
func (e *Engine) Reverse(authID string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if authID == "" {
		return fmt.Errorf("%w: Reverse(%q)", ErrInvalidParam, authID)
	}
	if err := e.checkDay(now); err != nil {
		return err
	}
	auth, ok := e.auths[authID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrAuthNotFound, authID)
	}
	if st := auth.status(now); st != StatusActive {
		return fmt.Errorf("%w: %q is %s", ErrAuthTerminated, authID, st)
	}
	acc := e.accounts[auth.accountID]
	acc.sweep(now)
	acc.removeHold(auth.expiryDay, auth.remainingHold)
	auth.remainingHold = 0
	auth.terminal = StatusReversed
	e.clk.advance(now)
	return nil
}

// Refund returns captured funds: it only decreases the posted balance, never
// restores holds, never changes the authorization state, its expiry, or its
// tolerance cap. Terminated authorizations can still be refunded.
func (e *Engine) Refund(authID string, amount, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if authID == "" || amount <= 0 {
		return fmt.Errorf("%w: Refund(%q, %d)", ErrInvalidParam, authID, amount)
	}
	if err := e.checkDay(now); err != nil {
		return err
	}
	auth, ok := e.auths[authID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrAuthNotFound, authID)
	}
	if refundable := auth.totalCaptured - auth.totalRefunded; amount > refundable {
		return fmt.Errorf("%w: %d over refundable %d", ErrRefundExceeds, amount, refundable)
	}
	acc := e.accounts[auth.accountID]
	acc.posted -= amount
	auth.totalRefunded += amount
	e.clk.advance(now)
	return nil
}

// Available reports the available credit of an account at day now. It is a
// pure query: it never mutates the ledger or the clock. now must not be
// earlier than the last accepted operation's day.
func (e *Engine) Available(accountID string, now int64) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if accountID == "" {
		return 0, fmt.Errorf("%w: Available(%q)", ErrInvalidParam, accountID)
	}
	if err := e.checkDay(now); err != nil {
		return 0, err
	}
	acc, ok := e.accounts[accountID]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrAccountNotFound, accountID)
	}
	return acc.availableAt(now), nil
}

// AuthState snapshots an authorization at day now. It is a pure query: it
// never mutates the ledger or the clock. now must not be earlier than the
// last accepted operation's day.
func (e *Engine) AuthState(authID string, now int64) (AuthView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if authID == "" {
		return AuthView{}, fmt.Errorf("%w: AuthState(%q)", ErrInvalidParam, authID)
	}
	if err := e.checkDay(now); err != nil {
		return AuthView{}, err
	}
	auth, ok := e.auths[authID]
	if !ok {
		return AuthView{}, fmt.Errorf("%w: %q", ErrAuthNotFound, authID)
	}
	return auth.view(now), nil
}
