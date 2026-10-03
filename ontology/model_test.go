package ontology

import (
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

var logFuzz = flag.Bool("log_fuzz", false, "log every differential fuzz operation with input, output and rationale")

// naiveRegistry is a literal re-implementation of the prose specification,
// kept deliberately simple (global scans, separate maps) to serve as an
// independent oracle for differential testing.
type naiveOld struct {
	tok       string
	retiredAt int64
}

type naiveBinding struct {
	user, device, cur string
	lastSeen          int64
	old               []naiveOld
}

type naiveRegistry struct {
	d, r   int
	g, bb  int64
	maxNow int64
	binds  map[string]*naiveBinding
	curOf  map[string]string // current token -> binding key
	oldOf  map[string]string // old token -> binding key
	blk    map[string]int64
}

func naiveNew(d, r int, g, bb int64) *naiveRegistry {
	return &naiveRegistry{
		d: d, r: r, g: g, bb: bb,
		binds: map[string]*naiveBinding{},
		curOf: map[string]string{},
		oldOf: map[string]string{},
		blk:   map[string]int64{},
	}
}

func nk(u, d string) string { return u + "\x00" + d }

func (m *naiveRegistry) releaseBinding(b *naiveBinding) {
	delete(m.curOf, b.cur)
	for _, ot := range b.old {
		delete(m.oldOf, ot.tok)
	}
	delete(m.binds, nk(b.user, b.device))
}

func (m *naiveRegistry) register(u, d, tok string, now int64) error {
	if until, ok := m.blk[tok]; ok && until > now {
		return ErrTokenBlocked
	}
	key := nk(u, d)
	// (1) release prior ownership
	if yk, ok := m.curOf[tok]; ok {
		if yk == key {
			m.binds[yk].lastSeen = now
			m.maxNow = now
			return nil
		}
		m.releaseBinding(m.binds[yk])
	} else if yk, ok := m.oldOf[tok]; ok {
		y := m.binds[yk]
		for i, ot := range y.old {
			if ot.tok == tok {
				y.old = append(y.old[:i], y.old[i+1:]...)
				break
			}
		}
		delete(m.oldOf, tok)
	}
	// (2) existing device
	if b, ok := m.binds[key]; ok {
		b.old = append([]naiveOld{{tok: b.cur, retiredAt: now}}, b.old...)
		for len(b.old) > m.r {
			drop := b.old[len(b.old)-1]
			delete(m.oldOf, drop.tok)
			b.old = b.old[:len(b.old)-1]
		}
		if len(b.old) > 0 {
			m.oldOf[b.old[0].tok] = key
		}
		delete(m.curOf, b.cur)
		b.cur = tok
		b.lastSeen = now
		m.curOf[tok] = key
		m.maxNow = now
		return nil
	}
	// (3) new device; count reflects step (1) deletions
	count := 0
	var victim *naiveBinding
	for _, b := range m.binds {
		if b.user != u {
			continue
		}
		count++
		if victim == nil || b.lastSeen < victim.lastSeen ||
			(b.lastSeen == victim.lastSeen && b.device < victim.device) {
			victim = b
		}
	}
	if count >= m.d {
		m.releaseBinding(victim)
	}
	nb := &naiveBinding{user: u, device: d, cur: tok, lastSeen: now}
	m.binds[key] = nb
	m.curOf[tok] = key
	m.maxNow = now
	return nil
}

func (m *naiveRegistry) feedback(tok string, now int64) error {
	if yk, ok := m.curOf[tok]; ok {
		m.releaseBinding(m.binds[yk])
	} else if yk, ok := m.oldOf[tok]; ok {
		y := m.binds[yk]
		for i, ot := range y.old {
			if ot.tok == tok {
				y.old = append(y.old[:i], y.old[i+1:]...)
				break
			}
		}
		delete(m.oldOf, tok)
	} else {
		return ErrTokenUnowned
	}
	m.blk[tok] = now + m.bb
	m.maxNow = now
	return nil
}

func (m *naiveRegistry) touch(u, d string, now int64) error {
	b, ok := m.binds[nk(u, d)]
	if !ok {
		return ErrDeviceNotFound
	}
	b.lastSeen = now
	m.maxNow = now
	return nil
}

func (m *naiveRegistry) unregister(u, d string, now int64) error {
	b, ok := m.binds[nk(u, d)]
	if !ok {
		return ErrDeviceNotFound
	}
	m.releaseBinding(b)
	m.maxNow = now
	return nil
}

func (m *naiveRegistry) targets(u string, now int64) []string {
	var bs []*naiveBinding
	for _, b := range m.binds {
		if b.user == u {
			bs = append(bs, b)
		}
	}
	sort.Slice(bs, func(i, j int) bool {
		if bs[i].lastSeen != bs[j].lastSeen {
			return bs[i].lastSeen > bs[j].lastSeen
		}
		return bs[i].device < bs[j].device
	})
	type gt struct {
		tok       string
		retiredAt int64
	}
	var old []gt
	out := []string{}
	for _, b := range bs {
		out = append(out, b.cur)
		for _, ot := range b.old {
			if now < ot.retiredAt+m.g {
				old = append(old, gt{ot.tok, ot.retiredAt})
			}
		}
	}
	sort.Slice(old, func(i, j int) bool {
		if old[i].retiredAt != old[j].retiredAt {
			return old[i].retiredAt > old[j].retiredAt
		}
		return old[i].tok < old[j].tok
	})
	for _, ot := range old {
		out = append(out, ot.tok)
	}
	return out
}

const (
	opRegister = iota
	opFeedback
	opTouch
	opUnregister
	opTargets
)

type fuzzOp struct {
	kind      int
	u, d, tok string
	now       int64
}

func genSequence(rng *rand.Rand, n int) []fuzzOp {
	users := []string{"u1", "u2", "u3"}
	devices := []string{"da", "db", "dc", "d\x00z"}
	tokens := []string{"t1", "t2", "t3", "t4", "t5"}
	ops := make([]fuzzOp, n)
	now := int64(0)
	for i := range ops {
		if rng.Intn(5) != 0 {
			now += int64(rng.Intn(4))
		}
		ops[i] = fuzzOp{
			kind: rng.Intn(5),
			u:    users[rng.Intn(len(users))],
			d:    devices[rng.Intn(len(devices))],
			tok:  tokens[rng.Intn(len(tokens))],
			now:  now,
		}
	}
	return ops
}

func applyReal(reg *Registry, op fuzzOp) ([]string, error) {
	switch op.kind {
	case opRegister:
		return nil, reg.Register(b(op.u), b(op.d), b(op.tok), op.now)
	case opFeedback:
		return nil, reg.Feedback(b(op.tok), op.now)
	case opTouch:
		return nil, reg.Touch(b(op.u), b(op.d), op.now)
	case opUnregister:
		return nil, reg.Unregister(b(op.u), b(op.d), op.now)
	default:
		got, err := reg.Targets(b(op.u), op.now)
		out := make([]string, len(got))
		for i, x := range got {
			out[i] = string(x)
		}
		return out, err
	}
}

func applyNaive(m *naiveRegistry, op fuzzOp) ([]string, error) {
	switch op.kind {
	case opRegister:
		return nil, m.register(op.u, op.d, op.tok, op.now)
	case opFeedback:
		return nil, m.feedback(op.tok, op.now)
	case opTouch:
		return nil, m.touch(op.u, op.d, op.now)
	case opUnregister:
		return nil, m.unregister(op.u, op.d, op.now)
	default:
		return m.targets(op.u, op.now), nil
	}
}

func errName(e error) string {
	switch {
	case errors.Is(e, ErrInvalidArgument):
		return "invalid"
	case errors.Is(e, ErrClockSkew):
		return "skew"
	case errors.Is(e, ErrTokenBlocked):
		return "blocked"
	case errors.Is(e, ErrDeviceNotFound):
		return "no-device"
	case errors.Is(e, ErrTokenUnowned):
		return "no-token"
	case e == nil:
		return "ok"
	default:
		return e.Error()
	}
}

// TestDifferentialFuzz replays 2000 random operation sequences against both
// the real registry and the literal naive model, comparing rejection classes
// and Targets output after every operation.
func TestDifferentialFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		d := 1 + rng.Intn(4)
		r := rng.Intn(4)
		g := int64(rng.Intn(8))
		bb := int64(rng.Intn(8))
		reg, err := NewRegistry(d, r, g, bb)
		if err != nil {
			t.Fatal(err)
		}
		model := naiveNew(d, r, g, bb)
		ops := genSequence(rng, 60)
		if *logFuzz {
			t.Logf("--- seq %d D=%d R=%d G=%d B=%d", seq, d, r, g, bb)
		}
		for i, op := range ops {
			name := []string{"Register", "Feedback", "Touch", "Unregister", "Targets"}[op.kind]
			realOut, realErr := applyReal(reg, op)
			modelOut, modelErr := applyNaive(model, op)
			if *logFuzz {
				rationale := ""
				switch {
				case realErr != nil:
					rationale = "rejected: " + errName(realErr)
				case op.kind == opTargets:
					rationale = fmt.Sprintf("targets=%v", realOut)
				default:
					rationale = "accepted; maxNow advances to " + fmt.Sprint(op.now)
				}
				t.Logf("seq=%d #%02d %s(u=%q d=%q tok=%q now=%d) -> %s | %s",
					seq, i, name, op.u, op.d, op.tok, op.now, errName(realErr), rationale)
			}
			if errName(realErr) != errName(modelErr) {
				t.Fatalf("seq %d op %d %+v: real=%s model=%s", seq, i, op,
					errName(realErr), errName(modelErr))
			}
			if op.kind == opTargets && realErr == nil && !eqStrings(realOut, modelOut) {
				t.Fatalf("seq %d op %d targets: real=%v model=%v", seq, i, realOut, modelOut)
			}
			if realErr == nil {
				assertInvariants(t, reg)
			}
		}
	}
}

