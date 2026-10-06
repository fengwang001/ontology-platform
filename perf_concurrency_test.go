package ontology

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// 并发变更、脏标记与查询：任意时刻查询都必须对应某个已生效的串行状态，
// 即物化集合始终等于「当前提交 × 当前规则集版本」的朴素结果。
func TestConcurrentSerializability(t *testing.T) {
	e := newTestEngine(t, map[string][]string{
		"c1": {"a/f1", "a/f2", "b/f3", "b/f4"},
		"c2": {"a/f1", "c/f5"},
	})
	rulesets := [][]Rule{
		{{Include, "a/"}},
		{{Include, "b/"}},
		{{Include, "a/f1"}, {Include, "c/"}},
		nil,
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 变更者：循环注册版本、换提交（全部 force，避免受阻干扰）。
	versions := []int{0}
	var vmu sync.Mutex
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			v, _, _, err := e.ReplaceRules(rulesets[i%len(rulesets)], "", true)
			if err == nil {
				vmu.Lock()
				versions = append(versions, v)
				vmu.Unlock()
			}
			if _, _, err := e.ApplyRuleset([]string{"c1", "c2"}[i%2], 0, true); err != nil {
				t.Errorf("apply: %v", err)
				return
			}
		}
		close(stop)
	}()

	// 脏标记者。
	wg.Add(1)
	go func() {
		defer wg.Done()
		paths := []string{"a/f1", "a/f2", "b/f3", "b/f4", "c/f5"}
		for i := 0; i < 400; i++ {
			p := paths[i%len(paths)]
			if i%2 == 0 {
				_ = e.MarkDirty(p)
			} else {
				_ = e.ClearDirty(p)
			}
		}
	}()

	// 查询者：每次快照必须与朴素模型在同一 (提交,版本) 下一致。
	commitFiles := map[string][]string{
		"c1": {"a/f1", "a/f2", "b/f3", "b/f4"},
		"c2": {"a/f1", "c/f5"},
	}
	versionRules := map[int][]Rule{0: nil}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			cid, ver, got := e.CurrentState()
			vmu.Lock()
			known := false
			for _, v := range versions {
				if v == ver {
					known = true
				}
			}
			vmu.Unlock()
			if !known {
				continue
			}
			if _, ok := versionRules[ver]; !ok {
				versionRules[ver] = rulesets[(ver-1)%len(rulesets)]
			}
			want := naiveMaterialized(commitFiles[cid], versionRules[ver])
			if !eqStrings(got, want) {
				t.Errorf("non-serializable snapshot commit=%s version=%d\n got=%v\nwant=%v",
					cid, ver, got, want)
				return
			}
		}
	}()

	wg.Wait()
	tlog(t, "concurrent run finished; final commit=%s version=%d", e.CurrentCommit(), e.CurrentVersion())
}

// buildWideTree 构造 n 个互不相干的顶级目录，每个含 depth 深的一个文件。
func buildWideTree(n, depth int) []string {
	var files []string
	for i := 0; i < n; i++ {
		p := fmt.Sprintf("dir%05d", i)
		for d := 0; d < depth; d++ {
			p += "/sub"
		}
		files = append(files, p+"/file")
	}
	return files
}

// singleDirTree 构造一个含 n 个直接文件的扁平目录 big/。
func singleDirTree(n int) []string {
	var files []string
	for i := 0; i < n; i++ {
		files = append(files, fmt.Sprintf("big/f%06d", i))
	}
	return files
}

func nsSince(start time.Time) float64 {
	return float64(time.Since(start).Nanoseconds())
}

// 单路径查询开销不随无关目录文件数增长。
func TestPerfQueryIndependentOfTreeSize(t *testing.T) {
	measure := func(n int) float64 {
		files := buildWideTree(n, 6)
		e := newTestEngine(t, map[string][]string{"c": files})
		if _, _, _, err := e.ReplaceRules([]Rule{{Include, "dir00000/"}}, "c", false); err != nil {
			t.Fatal(err)
		}
		const iters = 3000
		var acc PathStatus
		start := time.Now()
		for i := 0; i < iters; i++ {
			acc = e.QueryPath("dir00000/sub/sub/sub/sub/sub/file")
		}
		_ = acc
		return nsSince(start) / float64(iters)
	}
	small := measure(10)
	large := measure(4000)
	ratio := large / small
	tlog(t, "QueryPath ns/op small=%.1f large=%.1f ratio=%.2f（无关目录x400）", small, large, ratio)
	if ratio > 6 {
		t.Fatalf("query scaled with unrelated files: ratio=%.2f", ratio)
	}
}

// 变更开销不随「未受影响的已物化路径」数增长。
// 规则集规模恒定（2 条），目录下已有 n 个物化文件；
// 反复只切换其中 1 个文件（精确排除再包含），其余 n-1 个文件保持物化。
// 受影响面恒为 1，故耗时不应随 n 线性增长。
func TestPerfChangeIndependentOfUnaffected(t *testing.T) {
	measure := func(n int) float64 {
		files := singleDirTree(n)
		e := newTestEngine(t, map[string][]string{"c": files})
		base := []Rule{{Include, "big/"}}
		withExclude := []Rule{{Include, "big/"}, {Exclude, "big/f000000"}, {Include, "big/f000000"}}
		// withExclude 最后仍是包含；为产生真实变化用一个稳定的「排除」版本：
		excluded := []Rule{{Include, "big/"}, {Exclude, "big/f000000"}}
		if _, _, _, err := e.ReplaceRules(base, "c", false); err != nil {
			t.Fatal(err)
		}
		_ = withExclude
		const iters = 400
		var total float64
		for i := 0; i < iters; i++ {
			var rs []Rule
			if i%2 == 0 {
				rs = excluded
			} else {
				rs = base
			}
			start := time.Now()
			if _, _, _, err := e.ReplaceRules(rs, "", true); err != nil {
				t.Fatal(err)
			}
			total += nsSince(start)
		}
		return total / float64(iters)
	}
	small := measure(64)
	large := measure(64 * 1024)
	ratio := large / small
	tlog(t, "ReplaceRules ns/op small(n=64)=%.1f large(n=64K)=%.1f ratio=%.2f（未受影响物化x1024）",
		small, large, ratio)
	// 受影响面恒为 1 个文件；允许常数/日志噪声，1024x 数据只允许 6x 时间。
	if ratio > 6 {
		t.Fatalf("change scaled with unaffected materialized paths: ratio=%.2f", ratio)
	}
}
