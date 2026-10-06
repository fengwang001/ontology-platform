package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

// 朴素参考模型：与引擎相互独立地直接重述题目规则（单线程、无惰性技巧、
// 不共享引擎的任何内部判定），用于随机操作序列对照。

type nVersion struct {
	score int
	at    int64
	src   Source
}

type nReview struct {
	opened   int64
	closedAt int64
	status   ReviewStatus
}

type nProposal struct {
	teacher  string
	score    int
	created  int64
	deadline int64
	active   bool
	decided  bool
}

type nFirst struct {
	who     string
	at      int64
	expires int64
	score   int
}

type nRec struct {
	student, course, term string
	versions              []nVersion
	reviews               []*nReview
	proposal              *nProposal
	first                 *nFirst
}

type naiveModel struct {
	cfg     Config
	records map[string]*nRec
	teacher map[string]string // term|course -> teacher
	level   map[string]int
	locked  map[string]bool
	clock   int64
	audit   []AuditEntry
}

func nKey(student, course, term string) string { return term + "|" + student + "|" + course }

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{
		cfg:     cfg,
		records: map[string]*nRec{},
		teacher: map[string]string{},
		level:   map[string]int{},
		locked:  map[string]bool{},
	}
}

func (m *naiveModel) regTeacher(term, course, who string) {
	m.teacher[term+"|"+course] = who
}

func (m *naiveModel) setLevel(who string, lvl int) { m.level[who] = lvl }

func (m *naiveModel) rec(student, course, term string) *nRec {
	return m.records[nKey(student, course, term)]
}

func (m *naiveModel) openReview(r *nRec) *nReview {
	if len(r.reviews) == 0 {
		return nil
	}
	if rv := r.reviews[len(r.reviews)-1]; rv.status == ReviewOpen {
		return rv
	}
	return nil
}

func (m *naiveModel) log(kind AuditKind, actor string, at int64, r *nRec, termID, detail string) {
	entry := AuditEntry{Seq: len(m.audit) + 1, Kind: kind, Actor: actor, At: at, Detail: detail}
	if r != nil {
		entry.StudentID, entry.CourseID, entry.TermID = r.student, r.course, r.term
	} else {
		entry.TermID = termID
	}
	m.audit = append(m.audit, entry)
}

// ---- 操作（与引擎接口一一对应；错误仅返回分类码）----

func (m *naiveModel) enter(who, student, course, term string, score int, at int64) string {
	if who == "" || student == "" || course == "" || term == "" {
		return ErrInvalid
	}
	if at < m.clock {
		return ErrClock
	}
	teacher := m.teacher[term+"|"+course]
	if teacher == "" {
		return ErrNotFound
	}
	if m.locked[term] {
		return ErrLocked
	}
	if teacher != who {
		return ErrForbidden
	}
	if m.rec(student, course, term) != nil {
		return ErrState
	}
	if score < m.cfg.MinScore || score > m.cfg.MaxScore {
		return ErrScore
	}
	r := &nRec{student: student, course: course, term: term}
	r.versions = append(r.versions, nVersion{score, at, SourceInitial})
	m.records[nKey(student, course, term)] = r
	m.clock = at
	return ""
}

// touch：朴素的惰性失效——任何变更操作触达记录时，先判断超时。
func (m *naiveModel) touch(r *nRec, at int64) {
	if p := r.proposal; p != nil && p.active && at > p.deadline {
		p.active = false
		r.proposal = nil
		if at > m.clock {
			m.clock = at
		}
		m.log(AuditExpire, "system", at, r, r.term,
			fmt.Sprintf("proposal t=%d->%d by %s expired at deadline t=%d: lazy expiry on touching record at t=%d; review back to open",
				p.created, p.score, p.teacher, p.deadline, at))
	}
}

func (m *naiveModel) apply(student, course, term string, at int64) string {
	if student == "" || course == "" || term == "" {
		return ErrInvalid
	}
	if at < m.clock {
		return ErrClock
	}
	r := m.rec(student, course, term)
	if r == nil {
		return ErrNotFound
	}
	if m.locked[term] {
		return ErrLocked
	}
	m.touch(r, at)
	start := r.versions[0].at
	if at > start+m.cfg.ReviewWindow {
		return ErrExpired
	}
	if m.openReview(r) != nil {
		return ErrState
	}
	r.reviews = append(r.reviews, &nReview{opened: at, status: ReviewOpen})
	m.clock = at
	m.log(AuditReview, student, at, r, r.term,
		fmt.Sprintf("accepted; window [%d,%d], at=%d within", start, start+m.cfg.ReviewWindow, at))
	return ""
}

