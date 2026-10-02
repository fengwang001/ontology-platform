package signals

import (
	"fmt"
	"math/bits"
	"math/rand"
	"reflect"
	"testing"
)

type naiveState struct {
	limit              int
	threadCount        int
	masks              [65]uint64
	stacks             [65][]uint64
	stdShared          uint64
	rtShared           map[int][]int
	stdPrivate         [65]uint64
	rtPrivate          [65]map[int][]int
	actions            [65]Action
	curr               int
	lost               int
	examined           int
	terminated         bool
	enqueued           int
	delivered          int
	dropped            int
	immediatelyDropped int
	flushed            int
	killCount          int
}

type naiveOp struct {
	name   string
	tid    int
	sig    int
	value  int
	mask   uint64
	limit  int
	action Action
}

type pendingSnapshot struct {
	Standard uint64
	RT       map[int][]int
}

func (s *Subsystem) sharedPendingSnapshot() pendingSnapshot {
	return pendingSnapshot{Standard: s.shared.std, RT: rtSnapshot(s.shared.rt[:])}
}

func (s *Subsystem) threadPendingSnapshot(tid int) pendingSnapshot {
	target := s.threads[tid]
	return pendingSnapshot{Standard: target.stdPending, RT: rtSnapshot(target.rt[:])}
}

func (s *Subsystem) maskStackSnapshot(tid int) []uint64 {
	return append([]uint64(nil), s.threads[tid].maskStack...)
}

func rtSnapshot(queues [][]int) map[int][]int {
	result := make(map[int][]int)
	for sig := 32; sig <= 64; sig++ {
		if len(queues[sig]) > 0 {
			result[sig] = append([]int(nil), queues[sig]...)
		}
	}
	return result
}

func (n *naiveState) sharedPendingSnapshot() pendingSnapshot {
	return pendingSnapshot{Standard: n.stdShared, RT: copyRTSnapshot(n.rtShared)}
}

func (n *naiveState) threadPendingSnapshot(tid int) pendingSnapshot {
	return pendingSnapshot{Standard: n.stdPrivate[tid], RT: copyRTSnapshot(n.rtPrivate[tid])}
}

func copyRTSnapshot(source map[int][]int) map[int][]int {
	result := make(map[int][]int)
	for sig, values := range source {
		if len(values) > 0 {
			result[sig] = append([]int(nil), values...)
		}
	}
	return result
}

func (n *naiveState) rtSharedValues() map[int][]int {
	return n.rtShared
}

func bitsOnesCount(mask uint64) int {
	return bits.OnesCount64(mask)
}

func TestNaiveRandomComparison(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			limit := rng.Intn(6)
			if seed%17 == 0 {
				limit = 0
			}
			leaderMask := randomTestMask(rng)

			real, err := New(limit, leaderMask)
			naive := newNaive(limit, leaderMask)
			if (err != nil) != false {
				t.Fatalf("seed=%d constructor error=%v", seed, err)
			}

			log := fmt.Sprintf("seed=%d L=%d leaderMask=%#x\n", seed, limit, leaderMask)
			threadCount := 1
			ops := 20 + rng.Intn(41)
			for i := 0; i < ops; i++ {
				op := randomNaiveOp(rng, threadCount, naive.terminated)
				log += runNaiveOp(t, real, naive, op)
				if op.name == "add" && !naive.terminated && threadCount < 64 {
					threadCount++
				}
				compareNaiveState(t, real, naive, log)
			}
			t.Log(log)
		})
	}
}

func randomTestMask(rng *rand.Rand) uint64 {
	return rng.Uint64() &^ bit(killSig)
}

