package inlinecache

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func mustManager(t *testing.T, m int) *Manager {
	t.Helper()
	mgr, err := NewManager(m)
	if err != nil {
		t.Fatalf("NewManager(%d) 失败: %v", m, err)
	}
	return mgr
}

func mustCreate(t *testing.T, mgr *Manager, name string) {
	t.Helper()
	if err := mgr.CreateSite(name); err != nil {
		t.Fatalf("CreateSite(%q) 失败: %v", name, err)
	}
}

func mustDefine(t *testing.T, mgr *Manager, shape int, target string) {
	t.Helper()
	if err := mgr.Define(shape, target); err != nil {
		t.Fatalf("Define(%d, %q) 失败: %v", shape, target, err)
	}
}

func mustAccess(t *testing.T, mgr *Manager, site string, shape int) string {
	t.Helper()
	target, err := mgr.Access(site, shape)
	if err != nil {
		t.Fatalf("Access(%q, %d) 失败: %v", site, shape, err)
	}
	return target
}

func inspect(t *testing.T, mgr *Manager, site string) Snapshot {
	t.Helper()
	snap, err := mgr.Inspect(site)
	if err != nil {
		t.Fatalf("Inspect(%q) 失败: %v", site, err)
	}
	return snap
}

// expect 校验站点快照并在日志中打印判定依据。
func expect(t *testing.T, mgr *Manager, site string, state State, stats Stats, entries map[int]string) {
	t.Helper()
	snap := inspect(t, mgr, site)
	if snap.State != state {
		t.Errorf("站点 %q 状态 = %v, 期望 %v", site, snap.State, state)
	}
	if snap.Stats != stats {
		t.Errorf("站点 %q 统计 = %+v, 期望 %+v", site, snap.Stats, stats)
	}
	if entries != nil && !reflect.DeepEqual(snap.Entries, entries) {
		t.Errorf("站点 %q 条目 = %v, 期望 %v", site, snap.Entries, entries)
	}
	if got := snap.Stats.Total(); snap.Stats.Hits+snap.Stats.Misses+snap.Stats.Megamorphic != got {
		t.Errorf("站点 %q 统计之和 %d 不等于成功访问总数 %d", site,
			snap.Stats.Hits+snap.Stats.Misses+snap.Stats.Megamorphic, got)
	}
	t.Logf("判定通过: 站点=%q 状态=%v 条目=%v 统计=%+v (命中+未命中+超多态=%d)",
		site, snap.State, snap.Entries, snap.Stats, snap.Stats.Total())
}

// TestCapacityBoundary 恰好 M 个形状仍为多态，第 M+1 个形状进入超多态，
// 且第 M+1 个形状的访问计入未命中。
func TestCapacityBoundary(t *testing.T) {
	const m = 3
	mgr := mustManager(t, m)
	mustCreate(t, mgr, "s")
	for shape := 1; shape <= m+1; shape++ {
		mustDefine(t, mgr, shape, fmt.Sprintf("target-%d", shape))
	}

	for shape := 1; shape <= m; shape++ {
		got := mustAccess(t, mgr, "s", shape)
		t.Logf("输入: Access(s, %d) 输出: %q (未命中, 插入第 %d 个条目)", shape, got, shape)
	}
	expect(t, mgr, "s", StatePolymorphic, Stats{Misses: m},
		map[int]string{1: "target-1", 2: "target-2", 3: "target-3"})

	got := mustAccess(t, mgr, "s", m+1)
	t.Logf("输入: Access(s, %d) 输出: %q (缓存已满 M=%d, 进入超多态, 仍计未命中)", m+1, got, m)
	expect(t, mgr, "s", StateMegamorphic, Stats{Misses: m + 1}, map[int]string{})

	got = mustAccess(t, mgr, "s", 1)
	t.Logf("输入: Access(s, 1) 输出: %q (超多态直查方法表, 计超多态访问)", got)
	expect(t, mgr, "s", StateMegamorphic, Stats{Misses: m + 1, Megamorphic: 1}, map[int]string{})
}

