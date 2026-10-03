package redolog

// Randomized differential test: the optimized Buffer is compared against a
// naive simulation written directly from the specification rules. Every
// operation, its outcome and the decision basis are logged via t.Logf.

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// naiveBuffer is a deliberately simple reference implementation.
type naiveBuffer struct {
	l0, b, cap, mx uint64
	r, fd          uint64
	starts         []uint64 // reserved order
	ends           []uint64 // reserved order
	done           []bool   // completion flags, reserved order
	allEnds        []uint64 // every reserved end ever
	waitW          []int64  // pending waiters
	waitEnd        []uint64 // pending waiter ends
	waitSeq        []uint64 // registration sequence
	seq            uint64   // next registration sequence
	total          uint64   // cumulative blocks
}

func newNaive(l0, b, cap, mx uint64) *naiveBuffer {
	return &naiveBuffer{l0: l0, b: b, cap: cap, mx: mx, r: l0, fd: l0}
}

func (n *naiveBuffer) ready() uint64 {
	ready := n.l0
	for i := range n.starts {
		if !n.done[i] {
			break
		}
		ready = n.ends[i]
	}
	return ready
}

func (n *naiveBuffer) reserve(sz uint64) (uint64, uint64, error) {
	if sz < 1 || sz > n.cap {
		return 0, 0, ErrInvalidParam
	}
	if n.r+sz-n.fd > n.cap {
		return 0, 0, ErrBufferFull
	}
	start := n.r
	n.r += sz
	n.starts = append(n.starts, start)
	n.ends = append(n.ends, n.r)
	n.done = append(n.done, false)
	n.allEnds = append(n.allEnds, n.r)
	return start, n.r, nil
}

func (n *naiveBuffer) complete(start uint64) error {
	idx := -1
	for i, s := range n.starts {
		if s == start {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrIntervalNotFound
	}
	if n.done[idx] {
		return ErrAlreadyComplete
	}
	n.done[idx] = true
	return nil
}

func (n *naiveBuffer) wait(w int64, end uint64) (bool, error) {
	if w < 0 {
		return false, ErrInvalidParam
	}
	found := false
	for _, e := range n.allEnds {
		if e == end {
			found = true
			break
		}
	}
	if !found {
		return false, ErrEndNotFound
	}
	if end <= n.fd {
		return true, nil
	}
	for _, x := range n.waitW {
		if x == w {
			return false, ErrDuplicateWaiter
		}
	}
	n.waitW = append(n.waitW, w)
	n.waitEnd = append(n.waitEnd, end)
	n.waitSeq = append(n.waitSeq, n.seq)
	n.seq++
	return false, nil
}

func (n *naiveBuffer) flush(force bool) (uint64, uint64, []int64) {
	ready := n.ready()
	target := ready / n.b * n.b
	if force {
		target = ready
	}
	if target <= n.fd {
		return 0, n.fd, nil
	}
	ceil := func(x uint64) uint64 { return (x + n.b - 1) / n.b }
	if ceil(target)-n.fd/n.b > n.mx {
		target = (n.fd/n.b + n.mx) * n.b
	}
	blocks := ceil(target) - n.fd/n.b
	n.fd = target
	n.total += blocks
	type wokeWaiter struct {
		w   int64
		end uint64
		seq uint64
	}
	var woke []wokeWaiter
	var keepW []int64
	var keepEnd, keepSeq []uint64
	for i := range n.waitW {
		if n.waitEnd[i] <= n.fd {
			woke = append(woke, wokeWaiter{n.waitW[i], n.waitEnd[i], n.waitSeq[i]})
		} else {
			keepW = append(keepW, n.waitW[i])
			keepEnd = append(keepEnd, n.waitEnd[i])
			keepSeq = append(keepSeq, n.waitSeq[i])
		}
	}
	n.waitW, n.waitEnd, n.waitSeq = keepW, keepEnd, keepSeq
	sort.Slice(woke, func(i, j int) bool {
		if woke[i].end != woke[j].end {
			return woke[i].end < woke[j].end
		}
		return woke[i].seq < woke[j].seq
	})
	var ids []int64
	for _, x := range woke {
		ids = append(ids, x.w)
	}
	return blocks, n.fd, ids
}

func checkErr(t *testing.T, op string, got, want error) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: err = %v, naive = %v", op, got, want)
	}
}