func (m *naiveModel) rejectReview(who, student, course, term string, at int64) string {
	if who == "" || student == "" || course == "" || term == "" {
		return ErrInvalid
	}
	if at < m.clock {
		return ErrClock
	}
	r := m.rec(student, course, term)
	if r == nil {
		return ErrNotFound
	}
	if m.locked[term] {
		return ErrLocked
	}
	isTeacher := m.teacher[term+"|"+course] == who
	if !isTeacher && m.level[who] < m.cfg.MinApproveLvl {
		return ErrForbidden
	}
	m.touch(r, at)
	rv := m.openReview(r)
	if rv == nil {
		return ErrState
	}
	if r.proposal != nil && r.proposal.active {
		return ErrState
	}
	rv.status = ReviewRejected
	rv.closedAt = at
	m.clock = at
	m.log(AuditReviewReject, who, at, r, r.term, "review rejected; grade unchanged")
	return ""
}

func (m *naiveModel) propose(who, student, course, term string, score int, at int64) string {
	if who == "" || student == "" || course == "" || term == "" {
		return ErrInvalid
	}
	if at < m.clock {
		return ErrClock
	}
	r := m.rec(student, course, term)
	if r == nil {
		return ErrNotFound
	}
	if m.locked[term] {
		return ErrLocked
	}
	if m.teacher[term+"|"+course] != who {
		return ErrForbidden
	}
	pending := r.proposal != nil && r.proposal.active && at <= r.proposal.deadline
	if m.openReview(r) == nil {
		return ErrState
	}
	if pending {
		return ErrState
	}
	cur := r.versions[len(r.versions)-1].score
	if score < m.cfg.MinScore || score > m.cfg.MaxScore {
		return ErrScore
	}
	if absInt(score-cur) > m.cfg.MaxDelta {
		return ErrScore
	}
	m.touch(r, at)
	r.proposal = &nProposal{teacher: who, score: score, created: at, deadline: at + m.cfg.ApproveTimeout, active: true}
	m.clock = at
	m.log(AuditProposal, who, at, r, r.term,
		fmt.Sprintf("score %d->%d (|delta|<=%d); approve deadline t=%d (inclusive)",
			cur, score, m.cfg.MaxDelta, at+m.cfg.ApproveTimeout))
	return ""
}

func (m *naiveModel) approve(who, student, course, term string, at int64) string {
	if who == "" || student == "" || course == "" || term == "" {
		return ErrInvalid
	}
	if at < m.clock {
		return ErrClock
	}
	r := m.rec(student, course, term)
	if r == nil {
		return ErrNotFound
	}
	if m.locked[term] {
		return ErrLocked
	}
	p := r.proposal
	if p == nil {
		return ErrState
	}
	if who == p.teacher {
		return ErrForbidden
	}
	if m.level[who] < m.cfg.MinApproveLvl {
		return ErrForbidden
	}
	if p.active && at > p.deadline {
		m.touch(r, at)
	}
	if !r.proposal.active {
		return ErrExpired
	}
	if m.openReview(r) == nil {
		return ErrState
	}
	old := r.versions[len(r.versions)-1].score
	p.active, p.decided = false, true
	r.versions = append(r.versions, nVersion{p.score, at, SourceReview})
	rv := m.openReview(r)
	rv.status, rv.closedAt = ReviewApproved, at
	m.clock = at
	m.log(AuditApprove, who, at, r, r.term,
		fmt.Sprintf("approved; score %d->%d effective at t=%d; review closed", old, p.score, at))
	return ""
}

func (m *naiveModel) deny(who, student, course, term string, at int64) string {
	if who == "" || student == "" || course == "" || term == "" {
		return ErrInvalid
	}
	if at < m.clock {
		return ErrClock
	}
	r := m.rec(student, course, term)
	if r == nil {
		return ErrNotFound
	}
	if m.locked[term] {
		return ErrLocked
	}
	p := r.proposal
	if p == nil {
		return ErrState
	}
	if who == p.teacher {
		return ErrForbidden
	}
	if m.level[who] < m.cfg.MinApproveLvl {
		return ErrForbidden
	}
	if p.active && at > p.deadline {
		m.touch(r, at)
	}
	if r.proposal == nil || !r.proposal.active {
		return ErrExpired
	}
	r.proposal.active, r.proposal.decided = false, true
	r.proposal = nil
	m.clock = at
	m.log(AuditDeny, who, at, r, r.term, "proposal denied; review back to open; teacher may repropose")
	return ""
}

