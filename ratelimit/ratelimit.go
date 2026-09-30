// Package ratelimit implements a login-attempt limiter that tracks failed
// logins independently per account and per source and applies progressive,
// time-bounded lockouts.
package ratelimit

import (
	"errors"
	"sync"
	"time"
)

// Reason identifies why an attempt was rejected before a password was even
// evaluated.
type Reason string

const (
	// ReasonInvalidConfig is returned by New when its parameters are invalid.
	ReasonInvalidConfig Reason = "invalid_config"
	// ReasonEmptyAccount means the attempt carried an empty account.
	ReasonEmptyAccount Reason = "empty_account"
	// ReasonEmptySource means the attempt carried an empty source.
	ReasonEmptySource Reason = "empty_source"
	// ReasonClockRewound means the supplied time is earlier than the largest
	// time previously seen by the limiter.
	ReasonClockRewound Reason = "clock_rewound"
	// ReasonLocked means at least one of the account/source keys is locked.
	ReasonLocked Reason = "locked"
)

// Dimension identifies which key triggered a lockout.
type Dimension string

const (
	// DimensionAccount is the per-account key.
	DimensionAccount Dimension = "account"
	// DimensionSource is the per-source key.
	DimensionSource Dimension = "source"
)

// Config holds the tunable parameters of the limiter.
type Config struct {
	// Window is the sliding window in which failures are counted.
	Window time.Duration
	// Threshold is the number of failures inside a window that triggers a
	// lockout.
	Threshold int
	// BaseLock is the duration of the first lockout level.
	BaseLock time.Duration
	// MaxLock caps the duration of any single lockout level.
	MaxLock time.Duration
	// Cooldown is the quiet period after a lockout expires after which the
	// progressive level resets to zero.
	Cooldown time.Duration
}

// Result is the outcome of a single attempt.
type Result struct {
	// Allowed is true only when the attempt was not locked and the password
	// was correct.
	Allowed bool
	// Locked reports whether the attempt was rejected because a key was
	// currently locked. A wrong password leaves Locked false.
	Locked bool
	// Rejected reports a validation failure (empty account/source or a
	// rewound clock). Such attempts never mutate limiter state.
	Rejected bool
	// Reason carries the precise cause when Rejected or Locked is set.
	Reason Reason
	// PasswordCorrect is the caller-supplied verdict, recorded for logging.
	PasswordCorrect bool
	// UnlockAt is the later unlock time of the keys currently locked; it is
	// zero when Locked is false.
	UnlockAt time.Time
	// Dimensions lists the dimensions currently locked, ordered account
	// first.
	Dimensions []Dimension
}

// entry is the mutable per-key state.
type entry struct {
	// failures holds failure timestamps inside the current window.
	failures []time.Time
	// lockedUntil is the lockout deadline; zero means not locked.
	lockedUntil time.Time
	// level is the number of lockouts already triggered in the current
	// progressive series.
	level int
}

// Limiter is safe for concurrent use.
type Limiter struct {
	cfg Config

	mu       sync.Mutex
	maxNow   time.Time
	accounts map[string]*entry
	sources  map[string]*entry
	logger   Logger
}

// New validates the configuration and returns an empty limiter.
func New(cfg Config) (*Limiter, error) {
	if cfg.Window <= 0 || cfg.Threshold <= 0 || cfg.BaseLock <= 0 ||
		cfg.MaxLock <= 0 || cfg.Cooldown <= 0 {
		return nil, errors.New("ratelimit: all durations and the threshold must be positive")
	}
	if cfg.MaxLock < cfg.BaseLock {
		return nil, errors.New("ratelimit: MaxLock must not be smaller than BaseLock")
	}
	return &Limiter{
		cfg:      cfg,
		accounts: make(map[string]*entry),
		sources:  make(map[string]*entry),
		logger:   defaultLogger{},
	}, nil
}

