package enrollment

import (
	"fmt"
)

// refEvent is one accepted, fully specified fact. The naive model is a pure
// fold over events, written independently of the engine's helpers.
type refEvent struct {
	at      int64
	kind    string // admit | submit | approve | reject | quota | graduate
	sid     string
	major   string
	who     string
	typ     AppType
	terms   int
	effTerm int
	level   int
	subAt   int64
}

type refApp struct {
	ev      refEvent
	level   int
	levels  int
	seen    int
	who     []string
	expired bool
}

type refStudent struct {
	entry     int
	versions  []Version
	app       *refApp
	exhausted bool
}

type refModel struct {
	terms  []Term
	cfg    Config
	clock  int64
	set    bool
	quota  map[string]int
	events []refEvent
}

func newRefModel(terms []Term, cfg Config) *refModel {
	return &refModel{terms: terms, cfg: cfg, quota: map[string]int{}}
}

func (r *refModel) termAt(tick int64) (Term, bool) {
	for _, t := range r.terms {
		if tick >= t.Start && tick < t.End {
			return t, true
		}
	}
	return Term{}, false
}

// fold reconstructs all students from scratch at the given observation tick.
func (r *refModel) fold(tick int64) map[string]*refStudent {
	return r.foldOpt(tick, true)
}

func (r *refModel) foldKeepExpired(tick int64) map[string]*refStudent {
	// Raw replay ignoring expire facts and deadline pruning: used only to
	// detect a never-touched past-deadline application during decide.
	ss := map[string]*refStudent{}
	open := map[string]*refApp{}
	for _, ev := range r.events {
		if ev.at > tick {
			break
		}
		s := ss[ev.sid]
		if s == nil {
			s = &refStudent{}
			ss[ev.sid] = s
		}
		switch ev.kind {
		case "admit":
			t, _ := r.termAt(ev.at)
			s.entry = t.Index
			s.versions = []Version{{EffectiveAt: t.Start, Status: StatusEnrolled, Major: ev.major}}
		case "submit":
			a := &refApp{ev: ev, level: 0, levels: r.cfg.Levels[ev.typ], seen: 0}
			s.app = a
			open[ev.sid] = a
		case "approve":
			if a := open[ev.sid]; a != nil {
				a.seen = ev.level
				if ev.level < a.levels {
					a.level = a.seen
					continue
				}
				a.level = a.levels
				eff := r.terms[ev.effTerm].Start
				ns, _ := ev.typ.resultStatus()
				if ev.typ == AppWithdraw {
					ns = StatusWithdrawn
				}
				nm := s.versions[len(s.versions)-1].Major
				if ev.typ == AppTransfer {
					nm = ev.major
				}
				s.versions = append(s.versions, Version{EffectiveAt: eff, Status: ns, Major: nm, LeaveTerms: ev.terms})
				delete(open, ev.sid)
				s.app = nil
			}
		case "reject":
			delete(open, ev.sid)
			s.app = nil
		case "graduate":
			t, _ := r.termAt(ev.at)
			last := s.versions[len(s.versions)-1]
			s.versions = append(s.versions, Version{EffectiveAt: t.Start, Status: StatusGraduated, Major: last.Major})
			delete(open, ev.sid)
			s.app = nil
		}
	}
	return ss
}

