package kerberos

import (
	"bytes"
	"fmt"
	"strconv"
	"sync"
)

const (
	minTime = int64(0)
	maxTime = int64(1_000_000_000_000_000)
	minArg  = int64(1)
	maxArg  = int64(1_000_000_000)
)

const (
	KindTGT     = "TGT"
	KindService = "service"
)

var krbtgt = []byte("krbtgt")

type Ticket struct {
	ID        string
	Kind      string
	Subject   []byte
	Service   []byte
	Start     int64
	End       int64
	RenewTill int64
	Invalid   bool
	Issued    int64
}

type KDC struct {
	mu       sync.Mutex
	maxLife  int64
	maxRenew int64
	skew     int64
	maxPost  int64
	now      int64
	next     int64
	tickets  map[string]*ticketRecord
	keys     map[string]int64
	replay   replayCache
	hasClock bool
}

type ticketRecord struct {
	ticket Ticket
}

func NewKDC(maxLife, maxRenewable, clockSkew, maxPostdated int64) (*KDC, error) {
	if maxLife < minArg || maxLife > maxArg ||
		maxRenewable < minArg || maxRenewable > maxArg ||
		clockSkew < minArg || clockSkew > maxArg ||
		maxPostdated < minArg || maxPostdated > maxArg ||
		maxLife > maxRenewable {
		return nil, &Error{Operation: "NewKDC", Reason: ErrInvalidConfig,
			Detail: fmt.Sprintf("invalid L=%d R=%d S=%d P=%d", maxLife, maxRenewable, clockSkew, maxPostdated)}
	}
	return &KDC{maxLife: maxLife, maxRenew: maxRenewable, skew: clockSkew, maxPost: maxPostdated,
		tickets: map[string]*ticketRecord{}, keys: map[string]int64{}, replay: newReplayCache()}, nil
}