// Attempt evaluates one login attempt at time now. passwordCorrect is the
// caller's verdict about the supplied password.
func (l *Limiter) Attempt(account, source string, passwordCorrect bool, now time.Time) Result {
	if account == "" {
		res := Result{Rejected: true, Reason: ReasonEmptyAccount, PasswordCorrect: passwordCorrect}
		l.logAttempt(account, source, passwordCorrect, now, res, "account is empty")
		return res
	}
	if source == "" {
		res := Result{Rejected: true, Reason: ReasonEmptySource, PasswordCorrect: passwordCorrect}
		l.logAttempt(account, source, passwordCorrect, now, res, "source is empty")
		return res
	}

	l.mu.Lock()
	if !l.maxNow.IsZero() && now.Before(l.maxNow) {
		l.mu.Unlock()
		res := Result{Rejected: true, Reason: ReasonClockRewound, PasswordCorrect: passwordCorrect}
		l.logAttempt(account, source, passwordCorrect, now, res,
			"clock reading "+now.Format(time.RFC3339Nano)+" is earlier than last seen "+l.maxNow.Format(time.RFC3339Nano))
		return res
	}

	acc := l.accounts[account]
	src := l.sources[source]

	var lockedDims []Dimension
	unlockAt := now
	if acc != nil && now.Before(acc.lockedUntil) {
		lockedDims = append(lockedDims, DimensionAccount)
		if acc.lockedUntil.After(unlockAt) {
			unlockAt = acc.lockedUntil
		}
	}
	if src != nil && now.Before(src.lockedUntil) {
		lockedDims = append(lockedDims, DimensionSource)
		if src.lockedUntil.After(unlockAt) {
			unlockAt = src.lockedUntil
		}
	}
	if len(lockedDims) > 0 {
		// Locked attempts do not count, do not extend the lock and do not
		// move the clock; no key state changes.
		l.mu.Unlock()
		res := Result{
			Locked:          true,
			Reason:          ReasonLocked,
			PasswordCorrect: passwordCorrect,
			UnlockAt:        unlockAt,
			Dimensions:      lockedDims,
		}
		l.logAttempt(account, source, passwordCorrect, now, res,
			"key locked; attempt denied without counting the failure")
		return res
	}

	// The attempt is valid and evaluated; advance the monotonic clock.
	l.maxNow = now

	if passwordCorrect {
		// Success clears the account key's failures and progressive level;
		// the source key is deliberately left untouched.
		if acc != nil {
			acc.failures = nil
			acc.level = 0
		}
		l.mu.Unlock()
		res := Result{Allowed: true, PasswordCorrect: true}
		l.logAttempt(account, source, passwordCorrect, now, res,
			"password correct; cleared account failures and level, source key unchanged")
		return res
	}

	if acc == nil {
		acc = l.getOrCreateAccount(account)
	}
	if src == nil {
		src = l.getOrCreateSource(source)
	}
	accTriggered := acc.recordFailure(now, l.cfg.Window, l.cfg.Threshold)
	if accTriggered {
		l.triggerLock(acc, now)
	}
	srcTriggered := src.recordFailure(now, l.cfg.Window, l.cfg.Threshold)
	if srcTriggered {
		l.triggerLock(src, now)
	}
	l.mu.Unlock()

	res := Result{PasswordCorrect: false}
	basis := "password incorrect; failure recorded on account and source keys"
	if accTriggered || srcTriggered {
		basis += "; threshold reached, lockout triggered but the attempt still reports a wrong password"
	}
	l.logAttempt(account, source, passwordCorrect, now, res, basis)
	return res
}

// recordFailure prunes timestamps outside the window, appends now and reports
// whether this failure is the K-th failure inside the window.
func (e *entry) recordFailure(now time.Time, window time.Duration, threshold int) bool {
	cutoff := now.Add(-window)
	kept := e.failures[:0]
	for _, t := range e.failures {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	e.failures = append(kept, now)
	return len(e.failures) >= threshold
}

// triggerLock advances the progressive level, honoring the cooldown reset, and
// sets the lockout deadline. The window failures are cleared.
func (l *Limiter) triggerLock(e *entry, now time.Time) {
	if !e.lockedUntil.IsZero() && !now.Before(e.lockedUntil.Add(l.cfg.Cooldown)) {
		e.level = 0
	}
	e.level++

	d := l.cfg.MaxLock
	step := l.cfg.BaseLock
	for k := 1; k < e.level; k++ {
		if step >= l.cfg.MaxLock || step > l.cfg.MaxLock/2 {
			step = l.cfg.MaxLock
			break
		}
		step *= 2
	}
	if step < d {
		d = step
	}
	e.lockedUntil = now.Add(d)
	e.failures = nil
}

// getOrCreateAccount returns the account entry, creating it on first use.
// Callers must hold l.mu.
func (l *Limiter) getOrCreateAccount(name string) *entry {
	e := l.accounts[name]
	if e == nil {
		e = &entry{}
		l.accounts[name] = e
	}
	return e
}

// getOrCreateSource returns the source entry, creating it on first use.
// Callers must hold l.mu.
func (l *Limiter) getOrCreateSource(name string) *entry {
	e := l.sources[name]
	if e == nil {
		e = &entry{}
		l.sources[name] = e
	}
	return e
}
