package objstore

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/cond"
)

func strp(s string) *string { return &s }
func i64p(v int64) *int64   { return &v }
func u(v int64) *int64      { return &v }

// step is one row of a table-driven scenario.
type step struct {
	op      string // "put", "del", "setq", "setv"
	t, key  string
	size    int64 // put size / setq quota
	etag    string
	ver     int64
	on      bool
	c       cond.Cond
	now     int64
	wantErr error  // sentinel matched with errors.Is; nil means success
	wantSub string // expected sub-condition name for ErrPrecondition
	wantVer int64  // expected version number of a successful put
	wantU   *int64 // expected usage of stp.t after the step; nil skips
}

func runSteps(t *testing.T, st *Store, steps []step) {
	t.Helper()
	for i, stp := range steps {
		var err error
		var ver int64
		switch stp.op {
		case "put":
			ver, err = st.Put(stp.t, stp.key, stp.size, stp.etag, stp.c, stp.now)
		case "del":
			err = st.Delete(stp.t, stp.key, stp.ver, stp.c, stp.now)
		case "setq":
			err = st.SetQuota(stp.t, stp.size)
		case "setv":
			err = st.SetVersioning(stp.t, stp.on)
		default:
			t.Fatalf("step %d: unknown op %q", i, stp.op)
		}
		desc := fmt.Sprintf("step %d (%s %s/%s)", i, stp.op, stp.t, stp.key)
		if stp.wantErr == nil {
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", desc, err)
			}
		} else {
			if !errors.Is(err, stp.wantErr) {
				t.Fatalf("%s: err = %v, want %v", desc, err, stp.wantErr)
			}
			if stp.wantSub != "" {
				var pe *PreconditionError
				if !errors.As(err, &pe) || pe.Sub != stp.wantSub {
					t.Fatalf("%s: precondition sub = %v, want %q", desc, err, stp.wantSub)
				}
			}
		}
		if stp.op == "put" && err == nil && ver != stp.wantVer {
			t.Fatalf("%s: version = %d, want %d", desc, ver, stp.wantVer)
		}
		if stp.wantU != nil {
			if got := st.Usage(stp.t); got != *stp.wantU {
				t.Fatalf("%s: U = %d, want %d", desc, got, *stp.wantU)
			}
		}
	}
}

// --- naive reference model for the randomized cross-check ---

type mver struct {
	ver, size int64
	etag      string
	mtime     int64
	tomb      bool
}

// model is a deliberately naive, step-by-step implementation of the
// spec used to cross-check the real store. It scans versions linearly
// and keeps plain slices.
type model struct {
	quota map[string]int64
	usage map[string]int64
	verOn map[string]bool
	last  map[string]int64
	keys  map[string]map[string][]mver
}

func newModel() *model {
	return &model{
		quota: make(map[string]int64),
		usage: make(map[string]int64),
		verOn: make(map[string]bool),
		last:  make(map[string]int64),
		keys:  make(map[string]map[string][]mver),
	}
}

func (m *model) cur(t, k string) (mver, bool) {
	var best mver
	ok := false
	for _, v := range m.keys[t][k] {
		if !ok || v.ver > best.ver {
			best, ok = v, true
		}
	}
	return best, ok
}

func (m *model) checkCond(t, k string, c cond.Cond) string {
	cur, ok := m.cur(t, k)
	data := ok && !cur.tomb
	if c.IfMatch != nil && (!data || cur.etag != *c.IfMatch) {
		return "precond:" + cond.IfMatchName
	}
	if c.IfUnmodifiedSince != nil && (!data || cur.mtime > *c.IfUnmodifiedSince) {
		return "precond:" + cond.IfUnmodifiedSinceName
	}
	if c.IfNoneMatchStar && ok && !cur.tomb {
		return "precond:" + cond.IfNoneMatchName
	}
	return ""
}

func (m *model) setq(t string, q int64) string {
	if t == "" || q < 0 || q > 1_000_000_000_000_000 {
		return "invalid"
	}
	m.quota[t] = q
	return "ok"
}

func (m *model) setv(t string, on bool) string {
	if t == "" {
		return "invalid"
	}
	if on {
		m.verOn[t] = true
		return "ok"
	}
	if m.verOn[t] {
		return "verlock"
	}
	return "ok"
}

