package ontology

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestConcurrentOpsLinearizable 并发发起增量维护、默认时区版本迁移与查询，
// 验证：1) 竞态检测器下无数据竞争；2) 每次查询结果都满足视图不变量；
// 3) 并发收敛后的最终状态与朴素模型按事件历史的串行重放一致——
// 即可观察结果等价于某个全局串行顺序。
func TestConcurrentOpsLinearizable(t *testing.T) {
	p := NewPlatform()
	p.AddObjectType("A", "at", &TzDefVersion{Version: 1, ZoneID: "UTC", EffectiveSeq: 1})
	p.AddObjectType("B", "at", &TzDefVersion{Version: 1, ZoneID: "UTC+08:00", EffectiveSeq: 1})
	p.AddLink("a-b", "A", "B")
	p.CreateView("v", "a-b")
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errCh := make(chan string, 64)
	// 写入者：4 个 goroutine 持续写入时间属性并穿插增量维护。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			typ := []string{"A", "B"}[w%2]
			for i := 0; i < 250; i++ {
				obj := fmt.Sprintf("%s-%d", typ, (w*250+i)%40)
				val := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).
					Add(time.Duration((w*250+i)%2000) * time.Hour)
				p.WriteTimeProperty(typ, obj, val)
				if i%10 == 0 {
					p.Maintain("v")
				}
			}
		}(w)
	}
	// 迁移者：对两个类型交替发起版本迁移。
	// 版本号与生效序号在平台锁内校验，冲突迁移被拒绝属合法结果。
	wg.Add(1)
	go func() {
		defer wg.Done()
		zones := []string{"UTC", "UTC+08:00", "UTC+05:30", "UTC-05:00"}
		for i := 0; i < 40; i++ {
			typ := []string{"A", "B"}[i%2]
			_ = p.MigrateDefaultTz(typ, TzDefVersion{
				Version:      2 + i,
				ZoneID:       zones[i%len(zones)],
				EffectiveSeq: uint64(10 + i*7),
			})
		}
	}()
	// 查询者：持续查询并校验不变量。
	var qwg sync.WaitGroup
	for q := 0; q < 2; q++ {
		qwg.Add(1)
		go func() {
			defer qwg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				groups, _ := p.Query("v")
				if msg := checkInvariantsConcurrent(groups); msg != "" {
					select {
					case errCh <- msg:
					default:
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	qwg.Wait()
	select {
	case msg := <-errCh:
		t.Fatalf("invariant violated during concurrent queries: %s", msg)
	default:
	}
	// 收敛：最终维护后，增量视图必须等于朴素模型对全量历史的重放。
	p.Maintain("v")
	if view, naive := canonicalDump(mustQuery(p)), canonicalDump(p.NaiveDump("v")); view != naive {
		t.Fatalf("concurrent result not equivalent to a serial order\nincremental:\n%s\nnaive:\n%s", view, naive)
	}
	groups, _ := p.Query("v")
	checkViewInvariants(t, groups)
}

// checkInvariantsConcurrent 与 checkViewInvariants 相同的不变量，
// 但返回错误字符串而非调用 t.Fatalf（可在非测试 goroutine 中使用）。
func checkInvariantsConcurrent(groups []GroupView) string {
	seen := make(map[string]int64)
	prevKey := int64(-1) << 62
	for _, g := range groups {
		if g.Key < prevKey {
			return fmt.Sprintf("group keys not sorted: %v", groups)
		}
		prevKey = g.Key
		for i, m := range g.Members {
			if groupKeyOf(m.InstantUnix) != g.Key {
				return fmt.Sprintf("member %s instant inconsistent with group key", m.ObjectID)
			}
			if k, dup := seen[m.ObjectID]; dup {
				return fmt.Sprintf("object %s in two groups %d and %d", m.ObjectID, k, g.Key)
			}
			seen[m.ObjectID] = g.Key
			if i > 0 {
				a, b := g.Members[i-1], m
				less := a.InstantUnix < b.InstantUnix ||
					(a.InstantUnix == b.InstantUnix &&
						(a.ObjectTypeID < b.ObjectTypeID ||
							(a.ObjectTypeID == b.ObjectTypeID && a.ObjectID < b.ObjectID)))
				if !less {
					return fmt.Sprintf("members out of adjudicated order: %v then %v", a, b)
				}
			}
		}
	}
	return ""
}
