package ontology

import (
	"errors"
	"sync"
	"testing"
)

func TestValidationPriorityAndRejectionAtomicity(t *testing.T) {
	node := mustNode(t, "a", neighborSet("b"), 2, 3)
	if _, err := node.OnGossip(10, "b", "seen", "payload"); err != nil {
		t.Fatal(err)
	}

	if _, err := node.OnGossip(9, "x", "", "payload"); !errors.Is(err, ErrClockMovedBack) {
		t.Fatalf("OnGossip error = %v, want %v", err, ErrClockMovedBack)
	}
	if _, err := node.OnGossip(11, "x", "", "payload"); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("OnGossip error = %v, want %v", err, ErrEmptyID)
	}
	if _, err := node.OnGossip(11, "x", "seen", "payload"); !errors.Is(err, ErrUnknownNeighbor) {
		t.Fatalf("OnGossip error = %v, want %v", err, ErrUnknownNeighbor)
	}
	if _, err := node.OnPrune(9, "x"); !errors.Is(err, ErrClockMovedBack) {
		t.Fatalf("OnPrune error = %v, want %v", err, ErrClockMovedBack)
	}
	if _, err := node.OnPrune(12, "x"); !errors.Is(err, ErrUnknownNeighbor) {
		t.Fatalf("OnPrune error = %v, want %v", err, ErrUnknownNeighbor)
	}
	if _, err := node.Broadcast(8, "", ""); !errors.Is(err, ErrClockMovedBack) {
		t.Fatalf("Broadcast error = %v, want %v", err, ErrClockMovedBack)
	}
	if _, err := node.Broadcast(13, "", ""); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("Broadcast error = %v, want %v", err, ErrEmptyID)
	}
	if _, err := node.Broadcast(13, "seen", "changed"); !errors.Is(err, ErrAlreadyDelivered) {
		t.Fatalf("Broadcast error = %v, want %v", err, ErrAlreadyDelivered)
	}
	if _, err := node.Tick(7); !errors.Is(err, ErrClockMovedBack) {
		t.Fatalf("Tick error = %v, want %v", err, ErrClockMovedBack)
	}

	if node.lastNow != 10 {
		t.Fatalf("lastNow = %d, rejected operation changed clock", node.lastNow)
	}
	if node.messages["seen"] != "payload" {
		t.Fatalf("seen payload = %q, rejected operation changed state", node.messages["seen"])
	}
	assertSet(t, node.eager, "b")
	assertSet(t, node.lazy)
}

func TestRejectedIHaveDoesNotCreateTimer(t *testing.T) {
	node := mustNode(t, "a", neighborSet("b"), 2, 3)
	if _, err := node.OnIHave(5, "x", "m1"); !errors.Is(err, ErrUnknownNeighbor) {
		t.Fatalf("error = %v, want %v", err, ErrUnknownNeighbor)
	}
	if _, pending := node.pending["m1"]; pending {
		t.Fatalf("rejected IHAVE created timer: %#v", node.pending["m1"])
	}
}

func TestConcurrentDuplicateBroadcastHasOneSuccess(t *testing.T) {
	node := mustNode(t, "a", neighborSet("b", "c"), 2, 3)
	const goroutines = 64
	var wg sync.WaitGroup
	successes := make(chan []Message, goroutines)
	failures := make(chan error, goroutines)
	start := make(chan struct{})

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			<-start
			messages, err := node.Broadcast(1, "same", "payload")
			if err != nil {
				failures <- err
			} else {
				successes <- messages
			}
		}()
	}
	close(start)
	wg.Wait()
	close(successes)
	close(failures)

	if len(successes) != 1 {
		t.Fatalf("successes = %d, want 1", len(successes))
	}
	if len(failures) != goroutines-1 {
		t.Fatalf("failures = %d, want %d", len(failures), goroutines-1)
	}
	for err := range failures {
		if !errors.Is(err, ErrAlreadyDelivered) {
			t.Fatalf("failure = %v, want %v", err, ErrAlreadyDelivered)
		}
	}
	messages := <-successes
	assertMessages(t, messages, []Message{
		{Target: "b", Type: Gossip, ID: "same", Payload: "payload"},
		{Target: "c", Type: Gossip, ID: "same", Payload: "payload"},
	})

	messages[0].Target = "mutated"
	again, err := node.Broadcast(2, "next", "payload")
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Target == "mutated" {
		t.Fatalf("returned message slice appears aliased to internal state")
	}
}

func TestConcurrentUniqueBroadcastsDeliverOnce(t *testing.T) {
	node := mustNode(t, "a", neighborSet("b", "c", "d"), 2, 3)
	const goroutines = 40
	var wg sync.WaitGroup
	start := make(chan struct{})

	wg.Add(goroutines)
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			<-start
			if _, err := node.Broadcast(1, "m-"+string(rune('a'+i)), "p"); err != nil {
				t.Errorf("Broadcast(%d) error = %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if len(node.messages) != goroutines {
		t.Fatalf("delivered messages = %d, want %d", len(node.messages), goroutines)
	}
}
