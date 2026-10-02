package sigsub

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func bitsOf(sigs ...int) uint64 {
	var m uint64
	for _, sig := range sigs {
		m |= bit(sig)
	}
	return m
}

func mustNew(t *testing.T, quota int, mask uint64) *Sub {
	t.Helper()
	s, err := New(quota, mask)
	if err != nil {
		t.Fatalf("New(%d, %#x): %v", quota, mask, err)
	}
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustAddThread(t *testing.T, s *Sub, mask uint64) int {
	t.Helper()
	tid, err := s.AddThread(mask)
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}
	return tid
}

func mustSend(t *testing.T, s *Sub, sig int, value int64) (SendResult, int) {
	t.Helper()
	res, tgt, err := s.Send(sig, value)
	if err != nil {
		t.Fatalf("Send(%d, %d): %v", sig, value, err)
	}
	return res, tgt
}

func mustSendTo(t *testing.T, s *Sub, tid, sig int, value int64) (SendResult, int) {
	t.Helper()
	res, tgt, err := s.SendTo(tid, sig, value)
	if err != nil {
		t.Fatalf("SendTo(%d, %d, %d): %v", tid, sig, value, err)
	}
	return res, tgt
}

func mustDeliver(t *testing.T, s *Sub, tid int) DeliverResult {
	t.Helper()
	d, err := s.Deliver(tid)
	if err != nil {
		t.Fatalf("Deliver(%d): %v", tid, err)
	}
	return d
}

func checkMask(t *testing.T, s *Sub, tid int, want uint64) {
	t.Helper()
	got, err := s.Mask(tid)
	if err != nil {
		t.Fatalf("Mask(%d): %v", tid, err)
	}
	if got != want {
		t.Fatalf("Mask(%d) = %#x, want %#x", tid, got, want)
	}
}

