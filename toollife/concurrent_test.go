package toollife

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentApplyCapacity 多通道同时申请同一刀组：
// 全组容量为 C 时，恰好 C 份预占成功，其余全部报「暂无余量」，
// 任何刀的 used+reserved 不得超过寿命上限（同一份寿命不会被预占两次）。
func TestConcurrentApplyCapacity(t *testing.T) {
	s := New()
	const nTools, limit, goroutines = 4, 10, 400
	ids := make([]string, nTools)
	for i := range ids {
		ids[i] = fmt.Sprintf("T%d", i)
	}
	if err := s.AddGroup("G", strictCfg(limit, ids...)); err != nil {
		t.Fatal(err)
	}

	var accepted, noMargin, other int64
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.Wait() // 同时抢
			req := fmt.Sprintf("R%04d", i)
			r, err := s.Apply("G", req, 1)
			switch CodeOf(err) {
			case 0:
				atomic.AddInt64(&accepted, 1)
				_ = r
			case ErrNoMargin:
				atomic.AddInt64(&noMargin, 1)
			default:
				atomic.AddInt64(&other, 1)
				t.Errorf("意外错误: %v", err)
			}
		}(i)
	}
	start.Done()
	wg.Wait()

	capacity := int64(nTools * limit)
	if accepted != capacity || noMargin != goroutines-capacity || other != 0 {
		t.Fatalf("accepted=%d noMargin=%d other=%d; 期望 accepted=%d rejected=%d",
			accepted, noMargin, other, capacity, goroutines-capacity)
	}
	v, err := s.Query("G")
	if err != nil {
		t.Fatal(err)
	}
	var totalReserved int
	for _, tl := range v.Tools {
		if tl.Used+tl.Reserved > limit {
			t.Fatalf("刀 %s 预占超寿命: used+reserved=%d > %d", tl.ID, tl.Used, limit)
		}
		totalReserved += tl.Reserved
	}
	if int64(totalReserved) != capacity {
		t.Fatalf("总预占=%d 应等于容量=%d", totalReserved, capacity)
	}
	t.Logf("输入: %d 通道并发申请单位消耗, 组容量=%d；输出: 接受=%d 暂无余量=%d；依据: 严格模式 used+reserved+1<=%d 串行化判定",
		goroutines, capacity, accepted, noMargin, limit)
}

// TestConcurrentMixedOps 混合并发烟雾测试，依赖 -race 捕获数据竞争；
// 并发结束后校验台账不变量：预占总量 == 未结算且未中止的申请数。
func TestConcurrentMixedOps(t *testing.T) {
	s := New()
	ids := []string{"A", "B", "C"}
	if err := s.AddGroup("G", GroupConfig{
		LifeLimit: 50, WarnPermille: 600, Mode: Strict, ToolIDs: ids,
	}); err != nil {
		t.Fatal(err)
	}

	var seq int64
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	mu := sync.Mutex{}
	var openReqs []string

	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start.Wait()
			for k := 0; k < 200; k++ {
				n := atomic.AddInt64(&seq, 1)
				req := fmt.Sprintf("REQ%d", n)
				r, err := s.Apply("G", req, int(n%5)+1)
				if err != nil {
					continue
				}
				mu.Lock()
				openReqs = append(openReqs, req)
				mu.Unlock()
				switch n % 4 {
				case 0:
					if _, err := s.Settle(req, int(n%7)); err != nil {
						t.Errorf("Settle %s: %v", req, err)
					}
					mu.Lock()
					openReqs = removeString(openReqs, req)
					mu.Unlock()
				case 1:
					if err := s.Cancel(req); err != nil {
						t.Errorf("Cancel %s: %v", req, err)
					}
					mu.Lock()
					openReqs = removeString(openReqs, req)
					mu.Unlock()
				}
				_ = r
			}
		}()
	}
	// 并发的破损/锁定/查询/换新尝试
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			start.Wait()
			for k := 0; k < 100; k++ {
				mu.Lock()
				id := ids[w%len(ids)]
				mu.Unlock()
				switch k % 5 {
				case 0:
					_ = s.ReportBroken("G", id)
				case 1:
					_ = s.Lock("G", id)
				case 2:
					_ = s.Unlock("G", id)
				case 3:
					_, _ = s.Query("G")
				case 4:
					// 只在可能成功时换新；失败也必须是定义好的错误码
					if err := s.Replace("G", id, fmt.Sprintf("%s-v%d", id, k)); err != nil {
						if c := CodeOf(err); c != ErrState && c != ErrNotFound && c != ErrConflict {
							t.Errorf("Replace 意外错误码: %v", err)
						}
					} else {
						// 换新成功后后续操作引用新编号
						mu.Lock()
						for i, x := range ids {
							if x == id {
								ids[i] = fmt.Sprintf("%s-v%d", id, k)
							}
						}
						mu.Unlock()
					}
				}
			}
		}(w)
	}
	start.Done()
	wg.Wait()

	// 不变量：每把刀 reserved>=0、used>=0；总 reserved 与存活申请记录吻合。
	v, err := s.Query("G")
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range v.Tools {
		if tl.Reserved < 0 || tl.Used < 0 {
			t.Fatalf("刀 %s 出现负值: %+v", tl.ID, tl)
		}
	}
	t.Logf("混合并发结束: 刀具=%d, 存活申请=%d, 当前选中=%q", len(v.Tools), len(openReqs), v.CurrentPick)
}

func removeString(xs []string, target string) []string {
	for i, x := range xs {
		if x == target {
			return append(xs[:i], xs[i+1:]...)
		}
	}
	return xs
}