func randomNaiveOp(rng *rand.Rand, threads int, terminated bool) naiveOp {
	if rng.Intn(20) == 0 {
		switch rng.Intn(4) {
		case 0:
			return naiveOp{name: "bad-send", sig: 65, value: rng.Intn(1000)}
		case 1:
			return naiveOp{name: "bad-sendto", tid: threads + 2, sig: 1 + rng.Intn(64)}
		case 2:
			return naiveOp{name: "bad-action", sig: 9}
		default:
			return naiveOp{name: "bad-mask", tid: threads + 2}
		}
	}

	if !terminated && threads < 64 && rng.Intn(6) == 0 {
		return naiveOp{name: "add", mask: randomTestMask(rng)}
	}

	sig := 1 + rng.Intn(64)
	if rng.Intn(3) == 0 {
		sig = []int{4, 7, 8, 11, 17, 23, 28, 9}[rng.Intn(8)]
	}
	tid := 1 + rng.Intn(threads)
	op := naiveOp{
		tid:   tid,
		sig:   sig,
		value: rng.Intn(1000) - 500,
		mask:  randomTestMask(rng),
	}

	if terminated {
		op.name = []string{"send", "sendto", "deliver", "mask", "sigreturn"}[rng.Intn(5)]
		return op
	}

	switch rng.Intn(10) {
	case 0:
		op.name = "send"
	case 1:
		op.name = "sendto"
	case 2, 3:
		op.name = "deliver"
	case 4:
		op.name = "mask"
	case 5:
		op.name = "sigreturn"
	case 6:
		op.action = Action{Kind: ActionIgnore}
		op.name = "action"
	case 7:
		op.action = Action{
			Kind:           ActionHandler,
			HandlerID:      1 + rng.Intn(20),
			AdditionalMask: randomTestMask(rng),
			NoDefer:        rng.Intn(2) == 0,
		}
		op.name = "action"
	default:
		op.name = "send"
		if sig > 31 {
			op.sig = 32 + rng.Intn(33)
		}
	}
	return op
}

func newNaive(limit int, leaderMask uint64) *naiveState {
	state := &naiveState{
		limit:       limit,
		threadCount: 1,
		rtShared:    make(map[int][]int),
		curr:        1,
	}
	state.masks[1] = sanitizeMask(leaderMask)
	for tid := 1; tid <= 64; tid++ {
		state.rtPrivate[tid] = make(map[int][]int)
	}
	return state
}