func checkPending(t *testing.T, got []PendingEntry, want []PendingEntry, what string) {
	t.Helper()
	if want == nil {
		want = []PendingEntry{}
	}
	if got == nil {
		got = []PendingEntry{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// TestSpecWalkthrough replays the worked example from the specification.
func TestSpecWalkthrough(t *testing.T) {
	s := mustNew(t, 2, bitsOf(10))
	mustAddThread(t, s, 0)
	mustAddThread(t, s, 0)
	must(t, s.SetAction(10, HandlerAction(7, bitsOf(13), false)))
	must(t, s.SetAction(12, HandlerAction(8, 0, false)))
	must(t, s.SetAction(34, HandlerAction(9, 0, false)))

	// Leader masks 10: scan from curr=1 picks thread 2, curr=2.
	res, tgt := mustSend(t, s, 10, 0)
	if res != SendEnqueued || tgt != 2 {
		t.Fatalf("Send(10) = %v,%d want enqueued,2", res, tgt)
	}
	if c := s.Curr(); c != 2 {
		t.Fatalf("Curr() = %d, want 2", c)
	}
	// Standard signal merges in the shared set.
	res, _ = mustSend(t, s, 10, 0)
	if res != SendMerged || s.Lost() != 1 {
		t.Fatalf("Send(10) again = %v, Lost=%d want merged,1", res, s.Lost())
	}
	// Leader does not mask 12: target is the leader, curr unchanged.
	res, tgt = mustSend(t, s, 12, 0)
	if res != SendEnqueued || tgt != 1 || s.Curr() != 2 {
		t.Fatalf("Send(12) = %v,%d curr=%d want enqueued,1 curr=2", res, tgt, s.Curr())
	}
	if res, tgt = mustSendTo(t, s, 3, 12, 0); res != SendEnqueued || tgt != 3 {
		t.Fatalf("SendTo(3,12) = %v,%d want enqueued,3", res, tgt)
	}
	mustSend(t, s, 34, 111)
	mustSend(t, s, 34, 222)
	if q := s.RTQ(); q != 2 {
		t.Fatalf("RTQ() = %d, want 2", q)
	}
	if _, _, err := s.Send(35, 5); !errors.Is(err, ErrAgain) {
		t.Fatalf("Send(35) err = %v, want ErrAgain", err)
	}

	// Deliver(3): private 12 first, handler 8, mask becomes {12}.
	d := mustDeliver(t, s, 3)
	if d.Kind != DeliverHandler || d.Handler != 8 || d.Sig != 12 || d.Shared || d.Discarded != 0 {
		t.Fatalf("Deliver(3)#1 = %v", d)
	}
	checkMask(t, s, 3, bitsOf(12))
	// Deliver(3): private empty; shared 12 masked, so shared 10, handler 7.
	d = mustDeliver(t, s, 3)
	if d.Kind != DeliverHandler || d.Handler != 7 || d.Sig != 10 || !d.Shared {
		t.Fatalf("Deliver(3)#2 = %v", d)
	}
	checkMask(t, s, 3, bitsOf(10, 12, 13))
	// Deliver(3): shared realtime 34 with the first queued value.
	d = mustDeliver(t, s, 3)
	if d.Kind != DeliverHandler || d.Handler != 9 || d.Sig != 34 || d.Value != 111 || !d.Shared {
		t.Fatalf("Deliver(3)#3 = %v", d)
	}
	if q := s.RTQ(); q != 1 {
		t.Fatalf("RTQ() = %d, want 1", q)
	}
	// Sigreturn is LIFO.
	must(t, s.Sigreturn(3))
	checkMask(t, s, 3, bitsOf(10, 12, 13))
	must(t, s.Sigreturn(3))
	checkMask(t, s, 3, bitsOf(12))
	must(t, s.Sigreturn(3))
	checkMask(t, s, 3, 0)

	// SetAction(34, ignore) flushes the remaining queued entry.
	must(t, s.SetAction(34, IgnoreAction()))
	if q := s.RTQ(); q != 0 {
		t.Fatalf("RTQ() after flush = %d, want 0", q)
	}
	checkPending(t, s.SharedPending(), []PendingEntry{{Sig: 12, Count: 1}}, "shared after flush")

	// Drain the leftover shared 12 so the 17-experiment sees an empty set.
	d = mustDeliver(t, s, 1)
	if d.Kind != DeliverHandler || d.Sig != 12 || !d.Shared {
		t.Fatalf("Deliver(1) drain = %v", d)
	}
	must(t, s.Sigreturn(1))

	// Default-ignore 17 dropped while the leader does not mask it.
	res, _ = mustSend(t, s, 17, 0)
	if res != SendDropped {
		t.Fatalf("Send(17) = %v, want dropped", res)
	}
	// Masked by the leader: queued, then discarded at Deliver time.
	must(t, s.SetMask(1, bitsOf(17)))
	res, _ = mustSend(t, s, 17, 0)
	if res != SendEnqueued {
		t.Fatalf("Send(17) masked = %v, want enqueued", res)
	}
	must(t, s.SetMask(1, 0))
	d = mustDeliver(t, s, 1)
	if d.Kind != DeliverNone || d.Discarded != 1 {
		t.Fatalf("Deliver(1) discard = %v, want none discarded=1", d)
	}

	// SigKill terminates on any thread's Deliver.
	mustSend(t, s, 9, 0)
	d = mustDeliver(t, s, 2)
	if d.Kind != DeliverTerminated || d.Sig != 9 {
		t.Fatalf("Deliver(2) kill = %v, want terminated 9", d)
	}
	// Every mutating operation now reports process-terminated.
	if _, _, err := s.Send(5, 0); !errors.Is(err, ErrProcessTerminated) {
		t.Fatalf("Send after kill = %v", err)
	}
	if _, _, err := s.SendTo(1, 5, 0); !errors.Is(err, ErrProcessTerminated) {
		t.Fatalf("SendTo after kill = %v", err)
	}
	if err := s.SetMask(1, 0); !errors.Is(err, ErrProcessTerminated) {
		t.Fatalf("SetMask after kill = %v", err)
	}
	if err := s.SetAction(5, IgnoreAction()); !errors.Is(err, ErrProcessTerminated) {
		t.Fatalf("SetAction after kill = %v", err)
	}
	if _, err := s.AddThread(0); !errors.Is(err, ErrProcessTerminated) {
		t.Fatalf("AddThread after kill = %v", err)
	}
	if err := s.Sigreturn(1); !errors.Is(err, ErrProcessTerminated) {
		t.Fatalf("Sigreturn after kill = %v", err)
	}
	if _, err := s.Deliver(1); !errors.Is(err, ErrProcessTerminated) {
		t.Fatalf("Deliver after kill = %v", err)
	}
	// Queries still work after termination.
	checkPending(t, s.SharedPending(), []PendingEntry{{Sig: 9, Count: 1}}, "shared after kill")
}

func TestConstructorValidation(t *testing.T) {
	for _, q := range []int{-1, -100, 1001, 5000} {
		if _, err := New(q, 0); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("New(%d) err = %v, want ErrInvalidParam", q, err)
		}
	}
	for _, q := range []int{0, 1, 1000} {
		if _, err := New(q, 0); err != nil {
			t.Fatalf("New(%d) err = %v", q, err)
		}
	}
	// The kill bit is cleared from the leader mask at construction.
	s := mustNew(t, 0, bitsOf(9, 10))
	checkMask(t, s, 1, bitsOf(10))
}

// When the leader does not mask a signal the target is always the leader
// and curr never moves.
func TestTargetLeaderUnmasked(t *testing.T) {
	s := mustNew(t, 4, 0)
	mustAddThread(t, s, 0)
	mustAddThread(t, s, 0)
	for _, sig := range []int{5, 10, 20, 33, 40} {
		res, tgt := mustSend(t, s, sig, 0)
		if res != SendEnqueued || tgt != 1 {
			t.Fatalf("Send(%d) = %v,%d want enqueued,1", sig, res, tgt)
		}
		if c := s.Curr(); c != 1 {
			t.Fatalf("Curr() = %d after Send(%d), want 1", c, sig)
		}
	}
}

// With the leader masking, curr advances cyclically and wraps around.
func TestTargetCursorWraparound(t *testing.T) {
	s := mustNew(t, 4, bitsOf(10, 11, 12, 13))
	mustAddThread(t, s, bitsOf(11)) // tid 2
	mustAddThread(t, s, bitsOf(13)) // tid 3
	// Send(10): scan from curr=1: 1 masked, 2 free -> target 2, curr=2.
	if _, tgt := mustSend(t, s, 10, 0); tgt != 2 || s.Curr() != 2 {
		t.Fatalf("Send(10) tgt=%d curr=%d, want 2,2", tgt, s.Curr())
	}
	// Send(11): scan from curr=2: 2 masked, 3 free -> target 3, curr=3.
	if _, tgt := mustSend(t, s, 11, 0); tgt != 3 || s.Curr() != 3 {
		t.Fatalf("Send(11) tgt=%d curr=%d, want 3,3", tgt, s.Curr())
	}
	// Send(12): scan from curr=3: 3 free -> target 3, curr stays 3.
	if _, tgt := mustSend(t, s, 12, 0); tgt != 3 || s.Curr() != 3 {
		t.Fatalf("Send(12) tgt=%d curr=%d, want 3,3", tgt, s.Curr())
	}
	// Send(13): scan from curr=3: 3 masked, wrap to 1 masked, 2 free.
	if _, tgt := mustSend(t, s, 13, 0); tgt != 2 || s.Curr() != 2 {
		t.Fatalf("Send(13) tgt=%d curr=%d, want 2,2 (wraparound)", tgt, s.Curr())
	}
}

// When every thread masks the signal there is no target, but the signal
// stays in the shared set.
func TestTargetAllMasked(t *testing.T) {
	s := mustNew(t, 4, bitsOf(10))
	mustAddThread(t, s, bitsOf(10))
	mustAddThread(t, s, bitsOf(10))
	res, tgt := mustSend(t, s, 10, 0)
	if res != SendEnqueued || tgt != 0 {
		t.Fatalf("Send(10) = %v,%d want enqueued,0", res, tgt)
	}
	if c := s.Curr(); c != 1 {
		t.Fatalf("Curr() = %d, want 1 (unchanged)", c)
	}
	checkPending(t, s.SharedPending(), []PendingEntry{{Sig: 10, Count: 1}}, "shared")
	// Realtime signals behave the same way.
	must(t, s.SetMask(1, bitsOf(10, 34)))
	must(t, s.SetMask(2, bitsOf(10, 34)))
	must(t, s.SetMask(3, bitsOf(10, 34)))
	res, tgt = mustSend(t, s, 34, 7)
	if res != SendEnqueued || tgt != 0 {
		t.Fatalf("Send(34) = %v,%d want enqueued,0", res, tgt)
	}
	checkPending(t, s.SharedPending(),
		[]PendingEntry{{Sig: 10, Count: 1}, {Sig: 34, Count: 1}}, "shared")
}

// SigKill always targets the leader and never moves curr.
func TestTargetKillAlwaysLeader(t *testing.T) {
	s := mustNew(t, 4, bitsOf(9, 10)) // kill bit cleared anyway
	mustAddThread(t, s, 0)
	must(t, s.SetMask(2, 0))
	_, tgt := mustSend(t, s, 9, 0)
	if tgt != 1 || s.Curr() != 1 {
		t.Fatalf("Send(9) tgt=%d curr=%d, want 1,1", tgt, s.Curr())
	}
}

// Standard signals merge per set: the same signal may be pending once in
// the private set and once in the shared set at the same time.
func TestStandardMergePerSet(t *testing.T) {
	s := mustNew(t, 4, 0)
	mustAddThread(t, s, 0)
	mustSend(t, s, 10, 0)      // shared
	mustSendTo(t, s, 2, 10, 0) // private of thread 2
	res, _ := mustSend(t, s, 10, 0)
	if res != SendMerged || s.Lost() != 1 {
		t.Fatalf("Send(10) = %v lost=%d, want merged,1", res, s.Lost())
	}
	res, _ = mustSendTo(t, s, 2, 10, 0)
	if res != SendMerged || s.Lost() != 2 {
		t.Fatalf("SendTo(2,10) = %v lost=%d, want merged,2", res, s.Lost())
	}
	// A different thread's private set is a different set: no merge.
	res, _ = mustSendTo(t, s, 1, 10, 0)
	if res != SendEnqueued {
		t.Fatalf("SendTo(1,10) = %v, want enqueued", res)
	}
	checkPending(t, s.SharedPending(), []PendingEntry{{Sig: 10, Count: 1}}, "shared")
	p, err := s.Pending(2)
	must(t, err)
	checkPending(t, p, []PendingEntry{{Sig: 10, Count: 1}}, "private(2)")
	p, err = s.Pending(1)
	must(t, err)
	checkPending(t, p, []PendingEntry{{Sig: 10, Count: 1}}, "private(1)")
	// Merged sends must not change curr or pending.
	if c := s.Curr(); c != 1 {
		t.Fatalf("Curr() = %d, want 1", c)
	}
}

// Realtime signals queue FIFO with values; the quota can be filled
// exactly and ErrAgain only appears one past the limit.
func TestRealtimeFIFOAndQuota(t *testing.T) {
	s := mustNew(t, 2, 0)
	must(t, s.SetAction(34, HandlerAction(1, 0, false)))
	must(t, s.SetAction(35, HandlerAction(2, 0, false)))
	mustSend(t, s, 34, 111)
	mustSend(t, s, 34, 222)
	if q := s.RTQ(); q != 2 {
		t.Fatalf("RTQ() = %d, want 2 (quota exactly full)", q)
	}
	if _, _, err := s.Send(35, 5); !errors.Is(err, ErrAgain) {
		t.Fatalf("Send(35) err = %v, want ErrAgain", err)
	}
	if _, _, err := s.SendTo(1, 35, 5); !errors.Is(err, ErrAgain) {
		t.Fatalf("SendTo(1,35) err = %v, want ErrAgain", err)
	}
	// Deliver one entry: quota has one free slot again.
	d := mustDeliver(t, s, 1)
	if d.Kind != DeliverHandler || d.Sig != 34 || d.Value != 111 {
		t.Fatalf("Deliver = %v, want handler sig=34 value=111", d)
	}
	if q := s.RTQ(); q != 1 {
		t.Fatalf("RTQ() = %d, want 1", q)
	}
	res, _ := mustSend(t, s, 35, 5)
	if res != SendEnqueued {
		t.Fatalf("Send(35) = %v, want enqueued", res)
	}
	if _, _, err := s.Send(35, 6); !errors.Is(err, ErrAgain) {
		t.Fatalf("Send(35) again err = %v, want ErrAgain", err)
	}
	// FIFO order across deliveries: 222 then 5.
	must(t, s.Sigreturn(1))
	d = mustDeliver(t, s, 1)
	if d.Sig != 34 || d.Value != 222 {
		t.Fatalf("Deliver = %v, want sig=34 value=222 (FIFO)", d)
	}
	must(t, s.Sigreturn(1))
	d = mustDeliver(t, s, 1)
	if d.Sig != 35 || d.Value != 5 {
		t.Fatalf("Deliver = %v, want sig=35 value=5", d)
	}
	if q := s.RTQ(); q != 0 {
		t.Fatalf("RTQ() = %d, want 0", q)
	}
}

// Ignore disposition: dropped at Send time only when the leader does not
// mask the signal; masked signals stay pending and are discarded at
// Deliver time.
func TestIgnoreDropAndRetain(t *testing.T) {
	s := mustNew(t, 4, 0)
	mustAddThread(t, s, 0)
	// Default-ignore set member 17.
	res, _ := mustSend(t, s, 17, 0)
	if res != SendDropped {
		t.Fatalf("Send(17) = %v, want dropped", res)
	}
	checkPending(t, s.SharedPending(), nil, "shared")
	// Masked by the leader: queued instead of dropped.
	must(t, s.SetMask(1, bitsOf(17)))
	res, _ = mustSend(t, s, 17, 0)
	if res != SendEnqueued {
		t.Fatalf("Send(17) masked = %v, want enqueued", res)
	}
	// Explicit ignore action behaves the same for SendTo (own mask).
	must(t, s.SetAction(20, IgnoreAction()))
	currBefore := s.Curr()
	res, _ = mustSendTo(t, s, 2, 20, 0)
	if res != SendDropped {
		t.Fatalf("SendTo(2,20) = %v, want dropped", res)
	}
	must(t, s.SetMask(2, bitsOf(20)))
	res, _ = mustSendTo(t, s, 2, 20, 0)
	if res != SendEnqueued {
		t.Fatalf("SendTo(2,20) masked = %v, want enqueued", res)
	}
	// Unmask and deliver: both are discarded inside Deliver.
	must(t, s.SetMask(1, 0))
	must(t, s.SetMask(2, 0))
	d := mustDeliver(t, s, 1)
	if d.Kind != DeliverNone || d.Discarded != 1 {
		t.Fatalf("Deliver(1) = %v, want none discarded=1", d)
	}
	d = mustDeliver(t, s, 2)
	if d.Kind != DeliverNone || d.Discarded != 1 {
		t.Fatalf("Deliver(2) = %v, want none discarded=1", d)
	}
	// Dropped results never move curr or change pending.
	if c := s.Curr(); c != currBefore {
		t.Fatalf("Curr() = %d, want %d (dropped must not move curr)", c, currBefore)
	}
	checkPending(t, s.SharedPending(), nil, "shared")
}

// SetAction to an effective ignore flushes the signal from the shared set
// and every private set, including realtime entries, and the examined
// counter stays within (threads+1)+flushed entries.
func TestSetActionIgnoreFlush(t *testing.T) {
	s := mustNew(t, 8, 0)
	mustAddThread(t, s, 0)
	mustAddThread(t, s, 0)
	// Realtime entries in three different sets.
	mustSend(t, s, 34, 1)
	mustSend(t, s, 34, 2)
	mustSendTo(t, s, 2, 34, 3)
	mustSendTo(t, s, 3, 34, 4)
	// Standard instances in two sets.
	mustSend(t, s, 10, 0)
	mustSendTo(t, s, 2, 10, 0)
	// Unrelated signals must not be touched (and not examined).
	mustSend(t, s, 35, 9)
	mustSend(t, s, 11, 0)
	if q := s.RTQ(); q != 5 {
		t.Fatalf("RTQ() = %d, want 5", q)
	}

	must(t, s.SetAction(34, IgnoreAction()))
	if q := s.RTQ(); q != 1 {
		t.Fatalf("RTQ() after flush = %d, want 1 (only sig 35 left)", q)
	}
	if s.examined != 4+4 {
		t.Fatalf("examined = %d, want (3 threads + 1 shared) + 4 entries = 8", s.examined)
	}
	checkPending(t, s.SharedPending(),
		[]PendingEntry{{Sig: 10, Count: 1}, {Sig: 11, Count: 1}, {Sig: 35, Count: 1}}, "shared")
	p, _ := s.Pending(2)
	checkPending(t, p, []PendingEntry{{Sig: 10, Count: 1}}, "private(2)")
	p, _ = s.Pending(3)
	checkPending(t, p, nil, "private(3)")

	// Flushing a standard signal examines one bit per set only.
	must(t, s.SetAction(10, IgnoreAction()))
	if s.examined != 4 {
		t.Fatalf("examined = %d, want 4 (one probe per set)", s.examined)
	}
	checkPending(t, s.SharedPending(),
		[]PendingEntry{{Sig: 11, Count: 1}, {Sig: 35, Count: 1}}, "shared")
	p, _ = s.Pending(2)
	checkPending(t, p, nil, "private(2)")

	// Setting a default action on a default-ignore signal also flushes.
	mustSend(t, s, 17, 0) // dropped (leader unmasked)
	must(t, s.SetMask(1, bitsOf(17)))
	mustSend(t, s, 17, 0) // queued
	must(t, s.SetAction(17, DefaultAction()))
	checkPending(t, s.SharedPending(),
		[]PendingEntry{{Sig: 11, Count: 1}, {Sig: 35, Count: 1}}, "shared")

	// Switching back to a handler does not flush anything.
	mustSend(t, s, 11, 0) // merges, still one instance
	must(t, s.SetAction(11, HandlerAction(3, 0, false)))
	checkPending(t, s.SharedPending(),
		[]PendingEntry{{Sig: 11, Count: 1}, {Sig: 35, Count: 1}}, "shared")
}

// SetAction rejects sig 9 and out-of-range numbers.
func TestSetActionInvalid(t *testing.T) {
	s := mustNew(t, 1, 0)
	for _, sig := range []int{0, -1, 65, 9} {
		if err := s.SetAction(sig, IgnoreAction()); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("SetAction(%d) = %v, want ErrInvalidParam", sig, err)
		}
	}
}

// Synchronous signals {4,7,8,11} win over smaller non-sync signals.
func TestDeliverSyncPriority(t *testing.T) {
	s := mustNew(t, 4, 0)
	must(t, s.SetAction(5, HandlerAction(1, 0, false)))
	must(t, s.SetAction(8, HandlerAction(2, 0, false)))
	mustSendTo(t, s, 1, 5, 0)
	mustSendTo(t, s, 1, 8, 0)
	// 5 < 8, but 8 is synchronous.
	d := mustDeliver(t, s, 1)
	if d.Kind != DeliverHandler || d.Sig != 8 {
		t.Fatalf("Deliver = %v, want handler sig=8 (sync priority)", d)
	}
	must(t, s.Sigreturn(1))
	d = mustDeliver(t, s, 1)
	if d.Kind != DeliverHandler || d.Sig != 5 {
		t.Fatalf("Deliver = %v, want handler sig=5", d)
	}
	// Among several sync signals the smallest number wins.
	must(t, s.SetAction(4, HandlerAction(3, 0, false)))
	must(t, s.SetAction(11, HandlerAction(4, 0, false)))
	mustSendTo(t, s, 1, 11, 0)
	mustSendTo(t, s, 1, 4, 0)
	must(t, s.Sigreturn(1))
	d = mustDeliver(t, s, 1)
	if d.Sig != 4 {
		t.Fatalf("Deliver = %v, want sig=4 (smallest sync)", d)
	}
}

// The private set is consulted as a whole before the shared set, even
// when the shared set holds a smaller signal number.
func TestDeliverPrivateBeforeShared(t *testing.T) {
	s := mustNew(t, 4, 0)
	must(t, s.SetAction(3, HandlerAction(1, 0, false)))
	must(t, s.SetAction(20, HandlerAction(2, 0, false)))
	mustSend(t, s, 3, 0)       // shared, smaller number
	mustSendTo(t, s, 1, 20, 0) // private, larger number
	d := mustDeliver(t, s, 1)
	if d.Kind != DeliverHandler || d.Sig != 20 || d.Shared {
		t.Fatalf("Deliver = %v, want private sig=20 first", d)
	}
	must(t, s.Sigreturn(1))
	d = mustDeliver(t, s, 1)
	if d.Kind != DeliverHandler || d.Sig != 3 || !d.Shared {
		t.Fatalf("Deliver = %v, want shared sig=3 next", d)
	}
}

// Masked signals are skipped; a larger private signal beats a smaller
// shared one only when both are unmasked.
func TestDeliverSkipsMasked(t *testing.T) {
	s := mustNew(t, 4, 0)
	must(t, s.SetAction(6, HandlerAction(1, 0, false)))
	must(t, s.SetAction(30, HandlerAction(2, 0, false)))
	mustSend(t, s, 6, 0)
	mustSend(t, s, 30, 0)
	must(t, s.SetMask(1, bitsOf(6)))
	d := mustDeliver(t, s, 1)
	if d.Kind != DeliverHandler || d.Sig != 30 {
		t.Fatalf("Deliver = %v, want sig=30 (6 is masked)", d)
	}
	checkPending(t, s.SharedPending(), []PendingEntry{{Sig: 6, Count: 1}}, "shared")
}

// Handler masks: old mask OR additional mask OR own bit, unless nodefer.
func TestHandlerMaskStacking(t *testing.T) {
	s := mustNew(t, 4, bitsOf(2))
	must(t, s.SetAction(10, HandlerAction(1, bitsOf(13), false)))
	must(t, s.SetAction(12, HandlerAction(2, bitsOf(14), true)))
	mustSendTo(t, s, 1, 10, 0)
	mustSendTo(t, s, 1, 12, 0)
	d := mustDeliver(t, s, 1)
	if d.Sig != 10 {
		t.Fatalf("Deliver = %v, want sig=10", d)
	}
	// {2} | {13} | {10}
	checkMask(t, s, 1, bitsOf(2, 10, 13))
	d = mustDeliver(t, s, 1)
	if d.Sig != 12 {
		t.Fatalf("Deliver = %v, want sig=12", d)
	}
	// nodefer: {2,10,13} | {14}, without bit 12.
	checkMask(t, s, 1, bitsOf(2, 10, 13, 14))
	// The kill bit in an additional mask is cleared.
	must(t, s.SetAction(15, HandlerAction(3, bitsOf(9, 16), false)))
	mustSendTo(t, s, 1, 15, 0)
	d = mustDeliver(t, s, 1)
	if d.Sig != 15 {
		t.Fatalf("Deliver = %v, want sig=15", d)
	}
	checkMask(t, s, 1, bitsOf(2, 10, 13, 14, 15, 16))
}

// Sigreturn is LIFO, and a SetMask inside a handler is overwritten by
// the next Sigreturn.
func TestSigreturnLIFOAndSetMaskOverride(t *testing.T) {
	s := mustNew(t, 4, 0)
	must(t, s.SetAction(10, HandlerAction(1, bitsOf(13), false)))
	must(t, s.SetAction(12, HandlerAction(2, 0, false)))
	mustSendTo(t, s, 1, 10, 0)
	mustSendTo(t, s, 1, 12, 0)
	mustDeliver(t, s, 1) // handler 1, mask {10,13}
	checkMask(t, s, 1, bitsOf(10, 13))
	// Inside the handler: replace the mask wholesale.
	must(t, s.SetMask(1, bitsOf(20)))
	checkMask(t, s, 1, bitsOf(20))
	mustDeliver(t, s, 1) // handler 2, pushes {20}
	checkMask(t, s, 1, bitsOf(20, 12))
	// First Sigreturn restores the SetMask value {20}.
	must(t, s.Sigreturn(1))
	checkMask(t, s, 1, bitsOf(20))
	// Second Sigreturn restores the pre-handler mask {}, overriding the
	// in-handler SetMask.
	must(t, s.Sigreturn(1))
	checkMask(t, s, 1, 0)
	// Stack empty now.
	if err := s.Sigreturn(1); !errors.Is(err, ErrNoHandler) {
		t.Fatalf("Sigreturn = %v, want ErrNoHandler", err)
	}
}

// SigKill cannot be masked or given an action, and any thread's Deliver
// terminates the process while it is pending anywhere.
func TestKillSemantics(t *testing.T) {
	s := mustNew(t, 4, bitsOf(9))
	checkMask(t, s, 1, 0) // kill bit cleared at construction
	mustAddThread(t, s, 0)
	mustAddThread(t, s, 0)
	must(t, s.SetMask(2, bitsOf(9, 10)))
	checkMask(t, s, 2, bitsOf(10)) // kill bit cleared by SetMask
	if err := s.SetAction(9, IgnoreAction()); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("SetAction(9) = %v, want ErrInvalidParam", err)
	}
	if err := s.SetAction(9, HandlerAction(1, 0, false)); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("SetAction(9) = %v, want ErrInvalidParam", err)
	}
	// Kill pending in a private set terminates on another thread's Deliver.
	mustSendTo(t, s, 2, 9, 0)
	d := mustDeliver(t, s, 3)
	if d.Kind != DeliverTerminated || d.Sig != 9 {
		t.Fatalf("Deliver(3) = %v, want terminated sig=9", d)
	}
	if _, err := s.Deliver(1); !errors.Is(err, ErrProcessTerminated) {
		t.Fatalf("Deliver after kill = %v, want ErrProcessTerminated", err)
	}

	// Kill pending in the shared set also terminates.
	s2 := mustNew(t, 4, 0)
	mustAddThread(t, s2, 0)
	mustSend(t, s2, 9, 0)
	d = mustDeliver(t, s2, 2)
	if d.Kind != DeliverTerminated || d.Sig != 9 {
		t.Fatalf("Deliver(2) = %v, want terminated sig=9", d)
	}
}

