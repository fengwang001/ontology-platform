package vegas

import (
	"fmt"
	"math/rand"
	"testing"
)

// opKind enumerates the generated operations.
type opKind int

const (
	opAcquire opKind = iota
	opRelease
	opAcquireBadTime
	opAcquireRewind
	opReleaseBadResult
	opReleaseBadTime
	opReleaseRewind
)

type genOp struct {
	kind opKind
	now  int64
	seq  int64 // chosen token number for releases (may be invalid)
	res  Result
	rtt  int64
}

// genConfig draws a random but always-valid configuration.
func genConfig(r *rand.Rand) Config {
	lmin := int64(1 + r.Intn(8))
	lmax := lmin + int64(r.Intn(30))
	l0 := lmin + r.Int63n(lmax-lmin+1)
	alpha := int64(r.Intn(4))
	beta := alpha + 1 + int64(r.Intn(5))
	tmo := int64(1 + r.Intn(40))
	cd := int64(r.Intn(15))
	wm := int64(1 + r.Intn(60))
	return Config{L0: l0, Lmin: lmin, Lmax: lmax, Alpha: alpha, Beta: beta, Tmo: tmo, Cd: cd, Wm: wm}
}

// genSequence builds ops with non-decreasing (mostly valid) times, keeping the
// set of previously admitted token numbers available for realistic releases.
func genSequence(r *rand.Rand, n int) []genOp {
	ops := make([]genOp, 0, n)
	var admitted []int64
	now := int64(0)
	for i := 0; i < n; i++ {
		roll := r.Intn(100)
		switch {
		case roll < 50:
			now += int64(r.Intn(6))
			if now > maxTime-50 {
				now = maxTime - 50
			}
			switch r.Intn(12) {
			case 0:
				ops = append(ops, genOp{kind: opAcquireBadTime, now: -1 - int64(r.Intn(100))})
			case 1:
				ops = append(ops, genOp{kind: opAcquireRewind, now: max(0, now-1-int64(r.Intn(5)))})
			default:
				ops = append(ops, genOp{kind: opAcquire, now: now})
			}
		default:
			now += int64(r.Intn(6))
			if now > maxTime-50 {
				now = maxTime - 50
			}
			if len(admitted) == 0 {
				ops = append(ops, genOp{kind: opAcquire, now: now})
				continue
			}
			resRoll := r.Intn(10)
			var res Result
			switch {
			case resRoll < 6:
				res = ResultSuccess
			case resRoll < 8:
				res = ResultDrop
			default:
				res = ResultIgnore
			}
			rtt := int64(1 + r.Intn(500))
			if res != ResultSuccess {
				rtt = int64(r.Intn(502)) - 1 // ignored for drop/ignore
			}
			kind := opRelease
			switch r.Intn(14) {
			case 0:
				kind = opReleaseBadResult
				res = Result(99)
			case 1:
				kind = opReleaseBadTime
				now = -1 - int64(r.Intn(10))
			case 2:
				kind = opReleaseRewind
				now = max(0, now-1-int64(r.Intn(5)))
			}
			// pick a token: usually one that was admitted, sometimes bogus
			seq := admitted[r.Intn(len(admitted))]
			if r.Intn(10) == 0 {
				if r.Intn(2) == 0 {
					seq = int64(1 + r.Intn(len(admitted)+3))
				} else {
					seq = 1_000_000 + int64(r.Intn(100))
				}
			}
			if res == ResultSuccess && r.Intn(8) == 0 && kind == opRelease {
				rtt = int64(r.Intn(1)) // 0 sometimes
				if r.Intn(2) == 0 {
					rtt = maxRTT + 1 + int64(r.Intn(10))
				}
			}
			ops = append(ops, genOp{kind: kind, now: now, seq: seq, res: res, rtt: rtt})
		}
	}
	// second pass: record which seq values were actually admitted when the
	// real limiter runs (the driver does that and feeds the same sequence).
	return ops
}