func runNaiveOp(t *testing.T, real *Subsystem, naive *naiveState, op naiveOp) string {
	t.Helper()
	log := fmt.Sprintf("%s(tid=%d,sig=%d,value=%d,mask=%#x", op.name, op.tid, op.sig, op.value, op.mask)
	if op.name == "action" {
		log += fmt.Sprintf(",action={kind:%d,handler:%d,add:%#x,nodefer:%t}",
			op.action.Kind, op.action.HandlerID, op.action.AdditionalMask, op.action.NoDefer)
	}
	log += ") "

	var realErr, naiveErr error
	var realEnqueue EnqueueResult
	var naiveEnqueue EnqueueResult
	var realDeliver DeliverResult
	var naiveDeliver DeliverResult
	var realTid, naiveTid int

	switch op.name {
	case "add":
		realTid, realErr = real.AddThread(op.mask)
		naiveTid, naiveErr = naive.add(op.mask)
		log += fmt.Sprintf("=> real=(%d,%v) naive=(%d,%v); rule=append thread with kill bit cleared",
			realTid, realErr, naiveTid, naiveErr)
	case "action":
		realErr = real.SetAction(op.sig, op.action)
		naiveErr = naive.setAction(op.sig, op.action)
		log += fmt.Sprintf("=> real=%v naive=%v; rule=validate/set/flush if effectively ignored",
			realErr, naiveErr)
	case "mask":
		realErr = real.SetMask(op.tid, op.mask)
		naiveErr = naive.setMask(op.tid, op.mask)
		log += fmt.Sprintf("=> real=%v naive=%v; rule=replace mask and clear bit 9", realErr, naiveErr)
	case "send":
		realEnqueue, realErr = real.Send(op.sig, op.value)
		naiveEnqueue, naiveErr = naive.send(op.sig, op.value)
		log += fmt.Sprintf("=> real=(%+v,%v) naive=(%+v,%v); rule=shared drop/merge/quota/queue/target",
			realEnqueue, realErr, naiveEnqueue, naiveErr)
	case "sendto", "bad-sendto":
		realEnqueue, realErr = real.SendTo(op.tid, op.sig, op.value)
		naiveEnqueue, naiveErr = naive.sendTo(op.tid, op.sig, op.value)
		log += fmt.Sprintf("=> real=(%+v,%v) naive=(%+v,%v); rule=private validation/drop/merge/quota",
			realEnqueue, realErr, naiveEnqueue, naiveErr)
	case "deliver":
		realDeliver, realErr = real.Deliver(op.tid)
		naiveDeliver, naiveErr = naive.deliver(op.tid)
		log += fmt.Sprintf("=> real=(%+v,%v) naive=(%+v,%v); rule=kill then private-before/shared bit selection",
			realDeliver, realErr, naiveDeliver, naiveErr)
	case "sigreturn":
		realErr = real.Sigreturn(op.tid)
		naiveErr = naive.sigreturn(op.tid)
		log += fmt.Sprintf("=> real=%v naive=%v; rule=LIFO mask restore", realErr, naiveErr)
	case "bad-send":
		_, realErr = real.Send(op.sig, op.value)
		_, naiveErr = naive.send(op.sig, op.value)
		log += fmt.Sprintf("=> real=%v naive=%v; rule=invalid signal first", realErr, naiveErr)
	case "bad-action":
		realErr = real.SetAction(op.sig, Action{Kind: ActionHandler, HandlerID: 1})
		naiveErr = naive.setAction(op.sig, Action{Kind: ActionHandler, HandlerID: 1})
		log += fmt.Sprintf("=> real=%v naive=%v; rule=kill action is invalid", realErr, naiveErr)
	case "bad-mask":
		realErr = real.SetMask(op.tid, op.mask)
		naiveErr = naive.setMask(op.tid, op.mask)
		log += fmt.Sprintf("=> real=%v naive=%v; rule=missing thread", realErr, naiveErr)
	default:
		t.Fatalf("unknown op %s", op.name)
	}

	if !sameError(realErr, naiveErr) {
		t.Fatalf("error mismatch real=%v naive=%v\n%s", realErr, naiveErr, log)
	}
	if realEnqueue != naiveEnqueue || realDeliver != naiveDeliver {
		t.Fatalf("result mismatch real enqueue=%+v deliver=%+v naive enqueue=%+v deliver=%+v\n%s",
			realEnqueue, realDeliver, naiveEnqueue, naiveDeliver, log)
	}
	if realTid != naiveTid {
		t.Fatalf("tid mismatch real=%d naive=%d\n%s", realTid, naiveTid, log)
	}
	return log + "\n"
}