// A default (terminate) disposition terminates the process at Deliver.
func TestDefaultTerminate(t *testing.T) {
	s := mustNew(t, 4, 0)
	mustSend(t, s, 5, 0)
	d := mustDeliver(t, s, 1)
	if d.Kind != DeliverTerminated || d.Sig != 5 {
		t.Fatalf("Deliver = %v, want terminated sig=5", d)
	}
	// Ignored signals pending ahead of a terminating signal are discarded
	// first and counted.
	s2 := mustNew(t, 4, bitsOf(17))
	mustSend(t, s2, 17, 0) // queued because leader masks it
	mustSend(t, s2, 20, 0) // default action: terminate
	must(t, s2.SetMask(1, 0))
	d = mustDeliver(t, s2, 1)
	if d.Kind != DeliverTerminated || d.Sig != 20 || d.Discarded != 1 {
		t.Fatalf("Deliver = %v, want terminated sig=20 discarded=1", d)
	}
}

// Thread creation stops at 64 threads.
func TestTooManyThreads(t *testing.T) {
	s := mustNew(t, 0, 0)
	for i := 2; i <= MaxThreads; i++ {
		tid, err := s.AddThread(0)
		if err != nil || tid != i {
			t.Fatalf("AddThread #%d = %d, %v", i, tid, err)
		}
	}
	if _, err := s.AddThread(0); !errors.Is(err, ErrTooManyThreads) {
		t.Fatalf("AddThread #65 = %v, want ErrTooManyThreads", err)
	}
}

