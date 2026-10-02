package signals

import "testing"

func TestSpecExample(t *testing.T) {
	s, err := New(2, bit(10))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tid2, err := s.AddThread(0)
	if err != nil || tid2 != 2 {
		t.Fatalf("AddThread() = (%d, %v)", tid2, err)
	}
	tid3, err := s.AddThread(0)
	if err != nil || tid3 != 3 {
		t.Fatalf("AddThread() = (%d, %v)", tid3, err)
	}

	if err := s.SetAction(10, Action{Kind: ActionHandler, HandlerID: 7, AdditionalMask: bit(13)}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAction(12, Action{Kind: ActionHandler, HandlerID: 8}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAction(34, Action{Kind: ActionHandler, HandlerID: 9}); err != nil {
		t.Fatal(err)
	}

	r, err := s.Send(10, 0)
	if err != nil || r.Kind != EnqueueQueued || r.TargetThread != 2 || s.Curr() != 2 {
		t.Fatalf("Send(10) = %+v, %v; curr=%d", r, err, s.Curr())
	}
	r, err = s.Send(10, 0)
	if err != nil || r.Kind != EnqueueMerged || s.Lost() != 1 {
		t.Fatalf("second Send(10) = %+v, %v; lost=%d", r, err, s.Lost())
	}
	r, err = s.Send(12, 0)
	if err != nil || r.TargetThread != 1 || s.Curr() != 2 {
		t.Fatalf("Send(12) = %+v, %v; curr=%d", r, err, s.Curr())
	}
	r, err = s.SendTo(3, 12, 0)
	if err != nil || r.Kind != EnqueueQueued || r.TargetThread != 3 {
		t.Fatalf("SendTo(3,12) = %+v, %v", r, err)
	}
	if _, err := s.Send(34, 111); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(34, 222); err != nil {
		t.Fatal(err)
	}
	if got := s.RTQ(); got != 2 {
		t.Fatalf("RTQ() = %d", got)
	}
	if _, err := s.Send(35, 5); err != ErrAgain {
		t.Fatalf("Send(35) error = %v", err)
	}

	d, err := s.Deliver(3)
	if err != nil || d.Kind != DeliverHandler || d.HandlerID != 8 || d.Signal != 12 || d.FromShared {
		t.Fatalf("first Deliver(3) = %+v, %v", d, err)
	}
	if mask, _ := s.Mask(3); mask != bit(12) {
		t.Fatalf("mask = %#x", mask)
	}

	d, err = s.Deliver(3)
	if err != nil || d.HandlerID != 7 || d.Signal != 10 || !d.FromShared {
		t.Fatalf("second Deliver(3) = %+v, %v", d, err)
	}
	if mask, _ := s.Mask(3); mask != bit(10)|bit(12)|bit(13) {
		t.Fatalf("mask = %#x", mask)
	}

	d, err = s.Deliver(3)
	if err != nil || d.HandlerID != 9 || d.Signal != 34 || d.Value != 111 || !d.FromShared {
		t.Fatalf("third Deliver(3) = %+v, %v", d, err)
	}
	if got := s.RTQ(); got != 1 {
		t.Fatalf("RTQ() = %d", got)
	}

	expected := []uint64{bit(10) | bit(12) | bit(13), bit(12), 0}
	for i, want := range expected {
		if err := s.Sigreturn(3); err != nil {
			t.Fatalf("Sigreturn #%d: %v", i+1, err)
		}
		if mask, _ := s.Mask(3); mask != want {
			t.Fatalf("after Sigreturn #%d mask=%#x want %#x", i+1, mask, want)
		}
	}
	if err := s.Sigreturn(3); err != ErrNoHandler {
		t.Fatalf("extra Sigreturn error = %v", err)
	}

	if err := s.SetAction(34, Action{Kind: ActionIgnore}); err != nil {
		t.Fatal(err)
	}
	if got := s.RTQ(); got != 0 {
		t.Fatalf("RTQ after ignore = %d", got)
	}
}
