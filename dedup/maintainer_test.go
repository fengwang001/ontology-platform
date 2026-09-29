package dedup

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func newMaintainer(t *testing.T, n int) *Maintainer {
	t.Helper()
	m, err := NewMaintainer(n)
	if err != nil {
		t.Fatalf("NewMaintainer(%d) 失败: %v", n, err)
	}
	return m
}

func mustAdd(t *testing.T, m *Maintainer, p int, e string) {
	t.Helper()
	if err := m.Add(p, e); err != nil {
		t.Fatalf("Add(%d, %q) 失败: %v", p, e, err)
	}
}

func mustRemove(t *testing.T, m *Maintainer, p int, e string) {
	t.Helper()
	if err := m.Remove(p, e); err != nil {
		t.Fatalf("Remove(%d, %q) 失败: %v", p, e, err)
	}
}

func assertView(t *testing.T, m *Maintainer, want []string) {
	t.Helper()
	got := m.View()
	if got == nil {
		got = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("视图不一致: got=%v want=%v", got, want)
	}
	if err := m.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	t.Logf("输入=断言视图 结果=%v 判定依据=引用计数>=1 的元素集合(排序) 且自检与批量重算一致", got)
}

func assertLog(t *testing.T, m *Maintainer, want []Entry) {
	t.Helper()
	got := m.Log()
	if got == nil {
		got = []Entry{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("日志不一致: got=%v want=%v", got, want)
	}
	t.Logf("输入=断言日志 结果=%v 判定依据=仅引用计数 0->1 / 1->0 时按操作顺序追加", got)
}

// 跨分区持有：一个分区撤回后元素仍留在视图，全部撤回才撤下。
func TestCrossPartitionHold(t *testing.T) {
	m := newMaintainer(t, 3)

	mustAdd(t, m, 0, "x")
	mustAdd(t, m, 1, "x")
	t.Logf("输入=Add(0,x),Add(1,x) 结果=refCount(x)=%d 判定依据=两个分区持有,计数为2", m.RefCount("x"))

	mustRemove(t, m, 0, "x")
	t.Logf("输入=Remove(0,x) 结果=refCount(x)=%d 判定依据=分区1仍持有,不输出减条目", m.RefCount("x"))
	assertView(t, m, []string{"x"})
	assertLog(t, m, []Entry{{Seq: 0, Op: OpAdd, Partition: 0, Element: "x"}})

	mustRemove(t, m, 1, "x")
	t.Logf("输入=Remove(1,x) 结果=refCount(x)=%d 判定依据=无分区持有,输出减条目并撤下", m.RefCount("x"))
	assertView(t, m, []string{})
	assertLog(t, m, []Entry{
		{Seq: 0, Op: OpAdd, Partition: 0, Element: "x"},
		{Seq: 1, Op: OpRemove, Partition: 1, Element: "x"},
	})
}

// 已在视图：第二个分区加入同一元素不重复输出加条目；同分区重复加入为无操作。
func TestAlreadyInViewNoDuplicateAdd(t *testing.T) {
	m := newMaintainer(t, 2)

	mustAdd(t, m, 0, "a")
	mustAdd(t, m, 0, "a") // 同分区重复加入：无操作
	mustAdd(t, m, 1, "a") // 已在视图：计数+1，无加条目
	t.Logf("输入=Add(0,a)x2,Add(1,a) 结果=refCount(a)=%d 判定依据=同分区幂等+跨分区计数", m.RefCount("a"))

	assertView(t, m, []string{"a"})
	assertLog(t, m, []Entry{{Seq: 0, Op: OpAdd, Partition: 0, Element: "a"}})
}

// 不在分区：撤回不存在的元素为无操作，不产生减条目。
func TestRemoveAbsentIsNoop(t *testing.T) {
	m := newMaintainer(t, 2)

	mustAdd(t, m, 0, "a")
	mustRemove(t, m, 1, "a")     // 分区1从未持有 a
	mustRemove(t, m, 0, "ghost") // 元素从未出现
	t.Logf("输入=Remove(1,a),Remove(0,ghost) 结果=无日志追加 判定依据=幂等撤回不存在的元素为无操作")

	assertView(t, m, []string{"a"})
	assertLog(t, m, []Entry{{Seq: 0, Op: OpAdd, Partition: 0, Element: "a"}})
}

// 三类非法输入：互不相同、可判定，且被拒后状态不变。
func TestInvalidInputsRejected(t *testing.T) {
	if errors.Is(ErrPartitionOutOfRange, ErrEmptyElement) ||
		errors.Is(ErrPartitionOutOfRange, ErrInvalidPartitionCount) ||
		errors.Is(ErrEmptyElement, ErrInvalidPartitionCount) {
		t.Fatal("三类错误必须互不相同")
	}

	if _, err := NewMaintainer(0); !errors.Is(err, ErrInvalidPartitionCount) {
		t.Fatalf("分区数非正: got=%v want=ErrInvalidPartitionCount", err)
	}
	t.Logf("输入=NewMaintainer(0) 结果=%v 判定依据=errors.Is(err, ErrInvalidPartitionCount)", ErrInvalidPartitionCount)

	m := newMaintainer(t, 2)
	mustAdd(t, m, 0, "keep")
	viewBefore := m.View()
	logBefore := m.Log()

	cases := []struct {
		name string
		err  error
		op   func() error
	}{
		{"分区越界(负)", ErrPartitionOutOfRange, func() error { return m.Add(-1, "e") }},
		{"分区越界(超界)", ErrPartitionOutOfRange, func() error { return m.Add(2, "e") }},
		{"元素为空", ErrEmptyElement, func() error { return m.Add(0, "") }},
		{"撤回分区越界", ErrPartitionOutOfRange, func() error { return m.Remove(9, "e") }},
		{"撤回元素为空", ErrEmptyElement, func() error { return m.Remove(0, "") }},
	}
	for _, c := range cases {
		err := c.op()
		if !errors.Is(err, c.err) {
			t.Fatalf("%s: got=%v want=%v", c.name, err, c.err)
		}
		t.Logf("输入=%s 结果=%v 判定依据=errors.Is 可判定为 %v", c.name, err, c.err)
	}

	// 批量原子性：批内含非法条目则整批不生效。
	err := m.AddBatch([]Op{{Partition: 0, Element: "new"}, {Partition: 5, Element: "bad"}})
	if !errors.Is(err, ErrPartitionOutOfRange) {
		t.Fatalf("批量含越界条目: got=%v want=ErrPartitionOutOfRange", err)
	}
	t.Logf("输入=AddBatch([合法,越界]) 结果=%v 判定依据=整批拒绝,合法条目也不生效", err)

	err = m.RemoveBatch([]Op{{Partition: 0, Element: "keep"}, {Partition: 0, Element: ""}})
	if !errors.Is(err, ErrEmptyElement) {
		t.Fatalf("批量含空元素: got=%v want=ErrEmptyElement", err)
	}
	t.Logf("输入=RemoveBatch([合法,空元素]) 结果=%v 判定依据=整批拒绝,keep 未被撤回", err)

	if got := m.View(); !reflect.DeepEqual(got, viewBefore) {
		t.Fatalf("拒绝后视图被改变: got=%v want=%v", got, viewBefore)
	}
	if got := m.Log(); !reflect.DeepEqual(got, logBefore) {
		t.Fatalf("拒绝后日志被改变: got=%v want=%v", got, logBefore)
	}
	if m.RefCount("new") != 0 || m.RefCount("keep") != 1 {
		t.Fatalf("拒绝后引用计数被改变: new=%d keep=%d", m.RefCount("new"), m.RefCount("keep"))
	}
	if err := m.SelfCheck(); err != nil {
		t.Fatalf("拒绝后自检失败: %v", err)
	}
	t.Logf("输入=拒绝后状态比对 结果=视图/日志/引用计数均与拒绝前一致 判定依据=失败不改变分区集合、引用计数、日志与视图")
}

// 并发：并发加入/撤回与并发只读；并发只读同一实例得到的视图逐字段相同。
func TestConcurrentReadConsistency(t *testing.T) {
	m := newMaintainer(t, 8)
	const writers = 8
	const readers = 8
	const rounds = 200

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				e := fmt.Sprintf("elem-%d", (id+r)%16)
				if err := m.Add(id, e); err != nil {
					t.Errorf("Add 失败: %v", err)
					return
				}
				if err := m.Remove(id, e); err != nil {
					t.Errorf("Remove 失败: %v", err)
					return
				}
			}
		}(w)
	}

	// 写入进行中：每次读取必须是完整快照（自检通过），不做跨次比较。
	errCh := make(chan string, readers*rounds)
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				_ = m.View()
				_ = m.Log()
				if err := m.SelfCheck(); err != nil {
					errCh <- err.Error()
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Fatal(msg)
	}
	t.Logf("输入=%d写x%d读x%d轮 结果=无竞态、自检均通过 判定依据=race 检测通过且每次读取为一致快照", writers, readers, rounds)

	// 写入停止后：并发只读同一实例得到的视图必须逐字段相同。
	goldenView := m.View()
	goldenLog := m.Log()
	var rwg sync.WaitGroup
	mismatch := make(chan string, readers)
	for r := 0; r < readers; r++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for i := 0; i < rounds; i++ {
				if v := m.View(); !reflect.DeepEqual(v, goldenView) {
					mismatch <- fmt.Sprintf("并发只读视图不一致: got=%v want=%v", v, goldenView)
					return
				}
				if l := m.Log(); !reflect.DeepEqual(l, goldenLog) {
					mismatch <- "并发只读日志不一致"
					return
				}
			}
		}()
	}
	rwg.Wait()
	close(mismatch)
	for msg := range mismatch {
		t.Fatal(msg)
	}
	t.Logf("输入=%d个并发只读x%d轮 结果=全部与基准逐字段相同 判定依据=DeepEqual(视图/日志, 基准快照)", readers, rounds)

	// 写线程各自加完即撤，最终视图应为空，且日志加减条目成对。
	assertView(t, m, []string{})
	log := m.Log()
	balance := 0
	for i, e := range log {
		if e.Seq != i {
			t.Fatalf("日志序号不连续: entry=%+v 位于 %d", e, i)
		}
		if e.Op == OpAdd {
			balance++
		} else {
			balance--
		}
		if balance < 0 {
			t.Fatalf("减条目先于加条目: entry=%+v", e)
		}
	}
	if balance != 0 {
		t.Fatalf("加减条目不成对: balance=%d", balance)
	}
	t.Logf("输入=终态校验 结果=视图空且 %d 条日志加减成对 判定依据=同一元素加必先于减且最终平衡", len(log))
}

// 与批量重算一致：按日志重放得到的视图与实时视图一致。
func TestLogReplayMatchesView(t *testing.T) {
	m := newMaintainer(t, 3)
	mustAdd(t, m, 0, "a")
	mustAdd(t, m, 1, "a")
	mustAdd(t, m, 2, "b")
	mustRemove(t, m, 0, "a")
	mustRemove(t, m, 1, "a")
	mustAdd(t, m, 0, "c")

	replayed := map[string]struct{}{}
	for _, e := range m.Log() {
		switch e.Op {
		case OpAdd:
			replayed[e.Element] = struct{}{}
		case OpRemove:
			delete(replayed, e.Element)
		}
	}
	got := map[string]struct{}{}
	for _, v := range m.View() {
		got[v] = struct{}{}
	}
	if !reflect.DeepEqual(replayed, got) {
		t.Fatalf("日志重放与视图不一致: replayed=%v view=%v", replayed, got)
	}
	t.Logf("输入=日志重放 结果=%v 判定依据=下游按序应用日志得到的集合与视图一致", got)
}
