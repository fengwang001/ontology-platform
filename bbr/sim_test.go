package bbr

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"sync"
	"testing"
)

// naiveSim is a deliberately naive, step-by-step simulation of the
// specification: the bandwidth filter keeps every recorded sample and
// recomputes the window maximum by scanning, and all BDP arithmetic uses
// big integers. It exists to cross-check the optimized BBR controller.
type naiveSim struct {
	mss uint64

	D, R, N uint64

	samples []sample // every recorded sample; validity checked by scan

	minRtt    uint64
	stamp     uint64
	hasMinRtt bool

	state   State
	filled  bool
	fullBw  uint64
	fullCnt uint64

	ci int
	cs uint64
	pd uint64

	lastNow uint64
	hasAck  bool
}

func newNaiveSim(mss uint64) *naiveSim {
	return &naiveSim{mss: mss, state: Startup}
}

// maxBw scans all recorded samples; a sample from round r counts while
// R-r < filterWindow.
func (s *naiveSim) maxBw() uint64 {
	var m uint64
	for _, sm := range s.samples {
		if s.R-sm.round < filterWindow && sm.bw > m {
			m = sm.bw
		}
	}
	return m
}

func naiveBDP(maxBw, minRtt uint64) *big.Int {
	p := new(big.Int).Mul(new(big.Int).SetUint64(maxBw), new(big.Int).SetUint64(minRtt))
	return p.Div(p, big.NewInt(1000))
}

// ack runs the seven fixed steps exactly as specified.
func (s *naiveSim) ack(now, n, rtt, ds, inflight uint64, appLimited bool) error {
	if now > 1e12 || n < 1 || n > 1e6 || rtt < 1 || rtt > 1e5 || inflight > 1e12 {
		return ErrInvalidParam
	}
	if s.hasAck && now < s.lastNow {
		return ErrClockRewind
	}
	if ds > s.D {
		return ErrInvalidSample
	}

	startState := s.state

	// Step 1.
	s.D += n
	newRound := false
	if ds >= s.N {
		s.R++
		s.N = s.D
		newRound = true
	}
	bwSample := (s.D - ds) * 1000 / rtt

	// Step 2.
	if !appLimited || bwSample >= s.maxBw() {
		s.samples = append(s.samples, sample{round: s.R, bw: bwSample})
	}
	maxBw := s.maxBw()

	// Step 3.
	expired := s.hasMinRtt && now-s.stamp >= minRttExpiry
	if !s.hasMinRtt || rtt <= s.minRtt || expired {
		s.minRtt = rtt
		s.stamp = now
		s.hasMinRtt = true
	}

	// Step 4.
	if newRound && !appLimited && !s.filled {
		if maxBw*100 >= s.fullBw*125 {
			s.fullBw = maxBw
			s.fullCnt = 0
		} else {
			s.fullCnt++
			if s.fullCnt >= 3 {
				s.filled = true
			}
		}
	}
	if s.state == Startup && s.filled {
		s.state = Drain
	}

	// Step 5.
	bdp := naiveBDP(maxBw, s.minRtt)
	bigInflight := new(big.Int).SetUint64(inflight)
	if s.state == Drain && bigInflight.Cmp(bdp) <= 0 {
		s.state = ProbeBW
		s.ci = 2
		s.cs = now
	}

	// Step 6.
	if startState == ProbeBW {
		el := now - s.cs
		advance := false
		switch pacingGainCycle[s.ci] {
		case 100:
			advance = el >= s.minRtt
		case 125:
			threshold := new(big.Int).Mul(bdp, big.NewInt(125))
			threshold.Div(threshold, big.NewInt(100))
			advance = el >= s.minRtt && bigInflight.Cmp(threshold) >= 0
		case 75:
			advance = el >= s.minRtt || bigInflight.Cmp(bdp) <= 0
		}
		if advance {
			s.ci = (s.ci + 1) % 8
			s.cs = now
		}
	}

	// Step 7.
	if expired && s.state != ProbeRTT {
		s.state = ProbeRTT
		s.pd = 0
	}
	if s.state == ProbeRTT {
		if s.pd == 0 && inflight <= 4*s.mss {
			s.pd = now + probeRttWait
		}
		if s.pd != 0 && now >= s.pd {
			s.stamp = now
			if s.filled {
				s.state = ProbeBW
				s.ci = 2
				s.cs = now
			} else {
				s.state = Startup
			}
			s.pd = 0
		}
	}

	s.lastNow = now
	s.hasAck = true
	return nil
}

func (s *naiveSim) pacing() uint64 {
	if !s.hasAck {
		return 0
	}
	var pg uint64
	switch s.state {
	case Startup:
		pg = startupPacing
	case Drain:
		pg = drainPacing
	case ProbeBW:
		pg = pacingGainCycle[s.ci]
	case ProbeRTT:
		pg = 100
	}
	return s.maxBw() * pg / 100
}

