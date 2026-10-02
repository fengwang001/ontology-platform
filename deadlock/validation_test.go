package deadlock

import (
	"fmt"
	"sync"
	"testing"
)

// TestConstructorValidation 构造参数越界报 ErrInvalidParam。
func TestConstructorValidation(t *testing.T) {
	goodT := []int64{1, 1}
	goodC := []int64{1, 1}
	cases := []struct {
		name string
		R    int
		T, c []int64
		P, L int
	}{
		{"R=0", 0, goodT, goodC, 1, 1},
		{"R=9", 9, make([]int64, 9), make([]int64, 9), 1, 1},
		{"len(T)!=R", 2, []int64{1}, goodC, 1, 1},
		{"len(c)!=R", 2, goodT, []int64{1}, 1, 1},
		{"T=0", 2, []int64{0, 1}, goodC, 1, 1},
		{"T>1e6", 2, []int64{1_000_001, 1}, goodC, 1, 1},
		{"c=0", 2, goodT, []int64{0, 1}, 1, 1},
		{"c>1000", 2, goodT, []int64{1001, 1}, 1, 1},
		{"P=0", 2, goodT, goodC, 0, 1},
		{"P=65", 2, goodT, goodC, 65, 1},
		{"L=0", 2, goodT, goodC, 1, 0},
		{"L=11", 2, goodT, goodC, 1, 11},
	}
	for _, tc := range cases {
		// 修正越界用例里顺带越界的切片长度，只让被测参数非法。
		if tc.name == "R=9" {
			for i := range tc.T {
				tc.T[i], tc.c[i] = 1, 1
			}
		}
		_, err := New(tc.R, tc.T, tc.c, tc.P, tc.L)
		wantErrCode(t, err, ErrInvalidParam)
	}
	if _, err := New(2, goodT, goodC, 1, 1); err != nil {
		t.Fatalf("valid constructor rejected: %v", err)
	}
}

// TestRequestErrorPrecedence Request 按 参数非法→进程不存在→已阻塞→永不可满足
// 的顺序只报第一个原因，且被拒绝时不改变状态。
func TestRequestErrorPrecedence(t *testing.T) {
	// 布景：L=1，进程 0、1 各持 1 并互相阻塞，进程 2 也阻塞；
	// Resolve 后进程 0 被永久中止，进程 1 被连锁授予持有 [2]，进程 2 仍阻塞。
	m := mustNew(t, 1, []int64{2}, []int64{1}, 3, 1)
	mustGrant(t, m, 0, [][]int64{{1}}, 0)
	mustGrant(t, m, 1, [][]int64{{1}}, 0)
	mustBlock(t, m, 0, [][]int64{{1}})
	mustBlock(t, m, 1, [][]int64{{1}})
	mustBlock(t, m, 2, [][]int64{{1}})
	m.Resolve()
	// 参数非法 优先于 进程不存在。
	_, err := m.Request(9, [][]int64{{}})
	wantErrCode(t, err, ErrInvalidParam)
	// 进程不存在 优先于 已阻塞/永不可满足（已中止进程 + 超总量备选）。
	_, err = m.Request(0, [][]int64{{3}})
	wantErrCode(t, err, ErrNoSuchProcess)
	// 已阻塞 优先于 永不可满足。
	_, err = m.Request(2, [][]int64{{3}})
	wantErrCode(t, err, ErrProcessBlocked)
	// 全部校验通过才报 永不可满足。
	_, err = m.Request(1, [][]int64{{3}})
	wantErrCode(t, err, ErrUnsatisfiable)
}