// assertInvariants checks the global and per-user structural guarantees.
func assertInvariants(t *testing.T, reg *Registry) {
	t.Helper()
	counts := map[string]int{}
	owned := map[string]bool{}
	for key, bd := range reg.bindings {
		if len(bd.old) > reg.r {
			t.Fatalf("binding %s holds %d old tokens > R=%d", key, len(bd.old), reg.r)
		}
		counts[string(bd.user)]++
		if owned[string(bd.cur)] {
			t.Fatalf("token %q owned twice", bd.cur)
		}
		owned[string(bd.cur)] = true
		for _, ot := range bd.old {
			if owned[string(ot.tok)] {
				t.Fatalf("old token %q owned twice", ot.tok)
			}
			owned[string(ot.tok)] = true
		}
	}
	for u, n := range counts {
		if n > reg.d {
			t.Fatalf("user %q has %d devices > D=%d", u, n, reg.d)
		}
		if got := len(reg.devices[u]); got != n {
			t.Fatalf("per-user counter drift for %q: index=%d bindings=%d", u, got, n)
		}
	}
	if len(reg.owners) != len(owned) {
		t.Fatalf("owner index size %d != distinct owned tokens %d", len(reg.owners), len(owned))
	}
	for tok, o := range reg.owners {
		if !owned[tok] {
			t.Fatalf("owner index references released token %q", tok)
		}
		found := false
		if !o.isOld && string(o.b.cur) == tok {
			found = true
		}
		for _, ot := range o.b.old {
			if string(ot.tok) == tok {
				found = true
			}
		}
		if !found {
			t.Fatalf("owner index points at binding not holding %q", tok)
		}
	}
}

