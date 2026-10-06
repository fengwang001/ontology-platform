package lineage

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// 并发载入同一标识: 只能有一个成功, 其余报重复。
func TestConcurrentDuplicateLoad(t *testing.T) {
	s := NewService()
	const n = 16
	var okCnt, dupCnt atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.LoadCommit(Commit{ID: "c", Files: map[string]string{"f": "a\n"}})
			switch {
			case err == nil:
				okCnt.Add(1)
			case IsKind(err, KindDuplicateCommit):
				dupCnt.Add(1)
			default:
				t.Errorf("意外错误: %v", err)
			}
		}()
	}
	wg.Wait()
	t.Logf("输入: %d 个 goroutine 同时载入同一标识 c", n)
	t.Logf("实际输出: 成功=%d 重复=%d", okCnt.Load(), dupCnt.Load())
	t.Logf("判定依据: 恰好 1 个成功, 其余 %d 个全部报重复", n-1)
	if okCnt.Load() != 1 || dupCnt.Load() != n-1 {
		t.Fatalf("成功=%d 重复=%d, 期望 1/%d", okCnt.Load(), dupCnt.Load(), n-1)
	}
}

// 并发载入 + 名单变更 + 查询: 对已载入提交的查询结果必须与并发前
// 串行计算的结果逐字节相同(等价于「查询排在所有并发载入之前」的串行序);
// 载入返回后的提交必须立即可查(线性一致)。
func TestConcurrentLoadAndQuery(t *testing.T) {
	s := NewService()
	n := newNaive()
	// 预载基础链 c0..c9, 每个提交改一行。
	content := ""
	for i := 0; i < 10; i++ {
		content += fmt.Sprintf("line%d\n", i)
		c := Commit{ID: fmt.Sprintf("c%d", i), Files: map[string]string{"f": content}}
		if i > 0 {
			c.Parents = []string{fmt.Sprintf("c%d", i-1)}
		}
		mustLoad(t, s, c)
		n.load(c)
	}
	v0 := mustVersion(t, s, "c3", "c7")
	// 基线: 并发前用朴素模型算出 (c9, f, v0) 的结果。
	baseline := n.blame("c9", "f", map[string]bool{"c3": true, "c7": true})
	t.Logf("输入: 预载 c0..c9, 名单 v%d={c3,c7}; 基线:\n%s", v0, attrsStr(baseline))

	var wg sync.WaitGroup
	// 4 个载入者: 各载入 25 个新提交(挂在链尾之后, 互不影响旧查询)。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			parent := "c9"
			for i := 0; i < 25; i++ {
				id := fmt.Sprintf("w%d_%d", w, i)
				c := Commit{
					ID:      id,
					Parents: []string{parent},
					Files:   map[string]string{fmt.Sprintf("g%d", w): fmt.Sprintf("x%d\n", i)},
				}
				if err := s.LoadCommit(c); err != nil {
					t.Errorf("load %s: %v", id, err)
					return
				}
				parent = id
			}
		}(w)
	}
	// 2 个名单变更者: 基于已载入提交创建新版本。
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if _, err := s.NewIgnoreVersion([]string{"c3"}); err != nil {
					t.Errorf("version: %v", err)
					return
				}
			}
		}(w)
	}
	// 8 个查询者: 反复查询 (c9, f, v0), 结果必须与基线逐字节相同。
	var mismatch atomic.Int64
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				got, err := s.Blame("c9", "f", v0)
				if err != nil {
					t.Errorf("blame: %v", err)
					return
				}
				if fmt.Sprint(got) != fmt.Sprint(baseline) {
					mismatch.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	t.Logf("实际输出: 4 载入者 x 25 提交 + 2 名单变更者 x 10 版本 + 8 查询者 x 200 查询全部完成, 结果不一致次数=%d", mismatch.Load())
	t.Logf("判定依据: 新提交与旧路径无关, 任何串行序下 (c9,f,v0) 的结果都等于基线 -> 并发结果与之逐字节相同即串行等价")
	if mismatch.Load() != 0 {
		t.Fatalf("出现 %d 次与基线不一致的查询结果", mismatch.Load())
	}
	// 线性一致: 载入已返回的提交必须立即可查。
	if _, err := s.Blame("w0_24", "g0", v0); err != nil {
		t.Fatalf("载入返回后查询失败: %v", err)
	}
	t.Logf("判定依据: 载入返回后立即可查(w0_24 查询成功), 满足线性一致")
}
