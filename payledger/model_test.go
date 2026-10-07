package payledger

// This file contains an independent naive model of the ledger, written
// directly from the specification rules. It deliberately uses O(history)
// scans and shares no logic with the Engine, so the randomized differential
// test below cross-checks two independent implementations of the rules.

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

type mAuth struct {
	account                              string
	authorized, captured, refunded, hold int64
	expiry                               int64
	terminal                             AuthStatus // StatusActive while live
}

type model struct {
	validity, tolBps int64
	last             int64
	started          bool
	limits           map[string]int64
	posted           map[string]int64
	auths            map[string]*mAuth
}

func newModel(validity, tolBps int64) *model {
	return &model{
		validity: validity,
		tolBps:   tolBps,
		limits:   map[string]int64{},
		posted:   map[string]int64{},
		auths:    map[string]*mAuth{},
	}
}

func (m *model) checkClock(now int64) error {
	if now < 0 {
		return ErrInvalidParam
	}
	if m.started && now < m.last {
		return ErrClockRegression
	}
	return nil
}

func (m *model) accept(now int64) {
	m.last = now
	m.started = true
}

func (m *model) statusOf(a *mAuth, now int64) AuthStatus {
	if a.terminal != StatusActive {
		return a.terminal
	}
	if now > a.expiry {
		return StatusExpired
	}
	return StatusActive
}

// available recomputes from scratch by scanning every authorization ever
// created: the naive O(history) reference of the pure specification.
func (m *model) available(acct string, now int64) int64 {
	holds := int64(0)
	for _, a := range m.auths {
		if a.account == acct && m.statusOf(a, now) == StatusActive {
			holds += a.hold
		}
	}
	return m.limits[acct] - m.posted[acct] - holds
}

