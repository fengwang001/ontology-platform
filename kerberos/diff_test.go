package kerberos

import (
	"fmt"
)

type refTicket struct {
	id        int64
	subject   string
	service   string
	isTGT     bool
	start     int64
	end       int64
	renewTill int64
	invalid   bool
	issued    int64
}

type refEntry struct{ id, authTime int64 }

type refResult struct {
	reason string
	ticket *refTicket
}

// naiveKDC re-implements every rule directly with a linear-scan replay cache
// and shares no code with KDC.
type naiveKDC struct {
	l, r, s, p int64
	maxNow     int64
	nextID     int64
	tickets    map[int64]*refTicket
	keys       map[string]int64
	cache      map[refEntry]int64 // entry -> authTime+S
	log        []string
}

func newNaive(L, R, S, P int64) (*naiveKDC, error) {
	if L < 1 || L > maxConfigVal || R < 1 || R > maxConfigVal ||
		S < 1 || S > maxConfigVal || P < 1 || P > maxConfigVal || L > R {
		return nil, newError(OpNewKDC, ReasonConfigInvalid, "bad config")
	}
	return &naiveKDC{
		l: L, r: R, s: S, p: P,
		tickets: map[int64]*refTicket{},
		keys:    map[string]int64{},
		cache:   map[refEntry]int64{},
	}, nil
}

func (n *naiveKDC) evict(now int64) {
	for e, expiry := range n.cache {
		if now > expiry {
			delete(n.cache, e)
		}
	}
}

func (n *naiveKDC) commit(now int64) {
	n.evict(now)
	if now > n.maxNow {
		n.maxNow = now
	}
}

func inTime(v int64) bool { return v >= 0 && v <= maxTime }