// Rejected operations must not change any observable state.
func TestRejectedOpsKeepState(t *testing.T) {
	s := mustNew(t, 1, bitsOf(10))
	mustAddThread(t, s, 0)
	must(t, s.SetAction(12, HandlerAction(7, bitsOf(13), false)))
	mustSend(t, s, 12, 0)
	mustSend(t, s, 34, 99)
	mustDeliver(t, s, 1) // enter handler 7, mask {10,12,13}

	snapshot := func() string {
		out := fmt.Sprintf("curr=%d rtq=%d lost=%d shared=%v",
			s.Curr(), s.RTQ(), s.Lost(), s.SharedPending())
		for tid := 1; tid <= 2; tid++ {
			p, _ := s.Pending(tid)
			m, _ := s.Mask(tid)
			out += fmt.Sprintf(" t%d=(%v,%#x)", tid, p, m)
		}
		return out
	}
	before := snapshot()

	rejections := []func() error{
		func() error { _, _, err := s.Send(0, 0); return err },
		func() error { _, _, err := s.Send(65, 0); return err },
		func() error { _, _, err := s.Send(35, 1); return err }, // quota full
		func() error { _, _, err := s.SendTo(9, 5, 0); return err },
		func() error { _, _, err := s.SendTo(1, 65, 0); return err },
		func() error { return s.SetMask(9, 0) },
		func() error { return s.SetAction(9, IgnoreAction()) },
		func() error { return s.SetAction(0, IgnoreAction()) },
		func() error { return s.Sigreturn(2) }, // empty stack
		func() error { _, err := s.Deliver(9); return err },
	}
	wantErrs := []error{
		ErrInvalidParam, ErrInvalidParam, ErrAgain, ErrThreadNotExist,
		ErrInvalidParam, ErrThreadNotExist, ErrInvalidParam,
		ErrInvalidParam, ErrNoHandler, ErrThreadNotExist,
	}
	for i, op := range rejections {
		if err := op(); !errors.Is(err, wantErrs[i]) {
			t.Fatalf("rejection #%d = %v, want %v", i, err, wantErrs[i])
		}
		if got := snapshot(); got != before {
			t.Fatalf("rejection #%d changed state:\nbefore: %s\nafter:  %s", i, before, got)
		}
	}
}