func (m *model) put(t, k string, size int64, etag string, c cond.Cond, now int64, hook bool) (int64, string) {
	if t == "" || k == "" || etag == "" || size < 0 || size > 1_000_000_000_000 ||
		now < 0 || now > 1_000_000_000_000 || !c.Valid() {
		return 0, "invalid"
	}
	if why := m.checkCond(t, k, c); why != "" {
		return 0, why
	}
	cur, ok := m.cur(t, k)
	d := size
	if !m.verOn[t] && ok {
		d = size - cur.size
	}
	if d > 0 && m.usage[t]+d > m.quota[t] {
		return 0, "quota"
	}
	if hook {
		return 0, "storage"
	}
	if m.keys[t] == nil {
		m.keys[t] = make(map[string][]mver)
	}
	var verNo int64
	if !m.verOn[t] {
		m.keys[t][k] = []mver{{ver: 0, size: size, etag: etag, mtime: now}}
	} else {
		m.last[t]++
		verNo = m.last[t]
		m.keys[t][k] = append(m.keys[t][k], mver{ver: verNo, size: size, etag: etag, mtime: now})
	}
	m.usage[t] += d
	return verNo, "ok"
}

func (m *model) del(t, k string, ver int64, c cond.Cond, now int64, hook bool) string {
	if t == "" || k == "" || ver < -1 || now < 0 || now > 1_000_000_000_000 || !c.Valid() {
		return "invalid"
	}
	if !m.verOn[t] && ver != -1 {
		return "invalid"
	}
	if why := m.checkCond(t, k, c); why != "" {
		return why
	}
	cur, ok := m.cur(t, k)
	var d int64
	idx := -1
	switch {
	case !m.verOn[t]:
		if !ok {
			return "notfound"
		}
		d = -cur.size
	case ver == -1:
		d = 0
	default:
		for i, v := range m.keys[t][k] {
			if v.ver == ver {
				idx = i
				d = -v.size
			}
		}
		if idx < 0 {
			return "notfound"
		}
	}
	if d > 0 && m.usage[t]+d > m.quota[t] {
		return "quota"
	}
	if hook {
		return "storage"
	}
	switch {
	case !m.verOn[t]:
		delete(m.keys[t], k)
	case ver == -1:
		m.last[t]++
		if m.keys[t] == nil {
			m.keys[t] = make(map[string][]mver)
		}
		m.keys[t][k] = append(m.keys[t][k], mver{ver: m.last[t], mtime: now, tomb: true})
	default:
		vs := m.keys[t][k]
		vs = append(vs[:idx], vs[idx+1:]...)
		if len(vs) == 0 {
			delete(m.keys[t], k)
		} else {
			m.keys[t][k] = vs
		}
	}
	m.usage[t] += d
	return "ok"
}

func kindOf(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidParam):
		return "invalid"
	case errors.Is(err, ErrPrecondition):
		var pe *PreconditionError
		if errors.As(err, &pe) {
			return "precond:" + pe.Sub
		}
		return "precond:?"
	case errors.Is(err, ErrNotFound):
		return "notfound"
	case errors.Is(err, ErrQuotaExceeded):
		return "quota"
	case errors.Is(err, ErrStorage):
		return "storage"
	case errors.Is(err, ErrVersioningLock):
		return "verlock"
	default:
		return "unknown:" + err.Error()
	}
}

var (
	simTenants = []string{"t1", "t2"}
	simKeys    = []string{"a", "b", "c"}
	simEtags   = []string{"e1", "e2", "e3"}
	simQuotas  = []int64{0, 50, 100, 200, 1000, 1_000_000_000_000_000}
)

func randCond(r *rand.Rand, m *model, t, k string) cond.Cond {
	var c cond.Cond
	cur, ok := m.cur(t, k)
	if r.Intn(100) < 35 {
		e := simEtags[r.Intn(len(simEtags))]
		if ok && !cur.tomb && r.Intn(2) == 0 {
			e = cur.etag
		}
		c.IfMatch = &e
	}
	if r.Intn(100) < 30 {
		var s int64
		switch r.Intn(12) {
		case 0:
			s = -1 // invalid
		case 1:
			s = 1_000_000_000_001 // invalid
		default:
			if ok && !cur.tomb {
				s = cur.mtime + int64(r.Intn(5)) - 2
			} else {
				s = r.Int63n(120)
			}
		}
		c.IfUnmodifiedSince = &s
	}
	if r.Intn(100) < 30 {
		c.IfNoneMatchStar = true
	}
	return c
}

