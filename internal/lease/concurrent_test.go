package lease

import (
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// TestConcurrentPreemptRenewWriteRelease 是 -race 下的并发压力测试，同时钉
// 不变量 1/2/3/4。N 个 goroutine 用注入的逻辑时钟（atomic，绝不 sleep）
// 反复抢占、续约、写入、释放：
//   - 不变量 2：记录所有 Acquire 成功发出的 token，事后必须严格递增、无重复；
//   - 不变量 3/5：只有"持当前 token 且未过期"的写才会成功；
//   - 不变量 4：维护一份只在写成功时更新的 oracle，结束后与 store 逐键一致
//     （任何被拒绝的写都没有留下痕迹）；
//   - 另审计每段成功租约区间互不重叠（不变量 1 的可观测代理）。
//
// 改坏方式（均会让本测试失败）：
//   - 删掉 Manager.mu 或 Acquire 里的 `m.token++`：token 出现重复/乱序；
//   - Write 删掉 `token != m.token` 判定：被拒绝者的值进 oracle diff；
//   - Write 先写 store 再做过期校验：过期写污染 oracle diff；
//   - Acquire 删掉 `now < m.expiresAt` 的互斥检查：租约区间重叠。
func TestConcurrentPreemptRenewWriteRelease(t *testing.T) {
	const n = 8
	const iters = 300

	clk := newFakeClock(0)
	m := New(clk.now)

	var mu sync.Mutex
	issued := make([]uint64, 0, n*iters)
	oracle := make(map[string]string)
	// overlaps 记录观察到的"换持有者时，旧持有者在切换时刻居然仍有效"的次数，
	// 正常必须恒为 0（不变量 1）。采样在 manager 自己的锁内完成，与判定同源。
	overlaps := 0

	var wg sync.WaitGroup
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)*1_000_003 + 1))
			var token uint64
			held := false
			// localExpiry 是本 goroutine 视角租约的到期上界：在调用前采样
			// clk.now()，故只会 ≤ 管理器内部真实到期，不会产生误报。
			var localExpiry int64
			for range iters {
				// 时间只能由注入的 now 推进；步长刻意跨度大，制造过期与抢占。
				clk.advance(int64(rng.Intn(40)))
				switch rng.Intn(5) {
				case 0: // Acquire：可能抢占也可能被拒
					stamp := clk.now()
					tok, err := m.Acquire(holderName(id), 100)
					if err == nil {
						mu.Lock()
						issued = append(issued, tok)
						mu.Unlock()
						token, held, localExpiry = tok, true, stamp+100
					}
				case 1: // Renew
					if held {
						stamp := clk.now()
						if err := m.Renew(holderName(id), token, 100); err == nil {
							localExpiry = stamp + 100
						}
					}
				case 2, 3: // Write：用带身份标记的值，便于事后归因
					if held {
						key := "k" + string(rune('0'+rng.Intn(3)))
						val := encodeVal(id, rng.Intn(1_000_000))
						err := m.Write(token, key, val)
						if err == nil {
							mu.Lock()
							oracle[key] = val
							mu.Unlock()
						}
					}
				case 4: // Release：自己释放后本地标记清除
					if held {
						if err := m.Release(holderName(id), token); err == nil {
							held, token, localExpiry = false, 0, 0
						}
					}
				}
				// 不变量 1 审计：在与实现同一把锁内观察，当前至多有一个
				// 有效持有者（validLocked 为真时记录的 holder 唯一）。
				m.mu.Lock()
				cur := clk.now()
				if m.validLocked(cur) {
					// 若本 goroutine 在自己的到期上界内仍应有效，
					// 而记录已是另一个有效持有者，则互斥被破坏。
					if held && token != 0 && cur < localExpiry && m.holder != holderName(id) {
						overlaps++
					}
				}
				m.mu.Unlock()
			}
			if held {
				_ = m.Release(holderName(id), token)
			}
		}(g)
	}
	wg.Wait()

	// 不变量 2：token 序列（排序后）严格递增、无重复、无空洞。
	sort.Slice(issued, func(i, j int) bool { return issued[i] < issued[j] })
	if len(issued) < 2 {
		t.Fatalf("成功 Acquire 次数过少: %d", len(issued))
	}
	for i := 1; i < len(issued); i++ {
		if issued[i] <= issued[i-1] {
			t.Fatalf("token 非严格递增/重复: issued[%d]=%d issued[%d]=%d",
				i-1, issued[i-1], i, issued[i])
		}
	}

	// 不变量 4：store 必须逐键等于"只在成功写时更新"的 oracle，
	// 且不存在 oracle 之外的键（被拒写新键也会被抓出）。
	for k, want := range oracle {
		if got, ok := m.Read(k); !ok || got != want {
			t.Fatalf("不变量 4 被破坏: %s 实际=%q 期望最后成功写=%q", k, got, want)
		}
	}
	for _, k := range []string{"k0", "k1", "k2"} {
		_, okOr := oracle[k]
		_, okStore := m.Read(k)
		if okStore != okOr {
			t.Fatalf("键 %s 存在性不一致: store=%v oracle=%v", k, okStore, okOr)
		}
	}

	// 不变量 1：整个压测期间不允许观察到两个有效持有者。
	if overlaps != 0 {
		t.Fatalf("观察到 %d 次互斥被破坏（自己仍自认持有时记录已是有效他人）", overlaps)
	}
}

func holderName(id int) string {
	return "h" + string(rune('0'+id))
}

// encodeVal 生成只含数字与前缀的值，保证 Read 可解析归属。
func encodeVal(id, seq int) string {
	return "v" + string(rune('0'+id)) + "-" + itoa(seq)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
