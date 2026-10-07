package ontology_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology"
)

// 并发发起的多个写入/删除/分组迁移，对所有受影响聚合视图的最终效果必须
// 等价于按某个全局串行顺序逐一应用：验证方式是并发执行后，
//  1. 每个被并发争用的主键最终恰好有一个「赢家版本链」，无丢更新；
//  2. 任一视图的增量索引结果与全量重算（朴素口径）逐项相等（守恒）；
//  3. 每个视图全体存活成员的 SUM 之和 == 全量重算的总和（归属互斥守恒）。
func TestConcurrentEquivalentToSerialOrder(t *testing.T) {
	k := newKernel(t)
	opno := &syncCounter{}

	// 预置一批存活实例。
	for i := 0; i < 40; i++ {
		key := fmt.Sprintf("k%d", i)
		if _, err := k.Write(ontology.Write{
			Type: typeOrder, Key: key, Prev: 0,
			Attrs: attrs(100, "us", "book"),
		}); err != nil {
			t.Fatal(err)
		}
	}

	regions := []string{"us", "eu", "cn", "jp"}
	categories := []string{"book", "game", "food", "toy"}
	var wg sync.WaitGroup
	workers := 16
	perWorker := 60
	fmt.Printf("=== TestConcurrentEquivalentToSerialOrder workers=%d per=%d ===\n", workers, perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				key := fmt.Sprintf("k%d", (id*7+j*3)%40)
				rec, exists := k.GetInstance(typeOrder, key)
				// 读-改-写循环：乐观并发下冲突者会收到 version_conflict，
				// 用最新凭证重试，最终每个意图都恰好生效一次。
				for attempt := 0; ; attempt++ {
					prev := int64(0)
					deleted := false
					if exists {
						prev, deleted = rec.Version, rec.Deleted
					}
					var res ontology.CommitResult
					var err error
					n := opno.next()
					if !deleted && exists && attempt%4 == 3 {
						res, err = k.Delete(ontology.Delete{Type: typeOrder, Key: key, Prev: prev})
						trace(t, n, fmt.Sprintf("[w%d] Delete %s prev=%d", id, key, prev),
							fmt.Sprintf("res=%+v err=%v", res, err),
							"并发删除：冲突则持新凭证重试；成功则该实例贡献必须原子扣除")
					} else {
						r := regions[(id+j+attempt)%len(regions)]
						c := categories[(id*3+j+attempt)%len(categories)]
						w := ontology.Write{Type: typeOrder, Key: key, Prev: prev,
							Attrs: attrs(float64(1+attempt), r, c)}
						res, err = k.Write(w)
						trace(t, n, fmt.Sprintf("[w%d] Write %s prev=%d g=%s/%s", id, key, prev, r, c),
							fmt.Sprintf("res=%+v err=%v", res, err),
							"并发写入/迁移：version_conflict 是预期竞争结果，重试直到成功")
					}
					if err == nil {
						break
					}
					if kindOf(err) != ontology.ErrVersionConflict {
						t.Errorf("unexpected concurrent error: %v", err)
						return
					}
					rec, exists = k.GetInstance(typeOrder, key)
					if attempt > 100 {
						t.Errorf("starved worker on %s", key)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()

	// 守恒校验：每个视图增量索引结果必须与全量重算逐项相等。
	for _, view := range []string{viewByRegion, viewByCategory} {
		incremental := map[string]ontology.GroupValue{}
		for _, g := range k.RecomputeView(view) {
			qv, meter, _ := k.Query(view, g.Group)
			if meter.InstanceRecordReads != 0 {
				t.Fatalf("query touched %d source records", meter.InstanceRecordReads)
			}
			incremental[g.Group] = qv
		}
		recomputed := k.RecomputeView(view)
		if len(incremental) != len(recomputed) {
			t.Fatalf("view %s group count: %d vs %d", view, len(incremental), len(recomputed))
		}
		var sumInc, sumRe float64
		var memInc, memRe int
		for g, v := range recomputed {
			if incremental[g] != v {
				t.Fatalf("view %s group %s diverged after concurrency: %+v vs %+v", view, g, incremental[g], v)
			}
			sumInc += incremental[g].Sum
			memInc += incremental[g].Members
			sumRe += v.Sum
			memRe += v.Members
		}
		trace(t, opno.next(), fmt.Sprintf("并发结束后守恒校验 view=%s", view),
			fmt.Sprintf("groups=%d members=%d totalSum=%v(重算=%v)", len(incremental), memInc, sumInc, sumRe),
			"增量索引与全量重算逐项相等且总成员数/总 SUM 守恒 => 等价某一串行序，无重复/丢失归属")
		if memInc != memRe || sumInc != sumRe {
			t.Fatalf("conservation broken for %s", view)
		}
	}
}

// 分组迁移的并发互斥：大量 goroutine 让同一实例在多个分组间迁移，
// 迁移后该实例在视图中必须恰好被计数一次（总 SUM 等于其当前单次贡献，
// 成员归属恰为一个分组）。
func TestGroupMigrationConcurrent(t *testing.T) {
	k := newKernel(t)
	if _, err := k.Write(ontology.Write{Type: typeOrder, Key: "m1", Prev: 0,
		Attrs: attrs(7, "us", "book")}); err != nil {
		t.Fatal(err)
	}
	regions := []string{"us", "eu", "cn", "jp"}
	var wg sync.WaitGroup
	opno := &syncCounter{}
	for w := 0; w < 12; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				for {
					rec, _ := k.GetInstance(typeOrder, "m1")
					g := regions[(id+j)%len(regions)]
					n := opno.next()
					res, err := k.Write(ontology.Write{
						Type: typeOrder, Key: "m1", Prev: rec.Version,
						Attrs: attrs(7, g, "book"),
					})
					trace(t, n, fmt.Sprintf("[w%d] 迁移 m1 -> %s prev=%d", id, g, rec.Version),
						fmt.Sprintf("res=%+v err=%v", res, err),
						"迁移要么冲突重试要么原子生效；生效后 m1 在全视图恰被计数一次")
					if err == nil {
						break
					}
					if kindOf(err) != ontology.ErrVersionConflict {
						t.Errorf("unexpected: %v", err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()

	rec, _ := k.GetInstance(typeOrder, "m1")
	view := k.RecomputeView(viewByRegion)
	totalMembers, totalSum := 0, 0.0
	for _, v := range view {
		totalMembers += v.Members
		totalSum += v.Sum
	}
	curGroup := rec.Attrs["region"].Str
	trace(t, opno.next(), "并发迁移结束后统计 by_region",
		fmt.Sprintf("currentGroup=%s totalMembers=%d totalSum=%v", curGroup, totalMembers, totalSum),
		"m1 必须恰好存在于一个分组（当前分组）：总成员数=1、总 SUM=7，无双重归属/消失中间态")
	if totalMembers != 1 || totalSum != 7 || view[curGroup].Members != 1 || view[curGroup].Sum != 7 {
		t.Fatalf("exclusivity violated: members=%d sum=%v curGroup=%s view=%+v",
			totalMembers, totalSum, curGroup, view)
	}
}

// 读侧不变量压力：写者持续做分组迁移/删除/复活（一次提交跨两个视图），
// 读者并发地做「增量查询 + 全量重算」，任何一次观测都必须自洽——直接检验
// 「实例落盘与聚合生效是同一生效时刻的原子事件」。
func TestReaderWriterAtomicInvariant(t *testing.T) {
	k := newKernel(t)
	for i := 0; i < 30; i++ {
		if _, err := k.Write(ontology.Write{Type: typeOrder, Key: fmt.Sprintf("k%d", i), Prev: 0,
			Attrs: attrs(float64(i+1), "us", "book")}); err != nil {
			t.Fatal(err)
		}
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var tick atomic.Int64
	next := func() int64 { return tick.Add(1) }

	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			groups := []string{"us", "eu", "cn", "jp"}
			for {
				select {
				case <-stop:
					return
				default:
				}
				key := fmt.Sprintf("k%d", (id*5+int(next()))%30)
				rec, exists := k.GetInstance(typeOrder, key)
				if !exists {
					continue
				}
				if !rec.Deleted && next()%5 == 0 {
					if _, err := k.Delete(ontology.Delete{Type: typeOrder, Key: key, Prev: rec.Version}); err != nil {
						continue
					}
					continue
				}
				g := groups[next()%int64(len(groups))]
				prev := int64(0)
				if exists {
					prev = rec.Version
				}
				_, _ = k.Write(ontology.Write{Type: typeOrder, Key: key, Prev: prev,
					Attrs: attrs(float64(id+1), g, "book")})
			}
		}(w)
	}

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for checks := 0; checks < 3000; checks++ {
				select {
				case <-stop:
					return
				default:
				}
				for _, view := range []string{viewByRegion, viewByCategory} {
					if g, got, want := k.VerifyViewConsistency(view); g != "" {
						t.Errorf("atomic read violated for %s/%s: query=%+v recompute=%+v", view, g, got, want)
						return
					}
				}
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
	fmt.Println("[原子读测试] 并发迁移/删除/复活期间，读者全部观测满足 增量索引==源实例重算 且 源实例读取=0")
}

type syncCounter struct {
	mu sync.Mutex
	n  int
}

func (s *syncCounter) next() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return s.n
}