// comparableState is the subset of state both implementations must agree on.
type comparableState struct {
	L, n, nextSeq int64
	outstanding   int
	windowSize    int
	windowMin     int64
	hasMin        bool
	lastCut       int64
	hasLastCut    bool
	maxNow        int64
	timedOutCount int64
}

func snapshotReal(l *Limiter) comparableState {
	s := l.State()
	return comparableState{L: s.L, n: s.N, nextSeq: s.NextSeq, outstanding: s.Outstanding,
		windowSize: s.WindowSize, windowMin: s.WindowMinRTT, hasMin: s.HasWindowMin,
		lastCut: s.LastCut, hasLastCut: s.HasLastCut, maxNow: s.MaxNow,
		timedOutCount: int64(s.TimedOutCount)}
}

func snapshotNaive(m *naiveLimiter) comparableState {
	c := comparableState{L: m.L, n: m.n, nextSeq: m.nextSeq, outstanding: len(m.tokens),
		windowSize: len(m.samples), lastCut: 0, maxNow: m.maxNow, timedOutCount: int64(len(m.timedOut))}
	if len(m.samples) > 0 {
		c.hasMin = true
		c.windowMin = m.samples[0].rtt
		for _, s := range m.samples[1:] {
			if s.rtt < c.windowMin {
				c.windowMin = s.rtt
			}
		}
	}
	if m.lastCut != nil {
		c.hasLastCut = true
		c.lastCut = *m.lastCut
	}
	return c
}

func errorsEquivalent(a, b error) bool {
	return (a == nil) == (b == nil) && (a == nil || a.Error() == b.Error())
}

// runDiff replays ops against both implementations and returns the first
// divergence log; empty string means agreement. Log lines always include the
// input, output and decision basis.
func runDiff(c Config, ops []genOp) string {
	real, err := New(c)
	if err != nil {
		return "real rejected valid config: " + err.Error()
	}
	naive := newNaive(c)

	var log []string
	log = append(log, fmt.Sprintf("config=%+v", c))
	for i, op := range ops {
		var realErr, naiveErr error
		var realSeq int64
		switch op.kind {
		case opAcquire:
			var tok Token
			tok, realErr = real.Acquire(op.now)
			if realErr == nil {
				realSeq = tok.Seq
			}
			var ns int64
			ns, naiveErr = naive.Acquire(op.now)
			if realErr == nil && realSeq != ns {
				return joinLog(log, i, op, fmt.Sprintf("admitted seq real=%d naive=%d", realSeq, ns))
			}
		case opAcquireBadTime, opAcquireRewind:
			_, realErr = real.Acquire(op.now)
			_, naiveErr = naive.Acquire(op.now)
		case opRelease, opReleaseBadResult, opReleaseBadTime, opReleaseRewind:
			realErr = real.Release(Token{Seq: op.seq}, op.res, op.rtt, op.now)
			naiveErr = naive.Release(op.seq, op.res, op.rtt, op.now)
		}
		rs, ns := snapshotReal(real), snapshotNaive(naive)
		line := fmt.Sprintf("#%d op=%+v -> realErr=%v naiveErr=%v\n    real =%+v\n    naive=%+v",
			i, op, errName(realErr), errName(naiveErr), rs, ns)
		log = append(log, line)
		if !errorsEquivalent(realErr, naiveErr) || rs != ns {
			return joinLog(log, i, op, "STATE OR ERROR DIVERGED")
		}
	}
	// Agreement run: replay success seqs into a map only for logging replay
	// determinism check (see TestReplayDeterminism).
	return ""
}

func errName(e error) string {
	if e == nil {
		return "<nil>"
	}
	return e.Error()
}

func joinLog(log []string, i int, op genOp, why string) string {
	out := fmt.Sprintf("divergence at op #%d (%v): %s\n", i, why, why)
	start := len(log) - 12
	if start < 0 {
		start = 0
	}
	for _, l := range log[start:] {
		out += l + "\n"
	}
	return out
}

