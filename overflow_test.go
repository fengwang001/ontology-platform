package ontology

import "testing"

func drainSeqs(s *Subscription) []uint64 {
	var seqs []uint64
	for {
		select {
		case m, ok := <-s.C():
			if !ok {
				return seqs
			}
			seqs = append(seqs, m.Seq)
		default:
			return seqs
		}
	}
}

// 队列容量 2，投递 5 条且无人读取：
// DropOldest 保留最后两条 [4 5]，丢弃 1/2/3，丢弃数与缺口对得上。
func TestDropOldest(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe(SubscriptionConfig{ID: "a", Prefix: "e", Buffer: 2, OnOverflow: DropOldest})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := d.Publish("e", "p", i); err != nil {
			t.Fatal(err)
		}
	}
	got := drainSeqs(s)
	want := []uint64{4, 5}
	if len(got) != len(want) {
		t.Fatalf("seqs=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("seqs=%v want %v", got, want)
		}
	}
	st := s.Stats()
	if st.Dropped != 3 || st.LastDropSeq != 3 {
		t.Fatalf("stats=%+v want Dropped=3 LastDropSeq=3", st)
	}
}

// DropNewest 保留最旧两条 [1 2]，丢弃 3/4/5。
func TestDropNewest(t *testing.T) {
	d := New()
	defer d.Close()
	s, _ := d.Subscribe(SubscriptionConfig{ID: "a", Prefix: "e", Buffer: 2, OnOverflow: DropNewest})
	for i := 0; i < 5; i++ {
		d.Publish("e", "p", i)
	}
	got := drainSeqs(s)
	want := []uint64{1, 2}
	if len(got) != len(want) || got[0] != 1 || got[1] != 2 {
		t.Fatalf("seqs=%v want %v", got, want)
	}
	st := s.Stats()
	if st.Dropped != 3 || st.LastDropSeq != 5 {
		t.Fatalf("stats=%+v want Dropped=3 LastDropSeq=5", st)
	}
}

// DisconnectLagging：前两条恰好填满缓冲，第三条到达时标记落后并断开。
func TestDisconnectLagging(t *testing.T) {
	d := New()
	defer d.Close()
	s, _ := d.Subscribe(SubscriptionConfig{ID: "a", Prefix: "e", Buffer: 2, OnOverflow: DisconnectLagging})
	d.Publish("e", "p", 1)
	d.Publish("e", "p", 2)
	d.Publish("e", "p", 3) // 写满：本条计一次丢弃并断开
	got := drainSeqs(s)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("seqs=%v want [1 2]", got)
	}
	st := s.Stats()
	if !st.Lagged || st.Dropped != 1 || st.LastDropSeq != 3 {
		t.Fatalf("stats=%+v want lagged Dropped=1 LastDropSeq=3", st)
	}
	// 已在队列中的消息保留可读；后续 Publish 对该 ID 不再投递。
	if extra := drainSeqs(s); len(extra) != 0 {
		t.Fatalf("residual after drain=%v", extra)
	}
	if _, err := d.Publish("e", "p", 4); err != nil {
		t.Fatal(err)
	}
	if extra := drainSeqs(s); len(extra) != 0 {
		t.Fatalf("lagged subscriber must not receive new messages, got %v", extra)
	}
}

// 同一轮扇出中，慢订阅者丢弃不得影响快订阅者收全。
func TestOverflowIsolation(t *testing.T) {
	d := New()
	defer d.Close()
	slow, _ := d.Subscribe(SubscriptionConfig{ID: "slow", Prefix: "e", Buffer: 1, OnOverflow: DropNewest})
	fast, _ := d.Subscribe(SubscriptionConfig{ID: "fast", Prefix: "e", Buffer: 10, OnOverflow: DropNewest})
	for i := 0; i < 8; i++ {
		d.Publish("e", "p", i)
	}
	if got := drainSeqs(fast); len(got) != 8 || got[0] != 1 || got[7] != 8 {
		t.Fatalf("fast subscriber should receive all 8, got %v", got)
	}
	if got := drainSeqs(slow); len(got) != 1 || got[0] != 1 {
		t.Fatalf("slow keeps only oldest 1, got %v", got)
	}
	if slow.Stats().Dropped != 7 {
		t.Fatalf("slow Dropped=%d want 7", slow.Stats().Dropped)
	}
	if fast.Stats().Dropped != 0 {
		t.Fatalf("fast Dropped=%d want 0", fast.Stats().Dropped)
	}
}
