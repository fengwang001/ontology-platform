package dedup

import (
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// judge 打印输入、结果与判定依据，便于在测试日志中审计。
func judge(t *testing.T, input string, got, want any, reason string) {
	t.Helper()
	t.Logf("输入=%s | 结果=%v | 期望=%v | 判定依据=%s", input, got, want, reason)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("断言失败: 输入=%s 结果=%v 期望=%v (%s)", input, got, want, reason)
	}
}

func viewKeys(view map[string]struct{}) []string {
	keys := make([]string, 0, len(view))
	for element := range view {
		keys = append(keys, element)
	}
	sort.Strings(keys)
	return keys
}

func kinds(log []Change) []string {
	out := make([]string, 0, len(log))
	for _, change := range log {
		switch change.Kind {
		case KindAdd:
			out = append(out, "+"+change.Element)
		case KindRemove:
			out = append(out, "-"+change.Element)
		}
	}
	return out
}

// TestCrossPartitionHold 验证跨分区持有时撤回单个分区不会撤下元素：
// 引用计数大于一，撤回仅减计数，不输出减条目。
func TestCrossPartitionHold(t *testing.T) {
	m, err := New(3)
	judge(t, "New(3)", err, nil, "分区数为正，构造成功")

	judge(t, "Add(0,a)", m.Add(0, "a"), nil, "首次加入，计数 0->1，应输出加条目")
	judge(t, "Add(1,a)", m.Add(1, "a"), nil, "第二分区持有，计数 1->2，无输出")
	judge(t, "Add(2,a)", m.Add(2, "a"), nil, "第三分区持有，计数 2->3，无输出")
	judge(t, "日志", kinds(m.Log()), []string{"+a"}, "仅零变一时输出加条目")

	judge(t, "Remove(0,a)", m.Remove(0, "a"), nil, "计数 3->2，仍有分区持有，不撤下")
	judge(t, "Has(a) 撤回分区0后", m.Has("a"), true, "计数 2>=1，元素仍在视图")
	judge(t, "RefCount(a)", m.RefCount("a"), 2, "恰有两个分区仍持有 a")
	judge(t, "日志", kinds(m.Log()), []string{"+a"}, "计数未到零，无减条目")

	judge(t, "Remove(1,a)", m.Remove(1, "a"), nil, "计数 2->1，仍有分区持有，不撤下")
	judge(t, "Has(a) 撤回分区1后", m.Has("a"), true, "分区2仍持有，元素仍在视图")
	judge(t, "日志", kinds(m.Log()), []string{"+a"}, "计数未到零，无减条目")

	judge(t, "Remove(2,a)", m.Remove(2, "a"), nil, "计数 1->0，最后一个持有分区撤回，撤下")
	judge(t, "Has(a) 全部撤回后", m.Has("a"), false, "无任何分区持有，元素退出视图")
	judge(t, "RefCount(a)", m.RefCount("a"), 0, "计数归零后键被清理")
	judge(t, "日志", kinds(m.Log()), []string{"+a", "-a"}, "一变零输出减条目，与加条目严格配对")
	judge(t, "SelfCheck", m.SelfCheck(), nil, "按分区集合重算的计数与内部一致")
}

// TestAlreadyInViewNoDuplicateAdd 验证元素已在视图/已在分区时重复加入幂等：
// 不重复计数、不重复输出加条目。
func TestAlreadyInViewNoDuplicateAdd(t *testing.T) {
	m, _ := New(2)

	judge(t, "Add(0,x)", m.Add(0, "x"), nil, "首次加入 x")
	judge(t, "重复 Add(0,x)", m.Add(0, "x"), nil, "同分区重复加入按幂等处理为无操作")
	judge(t, "RefCount(x) 同分区重复后", m.RefCount("x"), 1, "同分区至多持有一次，计数不增加")
	judge(t, "日志", kinds(m.Log()), []string{"+x"}, "重复加入不产生第二条加条目")

	judge(t, "Add(1,x)", m.Add(1, "x"), nil, "另一分区加入，计数 1->2 但无输出")
	judge(t, "RefCount(x) 跨分区后", m.RefCount("x"), 2, "两个分区各持有一次")
	judge(t, "重复 Add(1,x)", m.Add(1, "x"), nil, "同分区重复加入仍为无操作")
	judge(t, "RefCount(x) 再次重复后", m.RefCount("x"), 2, "计数保持 2")
	judge(t, "日志", kinds(m.Log()), []string{"+x"}, "元素已在视图时不重复加")
	judge(t, "SelfCheck", m.SelfCheck(), nil, "分区集合与计数一致")
}

