package ontology

import (
	"sync"
	"testing"
	"time"
)

// 高并发下慢订阅者不得阻塞生产者，也不得影响快订阅者收全。
func TestSlowSubscriberDoesNotBlockProducer(t *testing.T) {
	d := New()
	defer d.Close()

	// 慢订阅者：容量 1，永不读取。
	slow, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 1, Overflow: DropNewest})
	if err != nil {
		t.Fatal(err)
	}
	fast, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 8192, Overflow: DropNewest})
	if err != nil {
		t.Fatal(err)
	}

	const n = 2000
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			if _, err := d.Publish("user1", "name", i); err != nil {
				t.Errorf("publish %d: %v", i, err)
				return
			}
		}
	}()
	// 超时仅作为防挂死的兜底，正常路径立即完成。
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("producer blocked by slow subscriber")
	}

	got := readN(t, fast, n)
	for i, seq := range got {
		if seq != uint64(i+1) {
			t.Fatalf("fast seq[%d] = %d, want %d", i, seq, i+1)
		}
	}
	if fast.Dropped() != 0 {
		t.Fatalf("fast dropped = %d, want 0", fast.Dropped())
	}
	if slow.Dropped() != n-1 {
		t.Fatalf("slow dropped = %d, want %d", slow.Dropped(), n-1)
	}
}

// 多生产者并发 Publish：序号唯一且单调分配，投递不丢不重。
func TestConcurrentPublishers(t *testing.T) {
	d := New()
	defer d.Close()
	const producers = 8
	const perProducer = 250
	const total = producers * perProducer

	s, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: total, Overflow: DropNewest})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				if _, err := d.Publish("user1", "name", i); err != nil {
					t.Errorf("publish: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	seen := make(map[uint64]bool, total)
	prev := uint64(0)
	for i := 0; i < total; i++ {
		m := <-s.C()
		if m.Seq <= prev {
			t.Fatalf("seqs not strictly increasing: %d after %d", m.Seq, prev)
		}
		prev = m.Seq
		if seen[m.Seq] {
			t.Fatalf("duplicate seq %d", m.Seq)
		}
		seen[m.Seq] = true
	}
	if len(seen) != total {
		t.Fatalf("got %d distinct seqs, want %d", len(seen), total)
	}
	if s.Dropped() != 0 {
		t.Fatalf("dropped = %d, want 0", s.Dropped())
	}
}
