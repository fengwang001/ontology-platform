package fulljoin

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// referenceView 是不依赖增量逻辑的朴素批量重算，作为判定基准。
func referenceView(left, right map[string]Row) []OutRow {
	byKey := func(set map[string]Row) map[string][]Row {
		g := map[string][]Row{}
		for _, r := range set {
			g[r.Key] = append(g[r.Key], r)
		}
		return g
	}
	lk, rk := byKey(left), byKey(right)
	keys := map[string]struct{}{}
	for k := range lk {
		keys[k] = struct{}{}
	}
	for k := range rk {
		keys[k] = struct{}{}
	}
	var out []OutRow
	for k := range keys {
		ls, rs := lk[k], rk[k]
		switch {
		case len(ls) > 0 && len(rs) > 0:
			for i := range ls {
				for j := range rs {
					l, r := ls[i], rs[j]
					out = append(out, OutRow{Key: k, Left: &l, Right: &r})
				}
			}
		case len(ls) > 0:
			for i := range ls {
				l := ls[i]
				out = append(out, OutRow{Key: k, Left: &l})
			}
		case len(rs) > 0:
			for i := range rs {
				r := rs[i]
				out = append(out, OutRow{Key: k, Right: &r})
			}
		}
	}
	sortOutRows(out)
	return out
}

func normalize(rs []OutRow) []OutRow {
	out := make([]OutRow, len(rs))
	for i, o := range rs {
		out[i] = cloneOutRow(o)
	}
	sortOutRows(out)
	return out
}

func assertView(t *testing.T, m *Maintainer, want []OutRow) {
	t.Helper()
	if got := normalize(m.View()); !reflect.DeepEqual(got, normalize(want)) {
		t.Fatalf("视图不符\n期望 %v\n实际 %v", want, got)
	}
}

// TestZeroCrossing 覆盖两侧计数穿越零时补位行与配对行的切换、
// |L|*|R| 乘积配对，以及"先撤回旧形态、再输出新形态"的日志顺序。
func TestZeroCrossing(t *testing.T) {
	m := New()

	apply := func(tag string, cs ...Change) []Entry {
		t.Helper()
		t.Logf("输入批次[%s]: %v", tag, cs)
		es, err := m.Apply(cs)
		if err != nil {
			t.Fatalf("批次 %s 被拒: %v", tag, err)
		}
		for _, e := range es {
			t.Logf("  日志: %s", e)
		}
		if err := m.Check(); err != nil {
			t.Fatalf("批次 %s 自检失败: %v", tag, err)
		}
		t.Logf("  结果视图: %v", m.View())
		return es
	}

	// 1) 左行先到：空位在右侧的补足行。
	apply("左l1", ins(SideLeft, "k", "l1"))
	assertView(t, m, referenceView(map[string]Row{"l1": {Key: "k", ID: "l1", Val: "l1"}}, nil))

	// 2) 右计数 0->1 穿越：先撤回 (l1⋈∅)，再输出配对行。
	diff := apply("右r1到达-穿越", ins(SideRight, "k", "r1"))
	if len(diff) != 2 || diff[0].Kind != '-' || diff[0].Row.Left == nil || diff[0].Row.Right != nil ||
		diff[1].Kind != '+' || diff[1].Row.Left == nil || diff[1].Row.Right == nil {
		t.Fatalf("穿越顺序错误，期望 -补位行/+配对行，实际: %v", diff)
	}
	t.Log("判定: 撤回行 (l1⋈∅) 当前存在且空位形态在右；新行两侧均非 nil")

	// 3) 非穿越：左2×右1，仅新增 l2 相关配对行。
	diff = apply("左l2非穿越", ins(SideLeft, "k", "l2"))
	if len(diff) != 1 {
		t.Fatalf("非穿越插入应只新增 1 条配对行，实际 %v", diff)
	}

	// 4) 左2×右2 = 4 条配对行。
	apply("右r2非穿越", ins(SideRight, "k", "r2"))
	assertView(t, m, referenceView(
		map[string]Row{"l1": {Key: "k", ID: "l1", Val: "l1"}, "l2": {Key: "k", ID: "l2", Val: "l2"}},
		map[string]Row{"r1": {Key: "k", ID: "r1", Val: "r1"}, "r2": {Key: "k", ID: "r2", Val: "r2"}},
	))

	// 5) 左计数 2->1 非穿越：仅撤回涉及 l2 的 2 条。
	diff = apply("删l2非穿越", del(SideLeft, "k", "l2"))
	if len(diff) != 2 {
		t.Fatalf("非穿越删除应只撤回 2 条配对行，实际 %v", diff)
	}

	// 6) 左计数 1->0 穿越：先撤回全部配对行，再输出右行的左空位补足行（空位在左）。
	diff = apply("删l1穿越", del(SideLeft, "k", "l1"))
	if len(diff) != 4 {
		t.Fatalf("穿越应有 2 撤回 + 2 输出，实际 %v", diff)
	}
	if diff[0].Kind != '-' || diff[0].Row.Left == nil || diff[0].Row.Right == nil {
		t.Fatalf("穿越应先撤回配对行，实际 %v", diff[0])
	}
	for _, e := range diff[2:] {
		if e.Kind != '+' || e.Row.Left != nil || e.Row.Right == nil {
			t.Fatalf("右行补足行必须空位在左侧，实际 %v", e)
		}
	}
	t.Logf("判定: 左计数 1->0，先撤回配对行 %v，再输出空位在左的补位行 %v", diff[:2], diff[2:])
	assertView(t, m, referenceView(nil,
		map[string]Row{"r1": {Key: "k", ID: "r1", Val: "r1"}, "r2": {Key: "k", ID: "r2", Val: "r2"}},
	))

	// 7) 右计数 2->1 非穿越，再 1->0 穿越，视图清空。
	apply("删r2非穿越", del(SideRight, "k", "r2"))
	diff = apply("删r1穿越", del(SideRight, "k", "r1"))
	if len(diff) != 1 || diff[0].Kind != '-' || diff[0].Row.Left != nil || diff[0].Row.Right == nil {
		t.Fatalf("右计数 1->0 应撤回空位在左的补位行，实际 %v", diff)
	}
	assertView(t, m, nil)
}