func (s *naiveSim) cwnd() uint64 {
	if !s.hasAck {
		return 10 * s.mss
	}
	if s.state == ProbeRTT {
		return 4 * s.mss
	}
	var cg uint64
	switch s.state {
	case Startup, Drain:
		cg = cwndGain
	case ProbeBW:
		cg = probeBWCwnd
	}
	bdp := naiveBDP(s.maxBw(), s.minRtt)
	c := bdp.Mul(bdp, new(big.Int).SetUint64(cg))
	c.Div(c, big.NewInt(100))
	cwnd := c.Uint64()
	if min := 4 * s.mss; cwnd < min {
		cwnd = min
	}
	return cwnd
}

type ackEvent struct {
	now, n, rtt, ds, inflight uint64
	appLimited                bool
}

func (e ackEvent) String() string {
	return fmt.Sprintf("OnAck(now=%d n=%d rtt=%d ds=%d inflight=%d appLimited=%v)",
		e.now, e.n, e.rtt, e.ds, e.inflight, e.appLimited)
}

// genSequence builds a random, mostly valid acknowledgment sequence. A
// small fraction of events is deliberately invalid (bad parameter, clock
// rewind, or ds ahead of D) so rejection paths are compared too.
func genSequence(rng *rand.Rand, mss uint64, length int) []ackEvent {
	seq := make([]ackEvent, 0, length)
	var now, delivered uint64
	for i := 0; i < length; i++ {
		// Time: usually small steps, sometimes a jump across the 10s
		// min-RTT expiry horizon, rarely a clock rewind.
		switch r := rng.Intn(100); {
		case r < 2 && now > 0:
			now-- // clock rewind (rejected)
		case r < 20:
			now += uint64(9000 + rng.Intn(3000))
		default:
			now += uint64(rng.Intn(301))
		}
		if now > 1e12 {
			now = 1e12
		}

		n := uint64(1 + rng.Intn(1e6))
		if delivered > 1e12-n {
			n = 1
		}

		var ds uint64
		switch r := rng.Intn(100); {
		case r < 2:
			ds = delivered + uint64(1+rng.Intn(1000)) // invalid sample
		case r < 40:
			ds = delivered // force a new round
		default:
			if delivered > 0 {
				ds = uint64(rng.Int63n(int64(delivered) + 1))
			}
		}

		rtt := uint64(1 + rng.Intn(200))
		switch rng.Intn(100) {
		case 0:
			rtt = 0 // invalid
		case 1:
			rtt = 1e5 + 1 // invalid
		case 2, 3:
			rtt = uint64(1 + rng.Intn(1e5))
		}

		var inflight uint64
		switch rng.Intn(100) {
		case 0:
			inflight = 1e12 + 1 // invalid
		case 1, 2, 3:
			inflight = uint64(rng.Intn(1e5))
		default:
			inflight = uint64(rng.Int63n(int64(8*mss) + 1))
		}

		ev := ackEvent{
			now: now, n: n, rtt: rtt, ds: ds, inflight: inflight,
			appLimited: rng.Intn(4) == 0,
		}
		seq = append(seq, ev)

		// Track what a valid event would do to keep the sequence sane.
		valid := ev.n >= 1 && ev.n <= 1e6 && ev.rtt >= 1 && ev.rtt <= 1e5 &&
			ev.inflight <= 1e12 && ev.ds <= delivered
		if valid {
			delivered += ev.n
		}
	}
	return seq
}

// stateLine renders the full observable state for comparison and logging.
func stateLine(b *BBR) string {
	minRtt, hasMinRtt := b.MinRtt()
	return fmt.Sprintf("R=%d maxBw=%d minRtt=%d(%v) stamp=%d state=%v filled=%v fullBw=%d fullCnt=%d ci=%d cs=%d pd=%d pacing=%d cwnd=%d",
		b.Round(), b.MaxBw(), minRtt, hasMinRtt, b.stamp, b.State(), b.filled,
		b.fullBw, b.fullCnt, b.ci, b.cs, b.pd, b.Pacing(), b.Cwnd())
}

func simLine(s *naiveSim) string {
	return fmt.Sprintf("R=%d maxBw=%d minRtt=%d(%v) stamp=%d state=%v filled=%v fullBw=%d fullCnt=%d ci=%d cs=%d pd=%d pacing=%d cwnd=%d",
		s.R, s.maxBw(), s.minRtt, s.hasMinRtt, s.stamp, s.state, s.filled,
		s.fullBw, s.fullCnt, s.ci, s.cs, s.pd, s.pacing(), s.cwnd())
}