func (k *KDC) IssueTGT(subject []byte, start, till, renewTill, now int64) (*Ticket, error) {
	const op = "IssueTGT"
	if err := validateBytes(op, "subject", subject); err != nil {
		return nil, err
	}
	for name, value := range map[string]int64{"start": start, "renewTill": renewTill} {
		if err := validateTime(op, name, value, true); err != nil {
			return nil, err
		}
	}
	if err := validateTime(op, "till", till, false); err != nil {
		return nil, err
	}
	if err := validateTime(op, "now", now, true); err != nil {
		return nil, err
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.checkClock(op, now); err != nil {
		return nil, err
	}

	s := now
	if start != 0 {
		if start < now {
			return nil, &Error{Operation: op, Reason: ErrInvalidParameter,
				Detail: fmt.Sprintf("start=%d before now=%d", start, now)}
		}
		s = start
	}
	if till <= s {
		return nil, &Error{Operation: op, Reason: ErrInvalidInterval,
			Detail: fmt.Sprintf("till=%d <= start=%d", till, s)}
	}
	if s > now+k.maxPost {
		return nil, &Error{Operation: op, Reason: ErrTooFarPostdated,
			Detail: fmt.Sprintf("start=%d > now+P=%d", s, now+k.maxPost)}
	}

	end := minInt64(till, s+k.maxLife)
	effectiveRenewTill := int64(0)
	if renewTill > 0 {
		if rt := minInt64(renewTill, s+k.maxRenew); rt > end {
			effectiveRenewTill = rt
		}
	}
	ticket := Ticket{
		ID:        k.nextID(),
		Kind:      KindTGT,
		Subject:   cloneBytes(subject),
		Service:   cloneBytes(krbtgt),
		Start:     s,
		End:       end,
		RenewTill: effectiveRenewTill,
		Invalid:   s > now,
		Issued:    now,
	}
	k.tickets[ticket.ID] = &ticketRecord{ticket: cloneTicket(ticket)}
	k.acceptClock(now)
	result := cloneTicket(ticket)
	return &result, nil
}

func (k *KDC) TGS(tgtID string, service []byte, till, renewTill, now int64) (*Ticket, error) {
	const op = "TGS"
	if tgtID == "" {
		return nil, &Error{Operation: op, Reason: ErrInvalidParameter, Detail: "tgt id is empty"}
	}
	if err := validateBytes(op, "service", service); err != nil {
		return nil, err
	}
	if bytes.Equal(service, krbtgt) {
		return nil, &Error{Operation: op, Reason: ErrInvalidParameter, Detail: "service is krbtgt"}
	}
	if err := validateTime(op, "till", till, false); err != nil {
		return nil, err
	}
	if err := validateTime(op, "renewTill", renewTill, true); err != nil {
		return nil, err
	}
	if err := validateTime(op, "now", now, true); err != nil {
		return nil, err
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.checkClock(op, now); err != nil {
		return nil, err
	}
	tgt, err := k.lookupTicket(op, tgtID)
	if err != nil {
		return nil, err
	}
	if tgt.ticket.Kind != KindTGT {
		return nil, &Error{Operation: op, Reason: ErrNotTGT, TicketID: tgtID}
	}
	if err := checkUsable(op, tgt.ticket, k.keys, now); err != nil {
		return nil, err
	}
	if till <= now {
		return nil, &Error{Operation: op, Reason: ErrInvalidInterval, TicketID: tgtID,
			Detail: fmt.Sprintf("till=%d <= now=%d", till, now)}
	}

	end := minInt64(till, now+k.maxLife, tgt.ticket.End)
	effectiveRenewTill := int64(0)
	if renewTill > 0 && tgt.ticket.RenewTill > 0 {
		if rt := minInt64(renewTill, now+k.maxRenew, tgt.ticket.RenewTill); rt > end {
			effectiveRenewTill = rt
		}
	}
	ticket := Ticket{
		ID:        k.nextID(),
		Kind:      KindService,
		Subject:   cloneBytes(tgt.ticket.Subject),
		Service:   cloneBytes(service),
		Start:     now,
		End:       end,
		RenewTill: effectiveRenewTill,
		Issued:    now,
	}
	k.tickets[ticket.ID] = &ticketRecord{ticket: cloneTicket(ticket)}
	k.acceptClock(now)
	result := cloneTicket(ticket)
	return &result, nil
}

func (k *KDC) Renew(id string, now int64) (*Ticket, error) {
	const op = "Renew"
	if err := validateID(op, id); err != nil {
		return nil, err
	}
	if err := validateTime(op, "now", now, true); err != nil {
		return nil, err
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.checkClock(op, now); err != nil {
		return nil, err
	}
	record, err := k.lookupTicket(op, id)
	if err != nil {
		return nil, err
	}
	if err := checkUsable(op, record.ticket, k.keys, now); err != nil {
		return nil, err
	}
	if record.ticket.RenewTill == 0 {
		return nil, &Error{Operation: op, Reason: ErrNotRenewable, TicketID: id}
	}
	life := record.ticket.End - record.ticket.Start
	newEnd := minInt64(now+life, record.ticket.RenewTill)
	if newEnd <= record.ticket.End {
		return nil, &Error{Operation: op, Reason: ErrRenewLimit, TicketID: id,
			Detail: fmt.Sprintf("new end=%d <= end=%d", newEnd, record.ticket.End)}
	}
	record.ticket.Start = now
	record.ticket.End = newEnd
	k.acceptClock(now)
	result := cloneTicket(record.ticket)
	return &result, nil
}

func (k *KDC) Validate(id string, now int64) (*Ticket, error) {
	const op = "Validate"
	if err := validateID(op, id); err != nil {
		return nil, err
	}
	if err := validateTime(op, "now", now, true); err != nil {
		return nil, err
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.checkClock(op, now); err != nil {
		return nil, err
	}
	record, err := k.lookupTicket(op, id)
	if err != nil {
		return nil, err
	}
	if !record.ticket.Invalid {
		return nil, &Error{Operation: op, Reason: ErrNotPostdated, TicketID: id}
	}
	if now < record.ticket.Start {
		return nil, &Error{Operation: op, Reason: ErrNotYetValid, TicketID: id}
	}
	if now >= record.ticket.End {
		return nil, &Error{Operation: op, Reason: ErrExpired, TicketID: id}
	}
	if err := checkKey(op, record.ticket, k.keys, id); err != nil {
		return nil, err
	}
	record.ticket.Invalid = false
	k.acceptClock(now)
	result := cloneTicket(record.ticket)
	return &result, nil
}

func (k *KDC) Authenticate(id string, authTime, now int64) (*Ticket, error) {
	const op = "Authenticate"
	if err := validateID(op, id); err != nil {
		return nil, err
	}
	if err := validateTime(op, "authTime", authTime, true); err != nil {
		return nil, err
	}
	if err := validateTime(op, "now", now, true); err != nil {
		return nil, err
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.checkClock(op, now); err != nil {
		return nil, err
	}
	record, err := k.lookupTicket(op, id)
	if err != nil {
		return nil, err
	}
	diff := now - authTime
	if diff < 0 {
		diff = -diff
	}
	if diff > k.skew {
		return nil, &Error{Operation: op, Reason: ErrClockSkew, TicketID: id,
			Detail: fmt.Sprintf("skew=%d > S=%d", diff, k.skew)}
	}
	if now < record.ticket.Start {
		return nil, &Error{Operation: op, Reason: ErrNotYetValid, TicketID: id}
	}
	if record.ticket.Invalid {
		return nil, &Error{Operation: op, Reason: ErrInvalidTicket, TicketID: id}
	}
	if now >= record.ticket.End {
		return nil, &Error{Operation: op, Reason: ErrExpired, TicketID: id}
	}
	if err := checkKey(op, record.ticket, k.keys, id); err != nil {
		return nil, err
	}
	key := replayKey{ticket: id, authTime: authTime}
	if _, ok := k.replay.entries[key]; ok {
		return nil, &Error{Operation: op, Reason: ErrReplay, TicketID: id,
			Detail: fmt.Sprintf("authTime=%d", authTime)}
	}
	k.acceptClock(now)
	k.replay.add(key, authTime+k.skew)
	result := cloneTicket(record.ticket)
	return &result, nil
}

func (k *KDC) ChangeKey(subject []byte, now int64) error {
	const op = "ChangeKey"
	if err := validateBytes(op, "subject", subject); err != nil {
		return err
	}
	if err := validateTime(op, "now", now, true); err != nil {
		return err
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.checkClock(op, now); err != nil {
		return err
	}
	k.keys[string(cloneBytes(subject))] = now
	k.acceptClock(now)
	return nil
}

var _ = bytes.Equal

func validateBytes(op, field string, value []byte) error {
	if len(value) == 0 {
		return &Error{Operation: op, Reason: ErrInvalidParameter, Detail: field + " is empty"}
	}
	return nil
}

func validateID(op, id string) error {
	if id == "" {
		return &Error{Operation: op, Reason: ErrInvalidParameter, Detail: "ticket id is empty"}
	}
	return nil
}

func validateTime(op, field string, value int64, allowZero bool) error {
	if value < 0 || value > maxTime || (!allowZero && value == 0) {
		return &Error{Operation: op, Reason: ErrInvalidParameter,
			Detail: fmt.Sprintf("%s=%d outside allowed range", field, value)}
	}
	return nil
}

func (k *KDC) checkClock(op string, now int64) error {
	if k.hasClock && now < k.now {
		return &Error{Operation: op, Reason: ErrClockRollback,
			Detail: fmt.Sprintf("now=%d before max now=%d", now, k.now)}
	}
	return nil
}

func (k *KDC) acceptClock(now int64) {
	if !k.hasClock || now > k.now {
		k.now = now
	}
	k.hasClock = true
	k.replay.cleanup(k.now)
}

func (k *KDC) lookupTicket(op, id string) (*ticketRecord, error) {
	record, ok := k.tickets[id]
	if !ok {
		return nil, &Error{Operation: op, Reason: ErrTicketNotFound, TicketID: id}
	}
	return record, nil
}

func checkUsable(op string, ticket Ticket, keys map[string]int64, now int64) error {
	if now < ticket.Start {
		return &Error{Operation: op, Reason: ErrNotYetValid, TicketID: ticket.ID}
	}
	if ticket.Invalid {
		return &Error{Operation: op, Reason: ErrInvalidTicket, TicketID: ticket.ID}
	}
	if now >= ticket.End {
		return &Error{Operation: op, Reason: ErrExpired, TicketID: ticket.ID}
	}
	return checkKey(op, ticket, keys, ticket.ID)
}

func checkKey(op string, ticket Ticket, keys map[string]int64, id string) error {
	if changedAt, ok := keys[string(ticket.Subject)]; ok && ticket.Issued < changedAt {
		return &Error{Operation: op, Reason: ErrKeyChanged, TicketID: id,
			Detail: fmt.Sprintf("issued=%d < key changed at=%d", ticket.Issued, changedAt)}
	}
	return nil
}

func (k *KDC) nextID() string {
	k.next++
	return "t" + strconv.FormatInt(k.next, 10)
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}

func cloneTicket(t Ticket) Ticket {
	t.Subject = cloneBytes(t.Subject)
	t.Service = cloneBytes(t.Service)
	return t
}

func minInt64(values ...int64) int64 {
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}