// TestRemoveNotHeldIsNoop 验证撤回不存在的元素为无操作：
// 不改变视图、计数与日志。
func TestRemoveNotHeldIsNoop(t *testing.T) {
	m, _ := New(2)

	judge(t, "Remove(0,ghost) 元素从不存在", m.Remove(0, "ghost"), nil, "不在该分区，撤回为无操作")
	judge(t, "Has(ghost)", m.Has("ghost"), false, "无操作后元素仍不在视图")
	judge(t, "日志", kinds(m.Log()), []string{}, "无操作不产生减条目")

	judge(t, "Add(0,y)", m.Add(0, "y"), nil, "仅分区0持有 y")
	judge(t, "Remove(1,y) 其他分区撤回", m.Remove(1, "y"), nil, "y 不在分区1，撤回为无操作")
	judge(t, "Has(y)", m.Has("y"), true, "无操作不影响分区0的持有")
	judge(t, "RefCount(y)", m.RefCount("y"), 1, "计数保持 1")
	judge(t, "日志", kinds(m.Log()), []string{"+y"}, "无操作不产生减条目")
	judge(t, "SelfCheck", m.SelfCheck(), nil, "分区集合与计数一致")
}

// TestInvalidInputsRejectedAndStateUnchanged 覆盖三类非法输入：
// 分区数非正、分区越界、元素为空；互不相同且被拒后状态不变。
func TestInvalidInputsRejectedAndStateUnchanged(t *testing.T) {
	for _, n := range []int{0, -1, -99} {
		m, err := New(n)
		if !errors.Is(err, ErrInvalidPartitionCount) {
			t.Fatalf("New(%d) err=%v, want ErrInvalidPartitionCount", n, err)
		}
		t.Logf("输入=New(%d) | 结果=%v | 期望=ErrInvalidPartitionCount | 判定依据=分区数非正必须拒绝", n, err)
		if m != nil {
			t.Fatalf("New(%d) 返回了非空维护器", n)
		}
	}

	m, _ := New(2)
	if err := m.Add(0, "keep"); err != nil {
		t.Fatalf("准备数据失败: %v", err)
	}
	beforeView := viewKeys(m.View())
	beforeLog := kinds(m.Log())

	cases := []struct {
		name    string
		call    func() error
		wantErr error
		reason  string
	}{
		{"Add 分区-1", func() error { return m.Add(-1, "a") }, ErrPartitionOutOfRange, "负下标越界"},
		{"Add 分区2(n=2)", func() error { return m.Add(2, "a") }, ErrPartitionOutOfRange, "上界越界"},
		{"Remove 分区9", func() error { return m.Remove(9, "a") }, ErrPartitionOutOfRange, "越界撤回拒绝"},
		{"Add 空元素", func() error { return m.Add(0, "") }, ErrEmptyElement, "元素为空拒绝"},
		{"Remove 空元素", func() error { return m.Remove(0, "") }, ErrEmptyElement, "空元素撤回拒绝"},
	}
	for _, tc := range cases {
		err := tc.call()
		judge(t, tc.name, err, tc.wantErr, tc.reason)
	}

	if ErrInvalidPartitionCount == ErrPartitionOutOfRange ||
		ErrPartitionOutOfRange == ErrEmptyElement ||
		ErrInvalidPartitionCount == ErrEmptyElement {
		t.Fatal("三类非法输入错误必须互不相同")
	}
	t.Log("输入=三类错误 | 结果=互不相同 | 期望=互不相同 | 判定依据=各错误为独立哨兵值")

	judge(t, "非法调用后视图", viewKeys(m.View()), beforeView, "被拒操作不改变视图")
	judge(t, "非法调用后日志", kinds(m.Log()), beforeLog, "被拒操作不改变日志")
	judge(t, "RefCount(keep)", m.RefCount("keep"), 1, "被拒操作不改变引用计数")
	judge(t, "SelfCheck", m.SelfCheck(), nil, "被拒操作不改变分区集合与计数")
}

