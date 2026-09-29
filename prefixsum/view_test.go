package prefixsum

import (
	"fmt"
	"math"
	"testing"
)

// logStep 打印每步输入、全量前缀和视图与判定依据。
func logStep(t *testing.T, step int, op string, affected int, entries []Entry, basis string) {
	t.Helper()
	t.Logf("步骤%02d %-20s 受影响键数=%-2d 判定依据=%s", step, op, affected, basis)
	for _, e := range entries {
		t.Logf("        key=%4d value=%4d prefix=%4d", e.Key, e.Value, e.PrefixSum)
	}
}

// assertAgainstNaive 校验视图快照、单点查询、长度与自检全部和朴素重算一致。
func assertAgainstNaive(t *testing.T, v *View, m *naiveModel, label string) {
	t.Helper()
	want, ok := m.recompute()
	if !ok {
		t.Fatalf("%s: 朴素重算溢出，该状态不应出现", label)
	}
	got := v.Snapshot()
	if len(got) != len(want) {
		t.Fatalf("%s: 长度不一致 got=%d want=%d", label, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: 第%d项不一致 got=%+v want=%+v", label, i, got[i], want[i])
		}
		ps, err := v.PrefixSum(want[i].Key)
		if err != nil || ps != want[i].PrefixSum {
			t.Fatalf("%s: 单点查询 key=%d got=(%d,%v) want=%d", label, want[i].Key, ps, err, want[i].PrefixSum)
		}
	}
	if v.Len() != len(want) {
		t.Fatalf("%s: Len=%d want=%d", label, v.Len(), len(want))
	}
	if err := v.SelfCheck(); err != nil {
		t.Fatalf("%s: 自检失败: %v", label, err)
	}
}

func TestInsertUpdateDeleteZero(t *testing.T) {
	v, err := New(Config{MinKey: -100, MaxKey: 100, MaxKeys: 50})
	if err != nil {
		t.Fatal(err)
	}
	m := newNaive(-100, 100)

	ops := []struct {
		name  string
		fn    func() (int, error)
		naive func() int
	}{
		{"put(10,1)", func() (int, error) { return v.Put(10, 1) }, func() int { return m.put(10, 1) }},
		{"put(20,0) 零值", func() (int, error) { return v.Put(20, 0) }, func() int { return m.put(20, 0) }},
		{"put(5,3)", func() (int, error) { return v.Put(5, 3) }, func() int { return m.put(5, 3) }},
		{"put(15,-2) 负值", func() (int, error) { return v.Put(15, -2) }, func() int { return m.put(15, -2) }},
		{"put(10,4) 改值", func() (int, error) { return v.Put(10, 4) }, func() int { return m.put(10, 4) }},
		{"put(20,0) 同值", func() (int, error) { return v.Put(20, 0) }, func() int { return m.put(20, 0) }},
		{"put(0,0) 零值新键", func() (int, error) { return v.Put(0, 0) }, func() int { return m.put(0, 0) }},
		{"delete(15)", func() (int, error) { return v.Delete(15) }, func() int { return m.del(15) }},
		{"put(10,1) 改回", func() (int, error) { return v.Put(10, 1) }, func() int { return m.put(10, 1) }},
		{"delete(0) 零值键", func() (int, error) { return v.Delete(0) }, func() int { return m.del(0) }},
		{"delete(10)", func() (int, error) { return v.Delete(10) }, func() int { return m.del(10) }},
		{"delete(20)", func() (int, error) { return v.Delete(20) }, func() int { return m.del(20) }},
		{"delete(5)", func() (int, error) { return v.Delete(5) }, func() int { return m.del(5) }},
	}

	for i, op := range ops {
		affected, err := op.fn()
		if err != nil {
			t.Fatalf("步骤%d %s 意外失败: %v", i+1, op.name, err)
		}
		wantAffected := op.naive()
		basis := fmt.Sprintf("朴素模型独立计数=%d；快照逐键比对+单点查询+SelfCheck", wantAffected)
		if affected != wantAffected {
			t.Fatalf("步骤%d %s: 受影响键数 got=%d want=%d", i+1, op.name, affected, wantAffected)
		}
		logStep(t, i+1, op.name, affected, v.Snapshot(), basis)
		assertAgainstNaive(t, v, m, op.name)
	}
	if v.Len() != 0 {
		t.Fatalf("全部删除后应为空, Len=%d", v.Len())
	}
}

