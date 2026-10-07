package enrollment

import (
	"fmt"
	"sync"
)

type Config struct {
	Levels      map[AppType]int
	SuspendCap  int
	ReserveCap  int
	MaxYears    int
	AppDeadline int64
}

type SubmitInput struct {
	StudentID string
	Type      AppType
	At        int64
	Submitter string
	Major     string
	Terms     int
}

type DecisionInput struct {
	StudentID string
	Approver  string
	At        int64
	Reject    bool
}

type majorState struct {
	capacity int
	quota    int
}

type Engine struct {
	mu       sync.Mutex
	clock    int64
	clockSet bool
	cal      *Calendar
	cfg      Config
	ledger   *ledger
	students map[string]*student
	majors   map[string]*majorState
	audit    []AuditRecord
	appSeq   int
}

func (e *Engine) checkClock(at int64) error {
	if at < 0 {
		return newErr(ErrInvalidArgument, "negative tick %d", at)
	}
	if e.clockSet && at < e.clock {
		return newErr(ErrClockRegression, "tick %d before last tick %d", at, e.clock)
	}
	return nil
}

func (e *Engine) advance(at int64) {
	if !e.clockSet || at > e.clock {
		e.clock, e.clockSet = at, true
	}
}

func NewEngine(cal *Calendar, cfg Config) (*Engine, error) {
	if cal == nil {
		return nil, newErr(ErrInvalidArgument, "nil calendar")
	}
	if cfg.SuspendCap < 0 || cfg.ReserveCap < 0 || cfg.MaxYears <= 0 || cfg.AppDeadline < 0 {
		return nil, newErr(ErrInvalidArgument, "invalid config")
	}
	for t := AppType(0); t <= AppWithdraw; t++ {
		if cfg.Levels[t] < 1 {
			return nil, newErr(ErrInvalidArgument, "levels for %s must be >= 1", t)
		}
	}
	return &Engine{
		cal:      cal,
		cfg:      cfg,
		ledger:   newLedger(cal),
		students: map[string]*student{},
		majors:   map[string]*majorState{},
	}, nil
}

func (e *Engine) appendAudit(at int64, id, action, detail string) {
	e.audit = append(e.audit, AuditRecord{At: at, StudentID: id, Action: action, Detail: detail, Accepted: true})
}

// studentForTouch returns the student after lazily landing any pending
// expiry or year-exhaustion withdrawal. All work is local to that student.
func (e *Engine) studentForTouch(id string, at int64, expireApp bool) (*student, error) {
	s, ok := e.students[id]
	if !ok {
		return nil, newErr(ErrNotFound, "student %q", id)
	}
	if s.yearsExhausted {
		e.lazyWithdraw(s, at)
		s.yearsExhausted = false
	}
	return s, nil
}

// lazyWithdraw appends a terminal version effective at the current term start.
// Any open application is forcibly closed; no audit row is written because the
// landing is a side effect of (and reported by) the triggering operation.
func (e *Engine) lazyWithdraw(s *student, at int64) {
	term, ok := e.cal.termAt(at)
	eff := at
	if ok {
		eff = term.Start
	}
	if eff <= s.headVersion().EffectiveAt {
		eff = s.headVersion().EffectiveAt + 1
	}
	s.versions = append(s.versions, Version{
		EffectiveAt: eff,
		Status:      StatusWithdrawn,
		Major:       s.headVersion().Major,
	})
	if s.app != nil {
		s.app.closed, s.app.closedAt = true, at
		s.app = nil
	}
}

func (e *Engine) AddMajor(id string, capacity int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || capacity < 0 {
		return newErr(ErrInvalidArgument, "bad major %q capacity %d", id, capacity)
	}
	if _, ok := e.majors[id]; ok {
		return newErr(ErrInvalidArgument, "major %q exists", id)
	}
	e.majors[id] = &majorState{capacity: capacity, quota: capacity}
	return nil
}

func (e *Engine) AddQuota(major string, n int, at int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n <= 0 {
		return newErr(ErrInvalidArgument, "quota increment must be positive")
	}
	if err := e.checkClock(at); err != nil {
		return err
	}
	m, ok := e.majors[major]
	if !ok {
		return newErr(ErrNotFound, "major %q", major)
	}
	m.quota += n
	e.advance(at)
	return nil
}

func (e *Engine) Admit(id, major string, at int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || major == "" {
		return newErr(ErrInvalidArgument, "empty id or major")
	}
	if err := e.checkClock(at); err != nil {
		return err
	}
	if _, ok := e.students[id]; ok {
		return newErr(ErrInvalidArgument, "student %q exists", id)
	}
	if _, ok := e.majors[major]; !ok {
		return newErr(ErrNotFound, "major %q", major)
	}
	term, ok := e.cal.termAt(at)
	if !ok {
		return newErr(ErrInvalidArgument, "admission tick %d outside calendar", at)
	}
	e.students[id] = &student{
		id:        id,
		entryTerm: term.Index,
		major:     major,
		versions:  []Version{{EffectiveAt: term.Start, Status: StatusEnrolled, Major: major}},
	}
	e.advance(at)
	e.appendAudit(at, id, "admit", major)
	return nil
}