// Cross-check the store against the naive model on 1500 random
// operation sequences, logging input, output and the reason for
// every step.
func TestRandomSimulation(t *testing.T) {
	for seq := 0; seq < 1500; seq++ {
		r := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		st := New()
		m := newModel()
		hookArmed := false
		st.SetAfterReserveHook(func() error {
			if hookArmed {
				hookArmed = false
				return errors.New("injected")
			}
			return nil
		})
		ops := 15 + r.Intn(20)
		for i := 0; i < ops; i++ {
			if r.Intn(100) < 5 {
				hookArmed = true
			}
			tn := simTenants[r.Intn(len(simTenants))]
			key := simKeys[r.Intn(len(simKeys))]
			invBefore := m.usage[tn] <= m.quota[tn]
			var in, out, why string
			isSetQ := false
			switch r.Intn(100) {
			case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9: // setquota
				isSetQ = true
				q := simQuotas[r.Intn(len(simQuotas))]
				if r.Intn(100) < 4 {
					q = []int64{-1, 1_000_000_000_000_001}[r.Intn(2)]
				}
				err := st.SetQuota(tn, q)
				got, want := kindOf(err), m.setq(tn, q)
				in = fmt.Sprintf("SetQuota(t=%s q=%d)", tn, q)
				out, why = got, want
				if got != want {
					t.Fatalf("seq=%d op=%d %s: store=%s model=%s", seq, i, in, got, want)
				}
			case 10, 11, 12, 13, 14, 15, 16, 17, 18, 19: // setversioning
				on := r.Intn(100) < 70
				err := st.SetVersioning(tn, on)
				got, want := kindOf(err), m.setv(tn, on)
				in = fmt.Sprintf("SetVersioning(t=%s on=%v)", tn, on)
				out, why = got, want
				if got != want {
					t.Fatalf("seq=%d op=%d %s: store=%s model=%s", seq, i, in, got, want)
				}
			case 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34: // delete
				ver := int64(-1)
				if r.Intn(100) < 40 {
					ver = int64(r.Intn(int(m.last[tn]) + 3))
				}
				c := randCond(r, m, tn, key)
				now := r.Int63n(120)
				if r.Intn(100) < 3 {
					now = []int64{-1, 1_000_000_000_001}[r.Intn(2)]
				}
				hook := hookArmed
				err := st.Delete(tn, key, ver, c, now)
				got, want := kindOf(err), m.del(tn, key, ver, c, now, hook)
				if want == "storage" {
					hookArmed = false
				}
				in = fmt.Sprintf("Delete(t=%s key=%s ver=%d cond=%+v now=%d hook=%v)", tn, key, ver, c, now, hook)
				out, why = got, want
				if got != want {
					t.Fatalf("seq=%d op=%d %s: store=%s model=%s", seq, i, in, got, want)
				}
			default: // put
				size := r.Int63n(150)
				if r.Intn(100) < 3 {
					size = []int64{-1, 1_000_000_000_001}[r.Intn(2)]
				}
				etag := simEtags[r.Intn(len(simEtags))]
				if r.Intn(100) < 2 {
					etag = ""
				}
				c := randCond(r, m, tn, key)
				now := r.Int63n(120)
				if r.Intn(100) < 3 {
					now = []int64{-1, 1_000_000_000_001}[r.Intn(2)]
				}
				hook := hookArmed
				ver, err := st.Put(tn, key, size, etag, c, now)
				got, want := kindOf(err), ""
				wantVer, wantKind := m.put(tn, key, size, etag, c, now, hook)
				want = wantKind
				if want == "storage" {
					hookArmed = false
				}
				in = fmt.Sprintf("Put(t=%s key=%s size=%d etag=%q cond=%+v now=%d hook=%v)", tn, key, size, etag, c, now, hook)
				out, why = got, want
				if got != want {
					t.Fatalf("seq=%d op=%d %s: store=%s model=%s", seq, i, in, got, want)
				}
				if got == "ok" && ver != wantVer {
					t.Fatalf("seq=%d op=%d %s: store ver=%d model ver=%d", seq, i, in, ver, wantVer)
				}
			}
			for _, tt := range simTenants {
				if got, want := st.Usage(tt), m.usage[tt]; got != want {
					t.Fatalf("seq=%d op=%d %s: U(%s) store=%d model=%d", seq, i, in, tt, got, want)
				}
			}
			if invBefore && !isSetQ && st.Usage(tn) > m.quota[tn] {
				t.Fatalf("seq=%d op=%d %s: invariant broken: U=%d > Q=%d", seq, i, in, st.Usage(tn), m.quota[tn])
			}
			t.Logf("seq=%d op=%d in=%s out=%s why=%s U=%v", seq, i, in, out, why, m.usage)
		}
	}
}

