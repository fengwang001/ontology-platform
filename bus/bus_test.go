package bus

import (
	"errors"
	"testing"

	"ontology/version"
)

func collect() (*[]Notice, Subscriber) {
	var got []Notice
	return &got, func(n Notice) { got = append(got, n) }
}

func TestDeliverShuffled(t *testing.T) {
	b := New(0, 42)
	got, sub := collect()
	b.Subscribe(sub)
	for v := 1; v <= 10; v++ {
		_ = b.Publish(Notice{Key: "k", Ver: version.New(uint64(v))})
	}
	b.DeliverShuffled()
	if len(*got) != 10 {
		t.Fatalf("乱序投递不应丢失通知, got=%d", len(*got))
	}
	seen := map[uint64]bool{}
	for _, n := range *got {
		seen[uint64(n.Ver)] = true
	}
	if len(seen) != 10 {
		t.Fatal("乱序投递必须恰好包含全部通知各一次")
	}
	if b.Pending() != 0 {
		t.Fatal("投递后队列必须清空")
	}
}

func TestDuplicatedDelivery(t *testing.T) {
	b := New(0, 1)
	var got []Notice
	b.Subscribe(func(n Notice) { got = append(got, n) })
	_ = b.Publish(Notice{Key: "k", Ver: version.New(1)})
	b.DeliverDuplicated(2)
	if len(got) != 3 {
		t.Fatalf("每条应投递 3 份, got=%d", len(got))
	}
}

func TestLossyDelivery(t *testing.T) {
	b := New(0, 7)
	var got []Notice
	b.Subscribe(func(n Notice) { got = append(got, n) })
	for v := 1; v <= 100; v++ {
		_ = b.Publish(Notice{Key: "k", Ver: version.New(uint64(v))})
	}
	b.DeliverWithLoss(0.5)
	st := b.Stats()
	if st.Dropped == 0 || int(st.Delivered) != len(got) {
		t.Fatalf("丢失模拟异常: %+v delivered=%d", st, len(got))
	}
	if int(st.Dropped)+len(got) != 100 {
		t.Fatal("丢失数+投递数必须等于发布数")
	}
}

func TestQueueLimitRejectsWithoutStateChange(t *testing.T) {
	b := New(2, 1)
	if err := b.Publish(Notice{Key: "a", Ver: version.New(1)}); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(Notice{Key: "b", Ver: version.New(2)}); err != nil {
		t.Fatal(err)
	}
	err := b.Publish(Notice{Key: "c", Ver: version.New(3)})
	if !errors.Is(err, ErrQueueLimit) {
		t.Fatalf("超限必须返回 ErrQueueLimit, got %v", err)
	}
	if b.Pending() != 2 || b.Stats().Published != 2 {
		t.Fatal("拒绝不得改变已有队列状态")
	}
	if b.Stats().Rejected != 1 {
		t.Fatal("拒绝次数必须可读出")
	}
}
