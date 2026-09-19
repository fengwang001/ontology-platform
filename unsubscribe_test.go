package ontology

import (
	"sync"
	"testing"
)

// 取消后不再收到任何消息；重复取消不 panic、不报错。
func TestUnsubscribeIdempotent(t *testing.T) {
	d := New()
	defer d.Close()
	s, _ := d.Subscribe(SubscriptionConfig{ID: "a", Prefix: "e", Buffer: 5})
	d.Publish("e", "p", 1)
	s.Unsubscribe()
	s.Unsubscribe()
	d.Unsubscribe("a")     // 通过分发器重复取消
	d.Unsubscribe("ghost") // 从未存在的 ID

	seq, err := d.Publish("e", "p", 2)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 2 {
		t.Fatalf("publish after unsubscribe seq=%d", seq)
	}
	if _, ok := <-s.C(); ok {
		t.Fatal("DropPending channel should be closed")
	}
}

// DropPending：取消时残留消息全部丢弃并计入计量，通道关闭。
func TestUnsubscribeDropPending(t *testing.T) {
	d := New()
	s, _ := d.Subscribe(SubscriptionConfig{ID: "a", Prefix: "e", Buffer: 5, OnPending: DropPending})
	for i := 0; i < 3; i++ {
		d.Publish("e", "p", i)
	}
	s.Unsubscribe()
	st := s.Stats()
	if !st.Closed || st.Dropped != 3 || st.LastDropSeq != 3 {
		t.Fatalf("stats=%+v want closed Dropped=3 LastDropSeq=3", st)
	}
	if _, ok := <-s.C(); ok {
		t.Fatal("channel should be closed and empty")
	}
}

// DrainPending：取消时允许读完队列残留；新消息一律不再进入。
func TestUnsubscribeDrainPending(t *testing.T) {
	d := New()
	s, _ := d.Subscribe(SubscriptionConfig{ID: "a", Prefix: "e", Buffer: 5, OnPending: DrainPending})
	d.Publish("e", "p", 1)
	d.Publish("e", "p", 2)
	s.Unsubscribe()
	d.Publish("e", "p", 3) // 不再投递

	got := drainSeqs(s)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("got=%v want [1 2]", got)
	}
	if got := drainSeqs(s); len(got) != 0 {
		t.Fatalf("residual after drain=%v", got)
	}
}

// 高并发取消与发布交错：Publish 永不失败、不 panic，其他订阅者不受影响。
func TestConcurrentUnsubscribe(t *testing.T) {
	d := New()
	defer d.Close()
	const n = 8
	subs := make([]*Subscription, n)
	for i := range subs {
		id := string(rune('a' + i))
		subs[i], _ = d.Subscribe(SubscriptionConfig{ID: id, Prefix: "e", Buffer: 1, OnOverflow: DropNewest})
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() { // 生产者持续扇出
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := d.Publish("e", "p", i); err != nil {
				t.Errorf("publish failed: %v", err)
				return
			}
		}
	}()

	wg.Add(n)
	for _, s := range subs { // 每个订阅者都在反复取消
		go func(s *Subscription) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s.Unsubscribe()
			}
		}(s)
	}
	for _, s := range subs {
		for j := 0; j < 50; j++ {
			s.Unsubscribe()
		}
	}
	close(stop)
	wg.Wait()
}
