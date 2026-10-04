package ontology_test

// 根包差分测试：1500 组随机操作序列，与“保存全部 Move 流水逐条求和”的
// 朴素模型逐字段比对，日志打印每组的输入、输出与判定依据。

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/adjust"
	"ontology/authz"
	"ontology/count"
)

const numLocs = 4
const numUsers = 6

// oracle 保存全部被接受的 Move 流水，每次需要 book/mv 时逐条求和。
type oracle struct {
	initBook map[string]int64
	price    map[string]int64
	moves    map[string][]int64
	adj      map[string][]int64
	tabs     int64
	tpct     int64
	lim      int64
	tasks    map[string]*oTask
	busy     map[string]string
	approver map[string]bool
	senior   map[string]bool
}

type oTask struct {
	closed bool
	locs   map[string]*oLoc
}

type oLoc struct {
	phase    count.Phase
	c1, c2   int64
	m1       int64
	diff     int64
	counters []string
}

func newOracle(tabs, tpct, lim int64) *oracle {
	return &oracle{
		initBook: map[string]int64{}, price: map[string]int64{},
		moves: map[string][]int64{}, adj: map[string][]int64{},
		tasks: map[string]*oTask{}, busy: map[string]string{},
		approver: map[string]bool{}, senior: map[string]bool{},
		tabs: tabs, tpct: tpct, lim: lim,
	}
}

func (o *oracle) sum(xs []int64) int64 {
	var s int64
	for _, x := range xs {
		s += x
	}
	return s
}

func (o *oracle) book(loc string) int64 {
	return o.initBook[loc] + o.sum(o.moves[loc]) + o.sum(o.adj[loc])
}

func (o *oracle) mv(loc string) int64 { return o.sum(o.moves[loc]) }

func (o *oracle) tol(loc string) int64 {
	t := o.book(loc) * o.tpct / 100
	if o.tabs > t {
		t = o.tabs
	}
	return t
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

var errTable = map[string]error{
	"invalid":   adjust.ErrInvalid,
	"notfound":  adjust.ErrNotFound,
	"state":     adjust.ErrState,
	"conflict":  adjust.ErrConflict,
	"stock":     adjust.ErrStock,
	"rotate":    count.ErrRotate,
	"noapprove": authz.ErrNoApprove,
	"nosenior":  authz.ErrNoSenior,
}

func errName(err error) string {
	if err == nil {
		return "nil"
	}
	for n, e := range errTable {
		if errors.Is(err, e) {
			return n
		}
	}
	return err.Error()
}

func sameErr(got, want error) bool {
	if want == nil {
		return got == nil
	}
	return errors.Is(got, want)
}

func locName(i int) string  { return fmt.Sprintf("L%d", i) }
func userName(i int) string { return fmt.Sprintf("u%d", i) }

type sim struct {
	t   *testing.T
	rng *rand.Rand
	log strings.Builder
	db  *adjust.DB
	eng *count.Engine
	mgr *authz.Manager
	o   *oracle
}

func (s *sim) record(format string, args ...any) {
	fmt.Fprintf(&s.log, format+"\n", args...)
}

func (s *sim) checkErr(label string, got, want error) {
	if !sameErr(got, want) {
		s.t.Fatalf("%s: err=%s want=%s\n%s", label, errName(got), errName(want), s.log.String())
	}
	s.record("  -> %s %s", label, errName(got))
}

func (s *sim) comparePhase(tname, name string) {
	got, err := s.eng.Phase([]byte(tname), []byte(name))
	if err != nil {
		s.t.Fatal(err)
	}
	want := s.o.tasks[tname].locs[name].phase
	if got != want {
		s.t.Fatalf("phase(%s,%s)=%s want %s\n%s", tname, name, got, want, s.log.String())
	}
	book, _ := s.db.Book([]byte(name))
	if ob := s.o.book(name); book != ob {
		s.t.Fatalf("book(%s)=%d oracle=%d\n%s", name, book, ob, s.log.String())
	}
}

func TestRandomCycleDifferential(t *testing.T) {
	const groups = 1500
	for g := 0; g < groups; g++ {
		t.Run(fmt.Sprintf("seq%04d", g), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(g) + 1))
			tabs := rng.Int63n(6)
			tpct := rng.Int63n(11)
			lim := rng.Int63n(2000)
			db := adjust.New(tabs, tpct, lim)
			eng := count.New(db)
			mgr := authz.New(eng)
			oc := newOracle(tabs, tpct, lim)
			s := &sim{t: t, rng: rng, db: db, eng: eng, mgr: mgr, o: oc}

			for i := 0; i < numLocs; i++ {
				name := locName(i)
				book := rng.Int63n(300)
				price := rng.Int63n(30)
				if err := db.AddLoc([]byte(name), book, price); err != nil {
					t.Fatal(err)
				}
				oc.initBook[name] = book
				oc.price[name] = price
				oc.moves[name] = []int64{}
				oc.adj[name] = []int64{}
			}
			for u := 0; u < numUsers; u++ {
				name := userName(u)
				a := rng.Intn(3) > 0
				sen := a && rng.Intn(2) == 0
				if err := mgr.Grant([]byte(name), a, sen); err != nil {
					t.Fatal(err)
				}
				oc.approver[name] = a
				oc.senior[name] = sen
			}

			taskSeq := 0
			for k := 0; k < 100; k++ {
				switch rng.Intn(7) {
				case 0:
					s.opMove()
				case 1:
					s.opOpen(&taskSeq)
				case 2:
					s.opSubmit()
				case 3:
					s.opDecide(true)
				case 4:
					s.opDecide(false)
				case 5:
					s.opClose()
				case 6:
					s.opGrant()
				}
			}
			for i := 0; i < numLocs; i++ {
				name := locName(i)
				got, err := db.Book([]byte(name))
				if err != nil {
					t.Fatal(err)
				}
				if want := oc.book(name); got != want || got < 0 {
					t.Fatalf("invariant %s: got=%d want=%d\n%s", name, got, want, s.log.String())
				}
				mv, _ := db.Moved([]byte(name))
				if want := oc.mv(name); mv != want {
					t.Fatalf("mv(%s)=%d want=%d\n%s", name, mv, want, s.log.String())
				}
			}
			t.Logf("params tabs=%d tpct=%d lim=%d\n%s", tabs, tpct, lim, s.log.String())
		})
	}
}