func (m *naiveModel) lock(who, term string, at int64) string {
	if who == "" || term == "" {
		return ErrInvalid
	}
	if at < m.clock {
		return ErrClock
	}
	known := false
	for k := range m.teacher {
		if len(k) > len(term) && k[:len(term)] == term && k[len(term)] == '|' {
			known = true
		}
	}
	for k, r := range m.records {
		if len(k) > len(term) && k[:len(term)] == term {
			known = true
			_ = r
		}
	}
	if !known {
		return ErrNotFound
	}
	if m.locked[term] {
		return ErrLocked
	}
	keys := make([]string, 0)
	for k, r := range m.records {
		if r.term == term {
			keys = append(keys, k)
		}
	}
	sortStrings(keys)
	voided, closed := 0, 0
	for _, k := range keys {
		r := m.records[k]
		if p := r.proposal; p != nil && p.active {
			p.active = false
			r.proposal = nil
			voided++
			m.log(AuditLockVoid, who, at, r, r.term,
				fmt.Sprintf("voided pending proposal t=%d->%d by %s at term lock", p.created, p.score, p.teacher))
		}
		if rv := m.openReview(r); rv != nil {
			rv.status, rv.closedAt = ReviewClosed, at
			closed++
			m.log(AuditLockClose, who, at, r, r.term, "open review closed at term lock")
		}
		r.first = nil
	}
	m.locked[term] = true
	m.clock = at
	m.log(AuditLock, who, at, nil, term,
		fmt.Sprintf("term locked; reviews closed=%d, proposals voided=%d; lock irreversible", closed, voided))
	return ""
}

func (m *naiveModel) specialFirst(who, student, course, term string, score int, at int64) string {
	if who == "" || student == "" || course == "" || term == "" {
		return ErrInvalid
	}
	if at < m.clock {
		return ErrClock
	}
	r := m.rec(student, course, term)
	if r == nil {
		return ErrNotFound
	}
	if !m.locked[term] {
		return ErrState
	}
	if m.level[who] < m.cfg.MinSpecialLvl {
		return ErrForbidden
	}
	if r.first != nil && at > r.first.expires {
		r.first = nil
	}
	if r.first != nil {
		return ErrState
	}
	if score < m.cfg.MinScore || score > m.cfg.MaxScore {
		return ErrScore
	}
	cur := r.versions[len(r.versions)-1].score
	r.first = &nFirst{who: who, at: at, expires: at + m.cfg.FirstValidFor, score: score}
	m.clock = at
	m.log(AuditSpecialFirst, who, at, r, r.term,
		fmt.Sprintf("first confirmation: proposed score %d (current %d, no delta cap); valid until t=%d inclusive",
			score, cur, at+m.cfg.FirstValidFor))
	return ""
}

func (m *naiveModel) specialSecond(who, student, course, term string, at int64) string {
	if who == "" || student == "" || course == "" || term == "" {
		return ErrInvalid
	}
	if at < m.clock {
		return ErrClock
	}
	r := m.rec(student, course, term)
	if r == nil {
		return ErrNotFound
	}
	if !m.locked[term] {
		return ErrState
	}
	if m.level[who] < m.cfg.MinSpecialLvl {
		return ErrForbidden
	}
	f := r.first
	if f == nil {
		return ErrState
	}
	if who == f.who {
		return ErrForbidden
	}
	if at > f.expires {
		r.first = nil
		return ErrExpired
	}
	old := r.versions[len(r.versions)-1].score
	score, firstWho, firstAt := f.score, f.who, f.at
	r.first = nil
	r.versions = append(r.versions, nVersion{score, at, SourceSpecial})
	m.clock = at
	m.log(AuditSpecialSecond, who, at, r, r.term,
		fmt.Sprintf("second confirmation; first by %s at t=%d still valid at t=%d; score %d->%d effective (special, no delta cap)",
			firstWho, firstAt, at, old, score))
	return ""
}