// reasonClass maps an error to its rejection reason for comparison.
func reasonClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidParam):
		return "invalid-param"
	case errors.Is(err, ErrClockRewind):
		return "clock-rewind"
	case errors.Is(err, ErrInvalidSample):
		return "invalid-sample"
	}
	return "unknown"
}

// TestAgainstNaiveSimulation replays 2000 random acknowledgment sequences
// through both the optimized controller and the naive step-by-step
// simulation, comparing the full state after every event.
func TestAgainstNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(20261220))
	for trial := 0; trial < 2000; trial++ {
		mss := uint64(1 + rng.Intn(65535))
		length := 1 + rng.Intn(40)
		seq := genSequence(rng, mss, length)

		b, err := New(mss)
		if err != nil {
			t.Fatalf("trial %d: New(%d): %v", trial, mss, err)
		}
		sim := newNaiveSim(mss)

		for i, ev := range seq {
			errB := b.OnAck(ev.now, ev.n, ev.rtt, ev.ds, ev.inflight, ev.appLimited)
			errS := sim.ack(ev.now, ev.n, ev.rtt, ev.ds, ev.inflight, ev.appLimited)

			got, want := stateLine(b), simLine(sim)
			t.Logf("trial=%d ack=%d input=%s verdict=%s/%s\n  got : %s\n  want: %s",
				trial, i, ev, reasonClass(errB), reasonClass(errS), got, want)

			if reasonClass(errB) != reasonClass(errS) {
				t.Fatalf("trial %d ack %d (%s): reason %s != %s",
					trial, i, ev, reasonClass(errB), reasonClass(errS))
			}
			if got != want {
				t.Fatalf("trial %d ack %d (%s):\n got : %s\n want: %s",
					trial, i, ev, got, want)
			}
		}
	}
}

// The monotone-queue filter must cost at most 3 operations per ack.
func TestFilterOpsBound(t *testing.T) {
	for _, count := range []int{1000, 100000} {
		rng := rand.New(rand.NewSource(int64(count)))
		b := newBBR(t, 1500)
		var now, delivered uint64
		for i := 0; i < count; i++ {
			now += uint64(rng.Intn(50))
			n := uint64(1 + rng.Intn(1e6))
			if delivered > 1e12-n {
				delivered = 0 // keep D in range by resetting the epoch
			}
			ds := delivered
			if rng.Intn(2) == 0 && delivered > 0 {
				ds = uint64(rng.Int63n(int64(delivered) + 1))
			}
			rtt := uint64(1 + rng.Intn(1000))
			inflight := uint64(rng.Intn(1 << 20))
			if err := b.OnAck(now, n, rtt, ds, inflight, rng.Intn(4) == 0); err != nil {
				t.Fatalf("ack %d: %v", i, err)
			}
			delivered += n
		}
		if limit := uint64(3 * count); b.filterOps > limit {
			t.Fatalf("%d acks: filterOps = %d, want <= %d", count, b.filterOps, limit)
		}
		t.Logf("%d acks: filterOps = %d (limit %d)", count, b.filterOps, 3*count)
	}
}

// Replaying the same sequence must reproduce the exact same trace.
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	mss := uint64(1460)
	seq := genSequence(rng, mss, 500)

	run := func() []string {
		b := newBBR(t, mss)
		trace := make([]string, 0, len(seq))
		for _, ev := range seq {
			err := b.OnAck(ev.now, ev.n, ev.rtt, ev.ds, ev.inflight, ev.appLimited)
			trace = append(trace, reasonClass(err)+"|"+stateLine(b))
		}
		return trace
	}
	first, second := run(), run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("ack %d diverged:\n first : %s\n second: %s", i, first[i], second[i])
		}
	}
}

// OnAck and all queries are safe for concurrent use; the final state after
// a concurrent run equals the sequential replay of the same ack sequence.
func TestConcurrentAccess(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	mss := uint64(1500)
	// Valid-only sequence for the concurrent run.
	var seq []ackEvent
	for _, ev := range genSequence(rng, mss, 3000) {
		seq = append(seq, ev)
	}

	b := newBBR(t, mss)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = b.Pacing()
					_ = b.Cwnd()
					_ = b.State()
					_ = b.MaxBw()
					_, _ = b.MinRtt()
					_ = b.Round()
				}
			}
		}()
	}
	for _, ev := range seq {
		_ = b.OnAck(ev.now, ev.n, ev.rtt, ev.ds, ev.inflight, ev.appLimited)
	}
	close(stop)
	wg.Wait()

	// Sequential replay of the identical sequence must match exactly.
	ref := newBBR(t, mss)
	for _, ev := range seq {
		_ = ref.OnAck(ev.now, ev.n, ev.rtt, ev.ds, ev.inflight, ev.appLimited)
	}
	if got, want := stateLine(b), stateLine(ref); got != want {
		t.Fatalf("concurrent final state mismatch:\n got : %s\n want: %s", got, want)
	}
}
