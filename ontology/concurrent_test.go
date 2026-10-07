package ontologyindex

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

// TestConcurrentSerializability：并发发起 Ingest / Lookup / 切换操作。
// 性质 1（活性）：切换窗口外的查询永不返回 E4 以外的错误，且不崩溃。
// 性质 2（一致性）：切换完成后，查询结果必须与朴素批量重建完全一致，
// 即并发可观察结果等价于某一全局串行顺序。
func TestConcurrentSerializability(t *testing.T) {
	s := schemaForTests()
	aud := &MemoryAuditor{}
	eng := NewEngine(s, aud)
	naive := NewNaiveModel(s)
	if err := eng.CreateIndex("idx", "Person", "prop_ssn", ConstraintDuplicate); err != nil {
		t.Fatal(err)
	}
	naive.CreateIndex("idx", "Person", "prop_ssn", ConstraintDuplicate)

	const writers = 8
	const perWriter = 100
	var writerWG sync.WaitGroup

	// 预声明切换：writer 们的 ts 横跨切换点 100。
	var switchOnce sync.Once
	doSwitch := func() {
		if err := eng.BeginSwitch("idx", 100); err != nil {
			t.Errorf("BeginSwitch: %v", err)
			return
		}
		if err := eng.CommitSwitch("idx"); err != nil {
			t.Errorf("CommitSwitch: %v", err)
		}
	}

	for w := 0; w < writers; w++ {
		writerWG.Add(1)
		go func(w int) {
			defer writerWG.Done()
			for i := 0; i < perWriter; i++ {
				if w == 0 && i == perWriter/2 {
					switchOnce.Do(doSwitch)
				}
				ts := LogicalClock(1 + (w*perWriter+i)%198)
				prop := "prop_ssn"
				if ts >= 100 {
					prop = "prop_tax"
				}
				ev := ChangeEvent{
					EventID:     fmt.Sprintf("w%d-%d", w, i),
					ObjectType:  "Person",
					ObjectID:    fmt.Sprintf("p%d", (w*7+i)%12),
					PropertyID:  prop,
					NewValue:    StringValue(fmt.Sprintf("v%d", (w+i)%9)),
					EffectiveAt: ts,
				}
				if err := eng.Ingest(ev); err != nil {
					t.Errorf("Ingest: %v", err)
					return
				}
			}
		}(w)
	}

	// 并发查询者：E4（切换瞬间）是允许的，其他错误不允许。
	stop := make(chan struct{})
	const readers = 4
	var readerWG sync.WaitGroup
	for r := 0; r < readers; r++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, err := eng.Lookup("idx", StringValue("v1"))
				if err != nil {
					ie, ok := err.(*IndexError)
					if !ok || ie.Code != ErrSwitchInProgress {
						t.Errorf("并发查询出现非预期错误: %v", err)
						return
					}
				}
				runtime.Gosched()
			}
		}()
	}

	writerWG.Wait()
	close(stop)
	readerWG.Wait()

	// 串行重放同一批事件到朴素模型（逻辑顺序确定，与并发交织无关）。
	type ev struct{ w, i int }
	var all []ChangeEvent
	for w := 0; w < writers; w++ {
		for i := 0; i < perWriter; i++ {
			ts := LogicalClock(1 + (w*perWriter+i)%198)
			prop := "prop_ssn"
			if ts >= 100 {
				prop = "prop_tax"
			}
			all = append(all, ChangeEvent{
				EventID:     fmt.Sprintf("w%d-%d", w, i),
				ObjectType:  "Person",
				ObjectID:    fmt.Sprintf("p%d", (w*7+i)%12),
				PropertyID:  prop,
				NewValue:    StringValue(fmt.Sprintf("v%d", (w+i)%9)),
				EffectiveAt: ts,
			})
		}
	}
	// 若并发流结束时切换未完成，做一次确定性切换。
	if status, v, _ := eng.Status("idx"); status == "active" && v == 1 {
		if err := eng.BeginSwitch("idx", 100); err == nil {
			_ = eng.CommitSwitch("idx")
		}
	}
	for _, e := range all {
		_ = naive.Ingest(e)
	}
	assertSameView(t, "concurrent-final",
		engineSnapshot(t, eng, "idx"),
		naiveSnapshot(t, naive, "idx", true, 100))

	// 审计中：找到 committed 记录的位置，其后任何成功查询必须依据新版本，
	// 不允许出现“切换提交后仍依据旧版本返回”的混杂可观察状态。
	records := aud.Snapshot()
	commitPos := -1
	for i, r := range records {
		if r.Op == "commit_switch" && r.Decision == "committed" {
			commitPos = i
		}
	}
	var versionMismatch int
	if commitPos >= 0 {
		for _, r := range records[commitPos+1:] {
			if r.Op == "lookup" && r.Decision == "returned" && r.IndexVersion < 2 {
				versionMismatch++
			}
		}
	}
	if versionMismatch > 0 {
		t.Fatalf("切换提交后仍有 %d 条查询依据旧版本返回（新旧混杂）", versionMismatch)
	}
}
