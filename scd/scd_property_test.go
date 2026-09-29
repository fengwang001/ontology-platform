package scd

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"sync"
	"testing"
)

// TestConsistencyWithRecompute 随机乱序事件流：每批提交后增量历史
// 必须与全量批量重算一致，且与到达顺序无关、可复现。
func TestConsistencyWithRecompute(t *testing.T) {
	rng := rand.New(rand.NewPCG(20260929, 7))
	keys := []string{"k1", "k2", "k3"}
	values := []string{"A", "B", "C", "D"}

	const total = 300
	events := make([]Event, 0, total)
	for i := 0; i < total; i++ {
		e := Event{
			Key:           keys[rng.IntN(len(keys))],
			EffectiveTime: int64(rng.IntN(60)),
			Value:         values[rng.IntN(len(values))],
			Deleted:       rng.IntN(6) == 0,
		}
		events = append(events, e)
	}

	s := NewStore(64)
	s.SetLogger(nil)
	seen := map[string][]Event{}

	// 随机大小的批次提交，逐批与重算比对。
	for i := 0; i < len(events); {
		n := 1 + rng.IntN(7)
		if i+n > len(events) {
			n = len(events) - i
		}
		batch := events[i : i+n]
		i += n
		if rej := s.Commit(batch); rej != nil {
			t.Fatalf("合法事件流不应被拒绝: %v", rej)
		}
		for _, e := range batch {
			seen[e.Key] = append(seen[e.Key], e)
		}
		for _, k := range keys {
			got := s.History(k)
			want := Recompute(k, seen[k])
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("增量与重算不一致 key=%s\n  增量: %s\n  重算: %s",
					k, fmtIntervals(got), fmtIntervals(want))
			}
		}
		if err := s.CheckInvariants(); err != nil {
			t.Fatalf("第 %d 个事件后自检失败: %v", i, err)
		}
	}
	t.Logf("判定依据: 历史仅由变更点集合决定，故增量维护结果与批量重算逐区间一致（%d 个事件）", total)

	// 同刻不同值的事件其“后到者”本就由到达顺序决定；
	// 先按 (键,时刻) 去重得到无冲突事件集合，再换序重放，最终历史必须完全相同（可复现）。
	type kt struct {
		key string
		at  int64
	}
	dedup := map[kt]Event{}
	var distinct []Event
	for _, e := range events {
		p := kt{e.Key, e.EffectiveTime}
		if _, ok := dedup[p]; !ok {
			distinct = append(distinct, e)
		}
		dedup[p] = e
	}
	shuffled := append([]Event(nil), distinct...)
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	s2 := NewStore(64)
	for i := 0; i < len(shuffled); i += 5 {
		end := i + 5
		if end > len(shuffled) {
			end = len(shuffled)
		}
		if rej := s2.Commit(shuffled[i:end]); rej != nil {
			t.Fatalf("换序重放不应被拒绝: %v", rej)
		}
	}
	for _, k := range keys {
		var keyEvents []Event
		for _, e := range distinct {
			if e.Key == k {
				keyEvents = append(keyEvents, e)
			}
		}
		want := Recompute(k, keyEvents)
		if got := s2.History(k); !reflect.DeepEqual(got, want) {
			t.Fatalf("到达顺序影响结果 key=%s\n  顺序1: %s\n  顺序2: %s",
				k, fmtIntervals(got), fmtIntervals(want))
		}
	}
	t.Logf("判定依据: 无同刻冲突时变更点集合与到达顺序无关，换序重放后历史与重算完全一致")
}

// TestConcurrentAccess 历史、点查、自检与提交并发执行（配合 go test -race）。
func TestConcurrentAccess(t *testing.T) {
	s := NewStore(128)
	const k = "hot-key"
	for i := 0; i < 20; i++ {
		if rej := s.Commit([]Event{{Key: k, EffectiveTime: int64(i * 10), Value: fmt.Sprintf("v%d", i)}}); rej != nil {
			t.Fatalf("预置数据失败: %v", rej)
		}
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 写执行体：持续提交乱序事件。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			tm := int64(500 - i*3)
			e := Event{Key: k, EffectiveTime: tm, Value: fmt.Sprintf("w%d", i)}
			if i%11 == 0 {
				e.Deleted = true
			}
			s.Commit([]Event{e})
		}
		close(stop)
	}()

	// 读执行体：历史 / 点查 / 自检。
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				switch id % 3 {
				case 0:
					h := s.History(k)
					for i := 1; i < len(h); i++ {
						if h[i-1].End > h[i].Start || h[i].Start <= h[i-1].Start {
							t.Errorf("并发读取到非良构历史: %s", fmtIntervals(h))
							return
						}
					}
				case 1:
					s.ValueAt(k, int64(id*7))
				case 2:
					if err := s.CheckInvariants(); err != nil {
						t.Errorf("并发自检失败: %v", err)
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()

	if err := s.CheckInvariants(); err != nil {
		t.Fatalf("并发结束后自检失败: %v", err)
	}
	t.Logf("判定依据: 读操作基于锁内快照，与提交并发时历史始终良构（升序、不重叠）")
}
