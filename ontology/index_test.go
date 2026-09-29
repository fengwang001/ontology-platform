package ontology

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
)

// dumpIndex 返回各索引组的稳定文本：值升序、组内主键升序。
func dumpIndex(ix *Index) string {
	_, index := ix.Snapshot()
	values := make([]int, 0, len(index))
	for v := range index {
		values = append(values, v)
	}
	sort.Ints(values)
	var b strings.Builder
	b.WriteString("{")
	for i, v := range values {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%d=%v", v, index[v])
	}
	b.WriteString("}")
	return b.String()
}

func expect(t *testing.T, name string, got, want []string) {
	t.Helper()
	ok := len(got) == len(want)
	if ok {
		for i := range got {
			if got[i] != want[i] {
				ok = false
				break
			}
		}
	}
	t.Logf("判定[%s]: got=%v want=%v -> %v（等值/范围结果必须与按值、主键双升序重排的期望逐元素相同）",
		name, got, want, ok)
	if !ok {
		t.Fatalf("%s: got %v, want %v", name, got, want)
	}
}

// 场景 1：索引键更新——先删旧值组、再插新值组；相等值不产生重复项。
func TestIndexKeyUpdateAndNoop(t *testing.T) {
	ix := New()

	steps := []Op{
		{Kind: OpUpsert, Key: "k1", Val: 10},
		{Kind: OpUpsert, Key: "k2", Val: 10},
		{Kind: OpUpsert, Key: "k1", Val: 20}, // k1: 10 -> 20
		{Kind: OpUpsert, Key: "k1", Val: 20}, // 相等值：无操作、无重复
	}
	for _, op := range steps {
		if err := ix.Apply([]Op{op}); err != nil {
			t.Fatalf("Apply %+v: %v", op, err)
		}
		t.Logf("操作: %+v | 各索引组: %s", op, dumpIndex(ix))
	}

	expect(t, "Equal(10) 旧值组只留 k2", ix.Equal(10), []string{"k2"})
	expect(t, "Equal(20) 新值组只有一个 k1", ix.Equal(20), []string{"k1"})
	t.Logf("判定[相等值无重复]: 组长度=%d 且无重复主键 -> %v",
		len(ix.Equal(20)), len(ix.Equal(20)) == 1)
}

// 场景 2：范围查询左闭右开，且按字段值、主键双升序。
func TestRangeHalfOpen(t *testing.T) {
	ix := New()
	seed := []Op{
		{Kind: OpUpsert, Key: "a", Val: 0},
		{Kind: OpUpsert, Key: "b", Val: 5},
		{Kind: OpUpsert, Key: "c", Val: 5},
		{Kind: OpUpsert, Key: "d", Val: 10},
	}
	if err := ix.Apply(seed); err != nil {
		t.Fatal(err)
	}
	t.Logf("操作(批量种子): %+v | 各索引组: %s", seed, dumpIndex(ix))

	got := ix.Range(5, 10)
	t.Logf("操作: Range(lo=5, hi=10) | 查询结果: %v（判定依据：左闭含值5，右开不含值10）", got)
	expect(t, "Range[5,10)", got, []string{"b", "c"})

	expect(t, "空区间 lo>=hi 返回空", ix.Range(10, 10), []string{})

	// 删除后索引组与查询同步收缩。
	if err := ix.Remove("c"); err != nil {
		t.Fatal(err)
	}
	t.Logf("操作: Remove(c) | 各索引组: %s", dumpIndex(ix))
	expect(t, "Range[5,10) 删除后", ix.Range(0, 20), []string{"a", "b", "d"})
}

// 场景 3：非法批次整体拒绝，且不同原因可区分。
func TestBatchRejectedAtomically(t *testing.T) {
	cases := []struct {
		name string
		ops  []Op
		want error
	}{
		{"空主键", []Op{{Kind: OpUpsert, Key: "", Val: 1}}, ErrEmptyKey},
		{"删除不存在", []Op{{Kind: OpDelete, Key: "ghost"}}, ErrDeleteMissing},
		{"未知操作", []Op{{Kind: OpKind(99), Key: "x"}}, ErrUnknownOp},
		{"含空主键的混合批", []Op{
			{Kind: OpUpsert, Key: "ok", Val: 1},
			{Kind: OpUpsert, Key: "", Val: 2},
		}, ErrEmptyKey},
	}
	for _, tc := range cases {
		ix := New()
		err := ix.Apply(tc.ops)
		before := dumpIndex(ix)
		t.Logf("操作[%s]: %+v | 拒绝原因: %v | 各索引组(应保持为空): %s",
			tc.name, tc.ops, err, before)
		if err != tc.want {
			t.Fatalf("%s: err=%v want %v", tc.name, err, tc.want)
		}
		if before != "{}" {
			t.Fatalf("%s: 被拒批次产生了副作用: %s", tc.name, before)
		}
	}

	// 合法批次与非法批次：非法批不影响此前已提交状态。
	ix := New()
	if err := ix.Apply([]Op{{Kind: OpUpsert, Key: "k", Val: 7}}); err != nil {
		t.Fatal(err)
	}
	err := ix.Apply([]Op{
		{Kind: OpUpsert, Key: "k", Val: 8},
		{Kind: OpDelete, Key: "missing"},
	})
	t.Logf("操作: [k->8, delete missing] | 拒绝原因: %v | 各索引组: %s", err, dumpIndex(ix))
	if err != ErrDeleteMissing {
		t.Fatalf("want ErrDeleteMissing, got %v", err)
	}
	expect(t, "整批回滚，k 仍在旧值组", ix.Equal(7), []string{"k"})
	expect(t, "新值组不存在", ix.Equal(8), []string{})
}