func checkWatermarks(t *testing.T, buf *Buffer, nb *naiveBuffer) {
	t.Helper()
	if buf.R() != nb.r || buf.Fd() != nb.fd || buf.Ready() != nb.ready() ||
		buf.TotalBlocks() != nb.total {
		t.Fatalf("state = (R=%d Fd=%d Ready=%d Total=%d), naive = (R=%d Fd=%d Ready=%d Total=%d)",
			buf.R(), buf.Fd(), buf.Ready(), buf.TotalBlocks(),
			nb.r, nb.fd, nb.ready(), nb.total)
	}
}

// TestRandomAgainstNaive replays 2000 random operation sequences against both
// the Buffer and the naive simulation, requiring identical Ready, Fd, block
// counts and wake sequences at every step.
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261207))
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		l0Choices := []uint64{0, 0, 1, 100, 513, 4096, 1_000_000_000_000}
		l0 := l0Choices[rng.Intn(len(l0Choices))]
		blk := uint64(1 << rng.Intn(10)) // 1..512
		if rng.Intn(4) == 0 {
			blk = 1 + uint64(rng.Intn(65536))
		}
		cap := uint64(1 + rng.Intn(4096))
		mx := uint64(1 + rng.Intn(5))
		buf, err := NewBuffer(l0, blk, cap, mx)
		if err != nil {
			t.Fatalf("seq %d: NewBuffer: %v", seq, err)
		}
		nb := newNaive(l0, blk, cap, mx)
		t.Logf("seq=%d new L0=%d B=%d Cap=%d Mx=%d", seq, l0, blk, cap, mx)

		var starts, allEnds []uint64
		ops := 5 + rng.Intn(56)
		for op := 0; op < ops; op++ {
			log := fmt.Sprintf("seq=%d op=%d", seq, op)
			switch rng.Intn(4) {
			case 0: // Reserve
				n := uint64(rng.Intn(int(cap) + 2))
				gs, ge, gerr := buf.Reserve(n)
				ws, we, werr := nb.reserve(n)
				checkErr(t, log+" Reserve", gerr, werr)
				if gerr == nil && (gs != ws || ge != we) {
					t.Fatalf("%s Reserve(%d) = [%d,%d), naive = [%d,%d)",
						log, n, gs, ge, ws, we)
				}
				t.Logf("%s Reserve(%d) -> [%d,%d) err=%v basis: R+n-Fd=%d+%d-%d cap=%d",
					log, n, gs, ge, gerr, nb.r, n, nb.fd, cap)
				if gerr == nil {
					starts = append(starts, gs)
					allEnds = append(allEnds, ge)
				}
			case 1: // Complete
				var s uint64
				if len(starts) > 0 && rng.Intn(4) > 0 {
					s = starts[rng.Intn(len(starts))]
				} else {
					s = l0 + uint64(rng.Intn(int(cap)+2))
				}
				gerr := buf.Complete(s)
				werr := nb.complete(s)
				checkErr(t, log+" Complete", gerr, werr)
				t.Logf("%s Complete(%d) -> err=%v basis: ready=%d",
					log, s, gerr, nb.ready())
			case 2: // Wait
				w := int64(rng.Intn(12)) - 1 // sometimes negative
				var end uint64
				if len(allEnds) > 0 && rng.Intn(4) > 0 {
					end = allEnds[rng.Intn(len(allEnds))]
				} else {
					end = l0 + uint64(rng.Intn(int(cap)+2))
				}
				gsat, gerr := buf.Wait(w, end)
				wsat, werr := nb.wait(w, end)
				checkErr(t, log+" Wait", gerr, werr)
				if gerr == nil && gsat != wsat {
					t.Fatalf("%s Wait(%d,%d) satisfied = %v, naive = %v",
						log, w, end, gsat, wsat)
				}
				t.Logf("%s Wait(%d,%d) -> satisfied=%v err=%v basis: Fd=%d",
					log, w, end, gsat, gerr, nb.fd)
			case 3: // Flush
				force := rng.Intn(2) == 0
				gblk, gfd, gwoke := buf.Flush(force)
				wblk, wfd, wwoke := nb.flush(force)
				if gblk != wblk || gfd != wfd ||
					!reflect.DeepEqual(gwoke, wwoke) {
					t.Fatalf("%s Flush(%v) = (%d,%d,%v), naive = (%d,%d,%v)",
						log, force, gblk, gfd, gwoke, wblk, wfd, wwoke)
				}
				t.Logf("%s Flush(force=%v) -> blocks=%d Fd=%d woke=%v basis: ready=%d target<=%d noop=%v",
					log, force, gblk, gfd, gwoke, nb.ready(), nb.fd, gblk == 0)
			}
			checkWatermarks(t, buf, nb)
		}
	}
}
