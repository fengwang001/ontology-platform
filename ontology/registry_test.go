package ontology

import (
	"errors"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// logState 打印：设值、脏集、刷新顺序、变更日志与判定依据。
func logState(t *testing.T, tag, sets string, r *Registry, order []ViewChange, reason string) {
	t.Helper()
	snap := r.Snapshot()
	dirty := make([]string, 0, len(snap.Dirty))
	for k := range snap.Dirty {
		dirty = append(dirty, k)
	}
	sort.Strings(dirty)
	names := make([]string, 0, len(order))
	for _, c := range order {
		names = append(names, c.Name)
	}
	t.Logf("[%s] 设值=%s 脏集=%v 刷新顺序=%v 变更日志=%v 判定依据=%s",
		tag, sets, dirty, names, snap.Log, reason)
}

func mustNew(t *testing.T, def Definition, maxViews int) *Registry {
	t.Helper()
	r, err := New(def, maxViews)
	if err != nil {
		t.Fatalf("New 意外失败: %v", err)
	}
	return r
}

func expectMap(t *testing.T, got, want map[string]float64) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("值不一致\n got=%v\nwant=%v", got, want)
	}
}

func namesOf(changes []ViewChange) []string {
	out := make([]string, len(changes))
	for i, c := range changes {
		out[i] = c.Name
	}
	return out
}

// 共享子树：s 被 x、y 共享，z 聚合 x、y；同一视图内重复依赖边去重。
func TestSharedSubtreeDedup(t *testing.T) {
	r := mustNew(t, Definition{
		Bases: []string{"a", "b"},
		Views: map[string][]string{
			"s": {"a", "b", "a"}, // 重复的 a 只计一次
			"x": {"s"},
			"y": {"s"},
			"z": {"x", "y"},
		},
	}, 0)

	if err := r.SetBases(map[string]float64{"a": 1, "b": 2}); err != nil {
		t.Fatal(err)
	}
	wantDirty := map[string]bool{"s": true, "x": true, "y": true, "z": true}
	if got := r.Dirty(); !reflect.DeepEqual(got, wantDirty) {
		t.Fatalf("脏集传递闭包错误: got=%v want=%v", got, wantDirty)
	}

	changes := r.Refresh()
	logState(t, "共享子树", "a=1,b=2", r, changes,
		"s=a+b=3；x=y=s=3；z=x+y=6；每个脏视图撤回+建立各一次")

	expectMap(t, r.Snapshot().Views, map[string]float64{"s": 3, "x": 3, "y": 3, "z": 6})

	wantOrder := []string{"s", "x", "y", "z"}
	if got := namesOf(changes); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("刷新顺序错误: got=%v want=%v", got, wantOrder)
	}
	for _, c := range changes {
		if len(c.Events) != 2 || c.Events[0].Kind != "retract" || c.Events[1].Kind != "assert" {
			t.Fatalf("视图 %q 必须先撤回再建立: %+v", c.Name, c.Events)
		}
	}

	counts := map[string]map[string]int{}
	for _, e := range r.ChangeLog() {
		if counts[e.Name] == nil {
			counts[e.Name] = map[string]int{}
		}
		counts[e.Name][e.Kind]++
	}
	for _, v := range wantOrder {
		if counts[v]["retract"] != 1 || counts[v]["assert"] != 1 {
			t.Fatalf("视图 %q 日志计数错误: %v", v, counts[v])
		}
	}
	if err := r.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// 多级传递：a → l1 → l2 → l3，标脏沿整链传播，重算严格自底向上。
func TestMultiLevelTransitive(t *testing.T) {
	r := mustNew(t, Definition{
		Bases: []string{"a"},
		Views: map[string][]string{
			"l1": {"a"},
			"l2": {"l1"},
			"l3": {"l2"},
		},
	}, 10)
	if err := r.SetBase("a", 4); err != nil {
		t.Fatal(err)
	}
	changes := r.Refresh()
	logState(t, "多级传递", "a=4", r, changes,
		"l1=4,l2=4,l3=4；顺序 l1→l2→l3，依赖恒在视图之前")

	if got, want := namesOf(changes), []string{"l1", "l2", "l3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("拓扑顺序错误: got=%v want=%v", got, want)
	}
	expectMap(t, r.Snapshot().Views, map[string]float64{"l1": 4, "l2": 4, "l3": 4})

	// 未刷新时 RecomputeAll 以登记后的基底值自底向上核对。
	if err := r.SetBase("a", 9); err != nil {
		t.Fatal(err)
	}
	full, err := r.RecomputeAll()
	if err != nil {
		t.Fatal(err)
	}
	expectMap(t, full, map[string]float64{"l1": 9, "l2": 9, "l3": 9})
	again := r.Refresh()
	logState(t, "多级传递-再设值", "a=9", r, again, "全量重算与增量刷新结果逐视图相等")
	expectMap(t, r.Snapshot().Views, full)
}

