package redolog

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// sim is a deliberately naive reference implementation written directly from
// the specification. It recomputes Ready by scanning from the first interval
// on every query and keeps waiters in an unordered list, so it shares no
// logic with the incremental Buffer implementation.
type sim struct {
	l0, b, cap, mx int64
	r, fd          int64
	ivs            []simInterval
	waiters        []simWaiter
	nextSeq        int64
	total          int64
}

type simInterval struct {
	start, end int64
	done       bool
}

type simWaiter struct {
	w, end, seq int64
}

func newSim(l0, b, cap, mx int64) (*sim, error) {
	if l0 < 0 || l0 > maxL0 || b < 1 || b > maxBlockSize ||
		cap < 1 || cap > maxCapacity || mx < 1 || mx > maxMaxBlocks {
		return nil, ErrInvalidParam
	}
	return &sim{l0: l0, b: b, cap: cap, mx: mx, r: l0, fd: l0}, nil
}

func (s *sim) ready() int64 {
	ready := s.l0
	for _, iv := range s.ivs {
		if !iv.done {
			break
		}
		ready = iv.end
	}
	return ready
}

func (s *sim) reserve(n int64) (int64, int64, error) {
	if n < 1 || n > s.cap {
		return 0, 0, ErrInvalidParam
	}
	if s.r+n-s.fd > s.cap {
		return 0, 0, ErrBufferFull
	}
	start, end := s.r, s.r+n
	s.ivs = append(s.ivs, simInterval{start: start, end: end})
	s.r = end
	return start, end, nil
}

func (s *sim) complete(start int64) error {
	for i := range s.ivs {
		if s.ivs[i].start == start {
			if s.ivs[i].done {
				return ErrIntervalCompleted
			}
			s.ivs[i].done = true
			return nil
		}
	}
	return ErrIntervalNotFound
}

func (s *sim) wait(w, end int64) (bool, error) {
	if w < 0 {
		return false, ErrInvalidParam
	}
	found := false
	for _, iv := range s.ivs {
		if iv.end == end {
			found = true
			break
		}
	}
	if !found {
		return false, ErrEndNotFound
	}
	if end <= s.fd {
		return true, nil
	}
	for _, wt := range s.waiters {
		if wt.w == w {
			return false, ErrWaiterExists
		}
	}
	s.waiters = append(s.waiters, simWaiter{w: w, end: end, seq: s.nextSeq})
	s.nextSeq++
	return false, nil
}

func (s *sim) flush(force bool) (int64, int64, []int64) {
	target := s.ready()
	if !force {
		target = target / s.b * s.b
	}
	if target <= s.fd {
		return 0, s.fd, []int64{}
	}
	ceil := (target + s.b - 1) / s.b
	if ceil-s.fd/s.b > s.mx {
		target = (s.fd/s.b + s.mx) * s.b
	}
	blocks := (target+s.b-1)/s.b - s.fd/s.b
	s.fd = target
	s.total += blocks

	var hits []simWaiter
	var rest []simWaiter
	for _, wt := range s.waiters {
		if wt.end <= s.fd {
			hits = append(hits, wt)
		} else {
			rest = append(rest, wt)
		}
	}
	s.waiters = rest
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].end != hits[j].end {
			return hits[i].end < hits[j].end
		}
		return hits[i].seq < hits[j].seq
	})
	woken := []int64{}
	for _, h := range hits {
		woken = append(woken, h.w)
	}
	return blocks, s.fd, woken
}

// rndOp is one randomized operation in a replayable sequence.
type rndOp struct {
	kind int // 0=Reserve 1=Complete 2=Wait 3=Flush
	a, b int64
}

func (o rndOp) String() string {
	switch o.kind {
	case 0:
		return fmt.Sprintf("Reserve(%d)", o.a)
	case 1:
		return fmt.Sprintf("Complete(%d)", o.a)
	case 2:
		return fmt.Sprintf("Wait(%d,%d)", o.a, o.b)
	default:
		return fmt.Sprintf("Flush(force=%v)", o.a == 1)
	}
}

