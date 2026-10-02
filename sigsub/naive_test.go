package sigsub

// This file holds a deliberately naive reference implementation of the
// specification (linear scans over signals and threads, plain maps and
// slices) plus a randomized differential test that replays 2000 random
// operation sequences against the real implementation and compares every
// return value and every queryable state after every step.

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

var naiveSync = map[int]bool{4: true, 7: true, 8: true, 11: true}
var naiveDefaultIgnore = map[int]bool{17: true, 23: true, 28: true}

func naiveBit(sig int) uint64 { return uint64(1) << uint(sig-1) }

type naiveSub struct {
	quota      int
	masks      []uint64
	stacks     [][]uint64
	privStd    []map[int]bool
	privRT     []map[int][]int64
	sharedStd  map[int]bool
	sharedRT   map[int][]int64
	actions    map[int]Action
	curr       int
	rtq        int
	lost       uint64
	terminated bool
}

func newNaive(quota int, leaderMask uint64) *naiveSub {
	n := &naiveSub{
		quota:     quota,
		curr:      1,
		sharedStd: map[int]bool{},
		sharedRT:  map[int][]int64{},
		actions:   map[int]Action{},
	}
	n.masks = append(n.masks, leaderMask&^naiveBit(SigKill))
	n.stacks = append(n.stacks, nil)
	n.privStd = append(n.privStd, map[int]bool{})
	n.privRT = append(n.privRT, map[int][]int64{})
	return n
}

func (n *naiveSub) nthreads() int { return len(n.masks) }

func (n *naiveSub) isIgnored(sig int) bool {
	a, ok := n.actions[sig]
	if !ok {
		return naiveDefaultIgnore[sig]
	}
	switch a.Kind {
	case ActionIgnore:
		return true
	case ActionDefault:
		return naiveDefaultIgnore[sig]
	}
	return false
}

func (n *naiveSub) addThread(mask uint64) (int, error) {
	if n.nthreads() >= MaxThreads {
		return 0, ErrTooManyThreads
	}
	if n.terminated {
		return 0, ErrProcessTerminated
	}
	n.masks = append(n.masks, mask&^naiveBit(SigKill))
	n.stacks = append(n.stacks, nil)
	n.privStd = append(n.privStd, map[int]bool{})
	n.privRT = append(n.privRT, map[int][]int64{})
	return n.nthreads(), nil
}

func (n *naiveSub) setMask(tid int, mask uint64) error {
	if tid < 1 || tid > n.nthreads() {
		return ErrThreadNotExist
	}
	if n.terminated {
		return ErrProcessTerminated
	}
	n.masks[tid-1] = mask &^ naiveBit(SigKill)
	return nil
}

func (n *naiveSub) setAction(sig int, a Action) error {
	if sig < MinSig || sig > MaxSig || sig == SigKill {
		return ErrInvalidParam
	}
	if n.terminated {
		return ErrProcessTerminated
	}
	n.actions[sig] = a
	if !n.isIgnored(sig) {
		return nil
	}
	// Flush every instance of sig from the shared and all private sets.
	if sig <= MaxStandard {
		delete(n.sharedStd, sig)
		for t := 0; t < n.nthreads(); t++ {
			delete(n.privStd[t], sig)
		}
		return nil
	}
	n.rtq -= len(n.sharedRT[sig])
	delete(n.sharedRT, sig)
	for t := 0; t < n.nthreads(); t++ {
		n.rtq -= len(n.privRT[t][sig])
		delete(n.privRT[t], sig)
	}
	return nil
}

func (n *naiveSub) pickTarget(sig int) int {
	if sig == SigKill || n.masks[0]&naiveBit(sig) == 0 {
		return 1
	}
	for i := 0; i < n.nthreads(); i++ {
		tid := (n.curr-1+i)%n.nthreads() + 1
		if n.masks[tid-1]&naiveBit(sig) == 0 {
			n.curr = tid
			return tid
		}
	}
	return 0
}

func (n *naiveSub) send(sig int, value int64) (SendResult, int, error) {
	if sig < MinSig || sig > MaxSig {
		return 0, 0, ErrInvalidParam
	}
	if n.terminated {
		return 0, 0, ErrProcessTerminated
	}
	if n.isIgnored(sig) && n.masks[0]&naiveBit(sig) == 0 {
		return SendDropped, 0, nil
	}
	if sig <= MaxStandard {
		if n.sharedStd[sig] {
			n.lost++
			return SendMerged, 0, nil
		}
		n.sharedStd[sig] = true
	} else {
		if n.rtq >= n.quota {
			return 0, 0, ErrAgain
		}
		n.sharedRT[sig] = append(n.sharedRT[sig], value)
		n.rtq++
	}
	return SendEnqueued, n.pickTarget(sig), nil
}

