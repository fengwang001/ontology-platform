package restart

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// The sim below is an independent, deliberately naive implementation of the
// same specification: linear scans, deep-copy rollback on rejection. The
// randomized differential test replays identical operation sequences against
// both and compares labels, errors, and the journal step by step.

const (
	simNormal = iota
	simOffline
	simRecovering
)

type simOwner struct {
	stale       bool
	deadline    uint64
	placeholder bool
}

type simFree struct {
	label int
	at    uint64
}

type simExp struct {
	deadline uint64
	fec      string
	client   int
}

type sim struct {
	lo, hi    int
	hd, r     uint64
	q         int
	maxNow    uint64
	label2fec map[int]string
	fec2label map[string]int
	owners    map[string]map[int]simOwner
	cliFecs   map[int]map[string]bool
	states    map[int]int
	iso       map[int]uint64
	lastFree  map[string]simFree
	log       []LogEvent
}

func newSim(lo, hi int, hd, r uint64, q int) *sim {
	return &sim{
		lo: lo, hi: hi, hd: hd, r: r, q: q,
		label2fec: make(map[int]string),
		fec2label: make(map[string]int),
		owners:    make(map[string]map[int]simOwner),
		cliFecs:   make(map[int]map[string]bool),
		states:    make(map[int]int),
		iso:       make(map[int]uint64),
		lastFree:  make(map[string]simFree),
	}
}

func (s *sim) clone() *sim {
	c := newSim(s.lo, s.hi, s.hd, s.r, s.q)
	c.maxNow = s.maxNow
	for k, v := range s.label2fec {
		c.label2fec[k] = v
	}
	for k, v := range s.fec2label {
		c.fec2label[k] = v
	}
	for fec, owners := range s.owners {
		m := make(map[int]simOwner, len(owners))
		for k, v := range owners {
			m[k] = v
		}
		c.owners[fec] = m
	}
	for k, v := range s.cliFecs {
		m := make(map[string]bool, len(v))
		for f := range v {
			m[f] = true
		}
		c.cliFecs[k] = m
	}
	for k, v := range s.states {
		c.states[k] = v
	}
	for k, v := range s.iso {
		c.iso[k] = v
	}
	for k, v := range s.lastFree {
		c.lastFree[k] = v
	}
	c.log = append([]LogEvent(nil), s.log...)
	return c
}

func (s *sim) addCliFec(client int, fec string) {
	m := s.cliFecs[client]
	if m == nil {
		m = make(map[string]bool)
		s.cliFecs[client] = m
	}
	m[fec] = true
}

func (s *sim) removeOwner(fec string, client int, freeTime uint64) {
	owners := s.owners[fec]
	o, ok := owners[client]
	if !ok {
		return
	}
	delete(owners, client)
	if !o.placeholder {
		delete(s.cliFecs[client], fec)
		if len(s.cliFecs[client]) == 0 {
			delete(s.cliFecs, client)
		}
	}
	if len(owners) == 0 {
		label := s.fec2label[fec]
		delete(s.owners, fec)
		delete(s.fec2label, fec)
		delete(s.label2fec, label)
		s.iso[label] = freeTime + s.hd
		s.lastFree[fec] = simFree{label: label, at: freeTime}
		s.log = append(s.log, LogEvent{Op: LogFree, Fec: fec, Label: label, Time: freeTime})
	}
}

func (s *sim) sweep(now uint64) {
	var list []simExp
	for fec, owners := range s.owners {
		for client, o := range owners {
			if (o.stale || o.placeholder) && o.deadline <= now {
				list = append(list, simExp{deadline: o.deadline, fec: fec, client: client})
			}
		}
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.deadline != b.deadline {
			return a.deadline < b.deadline
		}
		if a.fec != b.fec {
			return a.fec < b.fec
		}
		return a.client < b.client
	})
	for _, e := range list {
		s.removeOwner(e.fec, e.client, e.deadline)
	}
}

func simValidFecClient(fec string, client int, now uint64) bool {
	return len(fec) >= 1 && len(fec) <= 64 && client >= 1 && client <= 10000 && now <= 1000000000000
}

func (s *sim) Bind(fec string, client int, now uint64) (int, error) {
	if !simValidFecClient(fec, client, now) {
		return 0, ErrInvalidParam
	}
	if now < s.maxNow {
		return 0, ErrClockBackwards
	}
	if s.states[client] == simOffline {
		return 0, ErrClientOffline
	}
	backup := s.clone()
	s.sweep(now)
	label, err := s.bindInner(fec, client, now)
	if err != nil {
		*s = *backup
		return 0, err
	}
	s.maxNow = now
	return label, nil
}

func (s *sim) bindInner(fec string, client int, now uint64) (int, error) {
	if label, ok := s.fec2label[fec]; ok {
		owners := s.owners[fec]
		if o, ok := owners[client]; ok {
			if o.stale {
				o.stale = false
				o.deadline = 0
				owners[client] = o
			}
			return label, nil
		}
		if len(s.cliFecs[client]) >= s.q {
			return 0, ErrOwnerLimit
		}
		owners[client] = simOwner{}
		s.addCliFec(client, fec)
		delete(owners, 0) // claim the placeholder if present
		return label, nil
	}
	if len(s.cliFecs[client]) >= s.q {
		return 0, ErrOwnerLimit
	}
	var label int
	if fr, ok := s.lastFree[fec]; ok && now < fr.at+s.hd {
		label = fr.label
		delete(s.iso, label)
	} else {
		label = -1
		for l := s.lo; l <= s.hi; l++ {
			if _, taken := s.label2fec[l]; taken {
				continue
			}
			if until, ok := s.iso[l]; ok && until > now {
				continue
			}
			delete(s.iso, l)
			label = l
			break
		}
		if label < 0 {
			return 0, ErrNoLabel
		}
	}
	s.label2fec[label] = fec
	s.fec2label[fec] = label
	s.owners[fec] = map[int]simOwner{client: {}}
	s.addCliFec(client, fec)
	s.log = append(s.log, LogEvent{Op: LogAlloc, Fec: fec, Label: label, Time: now})
	return label, nil
}