// TestBatchAtomicity 验证任一条被拒则整批不生效，失败不改变任何状态。
func TestBatchAtomicity(t *testing.T) {
	m, _ := New(2)

	good := []Op{{0, "a"}, {1, "b"}, {1, "a"}}
	judge(t, "AddBatch 合法批次", m.AddBatch(good), nil, "预检通过，整批生效")
	judge(t, "视图", viewKeys(m.View()), []string{"a", "b"}, "a 被两个分区持有但只出现一次")
	judge(t, "日志", kinds(m.Log()), []string{"+a", "+b"}, "零变一各输出一条加条目")

	beforeView := viewKeys(m.View())
	beforeLog := kinds(m.Log())

	judge(t, "AddBatch 含越界", m.AddBatch([]Op{{0, "c"}, {5, "d"}, {0, "e"}}), ErrPartitionOutOfRange, "任一条非法，整批拒绝")
	judge(t, "AddBatch 含空元素", m.AddBatch([]Op{{0, "x"}, {0, ""}}), ErrEmptyElement, "空元素同样整批拒绝")
	judge(t, "RemoveBatch 含越界", m.RemoveBatch([]Op{{1, "a"}, {-1, "z"}}), ErrPartitionOutOfRange, "撤回批次整批拒绝")

	judge(t, "失败后视图", viewKeys(m.View()), beforeView, "失败批次不改变视图（c/d/e/x 均未进入）")
	judge(t, "失败后日志", kinds(m.Log()), beforeLog, "失败批次不追加日志")
	judge(t, "RefCount(a)", m.RefCount("a"), 2, "被拒撤回批次未减少计数")
	judge(t, "SelfCheck", m.SelfCheck(), nil, "整批失败后内部状态仍一致")

	judge(t, "RemoveBatch 合法批次", m.RemoveBatch([]Op{{0, "a"}, {1, "b"}}), nil, "预检通过，整批生效")
	judge(t, "RefCount(a)", m.RefCount("a"), 1, "仅撤回分区0的持有，分区1仍持有")
	judge(t, "Has(b)", m.Has("b"), false, "b 唯一持有被撤回，退出视图")
	judge(t, "日志", kinds(m.Log()), []string{"+a", "+b", "-b"}, "仅 b 发生一变零")
	judge(t, "批次内重复 AddBatch", m.AddBatch([]Op{{0, "z"}, {0, "z"}}), nil, "同批重复元素按幂等只生效一次")
	judge(t, "RefCount(z)", m.RefCount("z"), 1, "同批重复不重复计数")
	judge(t, "空批次 AddBatch", m.AddBatch(nil), nil, "空批次预检通过且为无操作")
	judge(t, "空批次后 SelfCheck", m.SelfCheck(), nil, "空批次不改变状态")
}

// TestBatchRecomputeEquivalence 用独立的参考模型批量重算，
// 验证增量维护的视图与计数与批量重算逐字段一致。
func TestBatchRecomputeEquivalence(t *testing.T) {
	m, _ := New(4)
	ops := []struct {
		add       bool
		partition int
		element   string
	}{
		{true, 0, "u"}, {true, 1, "u"}, {true, 2, "v"}, {true, 3, "w"},
		{false, 0, "u"}, {true, 0, "v"}, {false, 2, "v"}, {true, 1, "w"},
		{false, 3, "w"}, {true, 2, "u"}, {false, 1, "u"}, {true, 0, "x"},
		{false, 0, "x"}, {true, 3, "v"},
	}

	model := make([]map[string]bool, 4)
	for i := range model {
		model[i] = make(map[string]bool)
	}
	for i, op := range ops {
		if op.add {
			if err := m.Add(op.partition, op.element); err != nil {
				t.Fatalf("op %d Add 意外失败: %v", i, err)
			}
			model[op.partition][op.element] = true
		} else {
			if err := m.Remove(op.partition, op.element); err != nil {
				t.Fatalf("op %d Remove 意外失败: %v", i, err)
			}
			delete(model[op.partition], op.element)
		}

		want := map[string]int{}
		for _, part := range model {
			for element := range part {
				want[element]++
			}
		}
		gotView := m.View()
		for element, count := range want {
			if _, ok := gotView[element]; !ok {
				t.Fatalf("op %d 后视图缺少 %q（重算计数 %d）", i, element, count)
			}
			if got := m.RefCount(element); got != count {
				t.Fatalf("op %d 后 %q 计数=%d 重算=%d", i, element, got, count)
			}
		}
		for element := range gotView {
			if want[element] == 0 {
				t.Fatalf("op %d 后视图多出 %q（重算计数 0）", i, element)
			}
		}
		if err := m.SelfCheck(); err != nil {
			t.Fatalf("op %d 后自检失败: %v", i, err)
		}
	}
	t.Log("输入=14 条交错增删 | 结果=每步视图/计数均与独立重算相同 | 判定依据=逐步批量重算等价")
	judge(t, "最终 SelfCheck", m.SelfCheck(), nil, "增量状态与分区重算一致")
}

