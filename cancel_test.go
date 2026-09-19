package ontology

import (
	"sync"
	"testing"
)

// 取消后不得再收到任何消息（DropPending：未读消息被丢弃）。
func TestCancelDropPending(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 8, Overflow: DropNewest, Drain: DropPending})
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 3)
	s.Cancel()
	publishN(t, d, 3)
	if _, ok := <-s.C(); ok {
		t.Fatal("expected no messages after cancel with DropPending")
	}
}

// DrainPending：取消后队列中剩余消息允许读完。
func TestCancelDrainPending(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 8, Overflow: DropNewest, Drain: DrainPending})
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 3)
	s.Cancel()
	publishN(t, d, 3)
	got := readN(t, s, 3)
	if !equalSeqs(got, []uint64{1, 2, 3}) {
		t.Fatalf("got %v, want [1 2 3]", got)
	}
	if _, ok := <-s.C(); ok {
		t.Fatal("expected channel closed after draining")
	}
}

// 重复取消不得 panic 也不得报错。
func TestCancelIsIdempotent(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 1})
	if err != nil {
		t.Fatal(err)
	}
	s.Cancel()
	s.Cancel()
	s.Cancel()
	if !s.Canceled() {
		t.Fatal("expected canceled")
	}
}

// 并发取消不得 panic 或数据竞争。
func TestConcurrentCancel(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 4})
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 4)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Cancel()
		}()
	}
	wg.Wait()
	if !s.Canceled() {
		t.Fatal("expected canceled")
	}
}

// 取消正在被扇出的订阅者，不得让 Publish 失败，
// 也不得丢失对其他订阅者的投递。
func TestCancelDuringPublish(t *testing.T) {
	d := New()
	defer d.Close()
	victim, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 4, Overflow: DropNewest})
	if err != nil {
		t.Fatal(err)
	}
	bystander, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 4096, Overflow: DropNewest})
	if err != nil {
		t.Fatal(err)
	}
	const n = 200
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if _, err := d.Publish("user1", "name", i); err != nil {
				t.Errorf("publish failed: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		victim.Cancel()
	}()
	wg.Wait()
	got := readN(t, bystander, n)
	for i, seq := range got {
		if seq != uint64(i+1) {
			t.Fatalf("bystander seq[%d] = %d, want %d", i, seq, i+1)
		}
	}
	if bystander.Dropped() != 0 {
		t.Fatalf("bystander dropped = %d, want 0", bystander.Dropped())
	}
}
