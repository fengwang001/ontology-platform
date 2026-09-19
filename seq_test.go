package ontology

import "testing"

// 订阅者收到的序号必须严格递增；缺口即丢弃区间，可由调用方还原。
func TestSeqStrictlyIncreasingWithGaps(t *testing.T) {
	d := New()
	defer d.Close()
	s, _ := d.Subscribe(SubscriptionConfig{ID: "a", Prefix: "e", Buffer: 3, OnOverflow: DropOldest})

	for i := 0; i < 10; i++ {
		d.Publish("e", "p", i)
	}
	got := drainSeqs(s)
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("seq not strictly increasing: %v", got)
		}
	}

	// 还原缺失区间：应收到 1..10，实际只收到 3 条，缺口数 == 丢弃数。
	st := s.Stats()
	missing := uint64(10) - uint64(len(got))
	if missing != st.Dropped {
		t.Fatalf("missing=%d dropped=%d", missing, st.Dropped)
	}

	seen := map[uint64]bool{}
	var gaps []uint64
	for _, v := range got {
		seen[v] = true
	}
	for seq := uint64(1); seq <= 10; seq++ {
		if !seen[seq] {
			gaps = append(gaps, seq)
		}
	}
	if uint64(len(gaps)) != st.Dropped {
		t.Fatalf("gaps=%v dropped=%d", gaps, st.Dropped)
	}
	// 最后一次丢弃：容量 3 时第 4 条到达即挤掉 seq=1。
	if st.LastDropSeq != 7 {
		// 投递 10 条，保留 8,9,10；最后被挤出的是 seq=7。
		t.Fatalf("LastDropSeq=%d want 7", st.LastDropSeq)
	}
}

// 序号是全局单调递增的，即使消息实体不同。
func TestGlobalMonotonicSeq(t *testing.T) {
	d := New()
	defer d.Close()
	s, _ := d.Subscribe(SubscriptionConfig{ID: "a", Buffer: 100})
	for _, e := range []string{"a", "b", "c", "a", "b"} {
		if _, err := d.Publish(e, "p", nil); err != nil {
			t.Fatal(err)
		}
	}
	got := drainSeqs(s)
	if len(got) != 5 {
		t.Fatalf("got=%v", got)
	}
	for i := uint64(1); i <= 5; i++ {
		if got[i-1] != i {
			t.Fatalf("seqs=%v want 1..5", got)
		}
	}
}

// 非匹配消息也消耗全局序号，订阅者看到的缺口恰好对应"别人的或被丢弃的"消息。
func TestSeqSkipsNonMatchingEntities(t *testing.T) {
	d := New()
	defer d.Close()
	s, _ := d.Subscribe(SubscriptionConfig{ID: "a", Prefix: "user", Buffer: 10})
	d.Publish("user/1", "name", 1) // seq1 命中
	d.Publish("group/1", "name", 2)
	d.Publish("superuser", "name", 3) // 前缀不匹配
	d.Publish("user/2", "age", 4)     // seq4 命中
	got := drainSeqs(s)
	if len(got) != 2 || got[0] != 1 || got[1] != 4 {
		t.Fatalf("got=%v want [1 4]", got)
	}
	if s.Stats().Dropped != 0 {
		t.Fatalf("non-match is not a drop: %+v", s.Stats())
	}
}
