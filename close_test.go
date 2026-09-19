package ontology

import (
	"errors"
	"sync"
	"testing"
)

// 关闭后 Publish 返回可判定的错误且不再投递；再订阅失败。
func TestPublishAndSubscribeAfterClose(t *testing.T) {
	d := New()
	s, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 8, Drain: DrainPending})
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 2)
	d.Close()

	if _, err := d.Publish("user1", "name", 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("Publish after close = %v, want ErrClosed", err)
	}
	if _, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 1}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Subscribe after close = %v, want ErrClosed", err)
	}
	// 已入队的两条按 DrainPending 仍可读完，之后通道关闭。
	if got := readN(t, s, 2); !equalSeqs(got, []uint64{1, 2}) {
		t.Fatalf("got %v, want [1 2]", got)
	}
	if _, ok := <-s.C(); ok {
		t.Fatal("expected channel closed")
	}
}

// 关闭是幂等的，并发关闭也安全。
func TestCloseIsIdempotent(t *testing.T) {
	d := New()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.Close()
		}()
	}
	wg.Wait()
	d.Close()
}

// 关闭时正在进行的 Publish 必须原子：要么完整投递给所有
// 订阅者，要么完全不生效。两个订阅者收到的序号集合必须相等，
// 且恰好等于所有成功 Publish 的序号集合。
func TestCloseIsAtomicWithPublish(t *testing.T) {
	d := New()
	a, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 4096, Drain: DrainPending})
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 4096, Drain: DrainPending})
	if err != nil {
		t.Fatal(err)
	}

	const n = 500
	okSeqs := make(chan uint64, n)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < n/4; j++ {
				seq, err := d.Publish("user1", "name", j)
				if errors.Is(err, ErrClosed) {
					return
				}
				if err != nil {
					t.Errorf("unexpected publish error: %v", err)
					return
				}
				okSeqs <- seq
			}
		}()
	}
	wg.Wait()
	d.Close()
	close(okSeqs)

	want := make(map[uint64]bool)
	for seq := range okSeqs {
		want[seq] = true
	}
	for _, s := range []*Subscription{a, b} {
		got := make(map[uint64]bool)
		for m := range s.C() {
			got[m.Seq] = true
		}
		if len(got) != len(want) {
			t.Fatalf("sub %d got %d messages, want %d", s.ID(), len(got), len(want))
		}
		for seq := range want {
			if !got[seq] {
				t.Fatalf("sub %d missing seq %d: partial fan-out", s.ID(), seq)
			}
		}
	}
}

// 关闭时 DropPending 的订阅者丢弃未读消息。
func TestCloseDropsPending(t *testing.T) {
	d := New()
	s, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 8, Drain: DropPending})
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 3)
	d.Close()
	if _, ok := <-s.C(); ok {
		t.Fatal("expected pending messages to be dropped on close")
	}
}