// The worked example from the spec, versioning off.
func TestSpecExampleOff(t *testing.T) {
	runSteps(t, New(), []step{
		{op: "setq", t: "t", size: 100},
		{op: "put", t: "t", key: "a", size: 60, etag: "e1", now: 1, wantVer: 0, wantU: u(60)},
		{op: "put", t: "t", key: "b", size: 50, etag: "e2", now: 2, wantErr: ErrQuotaExceeded, wantU: u(60)}, // 110 > 100
		{op: "put", t: "t", key: "a", size: 90, etag: "e3", c: cond.Cond{IfMatch: strp("e1")}, now: 3, wantVer: 0, wantU: u(90)},
		{op: "setq", t: "t", size: 80}, // accepted: over-quota state 90 > 80
		{op: "put", t: "t", key: "a", size: 90, etag: "e4", now: 4, wantVer: 0, wantU: u(90)},                // d=0 passes
		{op: "put", t: "t", key: "a", size: 95, etag: "e5", now: 5, wantErr: ErrQuotaExceeded, wantU: u(90)}, // d=5 rejected
		{op: "put", t: "t", key: "a", size: 10, etag: "e6", now: 6, wantVer: 0, wantU: u(10)},                // d=-80 passes
	})
}

// U+d == Q passes, U+d == Q+1 fails; over-quota state passes d=0 and
// rejects d=1.
func TestQuotaBoundary(t *testing.T) {
	runSteps(t, New(), []step{
		{op: "setq", t: "t", size: 100},
		{op: "put", t: "t", key: "a", size: 100, etag: "e1", now: 1, wantU: u(100)}, // exactly Q
		{op: "put", t: "t", key: "b", size: 1, etag: "e2", now: 2, wantErr: ErrQuotaExceeded, wantU: u(100)},
		{op: "put", t: "t", key: "a", size: 101, etag: "e3", now: 3, wantErr: ErrQuotaExceeded, wantU: u(100)}, // d=1
		{op: "put", t: "t", key: "a", size: 100, etag: "e4", now: 4, wantU: u(100)},                            // d=0
		{op: "setq", t: "t", size: 50},                                                                         // over-quota: 100 > 50
		{op: "put", t: "t", key: "a", size: 100, etag: "e5", now: 5, wantU: u(100)},                            // d=0 passes
		{op: "put", t: "t", key: "a", size: 99, etag: "e6", now: 6, wantU: u(99)},                              // shrink passes
		{op: "del", t: "t", key: "a", ver: -1, now: 7, wantU: u(0)},                                            // deletes pass
	})
}