func (r *refModel) foldOpt(tick int64, pruneExpired bool) map[string]*refStudent {
	ss := map[string]*refStudent{}
	open := map[string]*refApp{}
	for _, ev := range r.events {
		if ev.at > tick {
			break
		}
		s := ss[ev.sid]
		if s == nil {
			s = &refStudent{}
			ss[ev.sid] = s
		}
		switch ev.kind {
		case "admit":
			t, _ := r.termAt(ev.at)
			s.entry = t.Index
			s.versions = []Version{{EffectiveAt: t.Start, Status: StatusEnrolled, Major: ev.major}}
		case "submit":
			a := &refApp{ev: ev, level: 0, levels: r.cfg.Levels[ev.typ], seen: 0}
			s.app = a
			open[ev.sid] = a
		case "approve":
			a := open[ev.sid]
			if a == nil {
				continue
			}
			a.seen = ev.level
			if ev.level < a.levels {
				a.level = a.seen
				continue
			}
			a.level = a.levels
			eff := r.terms[ev.effTerm].Start
			ns, _ := ev.typ.resultStatus()
			if ev.typ == AppWithdraw {
				ns = StatusWithdrawn
			}
			nm := s.versions[len(s.versions)-1].Major
			if ev.typ == AppTransfer {
				nm = ev.major
			}
			s.versions = append(s.versions, Version{
				EffectiveAt: eff, Status: ns, Major: nm, LeaveTerms: ev.terms,
			})
			delete(open, ev.sid)
			s.app = nil
		case "reject":
			delete(open, ev.sid)
			s.app = nil
		case "graduate":
			t, _ := r.termAt(ev.at)
			last := s.versions[len(s.versions)-1]
			s.versions = append(s.versions, Version{EffectiveAt: t.Start, Status: StatusGraduated, Major: last.Major})
			delete(open, ev.sid)
			s.app = nil
		case "expire":
			if pruneExpired {
				if a, ok := open[ev.sid]; ok && a.ev.at == ev.subAt {
					delete(open, ev.sid)
					s.app = nil
				}
			}
		case "exhausted":
			s.exhausted = true
		case "withdraw_land":
			if ev.effTerm < 0 || ev.effTerm >= len(r.terms) {
				continue
			}
			eff := r.terms[ev.effTerm].Start
			last := s.versions[len(s.versions)-1]
			s.versions = append(s.versions, Version{EffectiveAt: eff, Status: StatusWithdrawn, Major: last.Major})
			s.exhausted = false
			delete(open, ev.sid)
			s.app = nil
		}
	}
	return ss
}

func (r *refModel) stateAt(s *refStudent, tick int64) Version {
	var v Version
	for _, x := range s.versions {
		if x.EffectiveAt <= tick {
			v = x
		}
	}
	return v
}

func (r *refModel) used(s *refStudent, curTerm int) int {
	span := curTerm - s.entry
	if span <= 0 {
		return 0
	}
	leave := 0
	for i, v := range s.versions {
		if v.Status != StatusSuspended && v.Status != StatusReserved {
			continue
		}
		tt, _ := r.termAt(v.EffectiveAt)
		tm := tt.Index
		n := v.LeaveTerms
		if i+1 < len(s.versions) {
			ntt, _ := r.termAt(s.versions[i+1].EffectiveAt)
			ntm := ntt.Index
			if ntm-tm < n {
				n = ntm - tm
			}
		}
		lo, hi := tm, tm+n
		if lo < s.entry {
			lo = s.entry
		}
		if hi > curTerm {
			hi = curTerm
		}
		if hi > lo {
			leave += hi - lo
		}
	}
	return span - leave
}

// pendingExhaustion reports whether the student has an unlanded year-exhaustion
// signal at the given tick.
func (r *refModel) pendingExhaustion(sid string, at int64) bool {
	pending := false
	for _, ev := range r.events {
		if ev.sid != sid || ev.at > at {
			continue
		}
		switch ev.kind {
		case "exhausted":
			pending = true
		case "withdraw_land":
			pending = false
		}
	}
	return pending
}

// landExhaustion appends the lazy withdrawal event and returns its effective
// term index.
func (r *refModel) landExhaustion(sid string, s *refStudent, at int64) {
	t, inTerm := r.termAt(at)
	eff := at
	if inTerm {
		eff = t.Start
	}
	last := r.head(s)
	if eff <= last.EffectiveAt {
		eff = last.EffectiveAt + 1
	}
	effTerm := -1
	if tt, ok := r.termAt(eff); ok {
		effTerm = tt.Index
	}
	s.versions = append(s.versions, Version{EffectiveAt: eff, Status: StatusWithdrawn, Major: last.Major})
	r.events = append(r.events, refEvent{at: at, kind: "withdraw_land", sid: sid, effTerm: effTerm})
}

func (r *refModel) leaveCount(s *refStudent, st Status) int {
	n := 0
	for i := 0; i+1 < len(s.versions); i++ {
		if s.versions[i].Status == st {
			n += s.versions[i].LeaveTerms
		}
	}
	if h := s.versions[len(s.versions)-1]; h.Status == st {
		n += h.LeaveTerms
	}
	return n
}

func (r *refModel) head(s *refStudent) Version { return s.versions[len(s.versions)-1] }

// clock/nf checks
func (r *refModel) touchClock(at int64) ErrCode {
	if at < 0 {
		return ErrInvalidArgument
	}
	if r.set && at < r.clock {
		return ErrClockRegression
	}
	return -1
}

func (r *refModel) advance(at int64) { r.set, r.clock = true, at }