func minI(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func (n *naiveKDC) issueTGT(subject string, start, till, renewTill, now int64) refResult {
	n.log = append(n.log, fmt.Sprintf("IssueTGT(%q,%d,%d,%d,%d)", subject, start, till, renewTill, now))
	if subject == "" || !inTime(start) || !inTime(till) || !inTime(renewTill) ||
		!inTime(now) || (start != 0 && start < now) {
		return refResult{reason: ReasonInvalidParam}
	}
	if now < n.maxNow {
		return refResult{reason: ReasonClockRewind}
	}
	s := now
	if start != 0 {
		s = start
	}
	if till <= s {
		return refResult{reason: ReasonBadInterval}
	}
	if s > now+n.p {
		return refResult{reason: ReasonPostdatedTooFar}
	}
	end := minI(till, s+n.l)
	rt := int64(0)
	if renewTill > 0 {
		if c := minI(renewTill, s+n.r); c > end {
			rt = c
		}
	}
	n.nextID++
	t := &refTicket{
		id: n.nextID, subject: subject, service: Krbtgt, isTGT: true,
		start: s, end: end, renewTill: rt, invalid: s > now, issued: now,
	}
	n.tickets[t.id] = t
	n.commit(now)
	n.log = append(n.log, fmt.Sprintf(
		"  -> ok t%d [%d,%d) rt=%d invalid=%v issued=%d | end=min(till,s+L); rt=min(renewTill,s+R) if >end",
		t.id, t.start, t.end, t.renewTill, t.invalid, t.issued))
	return refResult{ticket: t}
}

func (n *naiveKDC) usable(t *refTicket, now int64) string {
	if now < t.start {
		return ReasonNotYetValid
	}
	if t.invalid {
		return ReasonInvalidFlag
	}
	if now >= t.end {
		return ReasonExpired
	}
	if c, ok := n.keys[t.subject]; ok && t.issued < c {
		return ReasonKeyChanged
	}
	return ""
}

func (n *naiveKDC) tgs(id int64, service string, till, renewTill, now int64) refResult {
	n.log = append(n.log, fmt.Sprintf("TGS(t%d,%q,%d,%d,%d)", id, service, till, renewTill, now))
	if id < 1 || service == "" || service == Krbtgt || !inTime(till) ||
		!inTime(renewTill) || !inTime(now) {
		return refResult{reason: ReasonInvalidParam}
	}
	if now < n.maxNow {
		return refResult{reason: ReasonClockRewind}
	}
	t, ok := n.tickets[id]
	if !ok {
		return refResult{reason: ReasonTicketNotFound}
	}
	if !t.isTGT {
		return refResult{reason: ReasonNotTGT}
	}
	if why := n.usable(t, now); why != "" {
		return refResult{reason: why}
	}
	if till <= now {
		return refResult{reason: ReasonBadInterval}
	}
	end := minI(till, minI(now+n.l, t.end))
	rt := int64(0)
	if renewTill > 0 && t.renewTill > 0 {
		if c := minI(renewTill, minI(now+n.r, t.renewTill)); c > end {
			rt = c
		}
	}
	n.nextID++
	st := &refTicket{
		id: n.nextID, subject: t.subject, service: service, isTGT: false,
		start: now, end: end, renewTill: rt, invalid: false, issued: now,
	}
	n.tickets[st.id] = st
	n.commit(now)
	n.log = append(n.log, fmt.Sprintf(
		"  -> ok t%d [%d,%d) rt=%d | end=min(till,now+L,tgt.end); TGT end=%d rt=%d",
		st.id, st.start, st.end, st.renewTill, t.end, t.renewTill))
	return refResult{ticket: st}
}

func (n *naiveKDC) renew(id, now int64) refResult {
	n.log = append(n.log, fmt.Sprintf("Renew(t%d,%d)", id, now))
	if id < 1 || !inTime(now) {
		return refResult{reason: ReasonInvalidParam}
	}
	if now < n.maxNow {
		return refResult{reason: ReasonClockRewind}
	}
	t, ok := n.tickets[id]
	if !ok {
		return refResult{reason: ReasonTicketNotFound}
	}
	if why := n.usable(t, now); why != "" {
		return refResult{reason: why}
	}
	if t.renewTill <= 0 {
		return refResult{reason: ReasonNotRenewable}
	}
	life := t.end - t.start
	newEnd := minI(now+life, t.renewTill)
	if newEnd <= t.end {
		return refResult{reason: ReasonRenewLimit}
	}
	t.start = now
	t.end = newEnd
	n.commit(now)
	n.log = append(n.log, fmt.Sprintf(
		"  -> ok t%d [%d,%d) | current life=%d; e'=min(now+life,renewTill=%d)",
		t.id, t.start, t.end, life, t.renewTill))
	return refResult{ticket: t}
}

func (n *naiveKDC) validate(id, now int64) refResult {
	n.log = append(n.log, fmt.Sprintf("Validate(t%d,%d)", id, now))
	if id < 1 || !inTime(now) {
		return refResult{reason: ReasonInvalidParam}
	}
	if now < n.maxNow {
		return refResult{reason: ReasonClockRewind}
	}
	t, ok := n.tickets[id]
	if !ok {
		return refResult{reason: ReasonTicketNotFound}
	}
	if !t.invalid {
		return refResult{reason: ReasonNotPostdated}
	}
	if now < t.start {
		return refResult{reason: ReasonNotYetValid}
	}
	if now >= t.end {
		return refResult{reason: ReasonExpired}
	}
	if c, ok := n.keys[t.subject]; ok && t.issued < c {
		return refResult{reason: ReasonKeyChanged}
	}
	t.invalid = false
	n.commit(now)
	n.log = append(n.log, fmt.Sprintf("  -> ok t%d validated [%d,%d)", t.id, t.start, t.end))
	return refResult{ticket: t}
}

func (n *naiveKDC) authenticate(id, authTime, now int64) refResult {
	n.log = append(n.log, fmt.Sprintf("Authenticate(t%d,%d,%d)", id, authTime, now))
	if id < 1 || !inTime(authTime) || !inTime(now) {
		return refResult{reason: ReasonInvalidParam}
	}
	if now < n.maxNow {
		return refResult{reason: ReasonClockRewind}
	}
	diff := now - authTime
	if diff < 0 {
		diff = -diff
	}
	if diff > n.s {
		n.log = append(n.log, "  -> reject clock_skew | |now-authTime|>S precedes ticket checks")
		return refResult{reason: ReasonClockSkew}
	}
	t, ok := n.tickets[id]
	if !ok {
		return refResult{reason: ReasonTicketNotFound}
	}
	if now < t.start {
		return refResult{reason: ReasonNotYetValid}
	}
	if t.invalid {
		return refResult{reason: ReasonInvalidFlag}
	}
	if now >= t.end {
		return refResult{reason: ReasonExpired}
	}
	if c, ok := n.keys[t.subject]; ok && t.issued < c {
		return refResult{reason: ReasonKeyChanged}
	}
	n.evict(now)
	e := refEntry{id, authTime}
	if _, dup := n.cache[e]; dup {
		n.log = append(n.log, "  -> reject replay | entry live while now <= authTime+S")
		return refResult{reason: ReasonReplay}
	}
	n.cache[e] = authTime + n.s
	if now > n.maxNow {
		n.maxNow = now
	}
	n.log = append(n.log, "  -> ok | replay cache written only on success")
	return refResult{}
}

func (n *naiveKDC) changeKey(subject string, now int64) refResult {
	n.log = append(n.log, fmt.Sprintf("ChangeKey(%q,%d)", subject, now))
	if subject == "" || !inTime(now) {
		return refResult{reason: ReasonInvalidParam}
	}
	if now < n.maxNow {
		return refResult{reason: ReasonClockRewind}
	}
	n.keys[subject] = now
	n.commit(now)
	n.log = append(n.log, "  -> ok | c=now; tickets with issued<c are dead")
	return refResult{}
}
