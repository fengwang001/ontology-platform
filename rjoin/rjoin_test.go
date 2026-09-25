package rjoin

import (
	"fmt"
	"maps"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func applyStep(j *Join, step string) error {
	p := strings.SplitN(step, ":", 3)
	switch p[0] {
	case "L":
		return j.Left(p[1], p[2])
	case "U":
		return j.UpsertRight(p[1], p[2])
	}
	return j.DeleteRight(p[1])
}

func genSteps(seed int64, n int) []string {
	r := rand.New(rand.NewSource(seed))
	steps := make([]string, 0, n)
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("k%d", r.Intn(5))
		cand := []string{fmt.Sprintf("L:L%d:%s", i, k), fmt.Sprintf("U:%s:v%d", k, i), "D:" + k}
		steps = append(steps, cand[r.Intn(3)])
	}
	return steps
}

// TestReevalCountIndependentOfM 证明重新求值靠反向索引直接定位：
// 检查过的左事件个数 == 该 key 自身的左事件数，与总左事件数 m 无关。
// 同包测试直接读非导出字段 checked，不经过任何导出接口。
func TestReevalCountIndependentOfM(t *testing.T) {
	const hot = 3 // "hot" key 上的左事件数（很少）
	for _, m := range []int{100, 300, 1000, 3000, 10000} {
		j := New()
		for i := 0; i < m; i++ {
			key := fmt.Sprintf("k%d", i%97) // 分散到多个 key
			if i < hot {
				key = "hot"
			}
			if err := j.Left(fmt.Sprintf("L%d", i), key); err != nil {
				t.Fatalf("m=%d Left: %v", m, err)
			}
		}
		if err := j.UpsertRight("hot", "v"); err != nil {
			t.Fatalf("m=%d UpsertRight: %v", m, err)
		}
		if j.checked != hot {
			t.Fatalf("m=%d: checked=%d, want %d（随 m 线性增长即为全表扫描）", m, j.checked, hot)
		}
		if err := j.DeleteRight("hot"); err != nil {
			t.Fatalf("m=%d DeleteRight: %v", m, err)
		}
		if j.checked != hot {
			t.Fatalf("m=%d: DeleteRight 后 checked=%d, want %d", m, j.checked, hot)
		}
	}
}

// TestReverseIndexConsistency 钉不变量 2：每步操作后反向索引内部一致。
func TestReverseIndexConsistency(t *testing.T) {
	for _, seed := range []int64{7, 8, 9} {
		j := New()
		for _, st := range genSteps(seed, 300) {
			if err := applyStep(j, st); err != nil {
				t.Fatalf("seed=%d %s: %v", seed, st, err)
			}
			if err := j.Check(); err != nil {
				t.Fatalf("seed=%d %s: %v", seed, st, err)
			}
		}
	}
}

// TestConcurrentReadAtomicity：并发只读逐 leftID 一致；更新期间只见旧值或新值。
func TestConcurrentReadAtomicity(t *testing.T) {
	j := New()
	const keys, per, readers = 8, 20, 8
	id := func(k, i int) string { return fmt.Sprintf("L%d-%d", k, i) }
	for k := 0; k < keys; k++ {
		_ = j.UpsertRight(fmt.Sprintf("k%d", k), "old")
		for i := 0; i < per; i++ {
			_ = j.Left(id(k, i), fmt.Sprintf("k%d", k))
		}
	}
	read := func(dst map[string]string) {
		for k := 0; k < keys; k++ {
			for i := 0; i < per; i++ {
				dst[id(k, i)], _ = j.GetView(id(k, i))
			}
		}
	}
	var wg sync.WaitGroup
	views := make([]map[string]string, readers)
	for g := range views {
		views[g] = map[string]string{}
		wg.Add(1)
		go func(g int) { defer wg.Done(); read(views[g]) }(g)
	}
	wg.Wait()
	for g := 1; g < readers; g++ {
		if !maps.Equal(views[0], views[g]) {
			t.Fatalf("读者 %d 与读者 0 不一致", g)
		}
	}
	var stop atomic.Bool
	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				for i := 0; i < per; i++ {
					if v, ok := j.GetView(id(0, i)); !ok || (v != "old" && v != "new") {
						t.Errorf("中间态: (%q,%v)", v, ok)
						return
					}
				}
			}
		}()
	}
	_ = j.UpsertRight("k0", "new")
	stop.Store(true)
	wg.Wait()
	if v, ok := j.GetView(id(0, 0)); v != "new" || !ok {
		t.Fatalf("更新未生效: (%q,%v)", v, ok)
	}
}