func (e *Engine) Submit(in SubmitInput) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if in.StudentID == "" || in.Submitter == "" || in.At < 0 {
		return "", newErr(ErrInvalidArgument, "missing student/submitter or bad tick")
	}
	if in.Type < 0 || in.Type > AppWithdraw {
		return "", newErr(ErrInvalidArgument, "bad application type")
	}
	if err := e.checkClock(in.At); err != nil {
		return "", err
	}
	st, err := e.studentForTouch(in.StudentID, in.At, true)
	if err != nil {
		return "", err
	}
	cur, ok := e.cal.termAt(in.At)
	if !ok {
		return "", newErr(ErrInvalidArgument, "tick %d outside calendar", in.At)
	}
	head := st.headVersion()
	prevApp := st.app
	if head.Status.terminal() {
		return "", newErr(ErrTerminalState, "student %q is %s", st.id, head.Status)
	}
	// Fixed priority per specification.
	// 1. transfer target existence (a not-found major is NotFound)
	if in.Type == AppTransfer && in.Major != "" {
		if _, ok := e.majors[in.Major]; !ok {
			return "", newErr(ErrNotFound, "target major %q", in.Major)
		}
	}
	// 2. mutual exclusion (a clock-expired app is lazily closed here)
	if st.app != nil {
		if in.At <= st.app.submittedAt+e.cfg.AppDeadline {
			return "", newErr(ErrExistingApplication, "student %q has open application %s", st.id, st.app.id)
		}
	}
	// 3. transition legality
	if !in.Type.allowedFrom(head.Status) {
		return "", newErr(ErrStateNotAllowed, "%s not allowed from %s", in.Type, head.Status)
	}
	// 4. effective term / deadline
	effTerm := cur.Index
	if in.At > cur.SubmitDeadline {
		if in.Type == AppTransfer {
			return "", newErr(ErrDeadlinePassed, "transfer after term %d deadline", cur.Index)
		}
		if _, ok := e.cal.term(cur.Index + 1); !ok {
			return "", newErr(ErrDeadlinePassed, "no next term after %d", cur.Index)
		}
		effTerm = cur.Index + 1
	}
	// 5. remaining parameter validation
	if in.Type == AppTransfer {
		if in.Major == "" || in.Major == head.Major {
			return "", newErr(ErrInvalidArgument, "transfer needs a different target major")
		}
	}
	if in.Type == AppSuspend || in.Type == AppReserve {
		if in.Terms <= 0 {
			return "", newErr(ErrInvalidArgument, "%s terms must be positive", in.Type)
		}
		if effTerm+in.Terms > e.cal.numTerms() {
			return "", newErr(ErrInvalidArgument, "leave runs past calendar")
		}
	}
	// 6. caps
	if in.Type == AppSuspend {
		total := e.ledger.suspendedTerms(st, effTerm-1) + in.Terms
		if total > e.cfg.SuspendCap {
			return "", newErr(ErrLimitExceeded, "suspend terms %d exceed cap %d", total, e.cfg.SuspendCap)
		}
	}
	if in.Type == AppReserve {
		total := e.ledger.reserveTerms(st, effTerm-1) + in.Terms
		if total > e.cfg.ReserveCap {
			return "", newErr(ErrLimitExceeded, "reserve terms %d exceed cap %d", total, e.cfg.ReserveCap)
		}
	}
	if in.Type == AppResume {
		if used := e.ledger.usedYears(st, effTerm); used >= e.cfg.MaxYears {
			// Withdrawal lands lazily on the next touch, not on this
			// rejected resume.
			st.yearsExhausted = true
			return "", newErr(ErrLimitExceeded, "used years %d reached cap %d", used, e.cfg.MaxYears)
		}
	}
	// 7. version ordering
	effTick, _ := e.cal.term(effTerm)
	if effTick.Start <= head.EffectiveAt && len(st.versions) > 1 {
		return "", newErr(ErrEffectiveBeforeLatest,
			"effective tick %d not after latest version %d", effTick.Start, head.EffectiveAt)
	}
	e.appSeq++
	app := &application{
		id:            fmt.Sprintf("app-%d", e.appSeq),
		typ:           in.Type,
		submittedAt:   in.At,
		submitter:     in.Submitter,
		targetMajor:   in.Major,
		terms:         in.Terms,
		levels:        e.cfg.Levels[in.Type],
		level:         1,
		effectiveTerm: effTerm,
	}
	st.app = app
	st.appHist = append(st.appHist, app)
	if prevApp != nil && in.At > prevApp.submittedAt+e.cfg.AppDeadline {
		prevApp.closed, prevApp.expired, prevApp.closedAt = true, true, in.At
	}
	e.advance(in.At)
	e.appendAudit(in.At, st.id, "submit:"+in.Type.String(), app.id)
	return app.id, nil
}