func (n *naiveSub) sendTo(tid, sig int, value int64) (SendResult, int, error) {
	if sig < MinSig || sig > MaxSig {
		return 0, 0, ErrInvalidParam
	}
	if tid < 1 || tid > n.nthreads() {
		return 0, 0, ErrThreadNotExist
	}
	if n.terminated {
		return 0, 0, ErrProcessTerminated
	}
	if n.isIgnored(sig) && n.masks[tid-1]&naiveBit(sig) == 0 {
		return SendDropped, 0, nil
	}
	if sig <= MaxStandard {
		if n.privStd[tid-1][sig] {
			n.lost++
			return SendMerged, 0, nil
		}
		n.privStd[tid-1][sig] = true
	} else {
		if n.rtq >= n.quota {
			return 0, 0, ErrAgain
		}
		n.privRT[tid-1][sig] = append(n.privRT[tid-1][sig], value)
		n.rtq++
	}
	return SendEnqueued, tid, nil
}

// naivePick scans signals 1..64 linearly: smallest unmasked synchronous
// signal first, otherwise the smallest unmasked pending signal.
func naivePick(mask uint64, std map[int]bool, rt map[int][]int64) (int, bool) {
	best, bestSync := -1, -1
	for sig := MinSig; sig <= MaxSig; sig++ {
		if mask&naiveBit(sig) != 0 {
			continue
		}
		pending := std[sig] || len(rt[sig]) > 0
		if !pending {
			continue
		}
		if naiveSync[sig] {
			if bestSync == -1 {
				bestSync = sig
			}
		} else if best == -1 {
			best = sig
		}
	}
	if bestSync != -1 {
		return bestSync, true
	}
	if best != -1 {
		return best, true
	}
	return 0, false
}

func (n *naiveSub) killPending() bool {
	if n.sharedStd[SigKill] {
		return true
	}
	for t := 0; t < n.nthreads(); t++ {
		if n.privStd[t][SigKill] {
			return true
		}
	}
	return false
}

func (n *naiveSub) deliver(tid int) (DeliverResult, error) {
	if tid < 1 || tid > n.nthreads() {
		return DeliverResult{}, ErrThreadNotExist
	}
	if n.terminated {
		return DeliverResult{}, ErrProcessTerminated
	}
	if n.killPending() {
		n.terminated = true
		return DeliverResult{Kind: DeliverTerminated, Sig: SigKill}, nil
	}
	discarded := 0
	for {
		shared := false
		sig, ok := naivePick(n.masks[tid-1], n.privStd[tid-1], n.privRT[tid-1])
		if !ok {
			sig, ok = naivePick(n.masks[tid-1], n.sharedStd, n.sharedRT)
			shared = true
		}
		if !ok {
			return DeliverResult{Kind: DeliverNone, Discarded: discarded}, nil
		}
		var value int64
		if sig <= MaxStandard {
			if shared {
				delete(n.sharedStd, sig)
			} else {
				delete(n.privStd[tid-1], sig)
			}
		} else {
			if shared {
				value = n.sharedRT[sig][0]
				n.sharedRT[sig] = n.sharedRT[sig][1:]
			} else {
				value = n.privRT[tid-1][sig][0]
				n.privRT[tid-1][sig] = n.privRT[tid-1][sig][1:]
			}
			n.rtq--
		}
		switch {
		case n.isIgnored(sig):
			discarded++
			continue
		case n.actions[sig].Kind == ActionDefault:
			n.terminated = true
			return DeliverResult{Kind: DeliverTerminated, Sig: sig, Discarded: discarded}, nil
		default:
			a := n.actions[sig]
			n.stacks[tid-1] = append(n.stacks[tid-1], n.masks[tid-1])
			nm := n.masks[tid-1] | a.Mask
			if !a.Nodefer {
				nm |= naiveBit(sig)
			}
			n.masks[tid-1] = nm &^ naiveBit(SigKill)
			return DeliverResult{
				Kind:      DeliverHandler,
				Handler:   a.Handler,
				Sig:       sig,
				Value:     value,
				Shared:    shared,
				Discarded: discarded,
			}, nil
		}
	}
}

func (n *naiveSub) sigreturn(tid int) error {
	if tid < 1 || tid > n.nthreads() {
		return ErrThreadNotExist
	}
	if n.terminated {
		return ErrProcessTerminated
	}
	st := n.stacks[tid-1]
	if len(st) == 0 {
		return ErrNoHandler
	}
	n.masks[tid-1] = st[len(st)-1]
	n.stacks[tid-1] = st[:len(st)-1]
	return nil
}

