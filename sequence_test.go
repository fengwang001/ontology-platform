package ontology

import "testing"

// 序号缺口必须与丢弃计量一一对应。
func TestGapsMatchDroppedCount(t *testing.T) {
	d := New()
	defer d.Close()

	oldest, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 2, Overflow: DropOldest})
	if err != nil {
		t.Fatal(err)
	}
	newest, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 2, Overflow: DropNewest})
	if err != nil {
		t.Fatal(err)
	}

	// 阶段一：发布 3 条。oldest 队列 [2 3]（丢弃 1）；
	// newest 队列 [1 2]（丢弃 3）。
	publishN(t, d, 3)
	// 阶段二：各读 1 条腾出空间，再发第 4 条。
	// oldest 收到 2，队列 [3 4]；newest 收到 1，队列 [2 4]。
	gotOldest := readN(t, oldest, 1)
	gotNewest := readN(t, newest, 1)
	publishN(t, d, 1)
	gotOldest = append(gotOldest, readN(t, oldest, 2)...) // [2 3 4]
	gotNewest = append(gotNewest, readN(t, newest, 2)...) // [1 2 4]

	// DropOldest：缺口在首部，首条之前的序号（got[0]-1 个）全部丢失。
	if lost := gotOldest[0] - 1 + GapCount(gotOldest); lost != oldest.Dropped() {
		t.Fatalf("DropOldest: lost %d != dropped %d", lost, oldest.Dropped())
	}
	// DropNewest：缺口 {3} 是内部缺口，与丢弃数一一对应。
	if GapCount(gotNewest) != newest.Dropped() {
		t.Fatalf("DropNewest: gap count %d != dropped %d", GapCount(gotNewest), newest.Dropped())
	}
	gaps := Gaps(gotNewest)
	if len(gaps) != 1 || gaps[0] != [2]uint64{3, 3} {
		t.Fatalf("DropNewest gaps = %v, want [{3 3}]", gaps)
	}
	if newest.LastDroppedSeq() != 3 {
		t.Fatalf("DropNewest lastDroppedSeq = %d, want 3", newest.LastDroppedSeq())
	}
	if oldest.LastDroppedSeq() != 1 {
		t.Fatalf("DropOldest lastDroppedSeq = %d, want 1", oldest.LastDroppedSeq())
	}
}

// 全量不变式：队列排空后，收到条数 + 丢弃条数 == 已发布条数。
func TestReceivedPlusDroppedEqualsPublished(t *testing.T) {
	d := New()
	defer d.Close()
	const n = 50
	s, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 3, Overflow: DropOldest})
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, n)
	got := readN(t, s, 3)
	if received, dropped := uint64(len(got)), s.Dropped(); received+dropped != n {
		t.Fatalf("received %d + dropped %d != published %d", received, dropped, n)
	}
	if got[len(got)-1] != n {
		t.Fatalf("last received seq = %d, want %d", got[len(got)-1], n)
	}
}

// 每个订阅者收到的序号必须严格递增。
func TestReceivedSeqsStrictlyIncreasing(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 4, Overflow: DropOldest})
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 20)
	got := readN(t, s, 4)
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("seqs not strictly increasing: %v", got)
		}
	}
	if got[len(got)-1] != 20 {
		t.Fatalf("last seq = %d, want 20", got[len(got)-1])
	}
}

// 丢弃计量是按订阅者独立归因的。
func TestDroppedAccountingIsPerSubscriber(t *testing.T) {
	d := New()
	defer d.Close()
	a, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 1, Overflow: DropNewest})
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 8, Overflow: DropNewest})
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 4)
	if a.Dropped() != 3 || a.LastDroppedSeq() != 4 {
		t.Fatalf("a: dropped=%d last=%d, want 3/4", a.Dropped(), a.LastDroppedSeq())
	}
	if b.Dropped() != 0 || b.LastDroppedSeq() != 0 {
		t.Fatalf("b: dropped=%d last=%d, want 0/0", b.Dropped(), b.LastDroppedSeq())
	}
}
