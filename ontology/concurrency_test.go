package ontology

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentSerialEquivalence 并发执行对象写入、引用更新、符号引用设置
// 与解析，验证结果等价于某个串行顺序：
//  1. 解析 "cur^{tree}" 永远成功 —— 一次解析看到的是某个瞬间的完整状态
//     （引用指向的提交及其树要么同时可见，要么同时不可见）；
//  2. "@{k}" 解析出的值一定是已写入序列中的真实成员；
//  3. 任一时刻算出的最短缩写之后永远唯一解析回同一对象（单调有效）；
//  4. 并发的符号引用设置不产生环（解析永不死循环）。
func TestConcurrentSerialEquivalence(t *testing.T) {
	const nCommits = 400
	cfg := Config{}
	svc := NewService(cfg)

	// 预生成提交链：c_i 的父是 c_{i-1}，树是 t_i。标识取随机值，
	// 使最短缩写在测试期间稳定（概率意义下不被后续对象撞车）。
	idRand := rand.New(rand.NewSource(7))
	type pair struct {
		commit, tree Object
	}
	pairs := make([]pair, nCommits)
	prev := ""
	for i := 0; i < nCommits; i++ {
		tree := Object{ID: randID(idRand), Type: TypeTree}
		c := Object{ID: randID(idRand), Type: TypeCommit, Tree: tree.ID}
		if prev != "" {
			c.Parents = []string{prev}
		}
		pairs[i] = pair{commit: c, tree: tree}
		prev = c.ID
	}

	var written sync.Map // 已完整写入（提交+树+引用）的提交标识
	var done atomic.Bool
	var wg sync.WaitGroup
	errCh := make(chan string, 64)
	report := func(format string, args ...any) {
		select {
		case errCh <- fmt.Sprintf(format, args...):
		default:
		}
	}

	// 写者：事务性地 Put 树、Put 提交、移动引用。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i, p := range pairs {
			if err := svc.Put(p.tree); err != nil {
				report("Put(tree %d): %v", i, err)
				return
			}
			if err := svc.Put(p.commit); err != nil {
				report("Put(commit %d): %v", i, err)
				return
			}
			written.Store(p.commit.ID, true) // 引用移动前先标记，保证读者看到的值必在集合中
			if err := svc.SetRef("refs/heads/cur", p.commit.ID); err != nil {
				report("SetRef: %v", err)
				return
			}
		}
		done.Store(true)
	}()

	// 符号引用写者：随机设置，成环被拒绝属正常。
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := rand.New(rand.NewSource(1))
		names := []string{"s0", "s1", "s2", "s3"}
		for i := 0; i < 200; i++ {
			a := names[r.Intn(len(names))]
			b := names[r.Intn(len(names))]
			if r.Intn(2) == 0 {
				b = "refs/heads/cur"
			}
			svc.SetSymbolic(a, b) // 成环时整体拒绝，忽略错误
		}
	}()

	// 读者：持续解析并校验串行等价不变量。
	var reads atomic.Int64
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for !done.Load() {
				reads.Add(1)
				// 不变量1：快照一致性。NotExist 只意味着读发生在首次
				// SetRef 之前（合法串行顺序）；一旦引用可见，其提交的树
				// 必然同时可见（非空标识），否则说明读到了撕裂状态。
				if res, err := svc.Resolve("cur^{tree}", Query{}); err != nil {
					if err.Kind != ErrNotExist {
						report("cur^{{tree}} failed mid-stream: %v", err)
					}
				} else if res.ID == "" || res.Type != TypeTree {
					report("cur^{{tree}} -> torn state %+v", res)
				}
				// 不变量2：日志导航结果属于已写入序列
				expr := fmt.Sprintf("cur@{%d}", r.Intn(8))
				if res, err := svc.Resolve(expr, Query{}); err == nil {
					if _, ok := written.Load(res.ID); !ok {
						report("%s -> %s not in written set", expr, res.ID)
					}
				} else if err.Kind != ErrNoSuchLogEntry && err.Kind != ErrNotExist {
					report("%s -> unexpected %v", expr, err)
				}
				// 不变量3：最短缩写单调有效
				if res, err := svc.Resolve("cur", Query{}); err == nil {
					if abbr, aerr := svc.ShortestAbbrev(res.ID); aerr == nil {
						if back, berr := svc.Resolve(abbr, Query{}); berr != nil || back.ID != res.ID {
							report("abbrev %q of %s no longer resolves back: %v", abbr, res.ID, berr)
						}
					}
				}
			}
		}(int64(w) + 100)
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}

	// 不变量4：所有符号引用链都可终止解析（无环）。
	for _, name := range []string{"s0", "s1", "s2", "s3"} {
		res, err := svc.Resolve(name, Query{})
		if err != nil && err.Kind != ErrNotExist {
			t.Errorf("resolve %q -> unexpected %v", name, err)
		}
		t.Logf("input=%q -> res=%+v err=%v | 判定依据: 并发符号引用设置后链必终止（成环已被拒绝）", name, res, err)
	}
	// 最终状态校验：引用指向最后一个提交，日志长度等于写入次数。
	res, err := svc.Resolve("cur@{0}", Query{})
	if err != nil || res.ID != pairs[nCommits-1].commit.ID {
		t.Fatalf("final cur@{0} -> %+v, %v; want %s", res, err, pairs[nCommits-1].commit.ID)
	}
	logLen := len(svc.refs.reflog("refs/heads/cur"))
	t.Logf("reads=%d reflog=%d final=%s | 判定依据: 日志长度等于串行写入次数，终值等于最后写入",
		reads.Load(), logLen, res.ID[:12])
	if logLen != nCommits {
		t.Fatalf("reflog length=%d, want %d", logLen, nCommits)
	}
}
