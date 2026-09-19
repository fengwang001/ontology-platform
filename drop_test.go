package ontology

import (
	"sync"
	"testing"
)

func TestDropAccountingMatchesSeqGaps(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	const total = 10
	sub := mustSubscribe(t, d, SubscribeOptions{
		Prefix: "e", BufferSize: 3, Full: FullDropOldest,
	})
	for i := 0; i < total; i++ {
		mustPublish(t, d, "e1", "p", i)
	}
	received := tryRecvSeqs(sub)
	dropped, last := sub.Dropped()
	if gaps := countGaps(received, total); gaps != dropped {
		t.Fatalf("gaps=%d != dropped=%d (received %v)", gaps, dropped, received)
	}
	if dropped != 7 || last != 7 {
		t.Fatalf("dropped=%d last=%d, want 7/7", dropped, last)
	}
}

func TestSeqStrictlyIncreasingWithGaps(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	const total = 2000
	sub := mustSubscribe(t, d, SubscribeOptions{
		Prefix: "e", BufferSize: 4, Full: FullDropNewest,
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < total; i++ {
			if err := d.Publish("e1", "p", i); err != nil {
				t.Errorf("Publish: %v", err)
				return
			}
		}
		sub.Cancel()
	}()
	var prev uint64
	var received uint64
	for {
		m, ok := sub.Receive()
		if !ok {
			break
		}
		if m.Seq <= prev {
			t.Fatalf("seq not strictly increasing: %d after %d", m.Seq, prev)
		}
		prev = m.Seq
		received++
	}
	wg.Wait()
	dropped, _ := sub.Dropped()
	if received+dropped != total {
		t.Fatalf("received %d + dropped %d != %d", received, dropped, total)
	}
}

func TestDroppedInitiallyZero(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	sub := mustSubscribe(t, d, SubscribeOptions{Prefix: "e", BufferSize: 1})
	if dropped, last := sub.Dropped(); dropped != 0 || last != 0 {
		t.Fatalf("fresh subscription dropped=%d last=%d", dropped, last)
	}
}