// TestMegamorphicNeverLeaves 超多态后重定义不回退。
func TestMegamorphicNeverLeaves(t *testing.T) {
	mgr := mustManager(t, 2)
	mustCreate(t, mgr, "s")
	mustDefine(t, mgr, 1, "a")
	mustDefine(t, mgr, 2, "b")
	mustDefine(t, mgr, 3, "c")
	mustAccess(t, mgr, "s", 1)
	mustAccess(t, mgr, "s", 2)
	mustAccess(t, mgr, "s", 3) // 溢出 -> 超多态
	expect(t, mgr, "s", StateMegamorphic, Stats{Misses: 3}, map[int]string{})

	mustDefine(t, mgr, 1, "a2") // 重定义形状 1
	t.Log("输入: Define(1, \"a2\") 重定义, 超多态站点不受影响")
	expect(t, mgr, "s", StateMegamorphic, Stats{Misses: 3}, map[int]string{})

	got := mustAccess(t, mgr, "s", 1)
	if got != "a2" {
		t.Errorf("超多态访问应直查方法表返回新目标 a2, 得到 %q", got)
	}
	t.Logf("输入: Access(s, 1) 输出: %q (超多态直查方法表拿到重定义后的目标)", got)
	expect(t, mgr, "s", StateMegamorphic, Stats{Misses: 3, Megamorphic: 1}, map[int]string{})
}

// TestRedefineDemotes 重定义使多态回落为单态再回落为空。
func TestRedefineDemotes(t *testing.T) {
	mgr := mustManager(t, 4)
	mustCreate(t, mgr, "s")
	mustDefine(t, mgr, 1, "a")
	mustDefine(t, mgr, 2, "b")
	mustAccess(t, mgr, "s", 1)
	mustAccess(t, mgr, "s", 1)
	mustAccess(t, mgr, "s", 2)
	expect(t, mgr, "s", StatePolymorphic, Stats{Hits: 1, Misses: 2},
		map[int]string{1: "a", 2: "b"})

	mustDefine(t, mgr, 2, "b2") // 重定义 -> 删除形状 2 条目
	t.Log("输入: Define(2, \"b2\") 重定义, 站点删除形状 2 条目, 多态回落为单态")
	expect(t, mgr, "s", StateMonomorphic, Stats{Hits: 1, Misses: 2},
		map[int]string{1: "a"})

	mustDefine(t, mgr, 1, "a2") // 重定义 -> 删除形状 1 条目
	t.Log("输入: Define(1, \"a2\") 重定义, 站点删除形状 1 条目, 单态回落为空")
	expect(t, mgr, "s", StateEmpty, Stats{Hits: 1, Misses: 2}, map[int]string{})

	got := mustAccess(t, mgr, "s", 1)
	if got != "a2" {
		t.Errorf("重定义后访问应返回新目标 a2, 得到 %q", got)
	}
	t.Logf("输入: Access(s, 1) 输出: %q (空站点未命中, 重新缓存新目标)", got)
	expect(t, mgr, "s", StateMonomorphic, Stats{Hits: 1, Misses: 3},
		map[int]string{1: "a2"})
}

// TestRedefineSameTargetNoInvalidation 相同目标重定义不失效。
func TestRedefineSameTargetNoInvalidation(t *testing.T) {
	mgr := mustManager(t, 2)
	mustCreate(t, mgr, "s")
	mustDefine(t, mgr, 1, "a")
	mustAccess(t, mgr, "s", 1)
	expect(t, mgr, "s", StateMonomorphic, Stats{Misses: 1}, map[int]string{1: "a"})

	mustDefine(t, mgr, 1, "a") // 目标相同, 无影响
	t.Log("输入: Define(1, \"a\") 目标相同, 站点条目与统计不变")
	expect(t, mgr, "s", StateMonomorphic, Stats{Misses: 1}, map[int]string{1: "a"})

	mustAccess(t, mgr, "s", 1) // 仍然命中
	expect(t, mgr, "s", StateMonomorphic, Stats{Hits: 1, Misses: 1}, map[int]string{1: "a"})
}

