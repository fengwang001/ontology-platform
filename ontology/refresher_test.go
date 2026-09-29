package ontology

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// mustAddBase 登记基底，失败即终止测试。
func mustAddBase(t *testing.T, r *Refresher, name string, v int64) {
	t.Helper()
	if err := r.AddBase(name, v); err != nil {
		t.Fatalf("AddBase(%q) 失败: %v", name, err)
	}
}

// mustAddView 登记视图，失败即终止测试。
func mustAddView(t *testing.T, r *Refresher, name string, deps ...string) {
	t.Helper()
	if err := r.AddView(name, deps...); err != nil {
		t.Fatalf("AddView(%q) 失败: %v", name, err)
	}
}

// mustSet 登记基底变更并打印设值日志。
func mustSet(t *testing.T, r *Refresher, name string, v int64) {
	t.Helper()
	if err := r.SetBase(name, v); err != nil {
		t.Fatalf("SetBase(%q) 失败: %v", name, err)
	}
	t.Logf("设值: 基底 %s := %d", name, v)
}

// refreshAndLog 执行刷新并打印脏集、刷新顺序与变更日志。
func refreshAndLog(t *testing.T, r *Refresher) []Change {
	t.Helper()
	t.Logf("刷新前脏集: %v", r.DirtySet())
	batch := r.Refresh()
	var order []string
	for _, c := range batch {
		if c.Kind == Retract {
			order = append(order, c.View)
		}
	}
	t.Logf("刷新顺序: %v", order)
	for _, c := range batch {
		t.Logf("变更日志: %s %s = %d", c.Kind, c.View, c.Value)
	}
	return batch
}

// assertEachViewOnce 判定依据：单次刷新中每个脏视图恰好出现一条撤回与一条建立，
// 且同一视图的撤回一定排在建立之前。
func assertEachViewOnce(t *testing.T, batch []Change, want map[string][2]int64) {
	t.Helper()
	retracts := make(map[string]int)
	asserts := make(map[string]int)
	seenRetract := make(map[string]bool)
	for _, c := range batch {
		switch c.Kind {
		case Retract:
			retracts[c.View]++
			seenRetract[c.View] = true
		case Assert:
			asserts[c.View]++
			if !seenRetract[c.View] {
				t.Errorf("视图 %s 的建立先于撤回", c.View)
			}
		}
	}
	for name := range want {
		if retracts[name] != 1 || asserts[name] != 1 {
			t.Errorf("视图 %s 在日志中出现 retract=%d assert=%d 次，判定依据: 每个脏视图恰好各一次",
				name, retracts[name], asserts[name])
		}
	}
	if len(retracts) != len(want) {
		t.Errorf("本批重算视图数 %d，期望 %d", len(retracts), len(want))
	}
	for _, c := range batch {
		pair := want[c.View]
		if c.Kind == Retract && c.Value != pair[0] {
			t.Errorf("视图 %s 撤回值 %d，期望旧值 %d", c.View, c.Value, pair[0])
		}
		if c.Kind == Assert && c.Value != pair[1] {
			t.Errorf("视图 %s 建立值 %d，期望新值 %d", c.View, c.Value, pair[1])
		}
	}
}