// Error precedence: only the first applicable error is reported.
func TestErrorPrecedence(t *testing.T) {
	s := mustNew(t, 0, 0)
	// SendTo: invalid sig beats unknown thread.
	if _, _, err := s.SendTo(99, 65, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("SendTo(99,65) = %v, want ErrInvalidParam", err)
	}
	// SendTo: unknown thread beats terminated.
	mustSend(t, s, 9, 0)
	mustDeliver(t, s, 1) // terminates
	if _, _, err := s.SendTo(99, 5, 0); !errors.Is(err, ErrThreadNotExist) {
		t.Fatalf("SendTo(99,5) = %v, want ErrThreadNotExist", err)
	}
	// Send: invalid sig beats terminated.
	if _, _, err := s.Send(65, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Send(65) = %v, want ErrInvalidParam", err)
	}
	// SetMask: unknown thread beats terminated.
	if err := s.SetMask(99, 0); !errors.Is(err, ErrThreadNotExist) {
		t.Fatalf("SetMask(99) = %v, want ErrThreadNotExist", err)
	}
	// Deliver: unknown thread beats terminated.
	if _, err := s.Deliver(99); !errors.Is(err, ErrThreadNotExist) {
		t.Fatalf("Deliver(99) = %v, want ErrThreadNotExist", err)
	}
	// Sigreturn: unknown thread, then terminated, then no-handler.
	if err := s.Sigreturn(99); !errors.Is(err, ErrThreadNotExist) {
		t.Fatalf("Sigreturn(99) = %v, want ErrThreadNotExist", err)
	}
	if err := s.Sigreturn(1); !errors.Is(err, ErrProcessTerminated) {
		t.Fatalf("Sigreturn(1) = %v, want ErrProcessTerminated", err)
	}
}

