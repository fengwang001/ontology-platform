package kerberos

import (
	"sync"
)

const (
	maxTime      = int64(1_000_000_000_000_000)
	maxConfigVal = int64(1_000_000_000)
)

// KDC is a Kerberos style ticket granting centre. All methods are safe for
// concurrent use and behave as if serialized in some order.
type KDC struct {
	mu         sync.Mutex
	L, R, S, P int64
	maxNow     int64
	nextID     int64
	tickets    map[int64]*Ticket
	keys       map[string]int64
	cache      replayCache
}

// NewKDC constructs a KDC with maximal ticket life L, maximal renewable
// duration R (R >= L), clock skew allowance S and maximal postdate length P,
// all in seconds and within [1, 1e9].
func NewKDC(L, R, S, P int64) (*KDC, error) {
	if L < 1 || L > maxConfigVal || R < 1 || R > maxConfigVal ||
		S < 1 || S > maxConfigVal || P < 1 || P > maxConfigVal || L > R {
		return nil, newError(OpNewKDC, ReasonConfigInvalid,
			"L, R, S, P must be within [1, 1e9] with L <= R")
	}
	return &KDC{
		L:       L,
		R:       R,
		S:       S,
		P:       P,
		tickets: make(map[int64]*Ticket),
		keys:    make(map[string]int64),
		cache:   newReplayCache(S),
	}, nil
}

