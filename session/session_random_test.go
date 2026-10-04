package session

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type opKind int

const (
	opRate opKind = iota
	opTopUp
	opOpen
	opUpdate
	opClose
)

type testOp struct {
	kind     opKind
	now      int64
	acct     string
	sess     string
	n        int64
	rg       int
	used     int64
	want     int64
	amt      int64
	price    int64
	closeUse map[int]int64
}

type modelGrant struct {
	acct    string
	sess    string
	rg      int
	units   int64
	price   int64
	expire  int64
	expired bool
}

type modelSession struct {
	acct     string
	unbilled int64
	lastSeq  int64
	last     Reply
}

type model struct {
	rates    map[int]int64
	balances map[string]int64
	topups   map[string]int64
	charged  map[string]int64
	sessions map[string]*modelSession
	count    map[string]int
	grants   []modelGrant
	maxNow   int64
	smax     int64
	lmin     int64
	v        int64
	nmax     int
}

func TestRandomNaiveComparison(t *testing.T) {
	for iter := 0; iter < 1500; iter++ {
		rng := rand.New(rand.NewSource(int64(iter + 1)))
		smax := int64(1 + rng.Intn(25))
		lmin := int64(1 + rng.Intn(int(smax)))
		validity := int64(1 + rng.Intn(30))
		nmax := 1 + rng.Intn(3)
		e := New(smax, lmin, validity, nmax)
		m := newModel(smax, lmin, validity, nmax)
		var log strings.Builder
		fmt.Fprintf(&log, "iter=%d smax=%d lmin=%d v=%d nmax=%d\n", iter, smax, lmin, validity, nmax)

		for step := 0; step < 180; step++ {
			op := randomOp(rng, step)
			before := e.touched
			gotReply, gotErr := runEngine(e, op)
			wantReply, wantErr, expired := runModel(m, op)
			fmt.Fprintf(&log, "step=%d input=%s output_engine=(%+v,%v) output_naive=(%+v,%v)\n", step, describe(op), gotReply, gotErr, wantReply, wantErr)
			if !sameError(gotErr, wantErr) || gotReply != wantReply {
				t.Fatalf("mismatch\n%sjudgment=engine and naive differ\ngot=(%+v,%v)\nwant=(%+v,%v)", log.String(), gotReply, gotErr, wantReply, wantErr)
			}
			if gotErr == nil && op.kind == opUpdate {
				observed := int64(e.touched - before)
				if observed > expired+2 {
					t.Fatalf("judgment=touched bound violated observed=%d landed_expired=%d\n%s", observed, expired, log.String())
				}
			}
			assertModelState(t, e, m, log.String())
		}
		t.Logf("%sjudgment=reply, state, invariants and touched bounds match", log.String())
	}
}