// Concurrent use must be race-free and preserve the invariants.
func TestConcurrent(t *testing.T) {
	s := mustNew(t, 16, 0)
	for i := 0; i < 4; i++ {
		mustAddThread(t, s, 0)
	}
	for sig := 2; sig <= 40; sig++ {
		if sig == SigKill {
			continue
		}
		switch sig % 3 {
		case 0:
			must(t, s.SetAction(sig, HandlerAction(sig, bitsOf(14), sig%2 == 0)))
		case 1:
			must(t, s.SetAction(sig, IgnoreAction()))
		}
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				sig := 2 + (w*7+i)%39
				tid := 1 + (w+i)%5
				switch i % 6 {
				case 0:
					s.Send(sig, int64(i))
				case 1:
					s.SendTo(tid, sig, int64(i))
				case 2:
					s.Deliver(tid)
				case 3:
					s.Sigreturn(tid)
				case 4:
					s.SetMask(tid, bitsOf(2+(i%30)))
				default:
					s.RTQ()
					s.SharedPending()
					s.Pending(tid)
					s.Mask(tid)
					s.Curr()
					s.Lost()
				}
			}
		}(w)
	}
	wg.Wait()
	// Invariants after the storm (process may have terminated).
	if q := s.RTQ(); q < 0 || q > 16 {
		t.Fatalf("RTQ() = %d out of bounds", q)
	}
	rtSum := 0
	for _, e := range s.SharedPending() {
		if e.Sig > MaxStandard {
			rtSum += e.Count
		} else if e.Count != 1 {
			t.Fatalf("standard signal %d has count %d", e.Sig, e.Count)
		}
	}
	for tid := 1; tid <= 5; tid++ {
		p, _ := s.Pending(tid)
		for _, e := range p {
			if e.Sig > MaxStandard {
				rtSum += e.Count
			} else if e.Count != 1 {
				t.Fatalf("standard signal %d has count %d", e.Sig, e.Count)
			}
		}
		m, _ := s.Mask(tid)
		if m&bit(SigKill) != 0 {
			t.Fatalf("thread %d mask contains kill bit", tid)
		}
	}
	if rtSum != s.RTQ() {
		t.Fatalf("RTQ() = %d, but queues hold %d", s.RTQ(), rtSum)
	}
}