func (r *refModel) effTermFor(s *refStudent, at int64, typ AppType) (int, ErrCode) {
	cur, ok := r.termAt(at)
	if !ok {
		return 0, ErrInvalidArgument
	}
	if at <= cur.SubmitDeadline {
		return cur.Index, -1
	}
	if typ == AppTransfer {
		return cur.Index, ErrDeadlinePassed
	}
	if cur.Index+1 >= len(r.terms) {
		return cur.Index, ErrDeadlinePassed
	}
	return cur.Index + 1, -1
}

func (r *refModel) admit(sid, major string, at int64) ErrCode {
	if sid == "" || major == "" {
		return ErrInvalidArgument
	}
	if c := r.touchClock(at); c >= 0 {
		return c
	}
	if _, ok := r.quota[major]; !ok {
		return ErrNotFound
	}
	if _, dup := r.fold(at)[sid]; dup {
		return ErrInvalidArgument
	}
	if _, ok := r.termAt(at); !ok {
		return ErrInvalidArgument
	}
	r.events = append(r.events, refEvent{at: at, kind: "admit", sid: sid, major: major})
	r.advance(at)
	return -1
}

func (r *refModel) submit(sid string, typ AppType, at int64, who, major string, terms int) ErrCode {
	if sid == "" || who == "" || typ < 0 || typ > AppWithdraw {
		return ErrInvalidArgument
	}
	if c := r.touchClock(at); c >= 0 {
		return c
	}
	s, ok := r.fold(at)[sid]
	if !ok {
		return ErrNotFound
	}
	cur, inTerm := r.termAt(at)
	if !inTerm {
		return ErrInvalidArgument
	}
	head := r.head(s)
	if head.Status.terminal() {
		return ErrTerminalState
	}
	if r.pendingExhaustion(sid, at) {
		r.landExhaustion(sid, s, at)
		return ErrTerminalState
	}
	if typ == AppTransfer && major != "" {
		if _, ok := r.quota[major]; !ok {
			return ErrNotFound
		}
	}
	if s.app != nil && at <= s.app.ev.at+r.cfg.AppDeadline {
		return ErrExistingApplication
	}
	if !typ.allowedFrom(head.Status) {
		return ErrStateNotAllowed
	}
	eff := cur.Index
	if at > cur.SubmitDeadline {
		if typ == AppTransfer {
			return ErrDeadlinePassed
		}
		if cur.Index+1 >= len(r.terms) {
			return ErrDeadlinePassed
		}
		eff = cur.Index + 1
	}
	if typ == AppTransfer {
		if major == "" || major == head.Major {
			return ErrInvalidArgument
		}
	}
	if (typ == AppSuspend || typ == AppReserve) && (terms <= 0 || eff+terms > len(r.terms)) {
		return ErrInvalidArgument
	}
	if typ == AppSuspend && r.leaveCount(s, StatusSuspended)+terms > r.cfg.SuspendCap {
		return ErrLimitExceeded
	}
	if typ == AppReserve && r.leaveCount(s, StatusReserved)+terms > r.cfg.ReserveCap {
		return ErrLimitExceeded
	}
	if typ == AppResume && r.used(s, eff) >= r.cfg.MaxYears {
		r.events = append(r.events, refEvent{at: at, kind: "exhausted", sid: sid})
		return ErrLimitExceeded
	}
	effTick := r.terms[eff].Start
	if effTick <= head.EffectiveAt && len(s.versions) > 1 {
		return ErrEffectiveBeforeLatest
	}
	// A clock-expired previous application is lazily closed only now that the
	// touch is accepted.
	if prior := s.app; prior != nil && at > prior.ev.at+r.cfg.AppDeadline {
		r.events = append(r.events, refEvent{at: at, kind: "expire", sid: sid, subAt: prior.ev.at})
	}
	r.events = append(r.events, refEvent{
		at: at, kind: "submit", sid: sid, who: who, typ: typ,
		major: major, terms: terms, effTerm: eff,
	})
	r.advance(at)
	return -1
}