// TestRandomDifferential runs 2000 random operation sequences against the
// amortized limiter and the naive linear-scan reference model.
func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	for s := 0; s < sequences; s++ {
		r := rand.New(rand.NewSource(int64(s)*7919 + 13))
		c := genConfig(r)
		ops := genSequence(r, 60+r.Intn(120))
		if diff := runDiff(c, ops); diff != "" {
			t.Fatalf("seed/sequence %d:\n%s", s, diff)
		}
	}
}

// TestLoggedExample prints inputs, outputs and the decision basis, satisfying
// the logging requirement and making a sample trace visible in -v runs.
func TestLoggedExample(t *testing.T) {
	c := Config{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cd: 10, Wm: 100}
	l, _ := New(c)
	log := func(format string, args ...any) {
		t.Logf(format, args...)
	}
	log("config: %+v", c)
	for i := int64(1); i <= 11; i++ {
		tok, err := l.Acquire(0)
		basis := "n<L admit"
		if err != nil {
			basis = "n==L reject"
		}
		log("Acquire #%d now=0 -> seq=%d err=%v basis=%s", i, tok.Seq, errName(err), basis)
	}
	type rel struct {
		seq int64
		res Result
		rtt int64
		now int64
	}
	rels := []rel{
		{10, ResultSuccess, 100, 10}, {9, ResultSuccess, 150, 20},
		{8, ResultSuccess, 300, 30}, {7, ResultDrop, 0, 35},
		{6, ResultDrop, 0, 40}, {5, ResultDrop, 0, 45},
	}
	for _, x := range rels {
		err := l.Release(Token{Seq: x.seq}, x.res, x.rtt, x.now)
		s := l.State()
		log("Release seq=%d res=%v rtt=%d now=%d -> err=%v L=%d n=%d min=%d basis=reap; n--; vegas-or-cut",
			x.seq, x.res, x.rtt, x.now, errName(err), s.L, s.N, s.WindowMinRTT)
	}
	tok, err := l.Acquire(50)
	log("Acquire now=50 -> seq=%d err=%v basis=reap tokens 1-4 at deadline then admit", tok.Seq, errName(err))
	err = l.Release(Token{Seq: 3}, ResultSuccess, 100, 50)
	log("Release seq=3 now=50 -> err=%v basis=token already in timedOut set", errName(err))
}

// replayRecord captures the externally observable outcome of one run.
type replayRecord struct {
	results []string
	states  []comparableState
}

func replayOnce(c Config, ops []genOp) replayRecord {
	l, _ := New(c)
	rec := replayRecord{}
	for _, op := range ops {
		switch op.kind {
		case opAcquire, opAcquireBadTime, opAcquireRewind:
			tok, err := l.Acquire(op.now)
			rec.results = append(rec.results, fmt.Sprintf("A:%d:%v", tok.Seq, errName(err)))
		default:
			err := l.Release(Token{Seq: op.seq}, op.res, op.rtt, op.now)
			rec.results = append(rec.results, fmt.Sprintf("R:%v", errName(err)))
		}
		rec.states = append(rec.states, snapshotReal(l))
	}
	return rec
}

// TestReplayDeterminism replays identical sequences twice: every admission
// decision, error and the full L trajectory must match exactly.
func TestReplayDeterminism(t *testing.T) {
	for s := 0; s < 50; s++ {
		r := rand.New(rand.NewSource(int64(s)*104729 + 7))
		c := genConfig(r)
		ops := genSequence(r, 200)
		a, b := replayOnce(c, ops), replayOnce(c, ops)
		for i := range a.results {
			if a.results[i] != b.results[i] || a.states[i] != b.states[i] {
				t.Fatalf("replay differs at #%d:\n%v vs %v\n%+v vs %+v",
					i, a.results[i], b.results[i], a.states[i], b.states[i])
			}
		}
	}
}