// effectiveAt 朴素时点视图。
func (m *naiveModel) effectiveAt(student, course, term string, at int64) (int, Source, bool, string) {
	r := m.rec(student, course, term)
	if r == nil {
		return 0, "", false, ErrNotFound
	}
	score, src := 0, Source("")
	found := false
	for i := len(r.versions) - 1; i >= 0; i-- {
		if v := r.versions[i]; v.at <= at {
			score, src, found = v.score, v.src, true
			break
		}
	}
	if !found {
		return 0, "", false, ErrNotFound
	}
	inReview := false
	for _, rv := range r.reviews {
		if rv.opened <= at && (rv.status == ReviewOpen || at < rv.closedAt) {
			inReview = true
		}
	}
	return score, src, inReview, ""
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// opKind 随机序列中的操作类型。
type opKind int

const (
	opEnter opKind = iota
	opApply
	opReject
	opPropose
	opApprove
	opDeny
	opLock
	opSpecial1
	opSpecial2
	opQuery
)

type randOp struct {
	kind  opKind
	who   string
	s     string // student
	c     string // course
	term  string
	score int
	at    int64
	qat   int64
}

// diffLogger 记录每步输入、输出与判定依据（测试日志）。
type diffLogger struct {
	t    *testing.T
	step int
}

func (l *diffLogger) logf(format string, args ...any) {
	l.step++
	l.t.Logf("[step %03d] %s", l.step, fmt.Sprintf(format, args...))
}

func TestNaiveDifferential(t *testing.T) {
	const iterations = 60
	const seqLen = 400
	for seed := int64(1); seed <= iterations; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runOneDifferential(t, seed, seqLen)
		})
	}
}

func runOneDifferential(t *testing.T, seed int64, seqLen int) {
	rng := rand.New(rand.NewSource(seed))
	cfg := Config{
		ReviewWindow:   int64(2 + rng.Intn(8)),
		MinScore:       0,
		MaxScore:       100,
		MaxDelta:       3 + rng.Intn(5),
		ApproveTimeout: int64(2 + rng.Intn(6)),
		MinApproveLvl:  2,
		MinSpecialLvl:  5,
		FirstValidFor:  int64(1 + rng.Intn(4)),
	}
	eng := NewEngine(cfg)
	nv := newNaive(cfg)

	terms := []string{"T1", "T2"}
	courses := []string{"math", "english", "physics"}
	students := []string{"s1", "s2", "s3"}
	teachers := map[string]string{} // term|course -> "te_<x>"
	approvers := []string{"boss", "low", "sup1", "sup2"}
	levels := map[string]int{"boss": 3, "low": 1, "sup1": 5, "sup2": 6, "teacher": 1}

	for _, term := range terms {
		for _, c := range courses {
			who := fmt.Sprintf("te_%s_%s", term, c)
			teachers[term+"|"+c] = who
			levels[who] = 1
			must(t, eng.RegisterTeacher(term, c, who), "eng register")
			nv.regTeacher(term, c, who)
		}
	}
	for who, lvl := range levels {
		must(t, eng.SetUserLevel(who, lvl), "eng level")
		nv.setLevel(who, lvl)
	}

	lg := &diffLogger{t: t}
	clock := int64(1)

	for i := 0; i < seqLen; i++ {
		op := randOp{
			kind: opKind(rng.Intn(int(opQuery) + 1)),
			s:    students[rng.Intn(len(students))],
			c:    courses[rng.Intn(len(courses))],
			term: terms[rng.Intn(len(terms))],
			at:   clock + int64(rng.Intn(4)),
		}
		// 少量时钟回退注入。
		if rng.Intn(10) == 0 {
			op.at = clock - 1 - int64(rng.Intn(3))
		}
		op.score = rng.Intn(cfg.MaxScore+8) - 2
		op.qat = int64(rng.Intn(int(clock) + 20))
		switch op.kind {
		case opEnter, opPropose:
			op.who = teachers[op.term+"|"+op.c]
			if rng.Intn(4) == 0 {
				op.who = "stranger"
			}
		case opReject, opApprove, opDeny:
			op.who = approvers[rng.Intn(len(approvers))]
		case opLock:
			op.who = "boss"
		case opSpecial1, opSpecial2:
			op.who = []string{"sup1", "sup2", "boss"}[rng.Intn(3)]
		default:
			op.who = op.s
		}

		engCode, nvCode, reason := dispatchCompare(t, eng, nv, op)
		if engCode != nvCode {
			dumpSnapshots(t, eng, nv, terms, students, courses)
			t.Fatalf("seed=%d step=%d op=%+v: engine code=%q naive code=%q", seed, i, op, engCode, nvCode)
		}
		lg.logf("op=%s actor=%s s=%s c=%s term=%s score=%d at=%d -> code=%q | %s",
			opName(op.kind), op.who, op.s, op.c, op.term, op.score, op.at, engCode, reason)

		if engCode == "" {
			clock = op.at
		}
		// 每步后快照等价。
		assertSnapshotsEqual(t, eng, nv, terms, students, courses, seed, int64(i))
	}

	// 重放确定性：用完全相同的操作序列无法直接重建（时钟共享），改为校验审计序号连续。
	aud := eng.Audit()
	for i, a := range aud {
		if a.Seq != i+1 {
			t.Fatalf("audit seq gap at %d: %+v", i, a)
		}
	}
}