// IssueTGT issues a ticket granting ticket.
func (k *KDC) IssueTGT(subject []byte, start, till, renewTill, now int64) (*Ticket, error) {
	// 1. parameter validation
	if len(subject) == 0 {
		return nil, newError(OpIssueTGT, ReasonInvalidParam, "subject must be non-empty")
	}
	if err := checkTime(OpIssueTGT, "start", start); err != nil {
		return nil, err
	}
	if err := checkTime(OpIssueTGT, "till", till); err != nil {
		return nil, err
	}
	if err := checkTime(OpIssueTGT, "renewTill", renewTill); err != nil {
		return nil, err
	}
	if err := checkNow(OpIssueTGT, now); err != nil {
		return nil, err
	}
	if start != 0 && start < now {
		return nil, newError(OpIssueTGT, ReasonInvalidParam,
			"non-zero start must not be before now")
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	// 2. clock rewind
	if now < k.maxNow {
		return nil, newError(OpIssueTGT, ReasonClockRewind,
			"now is earlier than a previously accepted now")
	}
	k.cache.evict(now)

	s := now
	if start != 0 {
		s = start
	}
	// 3. interval
	if till <= s {
		return nil, newError(OpIssueTGT, ReasonBadInterval, "till must be greater than start")
	}
	// 4. postdated too far
	if s > now+k.P {
		return nil, newError(OpIssueTGT, ReasonPostdatedTooFar,
			"start is more than P beyond now")
	}

	end := min64(till, s+k.L)
	rt := int64(0)
	if renewTill > 0 {
		if cand := min64(renewTill, s+k.R); cand > end {
			rt = cand
		}
	}
	t := &Ticket{
		ID:        k.nextID + 1,
		Subject:   append([]byte(nil), subject...),
		Service:   []byte(Krbtgt),
		IsTGT:     true,
		Start:     s,
		End:       end,
		RenewTill: rt,
		Invalid:   s > now,
		Issued:    now,
	}
	k.nextID++
	k.tickets[t.ID] = t
	if now > k.maxNow {
		k.maxNow = now
	}
	return cloneTicket(t), nil
}

// TGS issues a service ticket using a TGT.
func (k *KDC) TGS(tgtID int64, service []byte, till, renewTill, now int64) (*Ticket, error) {
	if tgtID < 1 {
		return nil, newError(OpTGS, ReasonInvalidParam, "ticket id must be positive")
	}
	if len(service) == 0 {
		return nil, newError(OpTGS, ReasonInvalidParam, "service must be non-empty")
	}
	if string(service) == Krbtgt {
		return nil, newError(OpTGS, ReasonInvalidParam,
			"service name must not be "+Krbtgt)
	}
	if err := checkTime(OpTGS, "till", till); err != nil {
		return nil, err
	}
	if err := checkTime(OpTGS, "renewTill", renewTill); err != nil {
		return nil, err
	}
	if err := checkNow(OpTGS, now); err != nil {
		return nil, err
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	if now < k.maxNow {
		return nil, newError(OpTGS, ReasonClockRewind,
			"now is earlier than a previously accepted now")
	}
	k.cache.evict(now)
	tgt, ok := k.tickets[tgtID]
	if !ok {
		return nil, newError(OpTGS, ReasonTicketNotFound, "no ticket with that id")
	}
	if !tgt.IsTGT {
		return nil, withTicket(newError(OpTGS, ReasonNotTGT, "ticket is not a TGT"), tgtID)
	}
	// not yet valid, invalid flag, expired, key changed (in this order)
	if err := checkUsable(tgt, now, OpTGS, k.keys); err != nil {
		return nil, err
	}
	if till <= now {
		return nil, withTicket(newError(OpTGS, ReasonBadInterval,
			"till must be greater than now"), tgtID)
	}

	end := min64(till, min64(now+k.L, tgt.End))
	rt := int64(0)
	if renewTill > 0 && tgt.RenewTill > 0 {
		if cand := min64(renewTill, min64(now+k.R, tgt.RenewTill)); cand > end {
			rt = cand
		}
	}
	t := &Ticket{
		ID:        k.nextID + 1,
		Subject:   append([]byte(nil), tgt.Subject...),
		Service:   append([]byte(nil), service...),
		IsTGT:     false,
		Start:     now,
		End:       end,
		RenewTill: rt,
		Invalid:   false,
		Issued:    now,
	}
	k.nextID++
	k.tickets[t.ID] = t
	if now > k.maxNow {
		k.maxNow = now
	}
	return cloneTicket(t), nil
}

// Renew renews a ticket at time now.
func (k *KDC) Renew(id, now int64) (*Ticket, error) {
	if id < 1 {
		return nil, newError(OpRenew, ReasonInvalidParam, "ticket id must be positive")
	}
	if err := checkNow(OpRenew, now); err != nil {
		return nil, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if now < k.maxNow {
		return nil, newError(OpRenew, ReasonClockRewind,
			"now is earlier than a previously accepted now")
	}
	k.cache.evict(now)
	t, ok := k.tickets[id]
	if !ok {
		return nil, newError(OpRenew, ReasonTicketNotFound, "no ticket with that id")
	}
	if err := checkUsable(t, now, OpRenew, k.keys); err != nil {
		return nil, err
	}
	if t.RenewTill <= 0 {
		return nil, withTicket(newError(OpRenew, ReasonNotRenewable,
			"ticket has no renewTill"), id)
	}
	life := t.End - t.Start
	newEnd := min64(now+life, t.RenewTill)
	if newEnd <= t.End {
		return nil, withTicket(newError(OpRenew, ReasonRenewLimit,
			"renewal would not extend the ticket"), id)
	}
	t.Start = now
	t.End = newEnd
	if now > k.maxNow {
		k.maxNow = now
	}
	return cloneTicket(t), nil
}

// Validate validates a postdated ticket.
func (k *KDC) Validate(id, now int64) (*Ticket, error) {
	if id < 1 {
		return nil, newError(OpValidate, ReasonInvalidParam, "ticket id must be positive")
	}
	if err := checkNow(OpValidate, now); err != nil {
		return nil, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if now < k.maxNow {
		return nil, newError(OpValidate, ReasonClockRewind,
			"now is earlier than a previously accepted now")
	}
	k.cache.evict(now)
	t, ok := k.tickets[id]
	if !ok {
		return nil, newError(OpValidate, ReasonTicketNotFound, "no ticket with that id")
	}
	if !t.Invalid {
		return nil, withTicket(newError(OpValidate, ReasonNotPostdated,
			"ticket is not postdated"), id)
	}
	if now < t.Start {
		return nil, withTicket(newError(OpValidate, ReasonNotYetValid,
			"ticket is not yet valid"), id)
	}
	if now >= t.End {
		return nil, withTicket(newError(OpValidate, ReasonExpired,
			"ticket has expired"), id)
	}
	if c, ok := k.keys[string(t.Subject)]; ok && t.Issued < c {
		return nil, withTicket(newError(OpValidate, ReasonKeyChanged,
			"subject key changed after the ticket was issued"), id)
	}
	t.Invalid = false
	if now > k.maxNow {
		k.maxNow = now
	}
	return cloneTicket(t), nil
}

// Authenticate checks a service authentication request on the server side.
func (k *KDC) Authenticate(id, authTime, now int64) error {
	if id < 1 {
		return newError(OpAuthenticate, ReasonInvalidParam, "ticket id must be positive")
	}
	if err := checkTime(OpAuthenticate, "authTime", authTime); err != nil {
		return err
	}
	if err := checkNow(OpAuthenticate, now); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if now < k.maxNow {
		return newError(OpAuthenticate, ReasonClockRewind,
			"now is earlier than a previously accepted now")
	}
	k.cache.evict(now)
	// clock skew first
	diff := now - authTime
	if diff < 0 {
		diff = -diff
	}
	if diff > k.S {
		return newError(OpAuthenticate, ReasonClockSkew,
			"|now - authTime| exceeds the allowed skew")
	}
	t, ok := k.tickets[id]
	if !ok {
		return newError(OpAuthenticate, ReasonTicketNotFound, "no ticket with that id")
	}
	if now < t.Start {
		return withTicket(newError(OpAuthenticate, ReasonNotYetValid,
			"ticket is not yet valid"), id)
	}
	if t.Invalid {
		return withTicket(newError(OpAuthenticate, ReasonInvalidFlag,
			"ticket carries the invalid flag"), id)
	}
	if now >= t.End {
		return withTicket(newError(OpAuthenticate, ReasonExpired,
			"ticket has expired"), id)
	}
	if c, ok := k.keys[string(t.Subject)]; ok && t.Issued < c {
		return withTicket(newError(OpAuthenticate, ReasonKeyChanged,
			"subject key changed after the ticket was issued"), id)
	}
	key := replayKey{id: id, time: authTime}
	if k.cache.contains(key) {
		return withTicket(newError(OpAuthenticate, ReasonReplay,
			"(ticket, authTime) was already authenticated"), id)
	}
	k.cache.add(key, now)
	if now > k.maxNow {
		k.maxNow = now
	}
	return nil
}

// ChangeKey records a password change for subject at time now.
func (k *KDC) ChangeKey(subject []byte, now int64) error {
	if len(subject) == 0 {
		return newError(OpChangeKey, ReasonInvalidParam, "subject must be non-empty")
	}
	if err := checkNow(OpChangeKey, now); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if now < k.maxNow {
		return newError(OpChangeKey, ReasonClockRewind,
			"now is earlier than a previously accepted now")
	}
	k.cache.evict(now)
	k.keys[string(subject)] = now
	if now > k.maxNow {
		k.maxNow = now
	}
	return nil
}

// MaxNow returns the greatest now seen by an accepted operation.
func (k *KDC) MaxNow() int64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.maxNow
}

func checkNow(op string, now int64) error {
	return checkTime(op, "now", now)
}

func checkTime(op, name string, v int64) error {
	if v < 0 || v > maxTime {
		return newError(op, ReasonInvalidParam, name+" must be within [0, 1e15]")
	}
	return nil
}

// checkUsable enforces the shared order: not yet valid, invalid flag,
// expired, key changed. The ticket is known to exist.
func checkUsable(t *Ticket, now int64, op string, keys map[string]int64) error {
	if now < t.Start {
		return withTicket(newError(op, ReasonNotYetValid, "ticket is not yet valid"), t.ID)
	}
	if t.Invalid {
		return withTicket(newError(op, ReasonInvalidFlag, "ticket carries the invalid flag"), t.ID)
	}
	if now >= t.End {
		return withTicket(newError(op, ReasonExpired, "ticket has expired"), t.ID)
	}
	if c, ok := keys[string(t.Subject)]; ok && t.Issued < c {
		return withTicket(newError(op, ReasonKeyChanged,
			"subject key changed after the ticket was issued"), t.ID)
	}
	return nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func cloneTicket(t *Ticket) *Ticket {
	cp := *t
	cp.Subject = append([]byte(nil), t.Subject...)
	cp.Service = append([]byte(nil), t.Service...)
	return &cp
}