// assertTopoOrder 判定依据：日志中任何视图的重算都排在其全部脏依赖之后。
func assertTopoOrder(t *testing.T, r *Refresher, batch []Change) {
	t.Helper()
	pos := make(map[string]int)
	idx := 0
	for _, c := range batch {
		if c.Kind == Retract {
			pos[c.View] = idx
			idx++
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for name := range pos {
		for _, d := range r.views[name].deps {
			if depPos, ok := pos[d]; ok && depPos >= pos[name] {
				t.Errorf("拓扑序违例: 视图 %s(位置%d) 排在依赖 %s(位置%d) 之前",
					name, pos[name], d, depPos)
			}
		}
	}
}

// TestSharedSubtreeDedup 共享子树去重：菱形依赖 b→v1,v2→v3，
// 一次设值后 v3 只重算一次，且其值基于 v1、v2 的新值。
func TestSharedSubtreeDedup(t *testing.T) {
	r := NewRefresher(0)
	mustAddBase(t, r, "b", 1)
	mustAddView(t, r, "v1", "b")
	mustAddView(t, r, "v2", "b")
	mustAddView(t, r, "v3", "v1", "v2")

	mustSet(t, r, "b", 10)
	batch := refreshAndLog(t, r)

	// 判定依据: v1=v2=10, v3=v1+v2=20；旧值均为 0。
	assertEachViewOnce(t, batch, map[string][2]int64{
		"v1": {0, 10},
		"v2": {0, 10},
		"v3": {0, 20},
	})
	assertTopoOrder(t, r, batch)
	if got, _ := r.Get("v3"); got != 20 {
		t.Errorf("v3 = %d, 期望 20", got)
	}
	if err := r.SelfCheck(); err != nil {
		t.Errorf("自检失败: %v", err)
	}
	if dirty := r.DirtySet(); len(dirty) != 0 {
		t.Errorf("刷新后脏集应为空, 实际 %v", dirty)
	}
}

// TestMultiLevelPropagation 多级传递：b→a1→a2→a3 链式传播。
func TestMultiLevelPropagation(t *testing.T) {
	r := NewRefresher(0)
	mustAddBase(t, r, "b", 0)
	mustAddView(t, r, "a1", "b")
	mustAddView(t, r, "a2", "a1")
	mustAddView(t, r, "a3", "a2")

	mustSet(t, r, "b", 7)
	batch := refreshAndLog(t, r)

	// 判定依据: 链上每层等于下层新值, 最终全部为 7。
	assertEachViewOnce(t, batch, map[string][2]int64{
		"a1": {0, 7},
		"a2": {0, 7},
		"a3": {0, 7},
	})
	assertTopoOrder(t, r, batch)
	var order []string
	for _, c := range batch {
		if c.Kind == Retract {
			order = append(order, c.View)
		}
	}
	if !reflect.DeepEqual(order, []string{"a1", "a2", "a3"}) {
		t.Errorf("刷新顺序 %v, 期望 [a1 a2 a3]", order)
	}
	if err := r.SelfCheck(); err != nil {
		t.Errorf("自检失败: %v", err)
	}
}

// TestCycleDetection 环检测：重定义视图依赖成环必须整体拒绝。
func TestCycleDetection(t *testing.T) {
	r := NewRefresher(0)
	mustAddBase(t, r, "b", 1)
	mustAddView(t, r, "x", "b")
	mustAddView(t, r, "y", "x")

	err := r.AddView("x", "y")
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("重定义 x 依赖 y 应报依赖成环, 实际: %v", err)
	}
	t.Logf("判定依据: 拒绝原因 %v（x→y→x 成环）", err)

	err = r.AddView("z", "z")
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("视图依赖自身应报依赖成环, 实际: %v", err)
	}
	t.Logf("判定依据: 拒绝原因 %v（z 依赖自身）", err)

	// 失败后原有依赖关系不变, 功能仍正常。
	mustSet(t, r, "b", 4)
	batch := refreshAndLog(t, r)
	assertEachViewOnce(t, batch, map[string][2]int64{
		"x": {0, 4},
		"y": {0, 4},
	})
	if err := r.SelfCheck(); err != nil {
		t.Errorf("自检失败: %v", err)
	}
}

// TestRejectReasons 空名、未知依赖名、未知基底名、视图数超限分别可区分。
func TestRejectReasons(t *testing.T) {
	r := NewRefresher(2)
	mustAddBase(t, r, "b", 1)

	cases := []struct {
		name string
		err  error
		want error
	}{
		{"空基底名", r.AddBase("", 1), ErrEmptyName},
		{"空视图名", r.AddView("", "b"), ErrEmptyName},
		{"空依赖名", r.AddView("v", "b", ""), ErrEmptyName},
		{"未知依赖名", r.AddView("v", "nope"), ErrUnknownDependency},
		{"未知基底名", r.SetBase("nope", 1), ErrUnknownBase},
		{"重复基底名", r.AddBase("b", 2), ErrDuplicateName},
	}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: 错误 %v, 期望可判定为 %v", c.name, c.err, c.want)
		} else {
			t.Logf("判定依据: %s -> %v", c.name, c.err)
		}
	}

	mustAddView(t, r, "v1", "b")
	mustAddView(t, r, "v2", "b")
	if err := r.SetBase("v1", 1); !errors.Is(err, ErrUnknownBase) {
		t.Fatalf("对视图名设值应报未知基底名, 实际: %v", err)
	}
	if err := r.AddView("v3", "b"); !errors.Is(err, ErrTooManyViews) {
		t.Fatalf("第 3 个视图应报视图数超限, 实际: %v", err)
	}
	t.Logf("判定依据: 视图数上限 2, 第 3 个视图被拒绝")
	// 重定义已有视图不受上限影响。
	mustAddView(t, r, "v1", "b", "b")
}