// TestPadSides 验证左右两侧撤回的补足位置不同，撤回的是当前存在且形态相符的行。
func TestPadSides(t *testing.T) {
	m := New()

	es, err := m.Apply([]Change{ins(SideLeft, "k", "l1")})
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 || es[0].Row.Left == nil || es[0].Row.Right != nil {
		t.Fatalf("左行补位必须空位在右，实际: %v", es)
	}
	t.Logf("输入[+L/l1] 结果 %s 判定: Left 非 nil、Right 为 nil", es[0])

	es, err = m.Apply([]Change{del(SideLeft, "k", "l1"), ins(SideRight, "k", "r1")})
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Kind: '-', Row: OutRow{Key: "k", Left: &Row{Key: "k", ID: "l1", Val: "l1"}}},
		{Kind: '+', Row: OutRow{Key: "k", Right: &Row{Key: "k", ID: "r1", Val: "r1"}}},
	}
	if !reflect.DeepEqual(es, want) {
		t.Fatalf("左右补位形态不符:\n实际 %v\n期望 %v", es, want)
	}
	t.Logf("输入[-L/l1,+R/r1] 结果 %v 判定: 撤回行空位在右，新补位行空位在左", es)
	if err := m.Check(); err != nil {
		t.Fatal(err)
	}
}