func naiveList(std map[int]bool, rt map[int][]int64) []PendingEntry {
	var out []PendingEntry
	for sig := range std {
		out = append(out, PendingEntry{Sig: sig, Count: 1})
	}
	for sig, q := range rt {
		if len(q) > 0 {
			out = append(out, PendingEntry{Sig: sig, Count: len(q)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sig < out[j].Sig })
	return out
}

func (n *naiveSub) pending(tid int) []PendingEntry {
	return naiveList(n.privStd[tid-1], n.privRT[tid-1])
}

func (n *naiveSub) sharedPending() []PendingEntry {
	return naiveList(n.sharedStd, n.sharedRT)
}

// --- differential test ---

func sendStr(res SendResult, tgt int, err error) string {
	return fmt.Sprintf("(%v,tgt=%d,err=%v)", res, tgt, err)
}

func deliverStr(d DeliverResult, err error) string {
	return fmt.Sprintf("(%s,err=%v)", d, err)
}

func pendingStr(list []PendingEntry) string {
	var b strings.Builder
	b.WriteByte('[')
	for _, e := range list {
		fmt.Fprintf(&b, "%d:%d ", e.Sig, e.Count)
	}
	b.WriteByte(']')
	return b.String()
}

// stateStr renders every queryable state of the real subsystem.
func stateStr(s *Sub, nthreads int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "curr=%d rtq=%d lost=%d shared=%s",
		s.Curr(), s.RTQ(), s.Lost(), pendingStr(s.SharedPending()))
	for tid := 1; tid <= nthreads; tid++ {
		p, _ := s.Pending(tid)
		m, _ := s.Mask(tid)
		fmt.Fprintf(&b, " t%d=%s/%#x", tid, pendingStr(p), m)
	}
	return b.String()
}

// naiveStateStr renders the same state of the naive model.
func naiveStateStr(n *naiveSub) string {
	var b strings.Builder
	fmt.Fprintf(&b, "curr=%d rtq=%d lost=%d shared=%s",
		n.curr, n.rtq, n.lost, pendingStr(n.sharedPending()))
	for tid := 1; tid <= n.nthreads(); tid++ {
		fmt.Fprintf(&b, " t%d=%s/%#x", tid, pendingStr(n.pending(tid)), n.masks[tid-1])
	}
	return b.String()
}

// checkInvariants verifies the global invariants on the real subsystem.
func checkInvariants(t *testing.T, s *Sub, nthreads, quota int) {
	t.Helper()
	rtq := s.RTQ()
	if rtq < 0 || rtq > quota {
		t.Fatalf("invariant violated: RTQ=%d outside [0,%d]", rtq, quota)
	}
	rtSum := 0
	pending := 0
	countSet := func(list []PendingEntry) {
		for _, e := range list {
			if e.Sig > MaxStandard {
				rtSum += e.Count
				pending += e.Count
			} else {
				if e.Count != 1 {
					t.Fatalf("invariant violated: standard sig %d count %d", e.Sig, e.Count)
				}
				pending++
			}
		}
	}
	countSet(s.SharedPending())
	for tid := 1; tid <= nthreads; tid++ {
		p, _ := s.Pending(tid)
		countSet(p)
		m, _ := s.Mask(tid)
		if m&bit(SigKill) != 0 {
			t.Fatalf("invariant violated: thread %d mask has kill bit", tid)
		}
	}
	if rtSum != rtq {
		t.Fatalf("invariant violated: RTQ=%d but queues hold %d", rtq, rtSum)
	}
	// Conservation: enqueued == delivered + discarded + flushed + pending.
	want := s.delivered + s.discarded + s.flushed + uint64(pending)
	if s.enqueued != want {
		t.Fatalf("invariant violated: enqueued=%d != delivered+discarded+flushed+pending=%d",
			s.enqueued, want)
	}
}

type randGen struct{ r *rand.Rand }

func (g randGen) sig() int {
	// Bias towards interesting signals: sync, default-ignore, kill,
	// realtime boundary, plus general coverage.
	switch g.r.Intn(10) {
	case 0:
		return []int{4, 7, 8, 11}[g.r.Intn(4)]
	case 1:
		return []int{17, 23, 28}[g.r.Intn(3)]
	case 2:
		return []int{9, 31, 32, 64}[g.r.Intn(4)]
	case 3:
		return 32 + g.r.Intn(10) // realtime
	default:
		return 1 + g.r.Intn(31) // standard
	}
}

func (g randGen) mask() uint64 {
	var m uint64
	for i := 0; i < g.r.Intn(5); i++ {
		m |= bit(g.sig())
	}
	return m
}

func (g randGen) action() Action {
	switch g.r.Intn(4) {
	case 0:
		return DefaultAction()
	case 1:
		return IgnoreAction()
	default:
		return HandlerAction(g.r.Intn(20), g.mask(), g.r.Intn(2) == 0)
	}
}

func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		g := randGen{r: rand.New(rand.NewSource(int64(seq)*2654435761 + 97))}
		quota := []int{0, 1, 2, 3, 5, 8, 16}[g.r.Intn(7)]
		leaderMask := g.mask()
		real, err := New(quota, leaderMask)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		naive := newNaive(quota, leaderMask)
		nthreads := 1

		ops := 30 + g.r.Intn(50)
		for step := 0; step < ops; step++ {
			var input, outReal, outNaive string
			switch g.r.Intn(12) {
			case 0, 1, 2: // Send
				sig, value := g.sig(), int64(g.r.Intn(1000))
				input = fmt.Sprintf("Send(%d,%d)", sig, value)
				r1, t1, e1 := real.Send(sig, value)
				r2, t2, e2 := naive.send(sig, value)
				outReal, outNaive = sendStr(r1, t1, e1), sendStr(r2, t2, e2)
			case 3, 4: // SendTo
				tid := 1 + g.r.Intn(nthreads+1) // may be invalid
				sig, value := g.sig(), int64(g.r.Intn(1000))
				input = fmt.Sprintf("SendTo(%d,%d,%d)", tid, sig, value)
				r1, t1, e1 := real.SendTo(tid, sig, value)
				r2, t2, e2 := naive.sendTo(tid, sig, value)
				outReal, outNaive = sendStr(r1, t1, e1), sendStr(r2, t2, e2)
			case 5, 6: // Deliver
				tid := 1 + g.r.Intn(nthreads+1)
				input = fmt.Sprintf("Deliver(%d)", tid)
				d1, e1 := real.Deliver(tid)
				d2, e2 := naive.deliver(tid)
				outReal, outNaive = deliverStr(d1, e1), deliverStr(d2, e2)
			case 7: // SetAction
				sig, a := g.sig(), g.action()
				input = fmt.Sprintf("SetAction(%d,%+v)", sig, a)
				e1 := real.SetAction(sig, a)
				e2 := naive.setAction(sig, a)
				outReal, outNaive = fmt.Sprintf("err=%v", e1), fmt.Sprintf("err=%v", e2)
			case 8: // SetMask
				tid := 1 + g.r.Intn(nthreads+1)
				m := g.mask()
				input = fmt.Sprintf("SetMask(%d,%#x)", tid, m)
				e1 := real.SetMask(tid, m)
				e2 := naive.setMask(tid, m)
				outReal, outNaive = fmt.Sprintf("err=%v", e1), fmt.Sprintf("err=%v", e2)
			case 9: // Sigreturn
				tid := 1 + g.r.Intn(nthreads+1)
				input = fmt.Sprintf("Sigreturn(%d)", tid)
				e1 := real.Sigreturn(tid)
				e2 := naive.sigreturn(tid)
				outReal, outNaive = fmt.Sprintf("err=%v", e1), fmt.Sprintf("err=%v", e2)
			case 10: // AddThread
				m := g.mask()
				input = fmt.Sprintf("AddThread(%#x)", m)
				id1, e1 := real.AddThread(m)
				id2, e2 := naive.addThread(m)
				if e1 == nil && e2 == nil {
					nthreads++
				}
				outReal = fmt.Sprintf("tid=%d,err=%v", id1, e1)
				outNaive = fmt.Sprintf("tid=%d,err=%v", id2, e2)
			default: // query-only step
				input = "Queries"
				outReal = stateStr(real, nthreads)
				outNaive = naiveStateStr(naive)
			}

			stReal, stNaive := stateStr(real, nthreads), naiveStateStr(naive)
			verdict := "一致"
			if outReal != outNaive || stReal != stNaive {
				verdict = "不一致"
			}
			t.Logf("seq=%d step=%d 输入=%s 输出 real=%s naive=%s 状态一致=%v 判定=%s",
				seq, step, input, outReal, outNaive, stReal == stNaive, verdict)
			if outReal != outNaive {
				t.Fatalf("seq %d step %d %s: output mismatch\n real=%s\nnaive=%s",
					seq, step, input, outReal, outNaive)
			}
			if stReal != stNaive {
				t.Fatalf("seq %d step %d %s: state mismatch\n real=%s\nnaive=%s",
					seq, step, input, stReal, stNaive)
			}
			checkInvariants(t, real, nthreads, quota)
		}
		t.Logf("seq=%d 完成: quota=%d threads=%d ops=%d 判定=一致", seq, quota, nthreads, ops)
	}
}