func (s *sim) Unbind(fec string, client int, now uint64) error {
	if !simValidFecClient(fec, client, now) {
		return ErrInvalidParam
	}
	if now < s.maxNow {
		return ErrClockBackwards
	}
	if s.states[client] == simOffline {
		return ErrClientOffline
	}
	backup := s.clone()
	s.sweep(now)
	owners, ok := s.owners[fec]
	if _, ok2 := owners[client]; !ok || !ok2 {
		*s = *backup
		return ErrBindingNotFound
	}
	s.removeOwner(fec, client, now)
	s.maxNow = now
	return nil
}

func (s *sim) ClientDown(client int, now uint64) error {
	if client < 1 || client > 10000 || now > 1000000000000 {
		return ErrInvalidParam
	}
	if now < s.maxNow {
		return ErrClockBackwards
	}
	if s.states[client] == simOffline {
		return ErrStateMismatch
	}
	s.sweep(now)
	for fec := range s.cliFecs[client] {
		o := s.owners[fec][client]
		if !o.stale {
			o.stale = true
			o.deadline = now + s.r
			s.owners[fec][client] = o
		}
	}
	s.states[client] = simOffline
	s.maxNow = now
	return nil
}

func (s *sim) ClientUp(client int, now uint64) error {
	if client < 1 || client > 10000 || now > 1000000000000 {
		return ErrInvalidParam
	}
	if now < s.maxNow {
		return ErrClockBackwards
	}
	if s.states[client] != simOffline {
		return ErrStateMismatch
	}
	s.sweep(now)
	s.states[client] = simRecovering
	s.maxNow = now
	return nil
}

func (s *sim) EndOfRib(client int, now uint64) error {
	if client < 1 || client > 10000 || now > 1000000000000 {
		return ErrInvalidParam
	}
	if now < s.maxNow {
		return ErrClockBackwards
	}
	switch s.states[client] {
	case simOffline:
		return ErrClientOffline
	case simRecovering:
	default:
		return ErrStateMismatch
	}
	s.sweep(now)
	var fecs []string
	for fec := range s.cliFecs[client] {
		if s.owners[fec][client].stale {
			fecs = append(fecs, fec)
		}
	}
	sort.Strings(fecs)
	for _, fec := range fecs {
		s.removeOwner(fec, client, now)
	}
	s.states[client] = simNormal
	s.maxNow = now
	return nil
}

func (s *sim) Restore(log []LogEvent, now uint64) error {
	if now > 1000000000000 {
		return ErrInvalidParam
	}
	fresh := newSim(s.lo, s.hi, s.hd, s.r, s.q)
	var maxLog uint64
	for i, ev := range log {
		if ev.Op != LogAlloc && ev.Op != LogFree ||
			len(ev.Fec) == 0 || len(ev.Fec) > 64 ||
			ev.Label < s.lo || ev.Label > s.hi || ev.Time > 1000000000000 {
			return fmt.Errorf("%w: log entry %d", ErrInvalidParam, i)
		}
		if ev.Time > maxLog {
			maxLog = ev.Time
		}
		switch ev.Op {
		case LogAlloc:
			if _, ok := fresh.fec2label[ev.Fec]; ok {
				return fmt.Errorf("%w: log entry %d: fec already bound", ErrInvalidParam, i)
			}
			if _, taken := fresh.label2fec[ev.Label]; taken {
				return fmt.Errorf("%w: log entry %d: label unavailable", ErrInvalidParam, i)
			}
			fresh.fec2label[ev.Fec] = ev.Label
			fresh.label2fec[ev.Label] = ev.Fec
			fresh.owners[ev.Fec] = make(map[int]simOwner)
			delete(fresh.iso, ev.Label)
		case LogFree:
			label, ok := fresh.fec2label[ev.Fec]
			if !ok || label != ev.Label {
				return fmt.Errorf("%w: log entry %d: fec not bound to label", ErrInvalidParam, i)
			}
			delete(fresh.fec2label, ev.Fec)
			delete(fresh.label2fec, ev.Label)
			delete(fresh.owners, ev.Fec)
			fresh.iso[ev.Label] = ev.Time + s.hd
			fresh.lastFree[ev.Fec] = simFree{label: ev.Label, at: ev.Time}
		}
	}
	if now < maxLog || now < s.maxNow {
		return ErrClockBackwards
	}
	for fec := range fresh.fec2label {
		fresh.owners[fec] = map[int]simOwner{0: {placeholder: true, deadline: now + s.r}}
	}
	fresh.log = append([]LogEvent(nil), log...)
	fresh.maxNow = now
	*s = *fresh
	return nil
}

func (s *sim) bindings() map[string]int {
	out := make(map[string]int, len(s.fec2label))
	for fec, label := range s.fec2label {
		out[fec] = label
	}
	return out
}

// --- shared test helpers ---