func compareNaiveState(t *testing.T, real *Subsystem, naive *naiveState, log string) {
	t.Helper()
	fail := func(format string, args ...any) {
		t.Fatalf(format+"\n%s", append(args, log)...)
	}

	threads := len(real.threads) - 1
	realRT := real.shared.rtCount
	naiveRT := 0
	for _, values := range naive.rtShared {
		naiveRT += len(values)
	}
	for tid := 1; tid <= threads; tid++ {
		realRT += real.threads[tid].rtCount
		for _, values := range naive.rtPrivate[tid] {
			naiveRT += len(values)
		}
		if real.threads[tid].mask != naive.masks[tid] {
			fail("tid=%d mask real=%#x naive=%#x", tid, real.threads[tid].mask, naive.masks[tid])
		}
		if !reflect.DeepEqual(real.threadPendingSnapshot(tid), naive.threadPendingSnapshot(tid)) {
			fail("tid=%d private pending mismatch real=%#v naive=%#v",
				tid, real.threadPendingSnapshot(tid), naive.threadPendingSnapshot(tid))
		}
		naiveStack := append([]uint64(nil), naive.stacks[tid]...)
		if !reflect.DeepEqual(real.maskStackSnapshot(tid), naiveStack) {
			fail("tid=%d mask stack mismatch real=%#v naive=%#v",
				tid, real.maskStackSnapshot(tid), naiveStack)
		}
	}

	if real.shared.std != naive.stdShared {
		fail("shared std real=%#x naive=%#x", real.shared.std, naive.stdShared)
	}
	if !reflect.DeepEqual(real.sharedPendingSnapshot(), naive.sharedPendingSnapshot()) {
		fail("shared rt mismatch real=%#v naive=%#v",
			real.sharedPendingSnapshot(), naive.sharedPendingSnapshot())
	}
	if realRT != naiveRT || realRT != real.RTQ() {
		fail("RTQ realCounter=%d naive=%d exported=%d", realRT, naiveRT, real.RTQ())
	}
	if real.curr != naive.curr || real.lost != naive.lost || real.examined != naive.examined {
		fail("scalars real(curr,lost,examined)=(%d,%d,%d) naive=(%d,%d,%d)",
			real.curr, real.lost, real.examined, naive.curr, naive.lost, naive.examined)
	}
	if real.terminated != naive.terminated || real.killCount != naive.killCount {
		fail("termination real=(%v,kills=%d) naive=(%v,kills=%d)",
			real.terminated, real.killCount, naive.terminated, naive.killCount)
	}
	if real.immediatelyDropped != naive.immediatelyDropped || real.dropped != naive.dropped {
		fail("drop counters real(immediate,queued)=(%d,%d) naive=(%d,%d)",
			real.immediatelyDropped, real.dropped, naive.immediatelyDropped, naive.dropped)
	}

	pending := real.shared.rtCount
	for _, target := range real.threads[1:] {
		pending += target.rtCount
	}
	pending += bitsOnesCount(real.shared.std)
	for _, target := range real.threads[1:] {
		pending += bitsOnesCount(target.stdPending)
	}
	totalOut := real.delivered + real.dropped + real.flushed + pending
	if real.enqueued != totalOut {
		fail("accounting enqueued=%d delivered=%d deliveryDropped=%d immediateDropped=%d flushed=%d pending=%d",
			real.enqueued, real.delivered, real.dropped, real.immediatelyDropped, real.flushed, pending)
	}
}

func sameError(left, right error) bool {
	return left == right
}

func (n *naiveState) ignored(sig int) bool {
	action := n.actions[sig]
	return action.Kind == ActionIgnore ||
		(action.Kind == ActionDefault && defaultIgnoredBits&bit(sig) != 0)
}

func (n *naiveState) rtCount() int {
	total := 0
	for _, values := range n.rtShared {
		total += len(values)
	}
	for tid := 1; tid <= n.threadCount; tid++ {
		for _, values := range n.rtPrivate[tid] {
			total += len(values)
		}
	}
	return total
}

func (n *naiveState) add(mask uint64) (int, error) {
	if n.threadCount >= 64 {
		return 0, ErrTooManyThreads
	}
	if n.terminated {
		return 0, ErrProcessTerminated
	}
	n.threadCount++
	n.masks[n.threadCount] = sanitizeMask(mask)
	return n.threadCount, nil
}

func (n *naiveState) setAction(sig int, action Action) error {
	if !validSignal(sig) || sig == killSig ||
		action.Kind < ActionDefault || action.Kind > ActionHandler {
		return ErrInvalidArgument
	}
	if n.terminated {
		return ErrProcessTerminated
	}

	n.actions[sig] = action
	if n.ignored(sig) {
		n.examined++
		n.flushShared(sig)
		for tid := 1; tid <= n.threadCount; tid++ {
			n.examined++
			n.flushThread(tid, sig)
		}
	}
	return nil
}

func (n *naiveState) flushShared(sig int) {
	if sig <= 31 {
		if n.stdShared&bit(sig) != 0 {
			n.stdShared &^= bit(sig)
			n.flushed++
			n.examined++
			if sig == killSig {
				n.killCount--
			}
		}
		return
	}
	values := n.rtShared[sig]
	if len(values) == 0 {
		return
	}
	delete(n.rtShared, sig)
	n.flushed += len(values)
	n.examined += len(values)
}

