package lease

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// 并发争抢测试（配合 go test -race 运行）。
// 钉住的不变量：
//   - 不变量 2：全过程中发出的 token 序列严格递增、无重复
//     （断言：收集到的 token 集合恰好是 1..K，连续无洞无重）。
//   - 不变量 4：任何被拒绝的写不留痕迹——所有成功写按加锁顺序记录，
//     结束后资源内容必须等于"最后一次成功写"的值。
//   - 不变量 1/3 由上述两条间接保证：若同一时刻出现两个有效持有者
//     或旧 token 写入成功，最终内容就会与记录不符。
//
// 时序不靠 sleep：所有 goroutine 跑固定迭代次数，时间只通过
// 注入的原子时钟推进（部分迭代里主动拨快时钟制造过期与抢占）。
//
// 判别力：把 Manager 的 mu 从方法上摘掉（或把 Write 的校验挪到写之后），
// -race 会直接报 data race，或 TestConcurrentContention 的内容断言失败。
func TestConcurrentContention(t *testing.T) {
	const (
		workers    = 8
		iterations = 200
		ttl        = 10
	)
	var clock atomic.Int64
	m := New(func() int64 { return clock.Load() })

	var (
		wmu      sync.Mutex // 保证"成功写的记录顺序"与"实际生效顺序"一致
		writes   []string   // 所有成功写入的值，按生效顺序
		seq      atomic.Int64
		tokensMu sync.Mutex
		tokens   []uint64 // 所有成功 Acquire 拿到的 token
	)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			holder := fmt.Sprintf("H%d", id)
			var myToken uint64
			for i := 0; i < iterations; i++ {
				// 周期性地拨快时钟，制造过期与抢占窗口。
				if i%7 == 0 {
					clock.Add(ttl)
				}
				switch i % 4 {
				case 0:
					tok, err := m.Acquire(holder, ttl)
					if err == nil {
						myToken = tok
						tokensMu.Lock()
						tokens = append(tokens, tok)
						tokensMu.Unlock()
					}
				case 1:
					if myToken == 0 {
						continue
					}
					val := fmt.Sprintf("w-%d", seq.Add(1))
					wmu.Lock()
					if err := m.Write(myToken, "k", val); err == nil {
						writes = append(writes, val)
					}
					wmu.Unlock()
				case 2:
					if myToken != 0 {
						_ = m.Renew(holder, myToken, ttl) // 成败皆可，不许 panic
					}
				case 3:
					if myToken != 0 {
						_ = m.Release(holder, myToken)
					}
				}
			}
		}(w)
	}
	wg.Wait()

	// 不变量 2：token 恰好是 1..K，连续、无重复、严格递增。
	sort.Slice(tokens, func(i, j int) bool { return tokens[i] < tokens[j] })
	for i, tok := range tokens {
		if tok != uint64(i+1) {
			t.Fatalf("token 序列有洞或重复: 第 %d 个是 %d，共 %d 个", i, tok, len(tokens))
		}
	}
	if len(tokens) == 0 {
		t.Fatal("没有任何一次 Acquire 成功，测试失去意义")
	}

	// 不变量 4：最终内容等于最后一次成功写入的值。
	wmu.Lock()
	if len(writes) == 0 {
		wmu.Unlock()
		t.Fatal("没有任何一次 Write 成功，测试失去意义")
	}
	last := writes[len(writes)-1]
	wmu.Unlock()
	if got, ok := m.Read("k"); !ok || got != last {
		t.Fatalf("最终内容 = %q,%v，应为最后一次成功写 %q", got, ok, last)
	}
}