func mustNew(t *testing.T, lo, hi int, hd, r uint64, q int) *Manager {
	t.Helper()
	m, err := New(lo, hi, hd, r, q)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func mustBind(t *testing.T, m *Manager, fec string, client int, now uint64, want int) {
	t.Helper()
	got, err := m.Bind(fec, client, now)
	if err != nil || got != want {
		t.Fatalf("Bind(%s,%d,%d) = %d,%v, want %d,nil", fec, client, now, got, err, want)
	}
}

func mustUnbind(t *testing.T, m *Manager, fec string, client int, now uint64) {
	t.Helper()
	if err := m.Unbind(fec, client, now); err != nil {
		t.Fatalf("Unbind(%s,%d,%d): %v", fec, client, now, err)
	}
}

func mustOK(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func mustErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s = %v, want %v", what, err, want)
	}
}

func hasEvent(log []LogEvent, op LogOp, fec string, label int, at uint64) bool {
	for _, ev := range log {
		if ev.Op == op && ev.Fec == fec && ev.Label == label && ev.Time == at {
			return true
		}
	}
	return false
}

// setupSpecExample builds the state from the specification's worked example
// at t=20: [100,103], Hd=50, R=30, Q=3; f1={A,B}@100, f3={A}@102, f2 freed
// at t=10 (101 isolated until 60).
func setupSpecExample(t *testing.T) *Manager {
	t.Helper()
	const A, B = 1, 2
	m := mustNew(t, 100, 103, 50, 30, 3)
	mustBind(t, m, "f1", A, 0, 100)
	mustBind(t, m, "f2", A, 0, 101)
	mustBind(t, m, "f1", B, 0, 100)
	mustUnbind(t, m, "f2", A, 10)
	if !hasEvent(m.Log(), LogFree, "f2", 101, 10) {
		t.Fatalf("log missing FREE(f2,101,10): %v", m.Log())
	}
	mustBind(t, m, "f3", A, 20, 102)
	return m
}

func TestSpecExampleHolddownAndAffinity(t *testing.T) {
	const B = 2
	t.Run("affinity retake inside holddown", func(t *testing.T) {
		m := setupSpecExample(t)
		mustBind(t, m, "f2", B, 30, 101)
	})
	t.Run("one millisecond before holddown end", func(t *testing.T) {
		m := setupSpecExample(t)
		mustBind(t, m, "f4", B, 59, 103) // 101 still isolated, affinity lost? no: 59<60 so affinity applies only to f2
	})
	t.Run("exactly at holddown end", func(t *testing.T) {
		m := setupSpecExample(t)
		mustBind(t, m, "f4", B, 60, 101) // isolation over, 101 is the minimum
	})
	t.Run("affinity expires exactly at holddown end", func(t *testing.T) {
		m := setupSpecExample(t)
		// f2's own affinity is invalid at 60 too, but 101 is the minimum
		// available label anyway.
		mustBind(t, m, "f2", B, 60, 101)
	})
	t.Run("owner limit precedes label exhaustion", func(t *testing.T) {
		const A = 1
		m := setupSpecExample(t)
		mustBind(t, m, "f5", A, 21, 103) // A now owns f1,f3,f5
		_, err := m.Bind("f6", A, 22)
		mustErr(t, "Bind f6", err, ErrOwnerLimit)
	})
}

func TestSpecExampleClientRestart(t *testing.T) {
	const A, B = 1, 2
	t.Run("refresh then endofrib frees at now", func(t *testing.T) {
		m := setupSpecExample(t)
		mustOK(t, "down", m.ClientDown(A, 40)) // f1,f3 stale until 70
		mustOK(t, "up", m.ClientUp(A, 45))
		mustBind(t, m, "f1", A, 45, 100) // refresh
		mustOK(t, "eor", m.EndOfRib(A, 50))
		if !hasEvent(m.Log(), LogFree, "f3", 102, 50) {
			t.Fatalf("log missing FREE(f3,102,50): %v", m.Log())
		}
		// 102 isolated until 100.
		const C = 3
		mustBind(t, m, "f4", C, 50, 103)
		mustBind(t, m, "f5", C, 60, 101)
		if _, err := m.Bind("f6", C, 99); !errors.Is(err, ErrNoLabel) {
			t.Fatalf("Bind f6 at 99 = %v, want ErrNoLabel", err)
		}
		mustBind(t, m, "f6", C, 100, 102)
	})
	t.Run("expiry lands at deadline not at now", func(t *testing.T) {
		m := setupSpecExample(t)
		mustOK(t, "down", m.ClientDown(A, 40))
		mustOK(t, "up", m.ClientUp(A, 45))
		mustBind(t, m, "f1", A, 45, 100)
		// No EndOfRib: the first accepted op at/after 70 lands the expiry.
		mustBind(t, m, "f1", B, 80, 100) // idempotent, triggers the sweep
		if !hasEvent(m.Log(), LogFree, "f3", 102, 70) {
			t.Fatalf("log missing FREE(f3,102,70): %v", m.Log())
		}
		// 102 isolated until 120.
		const C = 3
		mustBind(t, m, "f4", C, 80, 101)
		mustBind(t, m, "f5", C, 80, 103)
		if _, err := m.Bind("f6", C, 119); !errors.Is(err, ErrNoLabel) {
			t.Fatalf("Bind f6 at 119 = %v, want ErrNoLabel", err)
		}
		mustBind(t, m, "f6", C, 120, 102)
	})
	t.Run("unrefreshed shared binding survives", func(t *testing.T) {
		m := setupSpecExample(t)
		mustOK(t, "down", m.ClientDown(A, 40))
		mustOK(t, "up", m.ClientUp(A, 45))
		mustOK(t, "eor", m.EndOfRib(A, 50)) // drops f1(A) and f3(A)
		if got := m.Bindings()["f1"]; got != 100 {
			t.Fatalf("f1 binding = %d, want 100 (B remains)", got)
		}
		if hasEvent(m.Log(), LogFree, "f1", 100, 50) {
			t.Fatalf("f1 must not be freed: %v", m.Log())
		}
		if !hasEvent(m.Log(), LogFree, "f3", 102, 50) {
			t.Fatalf("log missing FREE(f3,102,50): %v", m.Log())
		}
	})
	t.Run("second down does not extend old deadline", func(t *testing.T) {
		m := setupSpecExample(t)
		mustOK(t, "down", m.ClientDown(A, 40)) // f1,f3 stale until 70
		mustOK(t, "up", m.ClientUp(A, 45))
		mustBind(t, m, "f1", A, 45, 100)        // refresh f1
		mustOK(t, "down2", m.ClientDown(A, 60)) // f1 stale until 90, f3 still 70
		mustBind(t, m, "f4", B, 75, 101)        // lands FREE(f3,102,70) only
		if !hasEvent(m.Log(), LogFree, "f3", 102, 70) {
			t.Fatalf("log missing FREE(f3,102,70): %v", m.Log())
		}
		if hasEvent(m.Log(), LogFree, "f1", 100, 70) {
			t.Fatalf("f1 must stay stale until 90: %v", m.Log())
		}
		mustBind(t, m, "f5", B, 95, 103) // lands f1's A-owner removal at 90; B keeps 100
		if hasEvent(m.Log(), LogFree, "f1", 100, 90) {
			t.Fatalf("f1 must not be freed, B still owns it: %v", m.Log())
		}
		if got := m.Bindings()["f1"]; got != 100 {
			t.Fatalf("f1 binding = %d, want 100", got)
		}
	})
	t.Run("expiry lands while client stays offline", func(t *testing.T) {
		m := setupSpecExample(t)
		mustOK(t, "down", m.ClientDown(A, 40))
		// A never comes back; an unrelated accepted op lands the expiries.
		mustBind(t, m, "f4", B, 75, 101)
		if !hasEvent(m.Log(), LogFree, "f3", 102, 70) {
			t.Fatalf("log missing FREE(f3,102,70): %v", m.Log())
		}
		if got := m.Bindings()["f1"]; got != 100 {
			t.Fatalf("f1 binding = %d, want 100 (B remains)", got)
		}
	})
}