// opResult captures everything an operation may return, for comparison.
type opResult struct {
	start, end int64
	err        error
	sat        bool
	blocks, fd int64
	woken      []int64
	reason     string
}

func runImpl(buf *Buffer, o rndOp) opResult {
	switch o.kind {
	case 0:
		start, end, err := buf.Reserve(o.a)
		r := opResult{start: start, end: end, err: err}
		switch {
		case err == ErrInvalidParam:
			r.reason = "rejected: n not in [1,Cap]"
		case err == ErrBufferFull:
			r.reason = "rejected: R+n-Fd > Cap"
		default:
			r.reason = fmt.Sprintf("reserved [%d,%d), R+n-Fd <= Cap", start, end)
		}
		return r
	case 1:
		err := buf.Complete(o.a)
		r := opResult{err: err}
		switch err {
		case nil:
			r.reason = "interval marked completed"
		case ErrIntervalNotFound:
			r.reason = "rejected: start is not any interval start"
		default:
			r.reason = "rejected: interval already completed"
		}
		return r
	case 2:
		sat, err := buf.Wait(o.a, o.b)
		r := opResult{sat: sat, err: err}
		switch {
		case err == ErrInvalidParam:
			r.reason = "rejected: negative waiter id"
		case err == ErrEndNotFound:
			r.reason = "rejected: end is not any interval end"
		case err == ErrWaiterExists:
			r.reason = "rejected: waiter already pending"
		case sat:
			r.reason = "satisfied: end <= Fd, not registered"
		default:
			r.reason = "registered as pending waiter"
		}
		return r
	default:
		blocks, fd, woken := buf.Flush(o.a == 1)
		r := opResult{blocks: blocks, fd: fd, woken: woken}
		switch {
		case blocks == 0:
			r.reason = "no-op: target <= Fd"
		default:
			r.reason = fmt.Sprintf("wrote %d block(s), Fd=%d, woke %v", blocks, fd, woken)
		}
		return r
	}
}

func runSim(s *sim, o rndOp) opResult {
	switch o.kind {
	case 0:
		start, end, err := s.reserve(o.a)
		return opResult{start: start, end: end, err: err}
	case 1:
		return opResult{err: s.complete(o.a)}
	case 2:
		sat, err := s.wait(o.a, o.b)
		return opResult{sat: sat, err: err}
	default:
		blocks, fd, woken := s.flush(o.a == 1)
		return opResult{blocks: blocks, fd: fd, woken: woken}
	}
}

func sameResult(x, y opResult) bool {
	return x.start == y.start && x.end == y.end && x.err == y.err &&
		x.sat == y.sat && x.blocks == y.blocks && x.fd == y.fd &&
		reflect.DeepEqual(x.woken, y.woken)
}

