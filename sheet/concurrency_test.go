package sheet_test

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/sheet"
)

// 并发调用：结果等价于某个串行顺序。
// 不变量：全局修订号 == 被接受的 Apply/Undo/Redo 总数
// （只有这三类被接受的操作推进修订号, 且每次恰推进 1）。
// 需配合 -race 运行以检测数据竞争。
func TestConcurrentSerialization(t *testing.T) {
	const nUsers = 8
	const opsPerUser = 300

	depths := map[string]int{}
	for i := 0; i < nUsers; i++ {
		depths[fmt.Sprintf("u%d", i)] = 5
	}
	s, err := sheet.New(depths)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var nowGen atomic.Int64
	var accepted atomic.Int64 // 被接受的 Apply/Undo/Redo 总数
	var wg sync.WaitGroup
	for u := 0; u < nUsers; u++ {
		wg.Add(1)
		go func(u int) {
			defer wg.Done()
			user := fmt.Sprintf("u%d", u)
			rng := rand.New(rand.NewSource(int64(u)*97 + 1))
			for i := 0; i < opsPerUser; i++ {
				now := nowGen.Add(1)
				switch rng.Intn(10) {
				case 0, 1, 2, 3, 4: // Apply（批量, 键去重）
					n := 1 + rng.Intn(5)
					seen := map[string]bool{}
					var edits []sheet.Edit
					for len(edits) < n {
						k := fmt.Sprintf("k%d", rng.Intn(16))
						if !seen[k] {
							seen[k] = true
							edits = append(edits, sheet.Edit{Key: k, Value: int64(rng.Intn(1000))})
						}
					}
					if r := s.Apply(user, edits, now); r.OK {
						accepted.Add(1)
					}
				case 5, 6, 7: // Undo
					if r := s.Undo(user, now); r.OK {
						accepted.Add(1)
					}
				case 8: // Redo
					if r := s.Redo(user, now); r.OK {
						accepted.Add(1)
					}
				case 9: // Protect / Unprotect（不推进修订号）
					k := fmt.Sprintf("k%d", rng.Intn(16))
					if rng.Intn(2) == 0 {
						s.Protect(user, k, now)
					} else {
						s.Unprotect(user, k, now)
					}
				}
			}
		}(u)
	}
	wg.Wait()

	if got, want := s.Revision(), accepted.Load(); got != want {
		t.Fatalf("串行化不变量被破坏: 修订号=%d, 被接受的变更操作数=%d", got, want)
	}
}
