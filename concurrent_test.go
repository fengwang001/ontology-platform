package ontology

import (
	"errors"
	"sync"
	"testing"
)

var errSeqNotIncreasing = errors.New("fast subscriber seq not strictly increasing")

// 生产者绝不被慢订阅者阻塞：三个永不读取、容量 1、三种满队列策略的订阅者
// 同时存在时，10000 次 Publish 仍必须立即完成，全程没有任何消费者配合。
func TestSlowSubscriberNeverBlocksPublisher(t *testing.T) {
	d := New()
	defer d.Close()
	d.Subscribe(SubscriptionConfig{ID: "old", Prefix: "e", Buffer: 1, OnOverflow: DropOldest})
	d.Subscribe(SubscriptionConfig{ID: "new", Prefix: "e", Buffer: 1, OnOverflow: DropNewest})
	d.Subscribe(SubscriptionConfig{ID: "lag", Prefix: "e", Buffer: 1, OnOverflow: DisconnectLagging})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			if _, err := d.Publish("e", "p", i); err != nil {
				t.Errorf("publish: %v", err)
				return
			}
		}
	}()
	wg.Wait() // 若 Publish 被阻塞，测试挂起即失败——确定性，无 time.Sleep
}

// 高并发扇出：多生产者 + 持续读取者 + 不读取的慢订阅者 + 并发取消/查询。
// 持续读取者缓冲足够大，必须收全且序号严格递增；慢订阅者不影响它。
func TestConcurrentFanout(t *testing.T) {
	d := New()
	defer d.Close()

	const producers = 4
	const perProducer = 2000
	const total = producers * perProducer

	fast, err := d.Subscribe(SubscriptionConfig{ID: "fast", Prefix: "e", Buffer: total})
	if err != nil {
		t.Fatal(err)
	}
	for i, policy := range []OverflowPolicy{DropOldest, DropNewest, DisconnectLagging} {
		if _, err := d.Subscribe(SubscriptionConfig{
			ID:         "slow" + string(rune('a'+i)),
			Prefix:     "e",
			Buffer:     1,
			OnOverflow: policy, // 从不读取的慢订阅者
			OnPending:  DrainPending,
		}); err != nil {
			t.Fatal(err)
		}
	}

	var pwg sync.WaitGroup
	pwg.Add(producers)
	for p := 0; p < producers; p++ {
		go func() {
			defer pwg.Done()
			for i := 0; i < perProducer; i++ {
				if _, err := d.Publish("e", "p", i); err != nil {
					return
				}
			}
		}()
	}

	readerDone := make(chan error, 1)
	go func() {
		var prev uint64
		for m := range fast.C() {
			if m.Seq <= prev {
				readerDone <- errSeqNotIncreasing
				return
			}
			prev = m.Seq
			if prev == total {
				readerDone <- nil
				return
			}
		}
	}()

	var cwg sync.WaitGroup
	cwg.Add(1)
	go func() { // 与扇出并发地取消/查询，要求幂等且无竞态
		defer cwg.Done()
		for i := 0; i < 300; i++ {
			d.Unsubscribe("slowa")
			d.Unsubscribe("ghost")
			d.Targets("e", "p")
		}
	}()

	pwg.Wait()
	cwg.Wait()
	if err := <-readerDone; err != nil {
		t.Fatal(err)
	}
	if got := fast.Stats().Dropped; got != 0 {
		t.Fatalf("fast subscriber dropped %d, expected 0", got)
	}
}