// TestRandomAgainstSimulation replays 2000 randomized operation sequences
// against both the real buffer (twice, to prove replay determinism) and the
// naive simulation, comparing every output and every water mark after each
// single operation. Inputs, outputs and the deciding rule are logged.
func TestRandomAgainstSimulation(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*2654435761 + 97))

		blkChoices := []int64{1, 2, 3, 5, 8, 16, 100, 512, 1024, 65536}
		blk := blkChoices[rng.Intn(len(blkChoices))]
		var l0 int64
		switch rng.Intn(3) {
		case 0:
			l0 = 0
		case 1:
			l0 = rng.Int63n(1_000_000)
		default:
			l0 = rng.Int63n(1000) * blk // block-aligned start
		}
		cap := 1 + rng.Int63n(4096)
		mx := int64(1 + rng.Intn(8))

		impl1, err := NewBuffer(l0, blk, cap, mx)
		if err != nil {
			t.Fatalf("seq=%d NewBuffer failed: %v", seq, err)
		}
		impl2, err := NewBuffer(l0, blk, cap, mx)
		if err != nil {
			t.Fatalf("seq=%d NewBuffer failed: %v", seq, err)
		}
		ref, err := newSim(l0, blk, cap, mx)
		if err != nil {
			t.Fatalf("seq=%d newSim failed: %v", seq, err)
		}
		t.Logf("seq=%d params: L0=%d B=%d Cap=%d Mx=%d", seq, l0, blk, cap, mx)

		var starts, ends []int64
		const ops = 60
		for i := 0; i < ops; i++ {
			var o rndOp
			switch rng.Intn(100) {
			case 0, 1, 2, 3: // invalid n
				o = rndOp{kind: 0, a: rng.Int63n(4) - 2}
			case 4, 5, 6: // likely too large n
				o = rndOp{kind: 0, a: cap + rng.Int63n(3)}
			case 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
				17, 18, 19, 20, 21, 22, 23, 24, 25, 26,
				27, 28, 29, 30, 31, 32, 33, 34, 35, 36:
				o = rndOp{kind: 0, a: 1 + rng.Int63n(cap)}
			case 37, 38, 39, 40, 41, 42, 43, 44, 45, 46,
				47, 48, 49, 50, 51, 52, 53, 54, 55, 56,
				57, 58, 59, 60, 61, 62, 63, 64, 65, 66:
				if len(starts) > 0 && rng.Intn(4) > 0 {
					o = rndOp{kind: 1, a: starts[rng.Intn(len(starts))]}
				} else {
					o = rndOp{kind: 1, a: rng.Int63n(l0 + cap + 1)}
				}
			case 67, 68, 69, 70, 71, 72, 73, 74, 75, 76,
				77, 78, 79, 80, 81, 82, 83, 84, 85, 86:
				w := rng.Int63n(12) - 1 // sometimes negative, small range forces duplicates
				if len(ends) > 0 && rng.Intn(4) > 0 {
					o = rndOp{kind: 2, a: w, b: ends[rng.Intn(len(ends))]}
				} else {
					o = rndOp{kind: 2, a: w, b: rng.Int63n(l0 + cap + 1)}
				}
			default:
				o = rndOp{kind: 3, a: int64(rng.Intn(2))}
			}

			got1 := runImpl(impl1, o)
			got2 := runImpl(impl2, o)
			want := runSim(ref, o)

			if o.kind == 0 && got1.err == nil {
				starts = append(starts, got1.start)
				ends = append(ends, got1.end)
			}

			t.Logf("seq=%d op=%d %s => err=%v sat=%v start=%d end=%d blocks=%d fd=%d woken=%v | basis: %s",
				seq, i, o, got1.err, got1.sat, got1.start, got1.end, got1.blocks, got1.fd, got1.woken, got1.reason)

			if !sameResult(got1, want) {
				t.Fatalf("seq=%d op=%d %s: impl=%+v sim=%+v", seq, i, o, got1, want)
			}
			if !sameResult(got1, got2) {
				t.Fatalf("seq=%d op=%d %s: replay mismatch %+v vs %+v", seq, i, o, got1, got2)
			}

			// Water marks and counters must agree after every single op.
			if impl1.Ready() != ref.ready() || impl1.Flushed() != ref.fd ||
				impl1.Reserved() != ref.r || impl1.TotalBlocks() != ref.total {
				t.Fatalf("seq=%d op=%d %s: state impl=(R=%d Fd=%d Ready=%d tot=%d) sim=(R=%d Fd=%d Ready=%d tot=%d)",
					seq, i, o,
					impl1.Reserved(), impl1.Flushed(), impl1.Ready(), impl1.TotalBlocks(),
					ref.r, ref.fd, ref.ready(), ref.total)
			}
			// Global invariants.
			fd, ready, r := impl1.Flushed(), impl1.Ready(), impl1.Reserved()
			if !(l0 <= fd && fd <= ready && ready <= r) {
				t.Fatalf("seq=%d op=%d: invariant L0<=Fd<=Ready<=R violated (%d,%d,%d,%d)",
					seq, i, l0, fd, ready, r)
			}
			if r-fd > cap {
				t.Fatalf("seq=%d op=%d: invariant R-Fd<=Cap violated (%d-%d>%d)", seq, i, r, fd, cap)
			}
			if got1.blocks > mx {
				t.Fatalf("seq=%d op=%d: blocks=%d exceeds Mx=%d", seq, i, got1.blocks, mx)
			}
		}
	}
}