// 拓扑序与环检测：合法 Diamond 按拓扑序刷新；成环整体拒绝。
func TestTopoOrderAndCycle(t *testing.T) {
	r := mustNew(t, Definition{
		Bases: []string{"a"},
		Views: map[string][]string{
			"left":  {"a"},
			"right": {"a"},
			"top":   {"left", "right"},
		},
	}, 0)
	if err := r.SetBase("a", 5); err != nil {
		t.Fatal(err)
	}
	changes := r.Refresh()
	logState(t, "拓扑序", "a=5", r, changes,
		"left/right 先于 top；同层按字典序 left→right→top；top=10")

	if got, want := namesOf(changes), []string{"left", "right", "top"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("拓扑序错误: got=%v want=%v", got, want)
	}
	if changes[2].New != 10 {
		t.Fatalf("top 应为 5+5=10, got=%v", changes[2].New)
	}

	cyclic := Definition{
		Bases: []string{"a"},
		Views: map[string][]string{
			"p": {"q"},
			"q": {"r"},
			"r": {"p"},
		},
	}
	if _, err := New(cyclic, 0); !errors.Is(err, ErrCycle) {
		t.Fatalf("成环必须拒绝为 ErrCycle, got=%v", err)
	} else {
		t.Logf("[环检测] p→q→r→p 被拒绝: %v（判定依据：环上视图入度无法归零）", err)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []struct {
		name     string
		def      Definition
		maxViews int
		want     error
	}{
		{"空基底名", Definition{Bases: []string{""}}, 0, ErrEmptyName},
		{"空视图名", Definition{Views: map[string][]string{"": {"a"}}}, 0, ErrEmptyName},
		{"未知依赖名", Definition{Bases: []string{"a"}, Views: map[string][]string{"v": {"ghost"}}}, 0, ErrUnknownDep},
		{"基底视图重名", Definition{Bases: []string{"a"}, Views: map[string][]string{"a": {}}}, 0, ErrDuplicateName},
		{"视图数超限", Definition{Bases: []string{"a"}, Views: map[string][]string{"v1": {"a"}, "v2": {"a"}}}, 1, ErrTooManyViews},
		{"非法上限", Definition{}, -1, ErrInvalidLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.def, tc.maxViews)
			if !errors.Is(err, tc.want) {
				t.Fatalf("%s: got=%v want=%v", tc.name, err, tc.want)
			}
			t.Logf("[拒绝原因-%s] 整体拒绝且无实例产生: %v", tc.name, err)
		})
	}

	r := mustNew(t, Definition{Bases: []string{"a"}, Views: map[string][]string{"v": {"a"}}}, 0)
	if err := r.SetBase("ghost", 1); !errors.Is(err, ErrUnknownBase) {
		t.Fatalf("未知基底必须拒绝为 ErrUnknownBase, got=%v", err)
	}
	if _, err := r.Value("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未知查询必须拒绝为 ErrNotFound, got=%v", err)
	}
}

// 一次失败不得改变脏集、变更日志与已登记变更。
func TestAtomicFailureLeavesStateUntouched(t *testing.T) {
	r := mustNew(t, Definition{
		Bases: []string{"a", "b"},
		Views: map[string][]string{"v": {"a", "b"}},
	}, 0)
	if err := r.SetBases(map[string]float64{"a": 1, "b": 2}); err != nil {
		t.Fatal(err)
	}
	changes := r.Refresh()
	logLen := len(r.ChangeLog())

	err := r.SetBases(map[string]float64{"a": 7, "ghost": 1})
	if !errors.Is(err, ErrUnknownBase) {
		t.Fatalf("应拒绝未知基底: %v", err)
	}
	if got := r.Dirty(); len(got) != 0 {
		t.Fatalf("失败后脏集必须为空, got=%v", got)
	}
	if len(r.ChangeLog()) != logLen {
		t.Fatal("失败后变更日志不得增长")
	}
	if more := r.Refresh(); more != nil {
		t.Fatalf("失败的批次不得留下登记, got=%v", more)
	}
	expectMap(t, r.Snapshot().Views, map[string]float64{"v": 3})
	logState(t, "原子失败", "a=7,ghost=1(拒绝)", r, changes,
		"未知基底整体拒绝；脏集仍为空、日志长度不变、v 保持 3")
}