func (n *naiveState) flushThread(tid, sig int) {
	if sig <= 31 {
		if n.stdPrivate[tid]&bit(sig) != 0 {
			n.stdPrivate[tid] &^= bit(sig)
			n.flushed++
			n.examined++
			if sig == killSig {
				n.killCount--
			}
		}
		return
	}
	values := n.rtPrivate[tid][sig]
	if len(values) == 0 {
		return
	}
	delete(n.rtPrivate[tid], sig)
	n.flushed += len(values)
	n.examined += len(values)
}

func (n *naiveState) setMask(tid int, mask uint64) error {
	if tid < 1 || tid > n.threadCount {
		return ErrNoSuchThread
	}
	if n.terminated {
		return ErrProcessTerminated
	}
	n.masks[tid] = sanitizeMask(mask)
	return nil
}

func (n *naiveState) enqueueShared(sig, value int) {
	if sig <= 31 {
		n.stdShared |= bit(sig)
		if sig == killSig {
			n.killCount++
		}
	} else {
		n.rtShared[sig] = append(n.rtShared[sig], value)
	}
	n.enqueued++
}

func (n *naiveState) enqueueThread(tid, sig, value int) {
	if sig <= 31 {
		n.stdPrivate[tid] |= bit(sig)
		if sig == killSig {
			n.killCount++
		}
	} else {
		n.rtPrivate[tid][sig] = append(n.rtPrivate[tid][sig], value)
	}
	n.enqueued++
}

func (n *naiveState) selectTarget(sig int) int {
	if sig == killSig || n.masks[1]&bit(sig) == 0 {
		return 1
	}
	for offset := 0; offset < n.threadCount; offset++ {
		tid := 1 + (n.curr-1+offset)%n.threadCount
		if n.masks[tid]&bit(sig) == 0 {
			n.curr = tid
			return tid
		}
	}
	return 0
}

func (n *naiveState) send(sig, value int) (EnqueueResult, error) {
	if !validSignal(sig) {
		return EnqueueResult{}, ErrInvalidArgument
	}
	if n.terminated {
		return EnqueueResult{}, ErrProcessTerminated
	}
	if n.ignored(sig) && n.masks[1]&bit(sig) == 0 {
		n.immediatelyDropped++
		return EnqueueResult{Kind: EnqueueDiscarded}, nil
	}
	if sig <= 31 {
		if n.stdShared&bit(sig) != 0 {
			n.lost++
			return EnqueueResult{Kind: EnqueueMerged}, nil
		}
	} else if n.rtCount() >= n.limit {
		return EnqueueResult{}, ErrAgain
	}

	n.enqueueShared(sig, value)
	return EnqueueResult{Kind: EnqueueQueued, TargetThread: n.selectTarget(sig)}, nil
}

func (n *naiveState) sendTo(tid, sig, value int) (EnqueueResult, error) {
	if !validSignal(sig) || tid < 1 {
		return EnqueueResult{}, ErrInvalidArgument
	}
	if tid > n.threadCount {
		return EnqueueResult{}, ErrNoSuchThread
	}
	if n.terminated {
		return EnqueueResult{}, ErrProcessTerminated
	}
	if n.ignored(sig) && n.masks[tid]&bit(sig) == 0 {
		n.immediatelyDropped++
		return EnqueueResult{Kind: EnqueueDiscarded}, nil
	}
	if sig <= 31 {
		if n.stdPrivate[tid]&bit(sig) != 0 {
			n.lost++
			return EnqueueResult{Kind: EnqueueMerged}, nil
		}
	} else if n.rtCount() >= n.limit {
		return EnqueueResult{}, ErrAgain
	}

	n.enqueueThread(tid, sig, value)
	return EnqueueResult{Kind: EnqueueQueued, TargetThread: tid}, nil
}

func (n *naiveState) sigreturn(tid int) error {
	if tid < 1 || tid > n.threadCount {
		return ErrNoSuchThread
	}
	if n.terminated {
		return ErrProcessTerminated
	}
	if len(n.stacks[tid]) == 0 {
		return ErrNoHandler
	}
	last := len(n.stacks[tid]) - 1
	n.masks[tid] = n.stacks[tid][last]
	n.stacks[tid] = n.stacks[tid][:last]
	return nil
}