// TestRejections 各类拒绝场景：可区分原因且不改状态。
func TestRejections(t *testing.T) {
	if _, err := NewManager(1); !errors.Is(err, ErrInvalidCapacity) {
		t.Errorf("NewManager(1) 错误 = %v, 期望 ErrInvalidCapacity", err)
	}
	t.Log("判定通过: M<2 构造被拒绝, 原因 ErrInvalidCapacity")

	mgr := mustManager(t, 2)
	mustCreate(t, mgr, "s")
	mustDefine(t, mgr, 1, "a")
	mustAccess(t, mgr, "s", 1)
	before := inspect(t, mgr, "s")

	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"重复创建站点", func() error { return mgr.CreateSite("s") }, ErrSiteExists},
		{"定义非正形状", func() error { return mgr.Define(0, "x") }, ErrInvalidShape},
		{"定义负形状", func() error { return mgr.Define(-3, "x") }, ErrInvalidShape},
		{"访问不存在站点", func() error { _, err := mgr.Access("ghost", 1); return err }, ErrSiteNotFound},
		{"访问非正形状", func() error { _, err := mgr.Access("s", 0); return err }, ErrInvalidShape},
		{"访问未定义形状", func() error { _, err := mgr.Access("s", 99); return err }, ErrShapeUndefined},
		{"查询不存在站点", func() error { _, err := mgr.Inspect("ghost"); return err }, ErrSiteNotFound},
	}
	for _, c := range cases {
		err := c.run()
		if !errors.Is(err, c.want) {
			t.Errorf("%s: 错误 = %v, 期望 %v", c.name, err, c.want)
			continue
		}
		t.Logf("判定通过: %s 被拒绝, 原因 %v", c.name, err)
	}

	after := inspect(t, mgr, "s")
	if !reflect.DeepEqual(before, after) {
		t.Errorf("被拒绝的操作改变了站点: 前 %+v 后 %+v", before, after)
	}
	if target, ok := mgr.Lookup(1); !ok || target != "a" {
		t.Errorf("被拒绝的操作改变了方法表: Lookup(1) = %q, %v", target, ok)
	}
	t.Log("判定通过: 所有被拒绝操作未改变站点与方法表")
}

// TestDuplicateCreateAtomic 重复创建整体拒绝，原站点不受任何影响。
func TestDuplicateCreateAtomic(t *testing.T) {
	mgr := mustManager(t, 2)
	mustCreate(t, mgr, "s")
	mustDefine(t, mgr, 1, "a")
	mustAccess(t, mgr, "s", 1)
	if err := mgr.CreateSite("s"); !errors.Is(err, ErrSiteExists) {
		t.Fatalf("重复创建错误 = %v, 期望 ErrSiteExists", err)
	}
	expect(t, mgr, "s", StateMonomorphic, Stats{Misses: 1}, map[int]string{1: "a"})
}

// ---------- 朴素模拟 ----------

// model 是按需求规则直译的朴素模拟，与被测实现独立。
type model struct {
	m     int
	table map[int]string
	sites map[string]*modelSite
}

type modelSite struct {
	entries map[int]string
	mega    bool
	stats   Stats
}

func newModel(m int) *model {
	return &model{m: m, table: map[int]string{}, sites: map[string]*modelSite{}}
}

func (md *model) create(name string) error {
	if _, ok := md.sites[name]; ok {
		return ErrSiteExists
	}
	md.sites[name] = &modelSite{entries: map[int]string{}}
	return nil
}

func (md *model) define(shape int, target string) error {
	if shape <= 0 {
		return ErrInvalidShape
	}
	old, ok := md.table[shape]
	md.table[shape] = target
	if ok && old != target {
		for _, s := range md.sites {
			if !s.mega {
				delete(s.entries, shape)
			}
		}
	}
	return nil
}