// 同一批内多次设值同一基底，只最后一次生效。
func TestLastWriteWins(t *testing.T) {
	r := mustNew(t, Definition{Bases: []string{"a"}, Views: map[string][]string{"v": {"a"}}}, 0)
	for _, x := range []float64{1, 2, 3} {
		if err := r.SetBase("a", x); err != nil {
			t.Fatal(err)
		}
	}
	changes := r.Refresh()
	logState(t, "批内去重", "a:1→2→3", r, changes,
		"只最后一次 a=3 生效；v 仅重算一次，撤回 0 建立 3")
	if len(changes) != 1 || changes[0].Old != 0 || changes[0].New != 3 {
		t.Fatalf("末值生效错误: %+v", changes)
	}

	// 值未变化的设值仍使视图变脏，下一批每个脏视图仍恰好出现一次。
	if err := r.SetBase("a", 3); err != nil {
		t.Fatal(err)
	}
	again := r.Refresh()
	if len(again) != 1 || again[0].Old != 3 || again[0].New != 3 {
		t.Fatalf("值未变也必须记录一次撤回/建立: %+v", again)
	}
}

// TestConcurrentReadsAndRefresh 并发调用快照/自检/查询与写者（建议配合 -race）。
// 协调器用“写者让出 + 读者齐发广播”的方式，使每轮 8 个读者在写者停顿时同时
// 发起 Snapshot：RWMutex 允许它们并发进入同一读临界区，故视图必须逐字段相同；
// 读者绝不在持有锁时阻塞，避免读写互相饿死。结束后校验日志每批每视图各一次。
func TestConcurrentReadsAndRefresh(t *testing.T) {
	r := mustNew(t, Definition{
		Bases: []string{"a", "b"},
		Views: map[string][]string{
			"s": {"a", "b"},
			"x": {"s"},
			"y": {"s"},
			"z": {"x", "y"},
		},
	}, 0)

	const readers = 8
	const rounds = 30

	// pause：每轮协调器等待写者走到让出点；release：允许写者继续刷新。
	// 带缓冲 + select，使停止信号可打断任何一次握手，写者不会泄漏。
	pause := make(chan struct{}, 1)
	release := make(chan struct{}, 1)
	stop := make(chan struct{})

	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for i := 1; ; i++ {
			if err := r.SetBases(map[string]float64{"a": float64(i), "b": float64(2 * i)}); err != nil {
				t.Errorf("SetBases 失败: %v", err)
				return
			}
			select {
			case pause <- struct{}{}:
			case <-stop:
				return
			}
			select {
			case <-stop:
				return
			case <-release:
			}
			r.Refresh()
		}
	}()

	var mismatch atomic.Int64
	var failures atomic.Int64
	var readersWG sync.WaitGroup

	for round := 0; round < rounds; round++ {
		<-pause // 写者已登记并标脏、尚未刷新

		start := make(chan struct{})
		snaps := make([]Snapshot, readers)
		for i := 0; i < readers; i++ {
			readersWG.Add(1)
			go func(i int) {
				defer readersWG.Done()
				<-start // 8 个读者同时发起读取
				snaps[i] = r.Snapshot()
				if _, err := r.Value("z"); err != nil {
					failures.Add(1)
				}
				if err := r.SelfCheck(); err != nil {
					failures.Add(1)
				}
			}(i)
		}
		close(start)
		readersWG.Wait()
		release <- struct{}{}

		for i := 1; i < readers; i++ {
			if !reflect.DeepEqual(snaps[0], snaps[i]) {
				mismatch.Add(1)
			}
		}
	}

	close(stop)
	writer.Wait()

	if mismatch.Load() != 0 {
		t.Fatalf("并发读者快照出现 %d 次不一致", mismatch.Load())
	}
	if failures.Load() != 0 {
		t.Fatalf("并发查询/自检出现 %d 次失败", failures.Load())
	}

	// 每批次内每个视图恰好一次 retract + 一次 assert。
	type key struct {
		batch int
		name  string
	}
	eventsByKey := map[key]map[string]int{}
	batches := map[int]struct{}{}
	for _, e := range r.ChangeLog() {
		k := key{e.Batch, e.Name}
		if eventsByKey[k] == nil {
			eventsByKey[k] = map[string]int{}
		}
		eventsByKey[k][e.Kind]++
		batches[e.Batch] = struct{}{}
	}
	for k, cnt := range eventsByKey {
		if cnt["retract"] != 1 || cnt["assert"] != 1 {
			t.Fatalf("批次 %d 视图 %q 日志非恰好一次: %v", k.batch, k.name, cnt)
		}
	}
	if len(batches) == 0 {
		t.Fatal("写者应至少完成一次刷新")
	}
	t.Logf("[并发] %d 轮齐发读者快照全部逐字段相同；共 %d 个刷新批次，每视图每批撤回/建立各一次",
		rounds, len(batches))
}