// TestFailureAtomicity 一次失败不得改变脏集、变更日志与已登记变更。
func TestFailureAtomicity(t *testing.T) {
	r := NewRefresher(0)
	mustAddBase(t, r, "b", 1)
	mustAddView(t, r, "v", "b")
	mustSet(t, r, "b", 9)

	dirtyBefore := r.DirtySet()
	logBefore := r.Changes()

	if err := r.AddView("bad", "ghost"); !errors.Is(err, ErrUnknownDependency) {
		t.Fatalf("应报未知依赖名, 实际: %v", err)
	}
	if err := r.SetBase("ghost", 1); !errors.Is(err, ErrUnknownBase) {
		t.Fatalf("应报未知基底名, 实际: %v", err)
	}

	if got := r.DirtySet(); !reflect.DeepEqual(got, dirtyBefore) {
		t.Errorf("失败后脏集 %v, 期望保持 %v", got, dirtyBefore)
	}
	if got := r.Changes(); !reflect.DeepEqual(got, logBefore) {
		t.Errorf("失败后变更日志被改变: %v", got)
	}
	t.Logf("判定依据: 失败后脏集 %v 与日志条数 %d 均未变", r.DirtySet(), len(r.Changes()))

	// 已登记变更不受失败影响, 刷新后仍生效。
	batch := refreshAndLog(t, r)
	assertEachViewOnce(t, batch, map[string][2]int64{"v": {0, 9}})
}

// TestConcurrentReadWhileRefresh 查询与自检可并发调用。
// 阶段一：写协程交错设值刷新，读协程并发查询，验证无竞态；
// 阶段二：静止状态下并发读取，所有快照必须逐字段相同且自检通过。
func TestConcurrentReadWhileRefresh(t *testing.T) {
	r := NewRefresher(0)
	mustAddBase(t, r, "b", 1)
	mustAddView(t, r, "v1", "b")
	mustAddView(t, r, "v2", "v1")

	const rounds = 50
	var wg sync.WaitGroup
	errs := make(chan string, rounds*8)
	stop := make(chan struct{})

	// 写协程：逐轮设值并刷新。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := int64(1); i <= rounds; i++ {
			if err := r.SetBase("b", i); err != nil {
				errs <- fmt.Sprintf("SetBase: %v", err)
				return
			}
			r.Refresh()
		}
		close(stop)
	}()

	// 读协程：并发查询（设值后刷新前的待刷新状态合法, 只验证无竞态）。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := r.Snapshot()
				_, err1 := r.Get("v1")
				_, err2 := r.Get("v2")
				if err1 != nil || err2 != nil {
					errs <- fmt.Sprintf("reader %d: %v %v", id, err1, err2)
					return
				}
				_ = snap
				_ = r.DirtySet()
				_ = r.Changes()
				_ = r.SelfCheck()
			}
		}(w)
	}
	wg.Wait()

	if got, _ := r.Get("v2"); got != rounds {
		t.Errorf("最终 v2 = %d, 期望 %d", got, rounds)
	}

	// 阶段二：静止状态下并发读取, 所有快照必须逐字段相同。
	want := r.Snapshot()
	var wg2 sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg2.Add(1)
		go func(id int) {
			defer wg2.Done()
			for i := 0; i < 20; i++ {
				if got := r.Snapshot(); !reflect.DeepEqual(got, want) {
					errs <- fmt.Sprintf("reader %d: 快照 %v 与 %v 不逐字段相同", id, got, want)
					return
				}
				if err := r.SelfCheck(); err != nil {
					errs <- fmt.Sprintf("reader %d: 自检 %v", id, err)
					return
				}
			}
		}(w)
	}
	wg2.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
	t.Logf("判定依据: %d 轮设值刷新与并发读交错无竞态; 静止后并发快照逐字段相同: %v",
		rounds, want)
}