func TestErrorsDistinctAndNoTrace(t *testing.T) {
	v, err := New(Config{MinKey: 0, MaxKey: 10, MaxKeys: 2})
	if err != nil {
		t.Fatal(err)
	}
	m := newNaive(0, 10)
	v.Put(3, 5)
	m.put(3, 5)
	v.Put(7, -1)
	m.put(7, -1)

	reject := func(name string, fn func() error, want Reason) {
		t.Helper()
		before := v.Snapshot()
		rerr := fn()
		if rerr == nil {
			t.Fatalf("%s: 期望被拒绝却成功", name)
		}
		if got := ErrorReason(rerr); got != want {
			t.Fatalf("%s: 错误类别 got=%s want=%s (%v)", name, got, want, rerr)
		}
		after := v.Snapshot()
		if len(after) != len(before) {
			t.Fatalf("%s: 拒绝后长度变化", name)
		}
		for i := range before {
			if before[i] != after[i] {
				t.Fatalf("%s: 拒绝后状态变化 %v -> %v", name, before, after)
			}
		}
		t.Logf("拒绝用例 %-24s 类别=%-16s 判定依据=ErrorReason 唯一可区分且快照拒绝前后逐项相同", name, want)
		assertAgainstNaive(t, v, m, name)
	}

	reject("put(-1,1)", func() error { _, e := v.Put(-1, 1); return e }, ReasonKeyOutOfRange)
	reject("put(11,1)", func() error { _, e := v.Put(11, 1); return e }, ReasonKeyOutOfRange)
	reject("delete(-1)", func() error { _, e := v.Delete(-1); return e }, ReasonKeyOutOfRange)
	reject("prefix(-1)", func() error { _, e := v.PrefixSum(-1); return e }, ReasonKeyOutOfRange)
	reject("delete(4)", func() error { _, e := v.Delete(4); return e }, ReasonKeyNotFound)
	reject("prefix(4)", func() error { _, e := v.PrefixSum(4); return e }, ReasonKeyNotFound)
	reject("put(1,1) 超限", func() error { _, e := v.Put(1, 1); return e }, ReasonTooManyKeys)

	// 已有键改值不新增键，即使满容量也允许。
	n, e := v.Put(3, 9)
	if e != nil {
		t.Fatalf("已满时改已有键应允许: %v", e)
	}
	if n != 2 {
		t.Fatalf("改值受影响键数应为键3,7共2, got=%d", n)
	}
	m.put(3, 9)
	t.Logf("步骤 put(3,9) 改已有键(满容量) 受影响键数=%d 判定依据=不新增键不受 MaxKeys 限制", n)
	assertAgainstNaive(t, v, m, "put-existing-at-cap")

	if _, e := New(Config{MinKey: 5, MaxKey: 4, MaxKeys: 1}); ErrorReason(e) != ReasonInvalidArgument {
		t.Fatalf("坏区间配置应返回 invalid_argument, got=%v", e)
	}
	if _, e := New(Config{MinKey: 0, MaxKey: 4, MaxKeys: 0}); ErrorReason(e) != ReasonInvalidArgument {
		t.Fatalf("MaxKeys=0 应返回 invalid_argument, got=%v", e)
	}
	var nilV *View
	if _, e := nilV.Put(1, 1); ErrorReason(e) != ReasonInvalidArgument {
		t.Fatalf("nil Put 应返回 invalid_argument, got=%v", e)
	}
	if _, e := nilV.Delete(1); ErrorReason(e) != ReasonInvalidArgument {
		t.Fatalf("nil Delete 应返回 invalid_argument, got=%v", e)
	}
	if _, e := nilV.PrefixSum(1); ErrorReason(e) != ReasonInvalidArgument {
		t.Fatalf("nil PrefixSum 应返回 invalid_argument, got=%v", e)
	}
	t.Log("拒绝用例 nil-view/bad-config       类别=invalid_argument 判定依据=ErrorReason 唯一切换")
}

func TestOverflowRejectedAtomically(t *testing.T) {
	v, _ := New(Config{MinKey: 0, MaxKey: 100, MaxKeys: 100})
	m := newNaive(0, 100)

	if _, err := v.Put(1, math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	m.put(1, math.MaxInt64)

	// 键 2 的前缀和 MaxInt64+1 溢出：整次写入必须被拒绝、状态不变。
	_, err := v.Put(2, 1)
	if ErrorReason(err) != ReasonOverflow {
		t.Fatalf("期望 overflow, got=%v", err)
	}
	t.Logf("步骤 put(2,1) 溢出拒绝 类别=%s 判定依据=子树前缀极值越界；快照与拒绝前一致", ErrorReason(err))
	assertAgainstNaive(t, v, m, "after-overflow")

	// 总共和变小但中间前缀仍溢出的情形：[MaxInt64, 2, -1] 在键2处溢出。
	if _, err := v.Put(10, -1); err != nil {
		t.Fatal(err)
	}
	m.put(10, -1)
	_, err = v.Put(5, 2)
	if ErrorReason(err) != ReasonOverflow {
		t.Fatalf("期望中间前缀溢出 overflow, got=%v", err)
	}
	t.Log("步骤 put(5,2) 中间前缀溢出 类别=overflow 判定依据=即使总共和合法，子树内极值仍越界")
	assertAgainstNaive(t, v, m, "after-mid-overflow")

	// 改值解除溢出后写入恢复正常。
	n, err := v.Put(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	m.put(1, 10)
	logStep(t, 1, "put(1,10) 溢出解除后", n, v.Snapshot(), "朴素模型计数与逐键重算一致")
	assertAgainstNaive(t, v, m, "recover")
}

func TestModifyBackRestoresView(t *testing.T) {
	v, _ := New(Config{MinKey: 0, MaxKey: 100, MaxKeys: 100})
	m := newNaive(0, 100)
	for _, s := range []struct{ k, val int64 }{{10, 1}, {20, 2}, {30, 3}} {
		v.Put(s.k, s.val)
		m.put(s.k, s.val)
	}
	before := v.Snapshot()

	v.Put(20, 99)
	if n, _ := v.Put(20, 2); n != 2 {
		t.Fatalf("改回原值应影响键20,30 共2个, got=%d", n)
	}
	v.Put(25, 7)
	if n, _ := v.Delete(25); n != 1 {
		t.Fatalf("删除25应仅影响键30, got=%d", n)
	}

	after := v.Snapshot()
	if len(after) != len(before) {
		t.Fatalf("恢复后长度不同")
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("逐键未恢复: %v vs %v", before, after)
		}
	}
	assertAgainstNaive(t, v, m, "restore")
	t.Log("步骤 改值再改回/插入后删除 判定依据=快照与初始快照逐项相等且与朴素重算一致")
}