// setupRestoreExample builds the state of the specification's restore
// example at t=80: f1={A,B}@100 survives; f2 freed at 10; f3 freed at 50.
func setupRestoreExample(t *testing.T) *Manager {
	t.Helper()
	const A = 1
	m := setupSpecExample(t)
	mustOK(t, "down", m.ClientDown(A, 40))
	mustOK(t, "up", m.ClientUp(A, 45))
	mustBind(t, m, "f1", A, 45, 100)
	mustOK(t, "eor", m.EndOfRib(A, 50))
	mustOK(t, "restore", m.Restore(m.Log(), 80))
	if got := m.Bindings(); len(got) != 1 || got["f1"] != 100 {
		t.Fatalf("bindings after restore = %v, want f1:100", got)
	}
	return m
}

func TestRestorePlaceholder(t *testing.T) {
	const B = 2
	t.Run("claim before placeholder deadline", func(t *testing.T) {
		m := setupRestoreExample(t)
		mustBind(t, m, "f1", B, 90, 100) // claim: placeholder removed
		mustBind(t, m, "f9", B, 120, 101)
		if hasEvent(m.Log(), LogFree, "f1", 100, 110) {
			t.Fatalf("claimed binding must survive its placeholder deadline: %v", m.Log())
		}
	})
	t.Run("unclaimed placeholder expires at deadline", func(t *testing.T) {
		m := setupRestoreExample(t)
		mustBind(t, m, "f9", B, 111, 101) // lands FREE(f1,100,110)
		if !hasEvent(m.Log(), LogFree, "f1", 100, 110) {
			t.Fatalf("log missing FREE(f1,100,110): %v", m.Log())
		}
		if _, ok := m.Bindings()["f1"]; ok {
			t.Fatal("f1 should be released")
		}
	})
	t.Run("placeholder alive one millisecond before deadline", func(t *testing.T) {
		m := setupRestoreExample(t)
		mustBind(t, m, "f9", B, 109, 101)
		if hasEvent(m.Log(), LogFree, "f1", 100, 110) {
			t.Fatalf("placeholder expired early: %v", m.Log())
		}
		if got := m.Bindings()["f1"]; got != 100 {
			t.Fatalf("f1 binding = %d, want 100", got)
		}
	})
	t.Run("claim counts toward owner limit", func(t *testing.T) {
		m := mustNew(t, 100, 101, 50, 30, 1)
		mustBind(t, m, "f1", 1, 0, 100)
		mustBind(t, m, "f2", 2, 0, 101)
		mustOK(t, "restore", m.Restore(m.Log(), 10))
		mustBind(t, m, "f1", 3, 20, 100) // claim, client 3 now at Q=1
		_, err := m.Bind("f2", 3, 20)
		mustErr(t, "second claim", err, ErrOwnerLimit)
	})
	t.Run("restore rejects bad log and early clock", func(t *testing.T) {
		m := setupSpecExample(t)
		log := m.Log()
		mustErr(t, "early now", m.Restore(log, 5), ErrClockBackwards)
		bad := append([]LogEvent(nil), log...)
		bad = append(bad, LogEvent{Op: LogAlloc, Fec: "f3", Label: 100, Time: 30})
		mustErr(t, "double alloc", m.Restore(bad, 80), ErrInvalidParam)
		bad2 := append([]LogEvent(nil), log...)
		bad2 = append(bad2, LogEvent{Op: LogFree, Fec: "nope", Label: 100, Time: 30})
		mustErr(t, "free of unbound", m.Restore(bad2, 80), ErrInvalidParam)
		// Rejected restores leave the state untouched.
		if got := m.Bindings()["f1"]; got != 100 {
			t.Fatalf("state changed by rejected restore: %v", m.Bindings())
		}
	})
}