func (r *refModel) decide(sid, who string, at int64, reject bool) ErrCode {
	if sid == "" || who == "" {
		return ErrInvalidArgument
	}
	if c := r.touchClock(at); c >= 0 {
		return c
	}
	ss := r.foldKeepExpired(at)
	s, ok := ss[sid]
	if !ok {
		return ErrNotFound
	}
	a := s.app
	if a == nil {
		if r.pendingExhaustion(sid, at) {
			r.landExhaustion(sid, s, at)
		}
		return ErrNotFound
	}
	if r.pendingExhaustion(sid, at) {
		r.landExhaustion(sid, s, at)
		return ErrTerminalState
	}
	for _, ev := range r.events {
		if ev.sid == sid && ev.kind == "expire" && ev.subAt == a.ev.at && ev.at <= at {
			return ErrNotFound
		}
	}
	if at < a.ev.at {
		return ErrClockRegression
	}
	if at > a.ev.at+r.cfg.AppDeadline {
		r.events = append(r.events, refEvent{at: at, kind: "expire", sid: sid, subAt: a.ev.at})
		return ErrDeadlinePassed
	}
	for _, x := range a.who {
		if x == who {
			return ErrNoPermission
		}
	}
	for _, ev := range r.events {
		if ev.sid == sid && ev.kind == "approve" && ev.who == who &&
			ev.subAt == a.ev.at && ev.at < at {
			return ErrNoPermission
		}
	}
	if who == a.ev.who {
		return ErrNoPermission
	}
	final := a.seen+1 >= a.levels
	effTick := r.terms[a.ev.effTerm].Start
	head := r.head(s)
	if !reject && final &&
		(effTick < head.EffectiveAt || (effTick == head.EffectiveAt && len(s.versions) > 1)) {
		return ErrEffectiveBeforeLatest
	}
	if !reject && final && a.ev.typ == AppTransfer && r.quota[a.ev.major] <= 0 {
		return ErrQuotaInsufficient
	}
	if !reject && final && a.ev.typ == AppTransfer {
		r.quota[a.ev.major]--
	}
	a.who = append(a.who, who)
	kind := "approve"
	if reject {
		kind = "reject"
	}
	if !reject {
		a.seen++
		a.level = a.seen
	}
	r.events = append(r.events, refEvent{
		at: at, kind: kind, sid: sid, who: who, typ: a.ev.typ,
		major: a.ev.major, terms: a.ev.terms, effTerm: a.ev.effTerm,
		level: a.seen, subAt: a.ev.at,
	})
	r.advance(at)
	return -1
}

func (r *refModel) graduate(sid string, at int64) ErrCode {
	if sid == "" {
		return ErrInvalidArgument
	}
	if c := r.touchClock(at); c >= 0 {
		return c
	}
	ss := r.fold(at)
	s, ok := ss[sid]
	if !ok {
		return ErrNotFound
	}
	if r.head(s).Status.terminal() {
		return ErrTerminalState
	}
	cur, ok := r.termAt(at)
	if !ok {
		return ErrInvalidArgument
	}
	if cur.Start <= r.head(s).EffectiveAt && len(s.versions) > 1 {
		return ErrEffectiveBeforeLatest
	}
	r.events = append(r.events, refEvent{at: at, kind: "graduate", sid: sid})
	r.advance(at)
	return -1
}

func (r *refModel) addQuota(major string, n int, at int64) ErrCode {
	if n <= 0 {
		return ErrInvalidArgument
	}
	if c := r.touchClock(at); c >= 0 {
		return c
	}
	if _, ok := r.quota[major]; !ok {
		return ErrNotFound
	}
	r.quota[major] += n
	r.advance(at)
	return -1
}

func (r *refModel) snapshot(sid string, tick int64) (Snapshot, bool) {
	s, ok := r.fold(tick)[sid]
	if !ok {
		return Snapshot{}, false
	}
	v := r.stateAt(s, tick)
	// A lazy withdrawal recorded at or before the query tick applies.
	landed := false
	for _, ev := range r.events {
		if ev.sid == sid && ev.kind == "withdraw_land" && ev.at <= tick &&
			ev.effTerm >= 0 && ev.effTerm < len(r.terms) {
			v = Version{EffectiveAt: r.terms[ev.effTerm].Start, Status: StatusWithdrawn, Major: v.Major}
			landed = true
		}
	}
	_ = landed
	open := false
	if s.app != nil && s.app.ev.at <= tick && tick <= s.app.ev.at+r.cfg.AppDeadline {
		open = true
	}
	return Snapshot{Status: v.Status, Major: v.Major, OpenApp: open}, true
}

func codeName(c ErrCode) string {
	if c < 0 {
		return "OK"
	}
	return c.String()
}

var _ = fmt.Sprintf