// TestInvalidBatches 覆盖三类互不相同的可判定错误，并断言整批原子不生效。
func TestInvalidBatches(t *testing.T) {
	cases := []struct {
		name    string
		changes []Change
		pred    func(error) bool
		label   string
	}{
		{"重复插入行标识", []Change{ins(SideLeft, "k", "dup"), ins(SideLeft, "k2", "dup")}, IsDuplicateID, "ErrDuplicateID"},
		{"删除不存在行标识", []Change{del(SideRight, "k", "ghost")}, IsRowNotFound, "ErrRowNotFound"},
		{"插入键为空", []Change{ins(SideLeft, "", "l1")}, IsEmptyKey, "ErrEmptyKey"},
		{"删除键为空", []Change{del(SideRight, "", "r1")}, IsEmptyKey, "ErrEmptyKey"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New()
			seed, _ := m.Apply([]Change{ins(SideLeft, "k", "l1")})
			snapshot := normalize(m.View())

			t.Logf("输入: %v", tc.changes)
			es, err := m.Apply(tc.changes)
			if err == nil {
				t.Fatalf("期望被拒绝，实际成功: %v", es)
			}
			if !tc.pred(err) {
				t.Fatalf("错误类型不符，期望 %s，实际 %v", tc.label, err)
			}
			t.Logf("结果: 拒绝（%v），判定依据: %s；整批不生效", err, tc.label)

			if got := normalize(m.View()); !reflect.DeepEqual(got, snapshot) {
				t.Fatalf("拒绝后视图被改变: %v != %v", got, snapshot)
			}
			if got := m.Log(); !reflect.DeepEqual(got, seed) {
				t.Fatalf("拒绝后日志被改变: %v != %v", got, seed)
			}
			if err := m.Check(); err != nil {
				t.Fatalf("拒绝后自检失败: %v", err)
			}
		})
	}

	// 批内前面的条目合法、后面非法：整批仍不生效。
	m := New()
	_, err := m.Apply([]Change{ins(SideLeft, "k", "l1"), ins(SideLeft, "k", "l1")})
	if !IsDuplicateID(err) {
		t.Fatalf("期望重复错误，实际 %v", err)
	}
	if len(m.View()) != 0 || len(m.Log()) != 0 {
		t.Fatalf("非法批次必须原子回滚，view=%d log=%d", len(m.View()), len(m.Log()))
	}
	t.Log("判定: 批内重复插入 → 全部回滚，视图与日志长度均为 0")

	// 跨批次重复插入：仅拒绝新批，历史状态保留。
	m2 := New()
	_, _ = m2.Apply([]Change{ins(SideLeft, "k", "l1")})
	before := m2.Log()
	_, err = m2.Apply([]Change{ins(SideLeft, "k", "l1")})
	if !IsDuplicateID(err) || !reflect.DeepEqual(m2.Log(), before) {
		t.Fatalf("跨批重复应拒绝且日志不变，err=%v", err)
	}
}

// TestBatchMixed 一个批内混合两侧增删（含穿越），结果与批量重算一致。
func TestBatchMixed(t *testing.T) {
	m := New()
	_, _ = m.Apply([]Change{
		ins(SideLeft, "k", "l1"),
		ins(SideRight, "k", "r1"),
	})
	batch := []Change{
		ins(SideLeft, "k", "l2"),
		ins(SideRight, "k", "r2"),
		del(SideLeft, "k", "l1"),
	}
	t.Logf("输入混合批: %v", batch)
	es, err := m.Apply(batch)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		t.Logf("  日志: %s", e)
	}
	assertView(t, m, referenceView(
		map[string]Row{"l2": {Key: "k", ID: "l2", Val: "l2"}},
		map[string]Row{"r1": {Key: "k", ID: "r1", Val: "r1"}, "r2": {Key: "k", ID: "r2", Val: "r2"}},
	))
	if err := m.Check(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentReads 写入持续进行时并发只读，断言同一实例连续读取
// 逐字段相同、自检始终通过；-race 下验证同步正确。
func TestConcurrentReads(t *testing.T) {
	m := New()
	stop := make(chan struct{})
	var writers, readers sync.WaitGroup

	writers.Add(1)
	go func() {
		defer writers.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			key := fmt.Sprintf("k%d", i%5)
			id := fmt.Sprintf("l%d", i)
			rid := "r" + id
			_, _ = m.Apply([]Change{ins(SideLeft, key, id)})
			_, _ = m.Apply([]Change{ins(SideRight, key, rid)})
			_, _ = m.Apply([]Change{del(SideLeft, key, id)})
			_, _ = m.Apply([]Change{del(SideRight, key, rid)})
		}
	}()

	for r := 0; r < 8; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for j := 0; j < 400; j++ {
				// View 与 Check 都在 RLock 内取同一个一致性快照：
				// 快照必须自洽（行数 = 左、右计数的全外连接结果）。
				_ = normalize(m.View())
				if err := m.Check(); err != nil {
					t.Errorf("并发自检失败: %v", err)
					return
				}
			}
		}()
	}

	readers.Wait()
	close(stop)
	writers.Wait()

	if err := m.Check(); err != nil {
		t.Fatalf("最终自检失败: %v", err)
	}

	// 写入静止后，多个读者并发读取同一实例，所得快照必须逐字段相同。
	frozen := normalize(m.View())
	var wg2 sync.WaitGroup
	views := make([][]OutRow, 16)
	for i := range views {
		wg2.Add(1)
		go func(idx int) {
			defer wg2.Done()
			views[idx] = normalize(m.View())
		}(i)
	}
	wg2.Wait()
	for i, v := range views {
		if !reflect.DeepEqual(v, frozen) {
			t.Fatalf("静止状态下读者 %d 视图与基准不同: %v != %v", i, v, frozen)
		}
	}
	t.Log("判定: 并发只读视图逐字段相同，自检与批量重算一致")
}