func TestBoundaryConditions(t *testing.T) {
	const A, B = 1, 2
	t.Run("stale deadline exactly equal removes", func(t *testing.T) {
		m := mustNew(t, 100, 103, 50, 30, 3)
		mustBind(t, m, "f1", A, 0, 100)
		mustOK(t, "down", m.ClientDown(A, 40)) // stale until 70
		mustBind(t, m, "f2", B, 69, 101)       // too early: nothing lands
		if hasEvent(m.Log(), LogFree, "f1", 100, 70) {
			t.Fatal("stale ownership removed before its deadline")
		}
		mustBind(t, m, "f3", B, 70, 102) // lands FREE(f1,100,70)
		if !hasEvent(m.Log(), LogFree, "f1", 100, 70) {
			t.Fatalf("log missing FREE(f1,100,70): %v", m.Log())
		}
	})
	t.Run("shared binding keeps label until last owner", func(t *testing.T) {
		m := mustNew(t, 100, 103, 50, 30, 3)
		mustBind(t, m, "f1", A, 0, 100)
		mustBind(t, m, "f1", B, 0, 100)
		mustUnbind(t, m, "f1", A, 5)
		if n := len(m.Log()); n != 1 {
			t.Fatalf("log has %d entries, want 1 (no FREE yet)", n)
		}
		if got := m.Bindings()["f1"]; got != 100 {
			t.Fatalf("f1 binding = %d, want 100", got)
		}
		mustUnbind(t, m, "f1", B, 6)
		if !hasEvent(m.Log(), LogFree, "f1", 100, 6) {
			t.Fatalf("log missing FREE(f1,100,6): %v", m.Log())
		}
	})
	t.Run("minimum available label wins", func(t *testing.T) {
		m := mustNew(t, 100, 103, 0, 30, 3)
		mustBind(t, m, "f1", A, 0, 100)
		mustBind(t, m, "f2", A, 0, 101)
		mustBind(t, m, "f3", A, 0, 102)
		mustUnbind(t, m, "f2", A, 10) // hd=0: 101 immediately available
		mustBind(t, m, "f4", A, 10, 101)
	})
	t.Run("unbind of missing owner and fec", func(t *testing.T) {
		m := mustNew(t, 100, 103, 50, 30, 3)
		mustBind(t, m, "f1", A, 0, 100)
		mustErr(t, "not owner", m.Unbind("f1", B, 1), ErrBindingNotFound)
		mustErr(t, "no binding", m.Unbind("f9", A, 1), ErrBindingNotFound)
	})
	t.Run("client state machine errors", func(t *testing.T) {
		m := mustNew(t, 100, 103, 50, 30, 3)
		mustErr(t, "up while normal", m.ClientUp(A, 0), ErrStateMismatch)
		mustErr(t, "eor while normal", m.EndOfRib(A, 0), ErrStateMismatch)
		mustOK(t, "down", m.ClientDown(A, 10))
		mustErr(t, "down while offline", m.ClientDown(A, 11), ErrStateMismatch)
		mustErr(t, "bind while offline", func() error {
			_, err := m.Bind("f1", A, 11)
			return err
		}(), ErrClientOffline)
		mustErr(t, "unbind while offline", m.Unbind("f1", A, 11), ErrClientOffline)
		mustErr(t, "eor while offline", m.EndOfRib(A, 11), ErrClientOffline)
		mustOK(t, "up", m.ClientUp(A, 12))
		mustErr(t, "up while recovering", m.ClientUp(A, 13), ErrStateMismatch)
		mustOK(t, "down while recovering", m.ClientDown(A, 14))
		mustOK(t, "up again", m.ClientUp(A, 15))
		mustOK(t, "eor", m.EndOfRib(A, 16))
	})
}

func TestRejectionOrder(t *testing.T) {
	const A, B = 1, 2
	bindErr := func(m *Manager, fec string, client int, now uint64) error {
		_, err := m.Bind(fec, client, now)
		return err
	}
	t.Run("bind precedence", func(t *testing.T) {
		m := mustNew(t, 100, 101, 50, 30, 1)
		mustBind(t, m, "f1", A, 10, 100)
		mustOK(t, "down", m.ClientDown(B, 20))
		// Invalid parameter beats clock-backwards and client-offline.
		mustErr(t, "invalid first", bindErr(m, "", B, 5), ErrInvalidParam)
		mustErr(t, "invalid fec length", bindErr(m, string(make([]byte, 65)), A, 5), ErrInvalidParam)
		mustErr(t, "invalid client", bindErr(m, "f2", 0, 5), ErrInvalidParam)
		mustErr(t, "invalid now", bindErr(m, "f2", A, 1000000000001), ErrInvalidParam)
		// Clock-backwards beats client-offline.
		mustErr(t, "clock second", bindErr(m, "f2", B, 15), ErrClockBackwards)
		// Client-offline beats owner-limit and exhaustion.
		mustErr(t, "offline third", bindErr(m, "f2", B, 20), ErrClientOffline)
		// Idempotent bind succeeds even at the owner limit (Q=1, A owns f1).
		mustBind(t, m, "f1", A, 20, 100)
		// Owner limit beats label exhaustion: A is at Q=1 and only one
		// label remains.
		mustErr(t, "limit before exhaustion", bindErr(m, "f2", A, 20), ErrOwnerLimit)
		// Exhaustion only when the limit allows it.
		mustBind(t, m, "f2", 3, 20, 101)
		mustErr(t, "exhausted", bindErr(m, "f3", 4, 20), ErrNoLabel)
	})
	t.Run("unbind and endofrib precedence", func(t *testing.T) {
		m := mustNew(t, 100, 101, 50, 30, 1)
		mustBind(t, m, "f1", A, 10, 100)
		mustOK(t, "down", m.ClientDown(B, 20))
		mustErr(t, "invalid first", m.Unbind("", B, 5), ErrInvalidParam)
		mustErr(t, "clock second", m.Unbind("f1", B, 15), ErrClockBackwards)
		mustErr(t, "offline third", m.Unbind("f1", B, 20), ErrClientOffline)
		mustErr(t, "not found last", m.Unbind("f2", A, 20), ErrBindingNotFound)
		mustErr(t, "eor invalid first", m.EndOfRib(0, 5), ErrInvalidParam)
		mustErr(t, "eor clock second", m.EndOfRib(B, 15), ErrClockBackwards)
		mustErr(t, "eor offline third", m.EndOfRib(B, 20), ErrClientOffline)
		mustErr(t, "eor state last", m.EndOfRib(A, 20), ErrStateMismatch)
	})
}