// TestReproducible 可复现：同样的登记与设值序列重放两遍，
// 刷新顺序、变更日志与最终状态逐字段相同。
func TestReproducible(t *testing.T) {
	build := func() *Refresher {
		r := NewRefresher(0)
		mustAddBase(t, r, "b1", 1)
		mustAddBase(t, r, "b2", 2)
		mustAddView(t, r, "s", "b1", "b2")
		mustAddView(t, r, "t", "s", "b2")
		mustAddView(t, r, "u", "s", "t")
		return r
	}
	run := func(r *Refresher) [][]Change {
		var batches [][]Change
		mustSet(t, r, "b1", 10)
		mustSet(t, r, "b2", 20)
		batches = append(batches, refreshAndLog(t, r))
		mustSet(t, r, "b1", 100)
		batches = append(batches, refreshAndLog(t, r))
		return batches
	}

	r1, r2 := build(), build()
	log1, log2 := run(r1), run(r2)
	if !reflect.DeepEqual(log1, log2) {
		t.Errorf("两次重放变更日志不同:\n%v\n%v", log1, log2)
	}
	if s1, s2 := r1.Snapshot(), r2.Snapshot(); !reflect.DeepEqual(s1, s2) {
		t.Errorf("两次重放最终状态不同:\n%v\n%v", s1, s2)
	}
	// 判定依据: u = s+t = (b1+b2)+(s+b2) = 100+20+120+20 = 260。
	if got, _ := r1.Get("u"); got != 260 {
		t.Errorf("u = %d, 期望 260", got)
	}
	t.Logf("判定依据: 拓扑序按登记顺序打破并列, 重放结果逐字段相同: %v",
		r1.Snapshot())
}

// TestRefreshEmptyBatch 空批刷新不产生任何日志。
func TestRefreshEmptyBatch(t *testing.T) {
	r := NewRefresher(0)
	mustAddBase(t, r, "b", 1)
	if batch := r.Refresh(); len(batch) != 0 {
		t.Errorf("空批应返回空日志, 实际 %v", batch)
	}
	t.Log("判定依据: 无已登记变更时刷新为空操作")
}

// TestLogFormat 变更日志条目可打印为可读的撤回/建立记录。
func TestLogFormat(t *testing.T) {
	r := NewRefresher(0)
	mustAddBase(t, r, "b", 1)
	mustAddView(t, r, "v", "b")
	mustSet(t, r, "b", 3)
	batch := refreshAndLog(t, r)
	var sb strings.Builder
	for _, c := range batch {
		fmt.Fprintf(&sb, "%s %s=%d; ", c.Kind, c.View, c.Value)
	}
	got := sb.String()
	if !strings.Contains(got, "RETRACT v=0") || !strings.Contains(got, "ASSERT v=3") {
		t.Errorf("日志格式不符合预期: %s", got)
	}
	t.Logf("判定依据: 日志串 %q 含先撤回旧值 0 再建立新值 3", got)
}

// TestBatchDedupLastWins 批内去重：同一批内对同一基底多次设值只最后一次生效。
func TestBatchDedupLastWins(t *testing.T) {
	r := NewRefresher(0)
	mustAddBase(t, r, "b", 1)
	mustAddView(t, r, "v", "b")

	mustSet(t, r, "b", 2)
	mustSet(t, r, "b", 3)
	mustSet(t, r, "b", 5)
	if got, _ := r.Get("b"); got != 1 {
		t.Errorf("刷新前基底值 %d, 期望仍为旧值 1（设值只登记不刷新）", got)
	}
	batch := refreshAndLog(t, r)

	// 判定依据: 只有最后一次设值 5 生效, v 只重算一次。
	assertEachViewOnce(t, batch, map[string][2]int64{"v": {0, 5}})
	if got, _ := r.Get("b"); got != 5 {
		t.Errorf("b = %d, 期望 5", got)
	}
	if got, _ := r.Get("v"); got != 5 {
		t.Errorf("v = %d, 期望 5", got)
	}
	if err := r.SelfCheck(); err != nil {
		t.Errorf("自检失败: %v", err)
	}
}
