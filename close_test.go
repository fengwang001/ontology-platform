package ontology

import "testing"

// 关闭后 Publish 返回可判定错误且不再投递；再订阅失败；关闭幂等。
func TestCloseSemantics(t *testing.T) {
	d := New()
	s, _ := d.Subscribe(SubscriptionConfig{ID: "a", Prefix: "e", Buffer: 5})
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("repeat close: %v", err)
	}
	if _, err := d.Publish("e", "p", 1); err != ErrClosed {
		t.Fatalf("publish err=%v want ErrClosed", err)
	}
	if _, err := d.Subscribe(SubscriptionConfig{ID: "b", Buffer: 1}); err != ErrClosed {
		t.Fatalf("subscribe err=%v want ErrClosed", err)
	}
	if _, ok := <-s.C(); ok {
		t.Fatal("DropPending channel should be closed on Close")
	}
}

// Close 的原子性：Publish 与 Close 持同一把锁，不会出现"一次 Publish 只投递给
// 部分订阅者"的中间态。用小缓冲 DropNewest：关闭前每次被接受的 Publish 要么让
// 所有存活订阅者都收到，要么都因各自满队列而丢同一条——各订阅者收到数量必须相等。
// 关闭后的 Publish 完全不生效。
func TestCloseAtomic(t *testing.T) {
	d := New()
	var subs []*Subscription
	for _, id := range []string{"a", "b", "c"} {
		s, _ := d.Subscribe(SubscriptionConfig{ID: id, Prefix: "e", Buffer: 8, OnOverflow: DropNewest})
		subs = append(subs, s)
	}
	const total = 500
	var accepted int
	for i := 0; i < total; i++ {
		if _, err := d.Publish("e", "p", i); err == nil {
			accepted++
		}
	}
	// 关闭前先统计各订阅者队列：DropPending 会在 Close 时清空残留。
	counts := make([]int, len(subs))
	for i, s := range subs {
		counts[i] = len(drainSeqs(s))
	}
	d.Close()
	for i, n := range counts {
		if n != counts[0] {
			t.Fatalf("partial fanout: subscriber %d got %d, subscriber 0 got %d", i, n, counts[0])
		}
	}
	if counts[0] == 0 || counts[0] > accepted {
		t.Fatalf("unexpected received count %d accepted=%d", counts[0], accepted)
	}
}

// Close 时 DrainPending 的订阅者残留可读，DropPending 的残留被丢弃并关闭。
func TestClosePendingPolicies(t *testing.T) {
	d := New()
	drain, _ := d.Subscribe(SubscriptionConfig{ID: "d", Prefix: "e", Buffer: 5, OnPending: DrainPending})
	drop, _ := d.Subscribe(SubscriptionConfig{ID: "x", Prefix: "e", Buffer: 5, OnPending: DropPending})
	for i := 0; i < 3; i++ {
		d.Publish("e", "p", i)
	}
	d.Close()
	if got := drainSeqs(drain); len(got) != 3 {
		t.Fatalf("drain got=%v want 3 residual", got)
	}
	st := drop.Stats()
	if !st.Closed || st.Dropped != 3 {
		t.Fatalf("drop stats=%+v want closed Dropped=3", st)
	}
}