func TestRejectedOperationsDoNotLandExpirations(t *testing.T) {
	const A, B, C = 1, 2, 3
	m := mustNew(t, 100, 103, 50, 30, 3)
	mustBind(t, m, "f1", A, 0, 100)
	mustBind(t, m, "f2", B, 0, 101)
	mustBind(t, m, "f3", B, 0, 102)
	mustBind(t, m, "f4", B, 0, 103)
	mustOK(t, "down", m.ClientDown(A, 40)) // f1 stale until 70
	logLen := len(m.Log())
	bindErr := func(fec string, client int, now uint64) error {
		_, err := m.Bind(fec, client, now)
		return err
	}
	// Every flavor of rejected operation at t=80 must leave the pending
	// expiration (deadline 70) untouched.
	rejected := []struct {
		name string
		op   func() error
		want error
	}{
		{"invalid param", func() error { return bindErr("", A, 80) }, ErrInvalidParam},
		{"clock backwards", func() error { return bindErr("f1", B, 39) }, ErrClockBackwards},
		{"client offline", func() error { return bindErr("f5", A, 80) }, ErrClientOffline},
		{"owner limit", func() error { return bindErr("f5", B, 80) }, ErrOwnerLimit},
		{"no label", func() error { return bindErr("f5", C, 80) }, ErrNoLabel},
		{"binding not found", func() error { return m.Unbind("f9", B, 80) }, ErrBindingNotFound},
		{"unbind offline", func() error { return m.Unbind("f1", A, 80) }, ErrClientOffline},
		{"eor offline", func() error { return m.EndOfRib(A, 80) }, ErrClientOffline},
		{"eor state", func() error { return m.EndOfRib(B, 80) }, ErrStateMismatch},
		{"down state", func() error { return m.ClientDown(A, 80) }, ErrStateMismatch},
		{"up state", func() error { return m.ClientUp(B, 80) }, ErrStateMismatch},
	}
	for _, tc := range rejected {
		mustErr(t, tc.name, tc.op(), tc.want)
		if n := len(m.Log()); n != logLen {
			t.Fatalf("%s: rejected op wrote log (%d entries, want %d)", tc.name, n, logLen)
		}
		if got := m.Bindings()["f1"]; got != 100 {
			t.Fatalf("%s: rejected op landed the expiration (f1=%v)", tc.name, m.Bindings())
		}
	}
	// The next accepted op lands the expiration with the deadline as its
	// FREE time, not with the current time.
	mustOK(t, "up", m.ClientUp(A, 90))
	if !hasEvent(m.Log(), LogFree, "f1", 100, 70) {
		t.Fatalf("log missing FREE(f1,100,70): %v", m.Log())
	}
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		lo, hi  int
		hd, r   uint64
		q       int
		wantErr bool
	}{
		{16, 16, 0, 0, 1, false},
		{16, 1048575, 1000000000, 1000000000, 1000000, false},
		{15, 16, 0, 0, 1, true},
		{16, 1048576, 0, 0, 1, true},
		{20, 19, 0, 0, 1, true},
		{16, 20, 1000000001, 0, 1, true},
		{16, 20, 0, 1000000001, 1, true},
		{16, 20, 0, 0, 0, true},
		{16, 20, 0, 0, 1000001, true},
	}
	for _, tc := range cases {
		_, err := New(tc.lo, tc.hi, tc.hd, tc.r, tc.q)
		if tc.wantErr {
			mustErr(t, "New", err, ErrInvalidParam)
		} else if err != nil {
			t.Fatalf("New(%+v): %v", tc, err)
		}
	}
}

// checkLogInvariants replays a journal verifying that a label is bound to at
// most one fec at a time, a fec to at most one label, and that a label freed
// and later allocated to a different fec respects the hold-down period.
func checkLogInvariants(t *testing.T, log []LogEvent, hd uint64) {
	t.Helper()
	bound := make(map[int]string)
	fecBound := make(map[string]int)
	lastFree := make(map[int]LogEvent)
	for i, ev := range log {
		switch ev.Op {
		case LogAlloc:
			if prev, ok := bound[ev.Label]; ok {
				t.Fatalf("entry %d: label %d bound to both %s and %s", i, ev.Label, prev, ev.Fec)
			}
			if _, ok := fecBound[ev.Fec]; ok {
				t.Fatalf("entry %d: fec %s bound twice", i, ev.Fec)
			}
			if lf, ok := lastFree[ev.Label]; ok && lf.Fec != ev.Fec && ev.Time < lf.Time+hd {
				t.Fatalf("entry %d: label %d reallocated to %s %d ms after FREE, hd=%d",
					i, ev.Label, ev.Fec, ev.Time-lf.Time, hd)
			}
			bound[ev.Label] = ev.Fec
			fecBound[ev.Fec] = ev.Label
		case LogFree:
			if bound[ev.Label] != ev.Fec {
				t.Fatalf("entry %d: FREE of unbound %s/%d", i, ev.Fec, ev.Label)
			}
			delete(bound, ev.Label)
			delete(fecBound, ev.Fec)
			lastFree[ev.Label] = ev
		default:
			t.Fatalf("entry %d: unknown op %v", i, ev.Op)
		}
	}
}