// Quota is shared across the shared set and all private sets.
func TestRealtimeQuotaGlobal(t *testing.T) {
	s := mustNew(t, 3, 0)
	mustAddThread(t, s, 0)
	mustSend(t, s, 40, 1)      // shared
	mustSendTo(t, s, 2, 41, 2) // private of 2
	mustSendTo(t, s, 1, 42, 3) // private of 1
	if q := s.RTQ(); q != 3 {
		t.Fatalf("RTQ() = %d, want 3", q)
	}
	if _, _, err := s.Send(43, 4); !errors.Is(err, ErrAgain) {
		t.Fatalf("Send(43) err = %v, want ErrAgain", err)
	}
	checkPending(t, s.SharedPending(), []PendingEntry{{Sig: 40, Count: 1}}, "shared")
	p, _ := s.Pending(2)
	checkPending(t, p, []PendingEntry{{Sig: 41, Count: 1}}, "private(2)")
}

// L=0 rejects every realtime enqueue but allows standard signals.
func TestRealtimeQuotaZero(t *testing.T) {
	s := mustNew(t, 0, 0)
	if _, _, err := s.Send(32, 1); !errors.Is(err, ErrAgain) {
		t.Fatalf("Send(32) err = %v, want ErrAgain", err)
	}
	if _, _, err := s.SendTo(1, 64, 1); !errors.Is(err, ErrAgain) {
		t.Fatalf("SendTo(1,64) err = %v, want ErrAgain", err)
	}
	res, _ := mustSend(t, s, 31, 0)
	if res != SendEnqueued {
		t.Fatalf("Send(31) = %v, want enqueued", res)
	}
	if q := s.RTQ(); q != 0 {
		t.Fatalf("RTQ() = %d, want 0", q)
	}
}