func opName(k opKind) string {
	names := []string{"enter", "apply", "reject", "propose", "approve", "deny", "lock", "special1", "special2", "query"}
	return names[k]
}

// dispatchCompare 对同一操作分别执行引擎与朴素模型，返回 (引擎码, 朴素码, 判定依据描述)。
func dispatchCompare(t *testing.T, eng *Engine, nv *naiveModel, op randOp) (string, string, string) {
	t.Helper()
	switch op.kind {
	case opEnter:
		e1 := eng.EnterScore(op.who, op.s, op.c, op.term, op.score, op.at)
		c2 := nv.enter(op.who, op.s, op.c, op.term, op.score, op.at)
		return codeOf(e1), c2, "initial entry"
	case opApply:
		e1 := eng.ApplyReview(op.s, op.c, op.term, op.at)
		c2 := nv.apply(op.s, op.c, op.term, op.at)
		return codeOf(e1), c2, "review apply"
	case opReject:
		e1 := eng.RejectReview(op.who, op.s, op.c, op.term, op.at)
		c2 := nv.rejectReview(op.who, op.s, op.c, op.term, op.at)
		return codeOf(e1), c2, "review reject"
	case opPropose:
		e1 := eng.CreateProposal(op.who, op.s, op.c, op.term, op.score, op.at)
		c2 := nv.propose(op.who, op.s, op.c, op.term, op.score, op.at)
		return codeOf(e1), c2, "proposal create"
	case opApprove:
		e1 := eng.ApproveProposal(op.who, op.s, op.c, op.term, op.at)
		c2 := nv.approve(op.who, op.s, op.c, op.term, op.at)
		return codeOf(e1), c2, "proposal approve"
	case opDeny:
		e1 := eng.DenyProposal(op.who, op.s, op.c, op.term, op.at)
		c2 := nv.deny(op.who, op.s, op.c, op.term, op.at)
		return codeOf(e1), c2, "proposal deny"
	case opLock:
		e1 := eng.LockTerm(op.who, op.term, op.at)
		c2 := nv.lock(op.who, op.term, op.at)
		return codeOf(e1), c2, "term lock"
	case opSpecial1:
		e1 := eng.SpecialFirst(op.who, op.s, op.c, op.term, op.score, op.at)
		c2 := nv.specialFirst(op.who, op.s, op.c, op.term, op.score, op.at)
		return codeOf(e1), c2, "special first"
	case opSpecial2:
		e1 := eng.SpecialSecond(op.who, op.s, op.c, op.term, op.at)
		c2 := nv.specialSecond(op.who, op.s, op.c, op.term, op.at)
		return codeOf(e1), c2, "special second"
	case opQuery:
		v1, e1 := eng.EffectiveAt(op.s, op.c, op.term, op.qat)
		s2, src2, ir2, c2 := nv.effectiveAt(op.s, op.c, op.term, op.qat)
		if codeOf(e1) != c2 {
			return codeOf(e1), c2, "query code mismatch"
		}
		if e1 == nil && (v1.Score != s2 || v1.Source != src2 || v1.InReview != ir2) {
			return fmt.Sprintf("queryMismatch(%+v vs %d/%s/%v)", v1, s2, src2, ir2), c2, "point-in-time view mismatch"
		}
		return codeOf(e1), c2, fmt.Sprintf("query at=%d ok", op.qat)
	}
	return "", "", ""
}