func (md *model) access(name string, shape int) (string, error) {
	s, ok := md.sites[name]
	if !ok {
		return "", ErrSiteNotFound
	}
	if shape <= 0 {
		return "", ErrInvalidShape
	}
	target, ok := md.table[shape]
	if !ok {
		return "", ErrShapeUndefined
	}
	if s.mega {
		s.stats.Megamorphic++
		return target, nil
	}
	if t, ok := s.entries[shape]; ok {
		s.stats.Hits++
		return t, nil
	}
	s.stats.Misses++
	if len(s.entries) == md.m {
		s.entries = map[int]string{}
		s.mega = true
		return target, nil
	}
	s.entries[shape] = target
	return target, nil
}

func (md *model) state(name string) State {
	s := md.sites[name]
	if s.mega {
		return StateMegamorphic
	}
	switch len(s.entries) {
	case 0:
		return StateEmpty
	case 1:
		return StateMonomorphic
	default:
		return StatePolymorphic
	}
}

// ---------- 操作序列 ----------

type op struct {
	kind   string // "create" / "define" / "access"
	site   string
	shape  int
	target string
}

func (o op) String() string {
	switch o.kind {
	case "create":
		return fmt.Sprintf("CreateSite(%q)", o.site)
	case "define":
		return fmt.Sprintf("Define(%d, %q)", o.shape, o.target)
	default:
		return fmt.Sprintf("Access(%q, %d)", o.site, o.shape)
	}
}

func genOps(r *rand.Rand, n int) []op {
	sites := []string{"alpha", "beta", "gamma"}
	targets := []string{"t1", "t2", "t3", "t4"}
	ops := make([]op, 0, n)
	for i := 0; i < n; i++ {
		switch r.Intn(3) {
		case 0:
			ops = append(ops, op{kind: "create", site: sites[r.Intn(len(sites))]})
		case 1:
			shape := r.Intn(8) - 1 // 覆盖非正与未定义形状
			ops = append(ops, op{kind: "define", shape: shape, target: targets[r.Intn(len(targets))]})
		default:
			ops = append(ops, op{kind: "access", site: sites[r.Intn(len(sites))], shape: r.Intn(8) - 1})
		}
	}
	return ops
}

// runOps 在被测管理器上重放操作序列，返回每步错误与访问输出。
func runOps(mgr *Manager, ops []op) ([]error, []string) {
	errs := make([]error, len(ops))
	outs := make([]string, len(ops))
	for i, o := range ops {
		switch o.kind {
		case "create":
			errs[i] = mgr.CreateSite(o.site)
		case "define":
			errs[i] = mgr.Define(o.shape, o.target)
		default:
			outs[i], errs[i] = mgr.Access(o.site, o.shape)
		}
	}
	return errs, outs
}

func snapshots(mgr *Manager, names []string) map[string]Snapshot {
	out := map[string]Snapshot{}
	for _, n := range names {
		if snap, err := mgr.Inspect(n); err == nil {
			out[n] = snap
		}
	}
	return out
}