// runRandomSequence drives both the manager and the naive simulation through
// one randomized operation sequence, comparing every result and the journal
// after every step. It returns the final manager and its operation history.
func runRandomSequence(t *testing.T, seed int64) (*Manager, []string) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	lo := 16 + rng.Intn(4)
	hi := lo + 3 + rng.Intn(10)
	hd := uint64(rng.Intn(60))
	r := uint64(rng.Intn(40))
	q := 1 + rng.Intn(4)
	m := mustNew(t, lo, hi, hd, r, q)
	s := newSim(lo, hi, hd, r, q)
	params := fmt.Sprintf("lo=%d hi=%d hd=%d r=%d q=%d", lo, hi, hd, r, q)

	var now uint64
	var history []string
	record := func(format string, args ...any) {
		history = append(history, fmt.Sprintf(format, args...))
	}
	steps := 25 + rng.Intn(20)
	for step := 0; step < steps; step++ {
		now += uint64(rng.Intn(3)) * uint64(rng.Intn(25))
		fec := fmt.Sprintf("f%d", rng.Intn(8))
		client := 1 + rng.Intn(5)
		var gotLabel, wantLabel int
		var gotErr, wantErr error
		op := ""
		switch dice := rng.Intn(100); {
		case dice < 35:
			op = "bind"
			gotLabel, gotErr = m.Bind(fec, client, now)
			wantLabel, wantErr = s.Bind(fec, client, now)
		case dice < 55:
			op = "unbind"
			gotErr = m.Unbind(fec, client, now)
			wantErr = s.Unbind(fec, client, now)
		case dice < 65:
			op = "down"
			gotErr = m.ClientDown(client, now)
			wantErr = s.ClientDown(client, now)
		case dice < 73:
			op = "up"
			gotErr = m.ClientUp(client, now)
			wantErr = s.ClientUp(client, now)
		case dice < 81:
			op = "eor"
			gotErr = m.EndOfRib(client, now)
			wantErr = s.EndOfRib(client, now)
		case dice < 90:
			op = "restore"
			log := m.Log()
			gotErr = m.Restore(log, now)
			wantErr = s.Restore(log, now)
		case dice < 95:
			op = "invalid"
			switch rng.Intn(4) {
			case 0:
				fec = ""
			case 1:
				fec = string(make([]byte, 65))
			case 2:
				client = 0
			case 3:
				client = 10001
			}
			gotLabel, gotErr = m.Bind(fec, client, now)
			wantLabel, wantErr = s.Bind(fec, client, now)
		default:
			op = "backward"
			if now > 0 {
				now--
			}
			gotLabel, gotErr = m.Bind(fec, client, now)
			wantLabel, wantErr = s.Bind(fec, client, now)
		}
		record("step %d: %s(%s,%d,%d) -> label=%d err=%v", step, op, fec, client, now, gotLabel, gotErr)
		if (gotErr == nil) != (wantErr == nil) || (gotErr != nil && !errors.Is(gotErr, wantErr)) {
			t.Fatalf("seed=%d %s\nerror mismatch: manager=%v sim=%v\nhistory:\n%s",
				seed, params, gotErr, wantErr, joinLines(history))
		}
		if gotErr == nil && op != "restore" && gotLabel != wantLabel {
			t.Fatalf("seed=%d %s\nlabel mismatch: manager=%d sim=%d\nhistory:\n%s",
				seed, params, gotLabel, wantLabel, joinLines(history))
		}
		if gotLog, wantLog := m.Log(), s.log; !reflect.DeepEqual(gotLog, wantLog) {
			t.Fatalf("seed=%d %s\nlog mismatch:\nmanager=%v\nsim    =%v\nhistory:\n%s",
				seed, params, gotLog, wantLog, joinLines(history))
		}
		if got, want := m.Bindings(), s.bindings(); !reflect.DeepEqual(got, want) {
			t.Fatalf("seed=%d %s\nbindings mismatch: manager=%v sim=%v\nhistory:\n%s",
				seed, params, got, want, joinLines(history))
		}
	}
	checkLogInvariants(t, m.Log(), hd)
	t.Logf("seed=%d %s steps=%d events=%d: manager and simulation agree",
		seed, params, steps, len(m.Log()))
	return m, history
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

func TestRandomSequencesMatchSimulation(t *testing.T) {
	for seed := int64(0); seed < 1500; seed++ {
		m1, _ := runRandomSequence(t, seed)
		// Replaying the identical operation sequence must reproduce the
		// exact same labels and journal.
		m2, _ := runRandomSequence(t, seed)
		if got, want := m1.Log(), m2.Log(); !reflect.DeepEqual(got, want) {
			t.Fatalf("seed=%d: replay diverged:\nfirst=%v\nsecond=%v", seed, want, got)
		}
	}
}

