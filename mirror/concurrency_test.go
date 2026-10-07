package mirror

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// 所有操作可并发调用：多 goroutine 随机调用全部操作，
// 在 -race 下验证无数据竞争，且结束后在线成员数据一致、
// 卷状态自洽（等价于某个串行顺序）。
func TestConcurrentOps(t *testing.T) {
	const members, blocks, limit = 4, 64, 8
	v := mustNewVolume(t, members, blocks, limit)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 2000; i++ {
				switch rng.Intn(5) {
				case 0:
					failed := map[int]bool{}
					for id := 0; id < members; id++ {
						if rng.Intn(6) == 0 {
							failed[id] = true
						}
					}
					_ = v.Write(rng.Intn(blocks), rng.Uint64(), failed)
				case 1:
					_, _ = v.Read(rng.Intn(blocks))
				case 2:
					_ = v.ReportFault(rng.Intn(members))
				case 3:
					_ = v.Rejoin(rng.Intn(members), uint64(rng.Intn(4)))
				case 4:
					_, _ = v.AdvanceResync(rng.Intn(members), 1+rng.Intn(8))
				}
			}
		}(int64(g)*7919 + 13)
	}
	wg.Wait()

	snap := v.Snapshot()
	var ref []uint64
	online := 0
	for _, ms := range snap.Members {
		if ms.State != MemberOnline {
			continue
		}
		online++
		if len(ms.Pending) != 0 || ms.DirtyDropped {
			t.Fatalf("在线成员不应有待同步块或全量标记: %+v", ms)
		}
		if ref == nil {
			ref = ms.Data
		} else if !reflect.DeepEqual(ref, ms.Data) {
			t.Fatalf("并发结束后在线成员数据不一致")
		}
	}
	t.Logf("并发结束: 世代=%d 在线成员数=%d", snap.Generation, online)
}