func (n *naiveState) hasKill() bool {
	if n.stdShared&bit(killSig) != 0 {
		return true
	}
	for tid := 1; tid <= n.threadCount; tid++ {
		if n.stdPrivate[tid]&bit(killSig) != 0 {
			return true
		}
	}
	return false
}

func (n *naiveState) consumeKill() {
	if n.stdShared&bit(killSig) != 0 {
		n.stdShared &^= bit(killSig)
		n.killCount--
		return
	}
	for tid := 1; tid <= n.threadCount; tid++ {
		if n.stdPrivate[tid]&bit(killSig) != 0 {
			n.stdPrivate[tid] &^= bit(killSig)
			n.killCount--
			return
		}
	}
}

func (n *naiveState) chooseFrom(std uint64, rt map[int][]int, blocked uint64) (int, int) {
	for _, candidate := range []int{4, 7, 8, 11} {
		if std&bit(candidate) != 0 && blocked&bit(candidate) == 0 {
			return candidate, 0
		}
	}

	best := 0
	for sig := 1; sig <= 64; sig++ {
		var present bool
		if sig <= 31 {
			present = std&bit(sig) != 0
		} else {
			_, present = rt[sig]
		}
		if present && blocked&bit(sig) == 0 {
			best = sig
			break
		}
	}
	if best == 0 {
		return 0, 0
	}
	value := 0
	if best > 31 {
		value = rt[best][0]
	}
	return best, value
}

func (n *naiveState) remove(std *uint64, rt map[int][]int, sig int, shared bool) {
	if sig <= 31 {
		*std &^= bit(sig)
		if sig == killSig {
			n.killCount--
		}
		return
	}
	values := rt[sig]
	if len(values) == 1 {
		delete(rt, sig)
	} else {
		rt[sig] = append([]int(nil), values[1:]...)
	}
}

func (n *naiveState) deliver(tid int) (DeliverResult, error) {
	if tid < 1 || tid > n.threadCount {
		return DeliverResult{}, ErrNoSuchThread
	}
	if n.terminated {
		return DeliverResult{}, ErrProcessTerminated
	}

	if n.hasKill() {
		n.consumeKill()
		n.terminated = true
		n.delivered++
		return DeliverResult{Kind: DeliverTerminated, Signal: killSig}, nil
	}

	discarded := 0
	for {
		sig, value := n.chooseFrom(n.stdPrivate[tid], n.rtPrivate[tid], n.masks[tid])
		fromShared := false
		if sig == 0 {
			sig, value = n.chooseFrom(n.stdShared, n.rtShared, n.masks[tid])
			fromShared = true
		}
		if sig == 0 {
			return DeliverResult{Kind: DeliverNone, Discarded: discarded}, nil
		}

		if fromShared {
			n.remove(&n.stdShared, n.rtShared, sig, true)
		} else {
			n.remove(&n.stdPrivate[tid], n.rtPrivate[tid], sig, false)
		}

		action := n.actions[sig]
		switch {
		case n.ignored(sig):
			discarded++
			n.dropped++
		case action.Kind == ActionDefault:
			n.delivered++
			n.terminated = true
			return DeliverResult{Kind: DeliverTerminated, Signal: sig}, nil
		default:
			oldMask := n.masks[tid]
			newMask := oldMask | sanitizeMask(action.AdditionalMask)
			if !action.NoDefer {
				newMask |= bit(sig)
			}
			n.masks[tid] = sanitizeMask(newMask)
			n.stacks[tid] = append(n.stacks[tid], oldMask)
			n.delivered++
			return DeliverResult{
				Kind:       DeliverHandler,
				Signal:     sig,
				Value:      value,
				HandlerID:  action.HandlerID,
				FromShared: fromShared,
				Discarded:  discarded,
			}, nil
		}
	}
}