func (s *sim) opMove() {
	name := locName(s.rng.Intn(numLocs))
	delta := s.rng.Int63n(121) - 60
	if delta == 0 {
		delta = 1
	}
	s.record("Move(%s,%d) book=%d tol=%d", name, delta, s.o.book(name), s.o.tol(name))
	got := s.db.Move([]byte(name), delta)
	want := s.oMove(name, delta)
	s.checkErr("Move", got, want)
	if got == nil {
		s.record("  basis: accepted book->%d mv=%d", s.o.book(name), s.o.mv(name))
	}
}

func (s *sim) oMove(name string, delta int64) error {
	b := s.o.book(name)
	if b+delta < 0 {
		return adjust.ErrStock
	}
	if b+delta > 1_000_000_000 {
		return adjust.ErrInvalid
	}
	s.o.moves[name] = append(s.o.moves[name], delta)
	return nil
}

func (s *sim) opOpen(seq *int) {
	n := 1 + s.rng.Intn(3)
	perm := s.rng.Perm(numLocs)
	var names []string
	var ids [][]byte
	for i := 0; i < n; i++ {
		names = append(names, locName(perm[i]))
		ids = append(ids, []byte(names[i]))
	}
	tname := fmt.Sprintf("task%d", *seq)
	*seq++
	s.record("Open(%s,%v)", tname, names)
	got := s.eng.Open([]byte(tname), ids)
	want := s.oOpen(tname, names)
	s.checkErr("Open", got, want)
}

func (s *sim) oOpen(tname string, names []string) error {
	if _, ok := s.o.tasks[tname]; ok {
		return count.ErrConflict
	}
	for _, n := range names {
		if _, busy := s.o.busy[n]; busy {
			return count.ErrConflict
		}
	}
	t := &oTask{locs: map[string]*oLoc{}}
	for _, n := range names {
		t.locs[n] = &oLoc{phase: count.First}
		s.o.busy[n] = tname
	}
	s.o.tasks[tname] = t
	return nil
}

func (s *sim) pickTaskLoc() (string, string, *oTask, *oLoc, bool) {
	if len(s.o.tasks) == 0 {
		return "", "", nil, nil, false
	}
	keys := make([]string, 0, len(s.o.tasks))
	for k := range s.o.tasks {
		keys = append(keys, k)
	}
	tname := keys[s.rng.Intn(len(keys))]
	t := s.o.tasks[tname]
	ln := make([]string, 0, len(t.locs))
	for k := range t.locs {
		ln = append(ln, k)
	}
	name := ln[s.rng.Intn(len(ln))]
	return tname, name, t, t.locs[name], true
}

func (s *sim) opGrant() {
	name := userName(s.rng.Intn(numUsers))
	a := s.rng.Intn(2) == 0
	sen := a && s.rng.Intn(2) == 0
	s.record("Grant(%s,approve=%v,senior=%v)", name, a, sen)
	if err := s.mgr.Grant([]byte(name), a, sen); err != nil {
		s.t.Fatal(err)
	}
	s.o.approver[name] = a
	s.o.senior[name] = sen
}

func (s *sim) opSubmit() {
	tname, name, t, l, ok := s.pickTaskLoc()
	if !ok {
		return
	}
	user := s.pickUser(l)
	book := s.o.book(name)
	var counted int64
	switch s.rng.Intn(4) {
	case 0:
		counted = book
	case 1:
		counted = book + s.o.tol(name)
	case 2:
		counted = book + s.o.tol(name) + 1 + s.rng.Int63n(25)
	default:
		if d := s.o.tol(name) + 1 + s.rng.Int63n(25); book >= d {
			counted = book - d
		} else {
			counted = 0
		}
	}
	s.record("Submit(%s,%s,%d,%s) phase=%s book=%d mv=%d tol=%d",
		tname, name, counted, user, l.phase, book, s.o.mv(name), s.o.tol(name))
	got := s.eng.Submit([]byte(tname), []byte(name), counted, []byte(user))
	want := s.oSubmit(t, tname, name, l, counted, user)
	s.checkErr("Submit", got, want)
	s.comparePhase(tname, name)
}

