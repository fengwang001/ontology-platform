package van

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// invariantCheck 校验任一时刻必须成立的全部不变量。
func invariantCheck(t *testing.T, s *Service, msg string) {
	t.Helper()
	s.mu.RLock()
	defer s.mu.RUnlock()
	// 1) 各分区载重/容积不超限。
	for i, z := range s.zones {
		if z.weight > z.weightLimit || z.volume > z.volumeLimit {
			t.Fatalf("%s: 分区%d超载 重%d/%d 容%d/%d", msg, i+1, z.weight, z.weightLimit, z.volume, z.volumeLimit)
		}
		if !z.empty() && (z.minStop == 0 || z.maxStop == 0) {
			t.Fatalf("%s: 分区%d停靠点极值损坏", msg, i+1)
		}
	}
	// 2) 跨分区顺序：从车头到车尾，停靠点最小值不得上升
	//    （早卸货停点小，必须位于编号更大的分区；同区混装豁免）。
	prevMin := 0
	for i, z := range s.zones {
		if z.empty() {
			continue
		}
		if prevMin != 0 && z.minStop < prevMin {
			t.Fatalf("%s: 顺序不变量被破坏：分区%d最小停靠点%d小于前方分区最小%d",
				msg, i+1, z.minStop, prevMin)
		}
		prevMin = z.minStop
	}
	// 3) 隔离：逐分区类别集合。
	for i, z := range s.zones {
		if z.has[Flammable] && z.has[Oxidizer] {
			t.Fatalf("%s: 分区%d易燃与氧化同区", msg, i+1)
		}
		if z.has[Food] && (z.has[Flammable] || z.has[Oxidizer]) {
			t.Fatalf("%s: 分区%d食品与危险品同区", msg, i+1)
		}
	}
	// 4) loc 与分区内记录互相一致，且重量体积求和等于聚合值。
	count := 0
	for _, z := range s.zones {
		w, v := 0, 0
		for _, c := range z.items {
			count++
			if s.loc[c.ID] == 0 {
				t.Fatalf("%s: 货物%d在分区内但 loc 缺失", msg, c.ID)
			}
			w += c.Weight
			v += c.Volume
		}
		if w != z.weight || v != z.volume {
			t.Fatalf("%s: 分区聚合不一致 重%d/%d 容%d/%d", msg, w, z.weight, v, z.volume)
		}
	}
	if count != len(s.loc) {
		t.Fatalf("%s: 在车货物计数不一致 分区记录=%d loc=%d", msg, count, len(s.loc))
	}
}

func TestConcurrentSnapshotConsistency(t *testing.T) {
	s := New([]ZoneSpec{{40, 40}, {40, 40}, {40, 40}})
	var rwg, wwg sync.WaitGroup
	stop := make(chan struct{})

	// 读者：只做查询，读到的必须是某个已完成操作的一致快照。
	for r := 0; r < 6; r++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rem := s.Remaining()
				arr := s.ArrivedStop()
				if len(rem) != 3 || arr < 0 {
					t.Errorf("快照形状异常 rem=%v arr=%d", rem, arr)
					return
				}
				for _, x := range rem {
					if x.Weight < 0 || x.Volume < 0 {
						t.Errorf("快照出现负剩余容量 %+v", x)
						return
					}
				}
			}
		}()
	}

	// 写者：并发单件装货，编号互不相交避免重复错误干扰。
	id := 1000
	var mu sync.Mutex
	for w := 0; w < 6; w++ {
		wwg.Add(1)
		go func(w int) {
			defer wwg.Done()
			for k := 0; k < 200; k++ {
				mu.Lock()
				id++
				cid := id
				mu.Unlock()
				c := Cargo{
					ID:     cid,
					Weight: 1 + (cid % 3),
					Volume: 1 + (cid % 4),
					Stop:   1 + (cid % 4),
					Kind:   Category(cid % 4),
				}
				if _, err := s.Load(c); err == nil {
					invariantCheck(t, s, fmt.Sprintf("writer%d cid=%d", w, cid))
				}
			}
		}(w)
	}

	wwg.Wait() // 先等所有写者完成
	close(stop)
	rwg.Wait() // 再通知读者退出并等待
	invariantCheck(t, s, "并发结束")
}

func TestConcurrentBatchAtomicity(t *testing.T) {
	s := New([]ZoneSpec{{6, 6}})
	var wg sync.WaitGroup
	// 多个必然失败的批次（超重）并发执行，任何时刻都不得留下半批。
	base := 0
	var mu sync.Mutex
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			mu.Lock()
			b := base + 1
			base += 10
			mu.Unlock()
			batch := []Cargo{
				{ID: b, Weight: 4, Volume: 1, Stop: 1},
				{ID: b + 1, Weight: 4, Volume: 1, Stop: 1},
			}
			_, err := s.LoadBatch(batch)
			if err == nil {
				t.Errorf("批次 %d 本应失败", g)
			}
			var be *BatchError
			if !errors.As(err, &be) || be.Index != 1 {
				t.Errorf("批次 %d 期望下标1失败, 得到 %v", g, err)
			}
		}(g)
	}
	wg.Wait()
	if len(s.loc) != 0 {
		t.Fatalf("全部批次失败后车厢应为空, 实际 loc=%v", s.loc)
	}
	invariantCheck(t, s, "批次原子性")
}