// TestReleaseErrorPrecedence Release 按 参数非法→进程不存在→已阻塞→归还超过持有
// 的顺序只报第一个原因。
func TestReleaseErrorPrecedence(t *testing.T) {
	// 布景同上：进程 0 已中止，进程 1 持有 [2]，进程 2 仍阻塞。
	m := mustNew(t, 1, []int64{2}, []int64{1}, 3, 1)
	mustGrant(t, m, 0, [][]int64{{1}}, 0)
	mustGrant(t, m, 1, [][]int64{{1}}, 0)
	mustBlock(t, m, 0, [][]int64{{1}})
	mustBlock(t, m, 1, [][]int64{{1}})
	mustBlock(t, m, 2, [][]int64{{1}})
	m.Resolve() // L=1，牺牲者被永久中止
	// 参数非法 优先于 进程不存在。
	_, err := m.Release(9, []int64{})
	wantErrCode(t, err, ErrInvalidParam)
	// 进程不存在 优先于 已阻塞/超过持有。
	_, err = m.Release(0, []int64{2})
	wantErrCode(t, err, ErrNoSuchProcess)
	// 已阻塞 优先于 超过持有。
	_, err = m.Release(2, []int64{2})
	wantErrCode(t, err, ErrProcessBlocked)
	// 全部校验通过才报 超过持有。
	_, err = m.Release(1, []int64{3})
	wantErrCode(t, err, ErrOverRelease)
}

// TestConcurrentOps 并发调用等价于某个串行顺序：
// 不变量 avail>=0 且 avail+Σalloc==T 在任何时候成立（由互斥锁保证），
// 结束后阻塞进程都不存在可满足备选（授予不动点）。
func TestConcurrentOps(t *testing.T) {
	m := mustNew(t, 3, []int64{4, 4, 4}, []int64{1, 2, 3}, 8, 2)
	var wg sync.WaitGroup
	for p := 0; p < 8; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				switch i % 4 {
				case 0:
					_, _ = m.Request(p, [][]int64{{1, 0, 0}, {0, 1, 0}})
				case 1:
					_, _ = m.Release(p, []int64{1, 0, 0})
				case 2:
					_ = m.Detect()
				case 3:
					_, _ = m.Release(p, []int64{0, 1, 0})
				}
			}
		}(p)
	}
	wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	for r := 0; r < m.R; r++ {
		if m.avail[r] < 0 {
			t.Fatalf("avail[%d] = %d < 0", r, m.avail[r])
		}
		sum := m.avail[r]
		for p := 0; p < m.P; p++ {
			sum += m.alloc[p][r]
		}
		if sum != m.T[r] {
			t.Fatalf("conservation violated at resource %d: %d != %d", r, sum, m.T[r])
		}
	}
	for p := 0; p < m.P; p++ {
		if !m.alive[p] || !m.blocked[p] {
			continue
		}
		if idx := firstFitting(m.pending[p], m.avail); idx >= 0 {
			t.Fatalf("blocked process %d has satisfiable alt %d after ops", p, idx)
		}
	}
}

// TestReplayDeterminism 相同操作序列重放得到完全相同的授予列表、
// 死锁集合与消解步骤。
func TestReplayDeterminism(t *testing.T) {
	run := func() string {
		m := mustNew(t, 2, []int64{2, 2}, []int64{1, 3}, 4, 2)
		out := ""
		grant := func(p int, alts [][]int64) {
			res, err := m.Request(p, alts)
			out += fmt.Sprintf("req(%d,%v)=%+v,%v;", p, alts, res, err)
		}
		grant(0, [][]int64{{1, 0}})
		grant(1, [][]int64{{0, 2}})
		grant(2, [][]int64{{1, 0}})
		grant(0, [][]int64{{0, 1}})
		grant(1, [][]int64{{1, 0}, {0, 1}})
		grant(2, [][]int64{{0, 1}})
		out += fmt.Sprintf("detect=%v;", m.Detect())
		out += fmt.Sprintf("resolve=%+v;", m.Resolve())
		g, err := m.Release(1, []int64{0, 2})
		out += fmt.Sprintf("rel=%v,%v;", g, err)
		out += fmt.Sprintf("detect=%v;", m.Detect())
		return out
	}
	first := run()
	for i := 0; i < 10; i++ {
		if got := run(); got != first {
			t.Fatalf("replay mismatch:\n%s\n%s", first, got)
		}
	}
}
