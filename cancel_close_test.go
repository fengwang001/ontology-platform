package ontology

import (
	"errors"
	"io"
	"sync"
	"testing"
)

func TestUnsubscribeIdempotent(t *testing.T) {
	d := NewDispatcher()
	s, _ := d.Subscribe(SubscribeOptions{Buffer: 5})
	publishN(t, d, 3)
	for i := 0; i < 5; i++ {
		if err := d.Unsubscribe(s); err != nil {
			t.Fatalf("repeated unsubscribe %d: %v", i, err)
		}
	}
	if err := d.Unsubscribe(nil); err != nil {
		t.Fatalf("nil unsubscribe: %v", err)
	}
	// CancelDiscard default: queued messages are gone, immediate EOF.
	if _, err := s.Receive(); !errors.Is(err, io.EOF) {
		t.Fatalf("after discard cancel err = %v, want EOF", err)
	}
}

func TestCancelDrainPolicy(t *testing.T) {
	d := NewDispatcher()
	s, _ := d.Subscribe(SubscribeOptions{Buffer: 5, OnCancel: CancelDrain})
	publishN(t, d, 3)
	d.Unsubscribe(s)
	seqs := drainAll(s)
	if len(seqs) != 3 {
		t.Fatalf("drained %v, want 3 messages", seqs)
	}
	if _, err := s.Receive(); !errors.Is(err, io.EOF) {
		t.Fatalf("after drain err = %v, want EOF", err)
	}
}

func TestCancelDuringFanoutSafe(t *testing.T) {
	d := NewDispatcher()
	const n = 8
	subs := make([]*Subscription, n)
	for i := range subs {
		subs[i], _ = d.Subscribe(SubscribeOptions{Buffer: 1000, OnCancel: CancelDrain})
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				d.Unsubscribe(subs[(idx*2+k)%n])
			}
		}(i)
	}
	for k := 0; k < 500; k++ {
		if _, err := d.Publish(Change{Entity: "e", Attribute: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	d.Close()
}

func TestCloseSemantics(t *testing.T) {
	d := NewDispatcher()
	s, _ := d.Subscribe(SubscribeOptions{Buffer: 10, OnCancel: CancelDrain})
	publishN(t, d, 3)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil { // idempotent
		t.Fatalf("second close: %v", err)
	}
	if _, err := d.Publish(Change{Entity: "e", Attribute: "a"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("publish after close err = %v, want ErrClosed", err)
	}
	if _, err := d.Subscribe(SubscribeOptions{Buffer: 1}); !errors.Is(err, ErrClosed) {
		t.Fatalf("subscribe after close err = %v, want ErrClosed", err)
	}
	if seqs := drainAll(s); len(seqs) != 3 {
		t.Fatalf("drained %v, want 3", seqs)
	}
}

// Close must be atomic relative to Publish: every observed Publish either
// fully fans out to all matching subscribers or is rejected entirely.
func TestCloseAtomicity(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		d := NewDispatcher()
		const n = 6
		subs := make([]*Subscription, n)
		received := make([]int64, n)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range subs {
			subs[i], _ = d.Subscribe(SubscribeOptions{Buffer: 100000, OnCancel: CancelDrain})
		}
		var pubErr error
		var pubSeq int64
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			pubSeq, pubErr = d.Publish(Change{Entity: "e", Attribute: "a"})
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			d.Close()
		}()
		close(start)
		wg.Wait()
		if pubErr != nil {
			if !errors.Is(pubErr, ErrClosed) {
				t.Fatalf("unexpected publish err: %v", pubErr)
			}
			continue // fully rejected
		}
		for i, s := range subs {
			seqs := drainAll(s)
			received[i] = int64(len(seqs))
			if len(seqs) != 1 || seqs[0] != pubSeq {
				t.Fatalf("iter %d sub %d got %v, want exactly [%d]", iter, i, seqs, pubSeq)
			}
		}
	}
}

func TestCloseDiscardPolicy(t *testing.T) {
	d := NewDispatcher()
	s, _ := d.Subscribe(SubscribeOptions{Buffer: 5}) // CancelDiscard
	publishN(t, d, 4)
	d.Close()
	if _, err := s.Receive(); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want EOF", err)
	}
}