func TestTouchedScalesWithExpiredOnly(t *testing.T) {
	for _, sessions := range []int{10, 10000} {
		name := fmt.Sprintf("%d_sessions", sessions)
		t.Run(name, func(t *testing.T) {
			e := New(1, 1, 10, sessions+1)
			mustTestOK(t, e.SetRate(1, 1, 0))
			mustTestOK(t, e.TopUp("a", int64(sessions+1), 0))
			mustTestOK(t, e.Open("target", "a", 0))
			for i := 0; i < sessions; i++ {
				sess := fmt.Sprintf("s%d", i)
				mustTestOK(t, e.Open(sess, "a", 0))
				if _, err := e.Update(sess, 1, 1, 0, 1, 0); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.Update("target", 1, 1, 0, 1, 0); err != nil {
				t.Fatal(err)
			}
			before := e.touched
			if _, err := e.Update("target", 2, 1, 1, 0, 10); err != nil {
				t.Fatal(err)
			}
			got := e.touched - before
			want := uint64(sessions + 2)
			if got != want {
				t.Fatalf("touched=%d want=%d (10 vs 10000 session control)", got, want)
			}
		})
	}
}

func newModel(smax int64, lmin int64, validity int64, nmax int) *model {
	return &model{
		rates:    make(map[int]int64),
		balances: make(map[string]int64),
		topups:   make(map[string]int64),
		charged:  make(map[string]int64),
		sessions: make(map[string]*modelSession),
		count:    make(map[string]int),
		smax:     smax,
		lmin:     lmin,
		v:        validity,
		nmax:     nmax,
	}
}

func randomOp(rng *rand.Rand, step int) testOp {
	op := testOp{
		kind:  opKind(rng.Intn(5)),
		now:   int64(step) + int64(rng.Intn(5)) - 2,
		acct:  fmt.Sprintf("a%d", rng.Intn(3)),
		sess:  fmt.Sprintf("s%d", rng.Intn(8)),
		n:     int64(1 + rng.Intn(5)),
		rg:    1 + rng.Intn(4),
		used:  int64(rng.Intn(30)),
		want:  int64(rng.Intn(30)),
		amt:   int64(1 + rng.Intn(120)),
		price: int64(1 + rng.Intn(8)),
	}
	if rng.Intn(20) == 0 {
		op.sess = ""
	}
	if rng.Intn(15) == 0 {
		op.rg = 1001
	}
	if op.kind == opClose {
		op.closeUse = make(map[int]int64)
		if rng.Intn(2) == 0 {
			op.closeUse[op.rg] = op.used
		}
		if rng.Intn(8) == 0 {
			op.closeUse = nil
		}
	}
	return op
}

func runEngine(e *Engine, op testOp) (Reply, error) {
	switch op.kind {
	case opRate:
		return Reply{}, e.SetRate(op.rg, op.price, op.now)
	case opTopUp:
		return Reply{}, e.TopUp(op.acct, op.amt, op.now)
	case opOpen:
		return Reply{}, e.Open(op.sess, op.acct, op.now)
	case opUpdate:
		return e.Update(op.sess, op.n, op.rg, op.used, op.want, op.now)
	default:
		return Reply{}, e.Close(op.sess, op.n, op.closeUse, op.now)
	}
}

func sameError(got error, want error) bool {
	if got == nil || want == nil {
		return got == want
	}
	for _, target := range []error{
		ErrInvalidArgument,
		ErrClockBack,
		ErrSessionExists,
		ErrSessionNotFound,
		ErrSessionLimit,
		ErrSequence,
		ErrNoGrant,
	} {
		g := errors.Is(got, target)
		w := errors.Is(want, target)
		if g != w {
			return false
		}
	}
	return true
}

func describe(op testOp) string {
	if op.kind == opClose {
		return fmt.Sprintf("{kind=%d now=%d acct=%s sess=%s n=%d usedByRg=%v}", op.kind, op.now, op.acct, op.sess, op.n, op.closeUse)
	}
	return fmt.Sprintf("{kind=%d now=%d acct=%s sess=%s n=%d rg=%d used=%d want=%d amount=%d price=%d}",
		op.kind, op.now, op.acct, op.sess, op.n, op.rg, op.used, op.want, op.amt, op.price)
}

func mustTestOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func runModel(m *model, op testOp) (Reply, error, int64) {
	if invalidOp(op) {
		return Reply{}, ErrInvalidArgument, 0
	}
	if op.kind == opClose && !validCloseUse(op.closeUse) {
		return Reply{}, ErrInvalidArgument, 0
	}
	if op.now < m.maxNow {
		return Reply{}, ErrClockBack, 0
	}
	switch op.kind {
	case opRate:
		m.rates[op.rg] = op.price
		m.maxNow = op.now
		return Reply{}, nil, 0
	case opTopUp:
		m.balances[op.acct] += op.amt
		m.topups[op.acct] += op.amt
		m.maxNow = op.now
		return Reply{}, nil, 0
	case opOpen:
		if _, ok := m.sessions[op.sess]; ok {
			return Reply{}, ErrSessionExists, 0
		}
		if m.count[op.acct] >= m.nmax {
			return Reply{}, ErrSessionLimit, 0
		}
		m.sessions[op.sess] = &modelSession{acct: op.acct}
		m.count[op.acct]++
		m.maxNow = op.now
		return Reply{}, nil, 0
	case opUpdate:
		return runModelUpdate(m, op)
	default:
		return runModelClose(m, op)
	}
}

func validCloseUse(usedByGroup map[int]int64) bool {
	for rg, used := range usedByGroup {
		if rg < 1 || rg > 1000 || used < 0 || used > 1_000_000_000 {
			return false
		}
	}
	return true
}

func runModelUpdate(m *model, op testOp) (Reply, error, int64) {
	s := m.sessions[op.sess]
	if s == nil {
		return Reply{}, ErrSessionNotFound, 0
	}
	if op.n == s.lastSeq {
		return s.last, nil, 0
	}
	if op.n != s.lastSeq+1 {
		return Reply{}, ErrSequence, 0
	}
	var price int64
	if op.want > 0 {
		var ok bool
		price, ok = m.rates[op.rg]
		if !ok {
			return Reply{}, ErrInvalidArgument, 0
		}
	}
	idx := m.findGrant(op.sess, op.rg)
	if idx < 0 && op.used > 0 {
		return Reply{}, ErrNoGrant, 0
	}
	expired := m.expireAccount(s.acct, op.now)
	reply := Reply{}
	if idx >= 0 {
		idx = m.findGrant(op.sess, op.rg)
		grant := m.grants[idx]
		charged := min(op.used, m.free(s.acct)/grant.price)
		amount := charged * grant.price
		reply.Charged = charged
		reply.Unbilled = op.used - charged
		m.balances[s.acct] -= amount
		m.charged[s.acct] += amount
		s.unbilled += reply.Unbilled
		m.grants = append(m.grants[:idx], m.grants[idx+1:]...)
	}
	if op.want > 0 {
		affordable := m.free(s.acct) / price
		base := min(op.want, min(m.smax, affordable))
		granted := base
		if change := affordable - base; change > 0 && change < m.lmin {
			granted = affordable
		}
		if granted > 0 {
			m.grants = append(m.grants, modelGrant{
				acct: s.acct, sess: op.sess, rg: op.rg, units: granted,
				price: price, expire: op.now + m.v,
			})
			reply.ValidUntil = op.now + m.v
		}
		reply.Granted = granted
		reply.Final = granted == affordable
		reply.Denied = granted == 0
	}
	s.lastSeq = op.n
	s.last = reply
	m.maxNow = op.now
	return reply, nil, expired
}

func runModelClose(m *model, op testOp) (Reply, error, int64) {
	s := m.sessions[op.sess]
	if s == nil {
		return Reply{}, ErrSessionNotFound, 0
	}
	if op.n == s.lastSeq {
		return Reply{}, nil, 0
	}
	if op.n != s.lastSeq+1 {
		return Reply{}, ErrSequence, 0
	}
	for rg, used := range op.closeUse {
		if rg < 1 || rg > 1000 || used < 0 || used > 1_000_000_000 {
			return Reply{}, ErrInvalidArgument, 0
		}
		if m.findGrant(op.sess, rg) < 0 {
			return Reply{}, ErrNoGrant, 0
		}
	}
	m.expireAccount(s.acct, op.now)
	groups := make(map[int]bool)
	for _, grant := range m.grants {
		if grant.sess == op.sess {
			groups[grant.rg] = true
		}
	}
	ordered := make([]int, 0, len(groups))
	for rg := range groups {
		ordered = append(ordered, rg)
	}
	sort.Ints(ordered)
	for _, rg := range ordered {
		idx := m.findGrant(op.sess, rg)
		grant := m.grants[idx]
		used := op.closeUse[rg]
		charged := min(used, m.free(s.acct)/grant.price)
		amount := charged * grant.price
		m.balances[s.acct] -= amount
		m.charged[s.acct] += amount
		s.unbilled += used - charged
		m.grants = append(m.grants[:idx], m.grants[idx+1:]...)
	}
	m.count[s.acct]--
	delete(m.sessions, op.sess)
	m.maxNow = op.now
	return Reply{}, nil, 0
}

func invalidOp(op testOp) bool {
	if op.acct == "" || !modelNow(op.now) || op.n < 1 ||
		op.used < 0 || op.used > 1_000_000_000 ||
		op.want < 0 || op.want > 1_000_000_000 ||
		op.amt < 1 || op.amt > 1_000_000_000_000 ||
		op.price < 1 || op.price > 1_000_000 {
		return true
	}
	if op.kind == opOpen || op.kind == opUpdate || op.kind == opClose {
		if op.sess == "" {
			return true
		}
	}
	if op.kind == opRate && (op.rg < 1 || op.rg > 1000) {
		return true
	}
	if op.kind == opUpdate && (op.rg < 1 || op.rg > 1000) {
		return true
	}
	return false
}

func modelNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

func (m *model) findGrant(sess string, rg int) int {
	for i := range m.grants {
		if m.grants[i].sess == sess && m.grants[i].rg == rg {
			return i
		}
	}
	return -1
}

func (m *model) expireAccount(acct string, now int64) int64 {
	var count int64
	for i := range m.grants {
		if m.grants[i].acct == acct && !m.grants[i].expired && m.grants[i].expire <= now {
			m.grants[i].expired = true
			count++
		}
	}
	return count
}

func (m *model) reserved(acct string) int64 {
	var reserved int64
	for _, grant := range m.grants {
		if grant.acct == acct && !grant.expired {
			reserved += grant.units * grant.price
		}
	}
	return reserved
}

func (m *model) free(acct string) int64 {
	return m.balances[acct] - m.reserved(acct)
}

func assertModelState(t *testing.T, e *Engine, m *model, log string) {
	t.Helper()
	accounts := make(map[string]bool)
	for acct := range m.balances {
		accounts[acct] = true
	}
	for acct := range accounts {
		balance := e.ledger.Balance(acct)
		reserved := e.ledger.Reserved(acct)
		charged := e.ledger.ChargedTotal(acct)
		if balance != m.balances[acct] {
			t.Fatalf("judgment=balance mismatch acct=%s engine=%d naive=%d\n%s", acct, balance, m.balances[acct], log)
		}
		if reserved != m.reserved(acct) {
			t.Fatalf("judgment=reserved mismatch acct=%s engine=%d naive=%d\n%s", acct, reserved, m.reserved(acct), log)
		}
		if charged != m.charged[acct] {
			t.Fatalf("judgment=charged mismatch acct=%s engine=%d naive=%d\n%s", acct, charged, m.charged[acct], log)
		}
		if balance < 0 || reserved > balance {
			t.Fatalf("judgment=balance invariant violated balance=%d reserved=%d\n%s", balance, reserved, log)
		}
		if m.topups[acct] != balance+charged {
			t.Fatalf("judgment=conservation violated topups=%d balance=%d charged=%d\n%s", m.topups[acct], balance, charged, log)
		}
	}
	if len(e.sessions) != len(m.sessions) {
		t.Fatalf("judgment=session count mismatch engine=%d naive=%d\n%s", len(e.sessions), len(m.sessions), log)
	}
	for sess, wantState := range m.sessions {
		gotState := e.sessions[sess]
		if gotState == nil {
			t.Fatalf("judgment=session missing %s\n%s", sess, log)
		}
		if gotState.unbilled != wantState.unbilled || gotState.lastSeq != wantState.lastSeq || gotState.lastReply != wantState.last {
			t.Fatalf("judgment=session state mismatch sess=%s engine=(%d,%d,%+v) naive=(%d,%d,%+v)\n%s",
				sess, gotState.unbilled, gotState.lastSeq, gotState.lastReply,
				wantState.unbilled, wantState.lastSeq, wantState.last, log)
		}
	}
}