// 场景 4：查询与写入并发；任一已完成更新后主键只出现在新值组，
// 且并发读到的结果彼此逐元素相同。
func TestConcurrentReaders(t *testing.T) {
	ix := New()
	_ = ix.Apply([]Op{
		{Kind: OpUpsert, Key: "k1", Val: 0},
		{Kind: OpUpsert, Key: "k2", Val: 0},
	})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for v := 1; ; v++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = ix.Put("k1", v%2) // 在值组 0/1 之间迁移
		}
	}()

	// 多个读者：每次快照都必须满足 k1 只属于一个值组。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				got := ix.Range(0, 2)
				// 不变量：每次结果必须是某个已完成更新的一致状态。
				// k1 在值组0时 -> [k1 k2]；迁到值组1时 -> [k2 k1]，
				// 两种状态都按（字段值升序、主键升序）排列，且 k1 只出现一次。
				count := 0
				for _, k := range got {
					if k == "k1" {
						count++
					}
				}
				if count != 1 ||
					(!equalSlice(got, []string{"k1", "k2"}) &&
						!equalSlice(got, []string{"k2", "k1"})) {
					t.Errorf("reader %d 观察到不一致快照: %v count=%d", id, got, count)
					return
				}
			}
		}(r)
	}

	// 写线程停止后状态冻结：并发读者必须逐元素相同（可复现）。
	close(stop)
	wg.Wait()

	results := make([][]string, 8)
	var rwg sync.WaitGroup
	for i := range results {
		rwg.Add(1)
		go func(i int) {
			defer rwg.Done()
			results[i] = ix.Equal(0)
		}(i)
	}
	rwg.Wait()
	for i := 1; i < len(results); i++ {
		if !equalSlice(results[0], results[i]) {
			t.Fatalf("并发读者结果不一致: %v vs %v", results[0], results[i])
		}
	}
	t.Logf("操作: 8 个并发 Equal(0) 读者 | 查询结果: %v | 判定依据: 全部逐元素相同", results[0])

	// 最终再核对一次：k1 只出现在其现值的索引组。
	records, index := ix.Snapshot()
	cur := records["k1"]
	if inOld := hasString(index[1-cur], "k1"); inOld {
		t.Fatalf("k1 仍残留在旧值组 %d", 1-cur)
	}
	t.Logf("最终各索引组: %s | 判定依据: k1 现值=%d 且不在旧值组 %d",
		dumpIndex(ix), cur, 1-cur)
}

// TestRescanCrossCheck 用整体重扫记录表的方式核对索引结果。
func TestRescanCrossCheck(t *testing.T) {
	ix := New()
	ops := []Op{
		{Kind: OpUpsert, Key: "p", Val: 3},
		{Kind: OpUpsert, Key: "q", Val: 1},
		{Kind: OpUpsert, Key: "r", Val: 3},
		{Kind: OpUpsert, Key: "p", Val: 1}, // 更新
		{Kind: OpDelete, Key: "q"},
	}
	for _, op := range ops {
		if err := ix.Apply([]Op{op}); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("操作序列完成 | 各索引组(增量维护): %s", dumpIndex(ix))

	// 整体重扫：不看增量索引，直接从记录表重算期望。
	records, index := ix.Snapshot()
	rescan := map[int][]string{}
	for key, val := range records {
		rescan[val] = append(rescan[val], key)
	}
	for val := range rescan {
		sort.Strings(rescan[val])
	}
	values := make([]int, 0, len(rescan))
	for v := range rescan {
		values = append(values, v)
	}
	sort.Ints(values)
	var want []string
	for _, v := range values {
		want = append(want, rescan[v]...)
	}
	got := ix.Range(minVal(values)-1, maxVal(values)+1)
	t.Logf("Range 全量查询结果: %v | 整体重扫期望: %v", got, want)

	if len(index) != len(rescan) {
		t.Fatalf("索引值桶数量 %d 与重扫 %d 不一致", len(index), len(rescan))
	}
	for val, keys := range rescan {
		if !equalSlice(index[val], keys) {
			t.Fatalf("值组 %d 增量=%v 重扫=%v", val, index[val], keys)
		}
	}
	expect(t, "增量索引 == 整体重扫", got, want)
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func minVal(vs []int) int {
	m := vs[0]
	for _, v := range vs[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func maxVal(vs []int) int {
	m := vs[0]
	for _, v := range vs[1:] {
		if v > m {
			m = v
		}
	}
	return m
}