func (e *Engine) Decide(in DecisionInput) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if in.StudentID == "" || in.Approver == "" || in.At < 0 {
		return newErr(ErrInvalidArgument, "missing fields or bad tick")
	}
	if err := e.checkClock(in.At); err != nil {
		return err
	}
	st, err := e.studentForTouch(in.StudentID, in.At, false)
	if err != nil {
		return err
	}
	app := st.app
	if app == nil {
		return newErr(ErrNotFound, "no open application for %q", st.id)
	}
	if in.At < app.submittedAt {
		return newErr(ErrClockRegression, "decision before submission")
	}
	if in.At > app.submittedAt+e.cfg.AppDeadline {
		app.closed, app.expired, app.closedAt = true, true, in.At
		st.app = nil
		return newErr(ErrDeadlinePassed, "application %s expired", app.id)
	}
	for _, a := range app.approvers {
		if a == in.Approver {
			return newErr(ErrNoPermission, "approver %q already on application", in.Approver)
		}
	}
	if in.Approver == app.submitter {
		return newErr(ErrNoPermission, "submitter %q cannot self-approve", in.Approver)
	}
	term, _ := e.cal.term(app.effectiveTerm)
	headLen := len(st.versions)
	if !in.Reject && app.level == app.levels &&
		(term.Start < st.headVersion().EffectiveAt ||
			(term.Start == st.headVersion().EffectiveAt && headLen > 1)) {
		return newErr(ErrEffectiveBeforeLatest,
			"effective tick %d not after latest version %d", term.Start, st.headVersion().EffectiveAt)
	}
	if !in.Reject && app.level == app.levels && app.typ == AppTransfer {
		m := e.majors[app.targetMajor]
		if m.quota <= 0 {
			return newErr(ErrQuotaInsufficient, "major %q has no quota", app.targetMajor)
		}
		m.quota--
	}
	app.approvers = append(app.approvers, in.Approver)
	if in.Reject {
		app.closed, app.rejected, app.closedAt = true, true, in.At
		st.app = nil
		e.advance(in.At)
		e.appendAudit(in.At, st.id, "reject:"+app.typ.String(), app.id)
		return nil
	}
	if app.level < app.levels {
		app.level++
		e.advance(in.At)
		e.appendAudit(in.At, st.id, "approve-level", fmt.Sprintf("%s l%d", app.id, app.level-1))
		return nil
	}
	if term.Start < st.headVersion().EffectiveAt ||
		(term.Start == st.headVersion().EffectiveAt && len(st.versions) > 1) {
		return newErr(ErrEffectiveBeforeLatest,
			"effective tick %d not after latest version %d", term.Start, st.headVersion().EffectiveAt)
	}
	newStatus, _ := app.typ.resultStatus()
	if app.typ == AppWithdraw {
		newStatus = StatusWithdrawn
	}
	newMajor := st.headVersion().Major
	if app.typ == AppTransfer {
		newMajor = app.targetMajor
	}
	st.versions = append(st.versions, Version{
		EffectiveAt: term.Start,
		Status:      newStatus,
		Major:       newMajor,
		LeaveTerms:  app.terms,
		AcceptedAt:  in.At,
	})
	app.closed, app.accepted, app.closedAt = true, true, in.At
	st.app = nil
	e.advance(in.At)
	e.appendAudit(in.At, st.id, "accept:"+app.typ.String(),
		fmt.Sprintf("%s eff@%d", app.id, term.Start))
	return nil
}

func (e *Engine) Graduate(studentID string, at int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if studentID == "" {
		return newErr(ErrInvalidArgument, "empty student id")
	}
	if err := e.checkClock(at); err != nil {
		return err
	}
	st, err := e.studentForTouch(studentID, at, true)
	if err != nil {
		return err
	}
	head := st.headVersion()
	if head.Status.terminal() {
		return newErr(ErrTerminalState, "student %q is %s", st.id, head.Status)
	}
	term, ok := e.cal.termAt(at)
	if !ok {
		return newErr(ErrInvalidArgument, "tick %d outside calendar", at)
	}
	if term.Start <= head.EffectiveAt {
		return newErr(ErrEffectiveBeforeLatest, "cannot graduate before latest version")
	}
	st.versions = append(st.versions, Version{
		EffectiveAt: term.Start,
		Status:      StatusGraduated,
		Major:       head.Major,
	})
	if st.app != nil {
		st.app.closed, st.app.closedAt = true, at
		st.app = nil
	}
	e.advance(at)
	e.appendAudit(at, st.id, "graduate", "")
	return nil
}