// Fixed report order: invalid > precondition > not found > quota >
// storage. Only the first failure is reported.
func TestCheckOrder(t *testing.T) {
	hookArmed := false
	st := New()
	st.SetAfterReserveHook(func() error {
		if hookArmed {
			hookArmed = false
			return errors.New("injected")
		}
		return nil
	})
	runSteps(t, st, []step{
		{op: "setq", t: "t", size: 10},
		{op: "put", t: "t", key: "a", size: 5, etag: "e1", now: 1, wantU: u(5)},
		// invalid param beats failing precondition
		{op: "put", t: "t", key: "a", size: -1, etag: "e2", c: cond.Cond{IfMatch: strp("nope")}, now: 2, wantErr: ErrInvalidParam},
		{op: "put", t: "t", key: "a", size: 1, etag: "e2", now: -1, wantErr: ErrInvalidParam},
		{op: "put", t: "t", key: "a", size: 1, etag: "e2", c: cond.Cond{IfUnmodifiedSince: i64p(-1)}, now: 2, wantErr: ErrInvalidParam},
		// precondition beats quota (d=45 would exceed Q=10)
		{op: "put", t: "t", key: "a", size: 50, etag: "e2", c: cond.Cond{IfMatch: strp("nope")}, now: 2, wantErr: ErrPrecondition, wantSub: cond.IfMatchName},
		// precondition beats not found
		{op: "del", t: "t", key: "ghost", ver: -1, c: cond.Cond{IfMatch: strp("e1")}, now: 2, wantErr: ErrPrecondition, wantSub: cond.IfMatchName},
		// not found (cond passes, key absent, versioning off)
		{op: "del", t: "t", key: "ghost", ver: -1, now: 2, wantErr: ErrNotFound},
	})
	// quota beats injected storage failure; storage failure comes last.
	hookArmed = true
	runSteps(t, st, []step{
		{op: "put", t: "t", key: "a", size: 50, etag: "e2", now: 2, wantErr: ErrQuotaExceeded},
	})
	if !hookArmed {
		t.Fatal("hook must not fire when the quota check fails first")
	}
	runSteps(t, st, []step{
		{op: "put", t: "t", key: "a", size: 5, etag: "e2", now: 2, wantErr: ErrStorage, wantU: u(5)},
	})
}

// mtime == s passes IfUnmodifiedSince; mtime == s+1 fails.
func TestUnmodifiedSinceEquality(t *testing.T) {
	runSteps(t, New(), []step{
		{op: "setq", t: "t", size: 100},
		{op: "put", t: "t", key: "a", size: 5, etag: "e1", now: 50, wantU: u(5)},
		{op: "put", t: "t", key: "a", size: 5, etag: "e2", c: cond.Cond{IfUnmodifiedSince: i64p(50)}, now: 60, wantU: u(5)},
		{op: "put", t: "t", key: "a", size: 5, etag: "e3", c: cond.Cond{IfUnmodifiedSince: i64p(59)}, now: 70, wantErr: ErrPrecondition, wantSub: cond.IfUnmodifiedSinceName},
	})
}

// A delete marker as the current version: IfMatch and
// IfUnmodifiedSince fail, IfNoneMatch passes.
func TestTombstoneCurrent(t *testing.T) {
	runSteps(t, New(), []step{
		{op: "setq", t: "t", size: 100},
		{op: "setv", t: "t", on: true},
		{op: "put", t: "t", key: "a", size: 10, etag: "e1", now: 1, wantVer: 1, wantU: u(10)},
		{op: "del", t: "t", key: "a", ver: -1, now: 2, wantU: u(10)}, // marker v2
		{op: "put", t: "t", key: "a", size: 1, etag: "e2", c: cond.Cond{IfMatch: strp("e1")}, now: 3, wantErr: ErrPrecondition, wantSub: cond.IfMatchName},
		{op: "put", t: "t", key: "a", size: 1, etag: "e2", c: cond.Cond{IfUnmodifiedSince: i64p(1_000_000_000_000)}, now: 3, wantErr: ErrPrecondition, wantSub: cond.IfUnmodifiedSinceName},
		{op: "put", t: "t", key: "a", size: 1, etag: "e2", c: cond.Cond{IfNoneMatchStar: true}, now: 3, wantVer: 3, wantU: u(11)},
	})
}