// TestConcurrentReadOnlyConsistency 验证并发只读同一实例视图逐字段相同，
// 同时在 -race 下验证读写并发安全。
func TestConcurrentReadOnlyConsistency(t *testing.T) {
	m, _ := New(4)
	for _, op := range []Op{{0, "a"}, {1, "a"}, {2, "b"}, {3, "c"}, {0, "d"}, {1, "e"}} {
		if err := m.Add(op.Partition, op.Element); err != nil {
			t.Fatalf("准备数据失败: %v", err)
		}
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 一个并发写者持续制造跨分区增删与幂等操作。
	wg.Add(1)
	go func() {
		defer wg.Done()
		elements := []string{"a", "b", "c", "d", "e", "f"}
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			part := i % 4
			element := elements[i%len(elements)]
			if i%2 == 0 {
				_ = m.Add(part, element)
			} else {
				_ = m.Remove(part, element)
			}
		}
	}()

	// 多个并发读者同时取视图、日志与自检；每个快照必须是某个已提交的一致状态。
	var readerWg sync.WaitGroup
	for reader := 0; reader < 4; reader++ {
		readerWg.Add(1)
		go func() {
			defer readerWg.Done()
			for j := 0; j < 200; j++ {
				snapshot := m.View()
				for element := range snapshot {
					if element == "" {
						t.Errorf("视图中出现空元素")
					}
				}
				_ = m.Log()
				_ = m.Has("a")
				_ = m.RefCount("b")
				if err := m.SelfCheck(); err != nil {
					t.Errorf("并发期间自检失败: %v", err)
				}
			}
		}()
	}
	readerWg.Wait()

	// 读者结束后停止写者，之后不存在并发写。
	close(stop)
	wg.Wait()

	// 并发只读同一实例：多个读者同时取最终视图，必须逐字段相同。
	finalViews := make([]map[string]struct{}, 8)
	var readOnlyWg sync.WaitGroup
	for i := range finalViews {
		readOnlyWg.Add(1)
		go func(idx int) {
			defer readOnlyWg.Done()
			finalViews[idx] = m.View()
		}(i)
	}
	readOnlyWg.Wait()

	baseline := viewKeys(finalViews[0])
	for i := 1; i < len(finalViews); i++ {
		if !reflect.DeepEqual(viewKeys(finalViews[i]), baseline) {
			t.Fatalf("并发只读视图不一致: %v vs %v", viewKeys(finalViews[i]), baseline)
		}
	}
	judge(t, "8 个并发只读视图", baseline, baseline, "无并发写时只读视图逐字段相同")

	log1 := kinds(m.Log())
	log2 := kinds(m.Log())
	judge(t, "并发只读日志", log2, log1, "日志快照同样逐字段相同")
	judge(t, "最终 SelfCheck", m.SelfCheck(), nil, "并发增删后状态与分区重算一致")
	t.Logf("输入=1 写者 + 4 读者(各200轮) + 8 只读快照 | 结果=自检全部通过，最终视图=%v | 判定依据=读写互斥、快照均为已提交状态",
		baseline)
}
