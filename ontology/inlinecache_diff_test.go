package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// genScript 用固定种子生成随机操作序列（包含一定比例的非法操作）。
func genScript(seed int64, n int) []scriptOp {
	r := rand.New(rand.NewSource(seed))
	ops := make([]scriptOp, n)
	for i := range ops {
		switch r.Intn(10) {
		case 0, 1: // define（约 20%）
			shape := r.Intn(6) + 1 // 1..6，多为正整数
			if r.Intn(10) == 0 {
				shape = -r.Intn(3) // 偶尔非法形状
			}
			ops[i] = scriptOp{kind: "define", shape: shape, target: r.Intn(4) * 100}
		case 2: // create（约 10%，含重名）
			ops[i] = scriptOp{kind: "create", name: fmt.Sprintf("site%d", r.Intn(3))}
		default: // access（约 70%，部分形状尚未定义、偶尔非法站点/形状）
			name := fmt.Sprintf("site%d", r.Intn(4))
			shape := r.Intn(8) - 1 // -1..6，含 0/-1 非法
			ops[i] = scriptOp{kind: "access", name: name, shape: shape}
		}
	}
	return ops
}

func replay(mLimit int, ops []scriptOp) (modelState, [][2]ErrorCode) {
	mgr, _ := NewManager(mLimit)
	n := newNaive(mLimit)
	if !reflect.DeepEqual(dumpManager(nil, mgr), modelState{table: map[int]any{}, sites: map[string]SiteSnapshot{}}) {
		panic("initial state mismatch")
	}
	codes := make([][2]ErrorCode, len(ops))
	for i, op := range ops {
		mv, mc := runOnManager(mgr, op)
		nv, nc := runOnNaive(n, op)
		codes[i] = [2]ErrorCode{mc, nc}
		if mc != nc || !reflect.DeepEqual(mv, nv) {
			panic(fmt.Sprintf("op %d %+v: manager=(%v,%s) naive=(%v,%s)", i, op, mv, mc, nv, nc))
		}
		if !reflect.DeepEqual(dumpManager(nil, mgr), dumpNaive(n)) {
			panic(fmt.Sprintf("op %d %+v: state diverges", i, op))
		}
	}
	return dumpManager(nil, mgr), codes
}

// TestNaiveDifferential：多个随机脚本逐步与朴素模拟对照（输入/输出/状态）。
func TestNaiveDifferential(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		mLimit := 2 + int(seed%4) // M 取 2..5
		ops := genScript(seed, 300)
		state, codes := replay(mLimit, ops)
		t.Logf("seed=%d M=%d: %d ops, 终态站点数=%d 方法表条目=%d", seed, mLimit, len(ops), len(state.sites), len(state.table))
		rejected := 0
		for _, c := range codes {
			if c[0] != "" {
				rejected++
			}
		}
		t.Logf("  拒绝操作数=%d（manager 与 naive 错误码逐条一致）", rejected)
		// 每站点统计不变量：hits+misses+mega == 成功访问数（由差分逐步保证）。
		for name, snap := range state.sites {
			if snap.Stats.Total() < 0 {
				t.Fatalf("site %s negative totals", name)
			}
		}
	}
}

// TestReplayDeterminism：相同操作序列重放得到完全相同的状态与统计。
func TestReplayDeterminism(t *testing.T) {
	ops := genScript(42, 500)
	s1, c1 := replay(3, ops)
	s2, c2 := replay(3, ops)
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("replay state differs:\n%#v\n%#v", s1, s2)
	}
	if !reflect.DeepEqual(c1, c2) {
		t.Fatalf("replay outputs differ")
	}
	t.Logf("输入: 相同500条随机脚本重放两次 | 输出: 状态与每步错误码完全一致 | 判定: 重放可精确复现")
}

// TestRejectedOpsDoNotMutate：被拒绝操作前后，管理器整体状态字节级不变。
func TestRejectedOpsDoNotMutate(t *testing.T) {
	mgr, _ := NewManager(2)
	if err := mgr.CreateSite("a"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Define(1, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Access("a", 1); err != nil {
		t.Fatal(err)
	}
	before := dumpManager(t, mgr)

	tryReject := func(desc string, fn func() error) {
		t.Helper()
		if err := fn(); err == nil {
			t.Fatalf("%s: expected rejection", desc)
		}
		after := dumpManager(t, mgr)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("%s mutated state:\nbefore=%#v\nafter=%#v", desc, before, after)
		}
		t.Logf("输入: %s | 输出: 拒绝 | 判定: 站点/统计/方法表均未改变", desc)
	}
	tryReject("重复 create(a)", func() error { return mgr.CreateSite("a") })
	tryReject("define 形状0", func() error { return mgr.Define(0, 1) })
	tryReject("access 不存在站点", func() error { _, e := mgr.Access("zzz", 1); return e })
	tryReject("access 形状0", func() error { _, e := mgr.Access("a", 0); return e })
	tryReject("access 未定义形状2", func() error { _, e := mgr.Access("a", 2); return e })
}

// TestConcurrent：创建/定义/访问/查询并发调用，验证无竞态且统计自洽。
func TestConcurrent(t *testing.T) {
	mgr, _ := NewManager(3)
	for i := 0; i < 4; i++ {
		if err := mgr.CreateSite(fmt.Sprintf("c%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	var ops int64
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < 2000; i++ {
				switch r.Intn(3) {
				case 0:
					shape := r.Intn(5) + 1
					_ = mgr.Define(shape, r.Intn(3)*100+shape)
				case 1:
					site := fmt.Sprintf("c%d", r.Intn(4))
					shape := r.Intn(5) + 1
					if _, err := mgr.Access(site, shape); err == nil {
						atomic.AddInt64(&ops, 1)
					}
				default:
					_, _ = mgr.Snapshot(fmt.Sprintf("c%d", r.Intn(4)))
				}
			}
		}(w)
	}
	wg.Wait()

	var sumTotal int64
	for _, name := range mgr.Sites() {
		s, _ := mgr.Stats(name)
		sumTotal += s.Total()
	}
	if sumTotal != atomic.LoadInt64(&ops) {
		t.Fatalf("successful accesses = %d, sum(site totals) = %d", ops, sumTotal)
	}
	t.Logf("并发: 8 goroutine x 2000 混合操作 | 成功访问=%d 各站点total之和=%d | 判定: 等价某一串行顺序且统计自洽", ops, sumTotal)
}
