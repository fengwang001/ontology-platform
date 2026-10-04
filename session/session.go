package session

import (
	"errors"
	"fmt"
	"sync"

	"ontology/balance"
	"ontology/rating"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockBack       = errors.New("clock moved backwards")
	ErrSessionExists   = errors.New("session already exists")
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionLimit    = errors.New("session limit exceeded")
	ErrSequence        = errors.New("invalid sequence number")
	ErrNoGrant         = errors.New("usage reported without grant")
)

type Reply struct {
	Charged    int64
	Unbilled   int64
	Granted    int64
	Final      bool
	Denied     bool
	ValidUntil int64
}

type Engine struct {
	mu           sync.Mutex
	ledger       *balance.Ledger
	catalog      *rating.Catalog
	smax         int64
	lmin         int64
	validity     int64
	maxSessions  int
	maxNow       int64
	sessions     map[string]*state
	accountCount map[string]int
	touched      uint64
}

type state struct {
	account   string
	unbilled  int64
	lastSeq   int64
	lastReply Reply
}

func New(smax int64, lmin int64, validity int64, maxSessions int) *Engine {
	if smax < 1 || smax > 1_000_000 || lmin < 1 || lmin > smax ||
		validity < 1 || validity > 1_000_000_000 || maxSessions < 1 {
		return nil
	}
	return &Engine{
		ledger:       balance.New(),
		catalog:      rating.New(),
		smax:         smax,
		lmin:         lmin,
		validity:     validity,
		maxSessions:  maxSessions,
		sessions:     make(map[string]*state),
		accountCount: make(map[string]int),
	}
}

func (e *Engine) SetRate(rateGroup int, price int64, now int64) error {
	if !validRateGroup(int64(rateGroup)) || !validPrice(price) || !validNow(now) {
		return ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.maxNow {
		return ErrClockBack
	}
	if err := e.catalog.SetRate(rateGroup, price, now); err != nil {
		return wrap(err)
	}
	e.maxNow = now
	return nil
}

func (e *Engine) TopUp(account string, amount int64, now int64) error {
	if account == "" || !validAmount(amount) || !validNow(now) {
		return ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.maxNow {
		return ErrClockBack
	}
	if err := e.ledger.TopUp(account, amount, now); err != nil {
		return wrap(err)
	}
	e.maxNow = now
	return nil
}

func (e *Engine) Open(session string, account string, now int64) error {
	if session == "" || account == "" || !validNow(now) {
		return ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.maxNow {
		return ErrClockBack
	}
	if _, ok := e.sessions[session]; ok {
		return ErrSessionExists
	}
	if e.accountCount[account] >= e.maxSessions {
		return ErrSessionLimit
	}
	e.ledger.Ensure(account)
	e.sessions[session] = &state{account: account}
	e.accountCount[account]++
	e.maxNow = now
	return nil
}

func (e *Engine) Update(session string, n int64, rateGroup int, used int64, want int64, now int64) (Reply, error) {
	if session == "" || !validSeq(n) || !validRateGroup(int64(rateGroup)) || !validUsage(used) || !validUsage(want) || !validNow(now) {
		return Reply{}, ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.maxNow {
		return Reply{}, ErrClockBack
	}
	s, ok := e.sessions[session]
	if !ok {
		return Reply{}, ErrSessionNotFound
	}
	if n == s.lastSeq {
		return s.lastReply, nil
	}
	if n != s.lastSeq+1 {
		return Reply{}, ErrSequence
	}
	var price int64
	if want > 0 {
		var ok bool
		price, ok = e.catalog.Price(rateGroup)
		if !ok {
			return Reply{}, ErrInvalidArgument
		}
	}
	key := balance.Key{Session: session, RateGroup: rateGroup}
	existed := e.ledger.Exists(s.account, key)
	if !existed && used > 0 {
		return Reply{}, ErrNoGrant
	}

	reply := Reply{}
	if existed {
		charged, amount, _, settleTouched := e.ledger.Settle(s.account, key, used, now)
		reply.Charged = charged
		reply.Unbilled = used - charged
		s.unbilled += reply.Unbilled
		e.touched += uint64(settleTouched)
		_ = amount
	}
	if want > 0 {
		granted, affordable, validUntil, grantTouched, err := e.ledger.Grant(s.account, key, want, e.smax, e.lmin, price, now, e.validity)
		if err != nil {
			return Reply{}, wrap(err)
		}
		reply.Granted = granted
		reply.Final = granted == affordable
		reply.Denied = granted == 0
		reply.ValidUntil = validUntil
		e.touched += uint64(grantTouched)
	}
	s.lastSeq = n
	s.lastReply = reply
	e.maxNow = now
	return reply, nil
}

func (e *Engine) Close(session string, n int64, usedByGroup map[int]int64, now int64) error {
	if session == "" || !validSeq(n) || !validNow(now) {
		return ErrInvalidArgument
	}
	for rg, used := range usedByGroup {
		if !validRateGroup(int64(rg)) || !validUsage(used) {
			return ErrInvalidArgument
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.maxNow {
		return ErrClockBack
	}
	s, ok := e.sessions[session]
	if !ok {
		return ErrSessionNotFound
	}
	if n == s.lastSeq {
		return nil
	}
	if n != s.lastSeq+1 {
		return ErrSequence
	}
	_, touched, err := e.ledger.SettleAll(s.account, session, usedByGroup, now)
	if err != nil {
		return wrap(err)
	}
	e.touched += uint64(touched)
	delete(e.sessions, session)
	e.accountCount[s.account]--
	if e.accountCount[s.account] == 0 {
		delete(e.accountCount, s.account)
	}
	s.lastSeq = n
	e.maxNow = now
	return nil
}

func validRateGroup(value int64) bool { return value >= 1 && value <= 1000 }

func validPrice(value int64) bool { return value >= 1 && value <= 1_000_000 }

func validAmount(value int64) bool { return value >= 1 && value <= 1_000_000_000_000 }

func validNow(value int64) bool { return value >= 0 && value <= 1_000_000_000_000 }

func validSeq(value int64) bool { return value >= 1 }

func validUsage(value int64) bool { return value >= 0 && value <= 1_000_000_000 }

func wrap(err error) error {
	switch {
	case errors.Is(err, balance.ErrInvalidArgument) || errors.Is(err, rating.ErrInvalidArgument):
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	case errors.Is(err, balance.ErrNoGrant):
		return fmt.Errorf("%w: %v", ErrNoGrant, err)
	case errors.Is(err, balance.ErrClockBack):
		return fmt.Errorf("%w: %v", ErrClockBack, err)
	default:
		return err
	}
}
