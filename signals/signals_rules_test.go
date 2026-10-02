package signals

import (
	"errors"
	"fmt"
	"testing"
)

func mustAddThread(t *testing.T, s *Subsystem, mask uint64) int {
	t.Helper()
	tid, err := s.AddThread(mask)
	if err != nil {
		t.Fatalf("AddThread(%#x): %v", mask, err)
	}
	return tid
}

func mustSend(t *testing.T, s *Subsystem, sig int, value int) EnqueueResult {
	t.Helper()
	result, err := s.Send(sig, value)
	if err != nil {
		t.Fatalf("Send(%d,%d): %v", sig, value, err)
	}
	return result
}

func (s *Subsystem) stateSnapshotForTest() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	queues := ""
	for tid, target := range s.threads[1:] {
		queues += fmt.Sprintf(";t%d(m=%x,stack=%v,std=%x,rt=%d)",
			tid+1, target.mask, target.maskStack, target.stdPending, target.rtCount)
	}
	return fmt.Sprintf("curr=%d,lost=%d,shared(std=%x,rt=%d),terminated=%v%s",
		s.curr, s.lost, s.shared.std, s.shared.rtCount, s.terminated, queues)
}

func TestTargetLeaderAndCursorWrap(t *testing.T) {
	s, _ := New(10, bit(10))
	mustAddThread(t, s, bit(10))
	mustAddThread(t, s, 0)

	if got := mustSend(t, s, 10, 0); got.TargetThread != 3 || s.Curr() != 3 {
		t.Fatalf("target=%+v curr=%d", got, s.Curr())
	}
	if got := mustSend(t, s, 10, 0); got.Kind != EnqueueMerged {
		t.Fatalf("repeat = %+v", got)
	}

	if err := s.SetMask(3, bit(10)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMask(2, 0); err != nil {
		t.Fatal(err)
	}
	s.shared.std &^= bit(10)
	if got := mustSend(t, s, 10, 0); got.TargetThread != 2 || s.Curr() != 2 {
		t.Fatalf("wrapped target=%+v curr=%d", got, s.Curr())
	}

	s.shared.std &^= bit(10)
	if err := s.SetMask(2, bit(10)); err != nil {
		t.Fatal(err)
	}
	if got := mustSend(t, s, 10, 0); got.TargetThread != 0 || got.Kind != EnqueueQueued {
		t.Fatalf("blocked result=%+v", got)
	}

	got := mustSend(t, s, 12, 0)
	if got.TargetThread != 1 || s.Curr() != 2 {
		t.Fatalf("leader target=%+v curr=%d", got, s.Curr())
	}
}

func TestStandardMergesPerPendingCopy(t *testing.T) {
	s, _ := New(10, 0)
	tid := mustAddThread(t, s, 0)

	first := mustSend(t, s, 10, 0)
	second := mustSend(t, s, 10, 0)
	privateFirst, err := s.SendTo(tid, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	privateSecond, err := s.SendTo(tid, 10, 0)
	if err != nil {
		t.Fatal(err)
	}

	if first.Kind != EnqueueQueued || second.Kind != EnqueueMerged ||
		privateFirst.Kind != EnqueueQueued || privateSecond.Kind != EnqueueMerged || s.Lost() != 2 {
		t.Fatalf("results=%+v,%+v,%+v,%+v lost=%d", first, second, privateFirst, privateSecond, s.Lost())
	}
}

func TestRTFIFOAndQuotaBoundaries(t *testing.T) {
	s, _ := New(2, 0)
	if err := s.SetAction(34, Action{Kind: ActionHandler, HandlerID: 340}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAction(35, Action{Kind: ActionHandler, HandlerID: 350}); err != nil {
		t.Fatal(err)
	}
	for i, value := range []int{111, 222} {
		result := mustSend(t, s, 34, value)
		if result.Kind != EnqueueQueued || s.RTQ() != i+1 {
			t.Fatalf("send %d = %+v rtq=%d", value, result, s.RTQ())
		}
	}
	if _, err := s.Send(35, 5); !errors.Is(err, ErrAgain) {
		t.Fatalf("full quota error=%v", err)
	}

	d, err := s.Deliver(1)
	if err != nil || d.Signal != 34 || d.Value != 111 {
		t.Fatalf("deliver = %+v,%v", d, err)
	}
	result := mustSend(t, s, 35, 5)
	if result.Kind != EnqueueQueued || s.RTQ() != 2 {
		t.Fatalf("one-slot result=%+v rtq=%d", result, s.RTQ())
	}

	s2, _ := New(0, 0)
	if _, err := s2.Send(32, 1); !errors.Is(err, ErrAgain) {
		t.Fatalf("L=0 error=%v", err)
	}
}

func TestIgnoreDropRetainAndFlush(t *testing.T) {
	s, _ := New(10, bit(17))
	tid := mustAddThread(t, s, 0)

	private, err := s.SendTo(1, 17, 0)
	if err != nil || private.Kind != EnqueueQueued {
		t.Fatalf("retained ignored private send=%+v,%v", private, err)
	}
	if _, err := s.SendTo(tid, 34, 7); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAction(34, Action{Kind: ActionIgnore}); err != nil {
		t.Fatal(err)
	}
	if s.RTQ() != 0 {
		t.Fatalf("rtq=%d", s.RTQ())
	}

	if err := s.SetMask(1, 0); err != nil {
		t.Fatal(err)
	}
	d, err := s.Deliver(1)
	if err != nil || d.Kind != DeliverNone || d.Discarded != 1 {
		t.Fatalf("deliver retained ignored = %+v,%v", d, err)
	}
	if got := mustSend(t, s, 17, 0); got.Kind != EnqueueDiscarded {
		t.Fatalf("drop result=%+v", got)
	}
}

func TestDeliveryPriorityAndMasks(t *testing.T) {
	s, _ := New(10, 0)

	if err := s.SetAction(4, Action{Kind: ActionHandler, HandlerID: 40}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAction(1, Action{Kind: ActionHandler, HandlerID: 10}); err != nil {
		t.Fatal(err)
	}
	mustSend(t, s, 1, 0)
	mustSend(t, s, 4, 0)
	d, err := s.Deliver(1)
	if err != nil || d.Signal != 4 {
		t.Fatalf("sync priority = %+v,%v", d, err)
	}

	if err := s.SetAction(32, Action{Kind: ActionHandler, HandlerID: 320}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendTo(1, 32, 9); err != nil {
		t.Fatal(err)
	}
	d, err = s.Deliver(1)
	if err != nil || d.Signal != 32 || d.FromShared || d.Value != 9 {
		t.Fatalf("private priority = %+v,%v", d, err)
	}

	if err := s.SetMask(1, bit(13)); err != nil {
		t.Fatal(err)
	}
	if err := s.Sigreturn(1); err != nil {
		t.Fatal(err)
	}
	if err := s.Sigreturn(1); err != nil {
		t.Fatal(err)
	}
	if mask, _ := s.Mask(1); mask != 0 {
		t.Fatalf("sigreturn should replace in-handler mask, got %#x", mask)
	}

	s2, _ := New(10, 0)
	if err := s2.SetAction(5, Action{Kind: ActionHandler, HandlerID: 50, AdditionalMask: bit(6), NoDefer: true}); err != nil {
		t.Fatal(err)
	}
	mustSend(t, s2, 5, 0)
	if _, err := s2.Deliver(1); err != nil {
		t.Fatal(err)
	}
	if mask, _ := s2.Mask(1); mask != bit(6) {
		t.Fatalf("nodefer mask=%#x", mask)
	}
}

func TestKillAndTerminatedRejections(t *testing.T) {
	s, _ := New(10, bit(killSig))
	tid := mustAddThread(t, s, bit(killSig))
	if got := mustSend(t, s, killSig, 0); got.TargetThread != 1 {
		t.Fatalf("kill target=%+v", got)
	}
	d, err := s.Deliver(tid)
	if err != nil || d.Kind != DeliverTerminated || d.Signal != killSig {
		t.Fatalf("kill deliver=%+v,%v", d, err)
	}

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"add", func() error { _, err := s.AddThread(0); return err }},
		{"action", func() error { return s.SetAction(1, Action{}) }},
		{"mask", func() error { return s.SetMask(1, 0) }},
		{"send", func() error { _, err := s.Send(1, 0); return err }},
		{"sendto", func() error { _, err := s.SendTo(1, 1, 0); return err }},
		{"deliver", func() error { _, err := s.Deliver(1); return err }},
		{"sigreturn", func() error { return s.Sigreturn(1) }},
	} {
		if err := tc.call(); !errors.Is(err, ErrProcessTerminated) {
			t.Fatalf("%s after termination error=%v", tc.name, err)
		}
	}
}

func TestRejectedOperationsDoNotMutateState(t *testing.T) {
	s, _ := New(1, bit(10))
	tid := mustAddThread(t, s, 0)
	if err := s.SetAction(11, Action{Kind: ActionHandler, HandlerID: 11}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(32, 7); err != nil {
		t.Fatal(err)
	}

	before := s.stateSnapshotForTest()
	for _, call := range []func() error{
		func() error { _, err := s.Send(0, 1); return err },
		func() error { _, err := s.SendTo(tid, 0, 1); return err },
		func() error { _, err := s.SendTo(99, 11, 1); return err },
		func() error { return s.SetMask(99, 0) },
		func() error { return s.SetAction(killSig, Action{Kind: ActionIgnore}) },
		func() error { _, err := s.Send(33, 1); return err },
		func() error { _, err := s.Deliver(99); return err },
		func() error { return s.Sigreturn(99) },
	} {
		if err := call(); err == nil {
			t.Fatal("rejected operation unexpectedly succeeded")
		}
		if after := s.stateSnapshotForTest(); after != before {
			t.Fatalf("state changed after rejected call:\nbefore=%s\nafter=%s", before, after)
		}
	}
}

func TestThreadLimit(t *testing.T) {
	s, _ := New(10, 0)
	for want := 2; want <= 64; want++ {
		if got, err := s.AddThread(0); err != nil || got != want {
			t.Fatalf("AddThread() = (%d, %v), want tid %d", got, err, want)
		}
	}
	if _, err := s.AddThread(0); !errors.Is(err, ErrTooManyThreads) {
		t.Fatalf("65th thread error = %v", err)
	}
}

func TestExaminedFlushBoundIgnoresOtherSignals(t *testing.T) {
	s, _ := New(10, 0)
	tid := mustAddThread(t, s, 0)
	if _, err := s.SendTo(1, 34, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendTo(tid, 34, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendTo(tid, 35, 3); err != nil {
		t.Fatal(err)
	}

	before := s.Examined()
	if err := s.SetAction(34, Action{Kind: ActionIgnore}); err != nil {
		t.Fatal(err)
	}
	got := s.Examined() - before
	want := (len(s.threads) - 1 + 1) + 2
	if got > want {
		t.Fatalf("examined delta=%d want <= %d; unrelated RT entry must not count", got, want)
	}
	if items, _ := s.Pending(tid); len(items) != 1 || items[0].Signal != 35 {
		t.Fatalf("unrelated pending=%+v", items)
	}
}