// TestReplayDeterministic runs the same op sequence twice and requires equal
// Targets output at every step.
func TestReplayDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(777))
	ops := genSequence(rng, 120)
	run := func() [][]string {
		reg, _ := NewRegistry(3, 3, 6, 9)
		var snap [][]string
		for _, op := range ops {
			out, err := applyReal(reg, op)
			if err == nil && op.kind == opTargets {
				snap = append(snap, append([]string(nil), out...))
			}
		}
		return snap
	}
	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatal("replay length differs")
	}
	for i := range first {
		if !eqStrings(first[i], second[i]) {
			t.Fatalf("step %d: %v vs %v", i, first[i], second[i])
		}
	}
}

// TestConcurrentIssues drives all operations concurrently against many
// goroutines; invariants must hold throughout and no data race must occur.
func TestConcurrentIssues(t *testing.T) {
	reg, _ := NewRegistry(8, 4, 5, 5)
	const goroutines = 16
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			now := int64(0)
			for i := 0; i < 200; i++ {
				now += int64(rng.Intn(3))
				u := fmt.Sprintf("u%d", rng.Intn(4))
				d := fmt.Sprintf("d%d", rng.Intn(12))
				tok := fmt.Sprintf("t%d", rng.Intn(20))
				switch rng.Intn(5) {
				case 0:
					_ = reg.Register(b(u), b(d), b(tok), now)
				case 1:
					_ = reg.Feedback(b(tok), now)
				case 2:
					_ = reg.Touch(b(u), b(d), now)
				case 3:
					_ = reg.Unregister(b(u), b(d), now)
				case 4:
					_, _ = reg.Targets(b(u), now)
				}
			}
		}(g)
	}
	wg.Wait()
	assertInvariants(t, reg)
}