// Versioning switch rules and delete edge cases.
func TestVersioningRules(t *testing.T) {
	runSteps(t, New(), []step{
		{op: "setv", t: "t", on: false}, // no-op while disabled
		{op: "setv", t: "t", on: true},
		{op: "setv", t: "t", on: true}, // idempotent
		{op: "setv", t: "t", on: false, wantErr: ErrVersioningLock},
		{op: "setv", t: "", on: true, wantErr: ErrInvalidParam},
		// delete marker on a key with no versions still takes a number
		{op: "setq", t: "t", size: 100},
		{op: "del", t: "t", key: "a", ver: -1, now: 1},
		// permanent deletes: marker v1 and absent versions
		{op: "del", t: "t", key: "a", ver: 1, now: 2},
		{op: "del", t: "t", key: "a", ver: 1, now: 3, wantErr: ErrNotFound},
		{op: "del", t: "t", key: "a", ver: 99, now: 3, wantErr: ErrNotFound},
	})
	if _, _, _, _, tomb, ok := New().Stat("t", "a"); ok || tomb {
		t.Fatal("unexpected state")
	}
	runSteps(t, New(), []step{
		{op: "setq", t: "t", size: 100},
		{op: "put", t: "t", key: "a", size: 5, etag: "e1", now: 1, wantU: u(5)},
		// versioning off: ver != -1 is invalid
		{op: "del", t: "t", key: "a", ver: 0, now: 2, wantErr: ErrInvalidParam},
		{op: "del", t: "t", key: "a", ver: -2, now: 2, wantErr: ErrInvalidParam},
		{op: "del", t: "t", key: "a", ver: -1, now: 2, wantU: u(0)},
		{op: "del", t: "t", key: "a", ver: -1, now: 3, wantErr: ErrNotFound},
	})
}

// Injected storage failure rolls everything back: object, U, version
// numbers and mtime are unchanged.
func TestStorageFailureRollback(t *testing.T) {
	hookErr := error(nil)
	st := New()
	st.SetAfterReserveHook(func() error { return hookErr })
	runSteps(t, st, []step{
		{op: "setq", t: "t", size: 100},
		{op: "setv", t: "t", on: true},
		{op: "put", t: "t", key: "a", size: 10, etag: "e1", now: 7, wantVer: 1, wantU: u(10)},
	})
	hookErr = errors.New("disk on fire")
	runSteps(t, st, []step{
		{op: "put", t: "t", key: "a", size: 20, etag: "e2", now: 8, wantErr: ErrStorage, wantU: u(10)},
		{op: "del", t: "t", key: "a", ver: -1, now: 8, wantErr: ErrStorage, wantU: u(10)},
	})
	ver, size, etag, mtime, tomb, ok := st.Stat("t", "a")
	if !ok || tomb || ver != 1 || size != 10 || etag != "e1" || mtime != 7 {
		t.Fatalf("state changed after rollback: ver=%d size=%d etag=%s mtime=%d", ver, size, etag, mtime)
	}
	hookErr = nil
	// The failed put consumed no version number: next put is v2, not v3.
	runSteps(t, st, []step{
		{op: "put", t: "t", key: "a", size: 20, etag: "e2", now: 8, wantVer: 2, wantU: u(30)},
	})
}

// Invalid parameter table.
func TestInvalidParams(t *testing.T) {
	runSteps(t, New(), []step{
		{op: "put", t: "", key: "a", size: 1, etag: "e", now: 1, wantErr: ErrInvalidParam},
		{op: "put", t: "t", key: "", size: 1, etag: "e", now: 1, wantErr: ErrInvalidParam},
		{op: "put", t: "t", key: "a", size: 1, etag: "", now: 1, wantErr: ErrInvalidParam},
		{op: "put", t: "t", key: "a", size: 1_000_000_000_001, etag: "e", now: 1, wantErr: ErrInvalidParam},
		{op: "put", t: "t", key: "a", size: 1, etag: "e", now: 1_000_000_000_001, wantErr: ErrInvalidParam},
		{op: "put", t: "t", key: "a", size: 1_000_000_000_000, etag: "e", now: 1_000_000_000_000, wantErr: ErrQuotaExceeded},
		{op: "del", t: "t", key: "a", ver: -1, now: -1, wantErr: ErrInvalidParam},
		{op: "setq", t: "t", size: -1, wantErr: ErrInvalidParam},
		{op: "setq", t: "t", size: 1_000_000_000_000_001, wantErr: ErrInvalidParam},
		{op: "setq", t: "", size: 1, wantErr: ErrInvalidParam},
		{op: "setq", t: "t", size: 1_000_000_000_000_000},
	})
}