// TestAgainstNaiveModel 随机操作序列与朴素模拟逐步对照。
func TestAgainstNaiveModel(t *testing.T) {
	const m = 3
	siteNames := []string{"alpha", "beta", "gamma"}
	for seed := int64(0); seed < 20; seed++ {
		r := rand.New(rand.NewSource(seed))
		ops := genOps(r, 300)
		mgr := mustManager(t, m)
		md := newModel(m)

		for i, o := range ops {
			var gotErr, wantErr error
			var gotOut, wantOut string
			switch o.kind {
			case "create":
				gotErr, wantErr = mgr.CreateSite(o.site), md.create(o.site)
			case "define":
				gotErr, wantErr = mgr.Define(o.shape, o.target), md.define(o.shape, o.target)
			default:
				gotOut, gotErr = mgr.Access(o.site, o.shape)
				wantOut, wantErr = md.access(o.site, o.shape)
			}
			if (gotErr == nil) != (wantErr == nil) ||
				(gotErr != nil && !errors.Is(gotErr, wantErr)) ||
				gotOut != wantOut {
				t.Fatalf("seed=%d 第 %d 步 %s: 实现(%q, %v) != 模型(%q, %v)",
					seed, i, o, gotOut, gotErr, wantOut, wantErr)
			}
			if i%50 == 0 || i == len(ops)-1 {
				t.Logf("seed=%d 第 %d 步 输入: %s 输出: (%q, %v) 判定: 与朴素模拟一致",
					seed, i, o, gotOut, gotErr)
			}
		}

		for _, name := range siteNames {
			ms, ok := md.sites[name]
			snap, err := mgr.Inspect(name)
			if !ok {
				if !errors.Is(err, ErrSiteNotFound) {
					t.Fatalf("seed=%d 站点 %q 两边存在性不一致", seed, name)
				}
				continue
			}
			if err != nil {
				t.Fatalf("seed=%d 站点 %q 实现侧缺失", seed, name)
			}
			if snap.State != md.state(name) || snap.Stats != ms.stats ||
				!reflect.DeepEqual(snap.Entries, ms.entries) {
				t.Fatalf("seed=%d 站点 %q 终态不一致: 实现 %+v, 模型 状态=%v 条目=%v 统计=%+v",
					seed, name, snap, md.state(name), ms.entries, ms.stats)
			}
			if snap.Stats.Total() != snap.Stats.Hits+snap.Stats.Misses+snap.Stats.Megamorphic {
				t.Fatalf("seed=%d 站点 %q 统计口径被破坏", seed, name)
			}
			t.Logf("seed=%d 站点=%q 终态 状态=%v 条目=%v 统计=%+v 判定: 与朴素模拟一致",
				seed, name, snap.State, snap.Entries, snap.Stats)
		}
	}
}

// TestReplayDeterminism 相同操作序列重放得到完全相同的状态与统计。
func TestReplayDeterminism(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	ops := genOps(r, 500)
	siteNames := []string{"alpha", "beta", "gamma"}

	run := func() (map[string]Snapshot, []error, []string) {
		mgr := mustManager(t, 3)
		errs, outs := runOps(mgr, ops)
		return snapshots(mgr, siteNames), errs, outs
	}
	first, errs1, outs1 := run()
	second, errs2, outs2 := run()

	if !reflect.DeepEqual(first, second) ||
		!reflect.DeepEqual(errs1, errs2) || !reflect.DeepEqual(outs1, outs2) {
		t.Fatal("相同操作序列两次重放结果不一致")
	}
	t.Logf("判定通过: %d 步操作两次重放的状态、统计与输出完全相同", len(ops))
}

// TestConcurrent 并发调用等价于某个串行顺序：
// 不变式为每站点 命中+未命中+超多态 == 成功访问总数。
func TestConcurrent(t *testing.T) {
	mgr := mustManager(t, 4)
	mustDefine(t, mgr, 1, "a")
	mustDefine(t, mgr, 2, "b")
	mustDefine(t, mgr, 3, "c")
	siteNames := []string{"s0", "s1", "s2", "s3"}
	for _, n := range siteNames {
		mustCreate(t, mgr, n)
	}

	success := make([]int64, len(siteNames))
	var wg sync.WaitGroup
	var mu sync.Mutex
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 2000; i++ {
				idx := r.Intn(len(siteNames))
				switch r.Intn(4) {
				case 0:
					_ = mgr.Define(r.Intn(5)+1, fmt.Sprintf("v%d", r.Intn(3)))
				case 1:
					_, _ = mgr.Inspect(siteNames[idx])
				default:
					if _, err := mgr.Access(siteNames[idx], r.Intn(5)+1); err == nil {
						mu.Lock()
						success[idx]++
						mu.Unlock()
					}
				}
			}
		}(int64(w))
	}
	wg.Wait()

	for i, n := range siteNames {
		snap := inspect(t, mgr, n)
		if got := snap.Stats.Total(); got != uint64(success[i]) {
			t.Errorf("站点 %q 统计之和 %d != 成功访问数 %d", n, got, success[i])
			continue
		}
		t.Logf("判定通过: 站点=%q 状态=%v 统计=%+v 总和=%d 等于并发成功访问数",
			n, snap.State, snap.Stats, snap.Stats.Total())
	}
}