func (s *sim) pickUser(l *oLoc) string {
	switch l.phase {
	case count.Second:
		if s.rng.Intn(2) == 0 {
			return l.counters[0]
		}
		return userName(1 + s.rng.Intn(numUsers-1))
	case count.Third:
		if s.rng.Intn(2) == 0 {
			return l.counters[s.rng.Intn(2)]
		}
		return userName(2 + s.rng.Intn(numUsers-2))
	default:
		return userName(s.rng.Intn(numUsers))
	}
}

func (s *sim) oSubmit(t *oTask, tname, name string, l *oLoc, counted int64, user string) error {
	if t.closed {
		return count.ErrState
	}
	if l.phase == count.Pending || l.phase == count.Done {
		return count.ErrState
	}
	book := s.o.book(name)
	diff := counted - book
	within := abs64(diff) <= s.o.tol(name)
	switch l.phase {
	case count.First:
		l.counters = append(l.counters, user)
		if within {
			l.phase = count.Done
			s.o.adj[name] = append(s.o.adj[name], diff)
			s.record("  basis: First within adopt diff=%d", diff)
			return nil
		}
		l.c1, l.m1 = counted, s.o.mv(name)
		l.phase = count.Second
		s.record("  basis: First over tol c1=%d m1=%d", counted, s.o.mv(name))
		return nil
	case count.Second:
		if l.counters[0] == user {
			return count.ErrRotate
		}
		if len(l.counters) < 2 {
			l.counters = append(l.counters, user)
		} else {
			l.counters[1] = user
		}
		if within {
			l.phase = count.Done
			s.o.adj[name] = append(s.o.adj[name], diff)
			s.record("  basis: Second within adopt diff=%d", diff)
			return nil
		}
		if counted-l.c1 == s.o.mv(name)-l.m1 {
			l.diff = diff
			l.phase = count.Pending
			s.record("  basis: consistent %d==%d request diff=%d",
				counted-l.c1, s.o.mv(name)-l.m1, diff)
			return nil
		}
		l.c2 = counted
		l.phase = count.Third
		s.record("  basis: inconsistent -> Third c2=%d", counted)
		return nil
	default:
		for _, prior := range l.counters {
			if prior == user {
				return count.ErrRotate
			}
		}
		if len(l.counters) < 3 {
			l.counters = append(l.counters, user)
		} else {
			l.counters[2] = user
		}
		if within {
			l.phase = count.Done
			s.o.adj[name] = append(s.o.adj[name], diff)
			s.record("  basis: Third within adopt diff=%d", diff)
			return nil
		}
		l.diff = diff
		l.phase = count.Pending
		s.record("  basis: Third over tol request diff=%d", diff)
		return nil
	}
}

func (s *sim) opDecide(approve bool) {
	tname, name, _, l, ok := s.pickTaskLoc()
	if !ok {
		return
	}
	user := userName(s.rng.Intn(numUsers))
	label := "Reject"
	if approve {
		label = "Approve"
	}
	s.record("%s(%s,%s,%s) phase=%s", label, tname, name, user, l.phase)
	var got error
	if approve {
		got = s.mgr.Approve([]byte(tname), []byte(name), []byte(user))
	} else {
		got = s.mgr.Reject([]byte(tname), []byte(name), []byte(user))
	}
	want := s.oDecide(tname, l, name, user, approve)
	s.checkErr(label, got, want)
	s.comparePhase(tname, name)
}

func (s *sim) oDecide(tname string, l *oLoc, name, user string, approve bool) error {
	t := s.o.tasks[tname]
	if t.closed || l.phase != count.Pending {
		return authz.ErrState
	}
	if !s.o.approver[user] {
		return authz.ErrNoApprove
	}
	for _, c := range l.counters {
		if c == user {
			return authz.ErrRotate
		}
	}
	if approve && abs64(l.diff)*s.o.price[name] > s.o.lim && !s.o.senior[user] {
		return authz.ErrNoSenior
	}
	if approve {
		if s.o.book(name)+l.diff < 0 {
			return authz.ErrStock
		}
		s.o.adj[name] = append(s.o.adj[name], l.diff)
	}
	l.phase = count.Done
	l.diff = 0
	return nil
}

func (s *sim) opClose() {
	tname, _, t, _, ok := s.pickTaskLoc()
	if !ok {
		return
	}
	s.record("Close(%s)", tname)
	got := s.eng.Close([]byte(tname))
	want := s.oClose(t)
	s.checkErr("Close", got, want)
}

func (s *sim) oClose(t *oTask) error {
	if t.closed {
		return count.ErrState
	}
	for _, l := range t.locs {
		if l.phase != count.Done {
			return count.ErrState
		}
	}
	t.closed = true
	for n := range t.locs {
		delete(s.o.busy, n)
	}
	return nil
}