// Concurrent IfNoneMatch(*) puts on the same key: exactly one wins.
func TestConcurrentIfNoneMatch(t *testing.T) {
	st := New()
	if err := st.SetQuota("t", 1_000_000); err != nil {
		t.Fatal(err)
	}
	const n = 64
	var wg sync.WaitGroup
	var successes atomic.Int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := st.Put("t", "a", 1, fmt.Sprintf("e%d", i),
				cond.Cond{IfNoneMatchStar: true}, int64(i))
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrPrecondition) {
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if got := successes.Load(); got != 1 {
		t.Fatalf("successes = %d, want exactly 1", got)
	}
}

// A concurrent IfMatch chain: every old etag gets exactly one
// successor.
func TestConcurrentIfMatchChain(t *testing.T) {
	st := New()
	if err := st.SetQuota("t", 1_000_000); err != nil {
		t.Fatal(err)
	}
	curEtag := "gen0"
	if _, err := st.Put("t", "a", 1, curEtag, cond.Cond{}, 0); err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 5; round++ {
		const n = 8
		var wg sync.WaitGroup
		winner := make(chan string, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				etag := fmt.Sprintf("gen%d-%d", round, i)
				_, err := st.Put("t", "a", 1, etag, cond.Cond{IfMatch: &curEtag}, int64(round))
				if err == nil {
					winner <- etag
				} else if !errors.Is(err, ErrPrecondition) {
					t.Errorf("unexpected error: %v", err)
				}
			}(i)
		}
		wg.Wait()
		close(winner)
		var won []string
		for etag := range winner {
			won = append(won, etag)
		}
		if len(won) != 1 {
			t.Fatalf("round %d: %d successors for etag %q, want 1", round, len(won), curEtag)
		}
		curEtag = won[0]
	}
}

// Determining the current version touches at most 2 version records,
// independent of the key's version count (10 vs 10000).
func TestTouchedBound(t *testing.T) {
	for _, n := range []int{10, 10000} {
		st := New()
		if err := st.SetQuota("t", 1_000_000_000_000_000); err != nil {
			t.Fatal(err)
		}
		if err := st.SetVersioning("t", true); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			if _, err := st.Put("t", "a", 0, "e", cond.Cond{}, int64(i)); err != nil {
				t.Fatal(err)
			}
		}
		st.touched = 0
		if _, err := st.Put("t", "a", 0, "e2", cond.Cond{IfMatch: strp("nope")}, 1); !errors.Is(err, ErrPrecondition) {
			t.Fatalf("n=%d: want precondition failure, got %v", n, err)
		}
		if st.touched > 2 {
			t.Fatalf("n=%d: conditional put touched %d records, want <= 2", n, st.touched)
		}
		st.touched = 0
		if err := st.Delete("t", "a", 5, cond.Cond{}, 1); err != nil {
			t.Fatalf("n=%d: delete: %v", n, err)
		}
		if st.touched > 2 {
			t.Fatalf("n=%d: versioned delete touched %d records, want <= 2", n, st.touched)
		}
	}
}

// The worked examples from the spec, versioning on.
func TestSpecExampleVersioning(t *testing.T) {
	runSteps(t, New(), []step{
		{op: "setq", t: "t", size: 100},
		{op: "setv", t: "t", on: true},
		{op: "put", t: "t", key: "a", size: 60, etag: "e1", now: 1, wantVer: 1, wantU: u(60)},
		{op: "put", t: "t", key: "a", size: 60, etag: "e2", now: 2, wantErr: ErrQuotaExceeded, wantU: u(60)}, // 120 > 100
		{op: "del", t: "t", key: "a", ver: -1, now: 3, wantU: u(60)},                                         // delete marker v2
		{op: "del", t: "t", key: "a", ver: 1, now: 4, wantU: u(0)},                                           // purge v1 releases 60
		// current is the marker: IfNoneMatch passes.
		{op: "put", t: "t", key: "a", size: 5, etag: "e3", c: cond.Cond{IfNoneMatchStar: true}, now: 5, wantVer: 3, wantU: u(5)},
	})

	// Legacy object becomes version 0 when versioning is switched on.
	runSteps(t, New(), []step{
		{op: "setq", t: "t", size: 100},
		{op: "put", t: "t", key: "a", size: 5, etag: "e1", now: 1, wantVer: 0, wantU: u(5)},
		{op: "setv", t: "t", on: true},
		{op: "put", t: "t", key: "a", size: 5, etag: "e2", now: 2, wantVer: 1, wantU: u(10)},
	})
}