func assertSnapshotsEqual(t *testing.T, eng *Engine, nv *naiveModel, terms, students, courses []string, seed, step int64) {
	t.Helper()
	for _, term := range terms {
		for _, s := range students {
			for _, c := range courses {
				r := nv.rec(s, c, term)
				if r == nil {
					continue
				}
				ev, err := eng.RecordVersions(s, c, term)
				if err != nil {
					t.Fatalf("seed=%d step=%d versions %s/%s/%s: %v", seed, step, s, c, term, err)
				}
				if len(ev) != len(r.versions) {
					t.Fatalf("seed=%d step=%d version chain len %s/%s/%s: eng=%d naive=%d", seed, step, s, c, term, len(ev), len(r.versions))
				}
				for i, v := range ev {
					nv2 := r.versions[i]
					if v.Score != nv2.score || v.Effective != nv2.at || v.Source != nv2.src {
						t.Fatalf("seed=%d step=%d version[%d] %s/%s/%s: eng=%+v naive=%+v", seed, step, i, s, c, term, v, nv2)
					}
				}
				// 复核申请链。
				erec := eng.getRecord(s, c, term)
				if len(erec.Reviews) != len(r.reviews) {
					t.Fatalf("seed=%d step=%d review len %s/%s/%s", seed, step, s, c, term)
				}
				for i, rv := range erec.Reviews {
					nr := r.reviews[i]
					if rv.OpenedAt != nr.opened || rv.ClosedAt != nr.closedAt || rv.Status != nr.status {
						t.Fatalf("seed=%d step=%d review[%d] %s/%s/%s: eng=(%d,%d,%s) naive=(%d,%d,%s)",
							seed, step, i, s, c, term, rv.OpenedAt, rv.ClosedAt, rv.Status, nr.opened, nr.closedAt, nr.status)
					}
				}
				// 待审批提案关键态。
				if (erec.Proposal == nil) != (r.proposal == nil) {
					t.Fatalf("seed=%d step=%d proposal presence %s/%s/%s: eng=%v naive=%v",
						seed, step, s, c, term, erec.Proposal, r.proposal)
				}
				if erec.Proposal != nil && r.proposal != nil {
					p1, p2 := erec.Proposal, r.proposal
					if p1.Active != p2.active || p1.Score != p2.score || p1.Deadline != p2.deadline || p1.TeacherID != p2.teacher {
						t.Fatalf("seed=%d step=%d proposal %s/%s/%s mismatch", seed, step, s, c, term)
					}
				}
				if (erec.First == nil) != (r.first == nil) {
					t.Fatalf("seed=%d step=%d first presence mismatch", seed, step)
				}
				if erec.First != nil && r.first != nil {
					if erec.First.ApproverID != r.first.who || erec.First.ExpiresAt != r.first.expires || erec.FirstScore != r.first.score {
						t.Fatalf("seed=%d step=%d first mismatch", seed, step)
					}
				}
			}
		}
		if eng.IsLocked(term) != nv.locked[term] {
			t.Fatalf("seed=%d step=%d lock %s mismatch", seed, step, term)
		}
	}
	// 审计等价（序号、类型、操作人、时刻、定位键；Detail 在固定场景已逐一同源生成）。
	a1, a2 := eng.Audit(), append([]AuditEntry(nil), nv.audit...)
	if len(a1) != len(a2) {
		t.Fatalf("seed=%d step=%d audit length: eng=%d naive=%d", seed, step, len(a1), len(a2))
	}
	for i := range a1 {
		x, y := a1[i], a2[i]
		if x.Detail != y.Detail || x.Seq != y.Seq || x.Kind != y.Kind || x.Actor != y.Actor || x.At != y.At ||
			x.StudentID != y.StudentID || x.CourseID != y.CourseID || x.TermID != y.TermID {
			t.Fatalf("seed=%d step=%d audit[%d]:\n eng =%+v\n naive=%+v", seed, step, i, x, y)
		}
	}
	if eng.Clock() != nv.clock {
		t.Fatalf("seed=%d step=%d clock: eng=%d naive=%d", seed, step, eng.Clock(), nv.clock)
	}
}

func dumpSnapshots(t *testing.T, eng *Engine, nv *naiveModel, terms, students, courses []string) {
	t.Helper()
	t.Logf("--- engine audit (%d) ---", len(eng.Audit()))
	for _, a := range eng.Audit() {
		t.Logf("  %+v", a)
	}
	t.Logf("--- naive audit (%d) ---", len(nv.audit))
	for _, a := range nv.audit {
		t.Logf("  %+v", a)
	}
}