func TestRestoreAnyLogPrefix(t *testing.T) {
	// Drive one long random sequence, snapshotting the fec->label mapping
	// whenever the journal grows.
	rng := rand.New(rand.NewSource(20261005))
	m := mustNew(t, 16, 30, 7, 11, 3)
	snapshots := map[int]map[string]int{0: {}}
	var now uint64
	for step := 0; step < 120; step++ {
		now += uint64(rng.Intn(3)) * uint64(rng.Intn(20))
		fec := fmt.Sprintf("f%d", rng.Intn(10))
		client := 1 + rng.Intn(4)
		switch rng.Intn(6) {
		case 0, 1, 2:
			_, _ = m.Bind(fec, client, now)
		case 3:
			_ = m.Unbind(fec, client, now)
		case 4:
			_ = m.ClientDown(client, now)
		case 5:
			if rng.Intn(2) == 0 {
				_ = m.ClientUp(client, now)
			} else {
				_ = m.EndOfRib(client, now)
			}
		}
		snapshots[len(m.Log())] = m.Bindings()
	}
	log := m.Log()
	t.Logf("journal has %d events; restoring every prefix", len(log))
	for k := 0; k <= len(log); k++ {
		var maxLog uint64
		for _, ev := range log[:k] {
			if ev.Time > maxLog {
				maxLog = ev.Time
			}
		}
		m2 := mustNew(t, 16, 30, 7, 11, 3)
		if err := m2.Restore(log[:k], maxLog); err != nil {
			t.Fatalf("Restore(prefix %d): %v", k, err)
		}
		// The mapping rebuilt from a prefix must equal the mapping at the
		// moment that prefix was complete. Cross-check against the naive
		// simulation as an independent reference.
		s2 := newSim(16, 30, 7, 11, 3)
		if err := s2.Restore(log[:k], maxLog); err != nil {
			t.Fatalf("sim Restore(prefix %d): %v", k, err)
		}
		if got, want := m2.Bindings(), s2.bindings(); !reflect.DeepEqual(got, want) {
			t.Fatalf("prefix %d: manager=%v sim=%v", k, got, want)
		}
		if want, ok := snapshots[k]; ok {
			if got := m2.Bindings(); !reflect.DeepEqual(got, want) {
				t.Fatalf("prefix %d: restored=%v snapshot=%v", k, got, want)
			}
		}
	}
}

func TestBindProbes(t *testing.T) {
	// The number of labels examined while picking the minimum available
	// label must not depend on how many labels are allocated.
	examinedByN := make(map[int]int)
	for _, n := range []int{1000, 100000} {
		m := mustNew(t, 16, 16+n+100, 0, 0, 1000000)
		for i := 0; i < n; i++ {
			if _, err := m.Bind(fmt.Sprintf("f%06d", i), 1, 0); err != nil {
				t.Fatalf("bind %d: %v", i, err)
			}
		}
		// Fresh allocation with no expirations at all.
		if _, err := m.Bind("probe-fresh", 1, 0); err != nil {
			t.Fatal(err)
		}
		if m.probeExamined > m.probeExpiries+32 {
			t.Fatalf("n=%d: examined %d > expiries %d + 32", n, m.probeExamined, m.probeExpiries)
		}
		examinedByN[n] = m.probeExamined
		// Free 40 labels (hd=0) and allocate again: the 40 isolation
		// expirations are the landed expirations of this Bind.
		for i := 0; i < 40; i++ {
			if err := m.Unbind(fmt.Sprintf("f%06d", i), 1, 0); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := m.Bind("probe-reuse", 1, 1); err != nil {
			t.Fatal(err)
		}
		if m.probeExamined > m.probeExpiries+32 {
			t.Fatalf("n=%d reuse: examined %d > expiries %d + 32", n, m.probeExamined, m.probeExpiries)
		}
		if m.probeExpiries != 40 {
			t.Fatalf("n=%d: expiries = %d, want 40", n, m.probeExpiries)
		}
		t.Logf("n=%d: fresh examined=%d, reuse examined=%d (expiries=%d)",
			n, examinedByN[n], m.probeExamined, m.probeExpiries)
	}
	if examinedByN[1000] != examinedByN[100000] {
		t.Fatalf("examined labels depends on allocated count: %v", examinedByN)
	}
}

func TestConcurrentUse(t *testing.T) {
	m := mustNew(t, 16, 4000, 5, 10, 100)
	var wg sync.WaitGroup
	var clock sync.Mutex
	var now uint64
	next := func() uint64 {
		clock.Lock()
		defer clock.Unlock()
		now += uint64(1 + now%3)
		return now
	}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			client := g + 1
			for i := 0; i < 300; i++ {
				fec := fmt.Sprintf("g%d-f%d", g, i%40)
				t := next()
				switch i % 7 {
				case 0, 1, 2, 3:
					_, _ = m.Bind(fec, client, t)
				case 4:
					_ = m.Unbind(fec, client, t)
				case 5:
					_ = m.ClientDown(client, t)
				case 6:
					if err := m.ClientUp(client, t); err != nil {
						_ = m.EndOfRib(client, t)
					}
				}
			}
		}(g)
	}
	wg.Wait()
	// The journal of a run equivalent to some serial order must replay
	// cleanly and reproduce the same mapping.
	m2 := mustNew(t, 16, 4000, 5, 10, 100)
	log := m.Log()
	var maxLog uint64
	for _, ev := range log {
		if ev.Time > maxLog {
			maxLog = ev.Time
		}
	}
	if err := m2.Restore(log, maxLog); err != nil {
		t.Fatalf("Restore of concurrent journal: %v", err)
	}
	if got, want := m2.Bindings(), m.Bindings(); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored mapping %v != live mapping %v", got, want)
	}
	checkLogInvariants(t, log, 5)
}