func (m *model) createAccount(id string, limit, now int64) error {
	if id == "" || limit < 0 {
		return ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if _, ok := m.limits[id]; ok {
		return ErrAccountExists
	}
	m.limits[id] = limit
	m.accept(now)
	return nil
}

func (m *model) adjustLimit(id string, newLimit, now int64) error {
	if id == "" || newLimit < 0 {
		return ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if _, ok := m.limits[id]; !ok {
		return ErrAccountNotFound
	}
	if newLimit < m.limits[id] && newLimit-m.posted[id]-(m.limits[id]-m.posted[id]-m.available(id, now)) < 0 {
		return ErrInsufficientFunds
	}
	m.limits[id] = newLimit
	m.accept(now)
	return nil
}

func (m *model) authorize(acct, id string, amount, now int64) error {
	if acct == "" || id == "" || amount <= 0 {
		return ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if _, dup := m.auths[id]; dup {
		return ErrDuplicateAuthID
	}
	if _, ok := m.limits[acct]; !ok {
		return ErrAccountNotFound
	}
	if m.available(acct, now) < amount {
		return ErrInsufficientFunds
	}
	m.auths[id] = &mAuth{account: acct, authorized: amount, hold: amount, expiry: now + m.validity}
	m.accept(now)
	return nil
}

func (m *model) increment(id string, amount, now int64) error {
	if id == "" || amount <= 0 {
		return ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	a, ok := m.auths[id]
	if !ok {
		return ErrAuthNotFound
	}
	if m.statusOf(a, now) != StatusActive {
		return ErrAuthTerminated
	}
	if m.available(a.account, now) < amount {
		return ErrInsufficientFunds
	}
	a.authorized += amount
	a.hold += amount
	a.expiry = now + m.validity
	m.accept(now)
	return nil
}

func (m *model) capture(id string, amount int64, final bool, now int64) error {
	if id == "" || amount <= 0 {
		return ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	a, ok := m.auths[id]
	if !ok {
		return ErrAuthNotFound
	}
	if m.statusOf(a, now) != StatusActive {
		return ErrAuthTerminated
	}
	cap_ := a.authorized + a.authorized*m.tolBps/10000
	if a.captured+amount > cap_ {
		return ErrOverTolerance
	}
	if excess := amount - a.hold; excess > 0 && m.available(a.account, now) < excess {
		return ErrInsufficientFunds
	}
	if amount >= a.hold {
		a.hold = 0
	} else {
		a.hold -= amount
	}
	if final {
		a.hold = 0
		a.terminal = StatusFinalCaptured
	}
	m.posted[a.account] += amount
	a.captured += amount
	m.accept(now)
	return nil
}

func (m *model) reverse(id string, now int64) error {
	if id == "" {
		return ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	a, ok := m.auths[id]
	if !ok {
		return ErrAuthNotFound
	}
	if m.statusOf(a, now) != StatusActive {
		return ErrAuthTerminated
	}
	a.hold = 0
	a.terminal = StatusReversed
	m.accept(now)
	return nil
}

func (m *model) refund(id string, amount, now int64) error {
	if id == "" || amount <= 0 {
		return ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	a, ok := m.auths[id]
	if !ok {
		return ErrAuthNotFound
	}
	if amount > a.captured-a.refunded {
		return ErrRefundExceeds
	}
	m.posted[a.account] -= amount
	a.refunded += amount
	m.accept(now)
	return nil
}

// errKind maps an error to a stable comparable token.
func errKind(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidParam):
		return "invalid-param"
	case errors.Is(err, ErrClockRegression):
		return "clock-regression"
	case errors.Is(err, ErrDuplicateAuthID):
		return "duplicate-auth-id"
	case errors.Is(err, ErrAccountNotFound):
		return "account-not-found"
	case errors.Is(err, ErrAccountExists):
		return "account-exists"
	case errors.Is(err, ErrAuthNotFound):
		return "auth-not-found"
	case errors.Is(err, ErrAuthTerminated):
		return "auth-terminated"
	case errors.Is(err, ErrOverTolerance):
		return "over-tolerance"
	case errors.Is(err, ErrInsufficientFunds):
		return "insufficient-funds"
	case errors.Is(err, ErrRefundExceeds):
		return "refund-exceeds"
	default:
		return "unknown: " + err.Error()
	}
}

// TestRandomizedAgainstModel replays large random operation sequences
// against both the Engine and the independent naive model, requiring identical
// results at every step. Every operation is logged with its input, output
// and the rule (error kind) that decided it.
func TestRandomizedAgainstModel(t *testing.T) {
	const (
		seeds       = 30
		opsPerSeed  = 1500
		numAccounts = 4
		numAuthIDs  = 40
	)
	for seed := int64(0); seed < seeds; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			validity := int64(rng.Intn(5))
			tolBps := int64(rng.Intn(4) * 25) // 0, 25, 50, 75 bps
			e, err := NewEngine(validity, tolBps)
			mustOK(t, err)
			m := newModel(validity, tolBps)
			t.Logf("config: validity=%d days, tolerance=%d bps", validity, tolBps)

			accounts := []string{"acct-0", "acct-1", "acct-2", "acct-3"}
			authIDs := make([]string, 0, numAuthIDs)
			for i := 0; i < numAuthIDs; i++ {
				authIDs = append(authIDs, fmt.Sprintf("auth-%d", i))
			}
			now := int64(0)
			pickNow := func() int64 {
				switch r := rng.Intn(100); {
				case r < 3:
					return now - 1 - int64(rng.Intn(3)) // clock regression attempt
				case r < 5:
					return -1 // invalid day
				default:
					now += int64(rng.Intn(4))
					return now
				}
			}
			pickAmount := func() int64 {
				if rng.Intn(20) == 0 {
					return int64(-rng.Intn(10)) // invalid amount
				}
				return int64(1 + rng.Intn(2000))
			}

			for step := 0; step < opsPerSeed; step++ {
				var engErr, modErr error
				var desc string
				switch rng.Intn(100) {
				case 0, 1, 2, 3, 4: // create account
					acct := accounts[rng.Intn(numAccounts)]
					limit := int64(rng.Intn(5000))
					n := pickNow()
					desc = fmt.Sprintf("CreateAccount(%s, %d, now=%d)", acct, limit, n)
					engErr = e.CreateAccount(acct, limit, n)
					modErr = m.createAccount(acct, limit, n)
				case 5, 6, 7, 8, 9: // adjust limit
					acct := accounts[rng.Intn(numAccounts)]
					limit := int64(rng.Intn(5000))
					n := pickNow()
					desc = fmt.Sprintf("AdjustCreditLimit(%s, %d, now=%d)", acct, limit, n)
					engErr = e.AdjustCreditLimit(acct, limit, n)
					modErr = m.adjustLimit(acct, limit, n)
				default:
					switch op := rng.Intn(6); op {
					case 0: // authorize
						acct := accounts[rng.Intn(numAccounts)]
						id := authIDs[rng.Intn(len(authIDs))]
						amount := pickAmount()
						n := pickNow()
						desc = fmt.Sprintf("Authorize(%s, %s, %d, now=%d)", acct, id, amount, n)
						engErr = e.Authorize(acct, id, amount, n)
						modErr = m.authorize(acct, id, amount, n)
					case 1: // increment
						id := authIDs[rng.Intn(len(authIDs))]
						amount := pickAmount()
						n := pickNow()
						desc = fmt.Sprintf("Increment(%s, %d, now=%d)", id, amount, n)
						engErr = e.Increment(id, amount, n)
						modErr = m.increment(id, amount, n)
					case 2, 3: // capture
						id := authIDs[rng.Intn(len(authIDs))]
						amount := pickAmount()
						final := rng.Intn(4) == 0
						n := pickNow()
						desc = fmt.Sprintf("Capture(%s, %d, final=%v, now=%d)", id, amount, final, n)
						engErr = e.Capture(id, amount, final, n)
						modErr = m.capture(id, amount, final, n)
					case 4: // reverse
						id := authIDs[rng.Intn(len(authIDs))]
						n := pickNow()
						desc = fmt.Sprintf("Reverse(%s, now=%d)", id, n)
						engErr = e.Reverse(id, n)
						modErr = m.reverse(id, n)
					case 5: // refund
						id := authIDs[rng.Intn(len(authIDs))]
						amount := pickAmount()
						n := pickNow()
						desc = fmt.Sprintf("Refund(%s, %d, now=%d)", id, amount, n)
						engErr = e.Refund(id, amount, n)
						modErr = m.refund(id, amount, n)
					}
				}
				if errKind(engErr) != errKind(modErr) {
					t.Fatalf("step %d: %s\nengine: %v\nmodel:  %v", step, desc, engErr, modErr)
				}
				t.Logf("step %04d %-45s -> %-18s (rule: %s)", step, desc, errKind(engErr), errKind(engErr))

				// Cross-check observable state after every operation.
				qnow := now
				for _, acct := range accounts {
					if _, ok := m.limits[acct]; !ok {
						continue
					}
					got, err := e.Available(acct, qnow)
					mustOK(t, err)
					want := m.available(acct, qnow)
					if got != want {
						t.Fatalf("step %d (%s): available(%s)=%d, model=%d", step, desc, acct, got, want)
					}
					if got < 0 {
						t.Fatalf("step %d (%s): negative available %d on %s", step, desc, got, acct)
					}
				}
				for _, id := range authIDs {
					ma, ok := m.auths[id]
					if !ok {
						continue
					}
					view, err := e.AuthState(id, qnow)
					mustOK(t, err)
					want := AuthView{
						ID: id, AccountID: ma.account, Status: m.statusOf(ma, qnow),
						TotalAuthorized: ma.authorized, TotalCaptured: ma.captured,
						TotalRefunded: ma.refunded, ExpiryDay: ma.expiry,
					}
					if want.Status == StatusActive {
						want.RemainingHold = ma.hold
					}
					if view != want {
						t.Fatalf("step %d (%s): authState(%s)=%+v, model=%+v", step, desc, id, view, want)
					}
				}
			}
		})
	}
}
