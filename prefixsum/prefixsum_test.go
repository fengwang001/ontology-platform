package prefixsum

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

// naiveModel 是朴素重算参照模型：每次操作后全量重算前缀和。
type naiveModel struct {
	vals map[int64]int64
}

func newNaive() *naiveModel { return &naiveModel{vals: map[int64]int64{}} }

func (m *naiveModel) sortedKeys() []int64 {
	keys := make([]int64, 0, len(m.vals))
	for k := range m.vals {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func (m *naiveModel) prefixSums() map[int64]int64 {
	out := make(map[int64]int64, len(m.vals))
	var run int64
	for _, k := range m.sortedKeys() {
		run += m.vals[k]
		out[k] = run
	}
	return out
}

func (m *naiveModel) put(key, value int64) (affected int, basis string) {
	old, ok := m.vals[key]
	if !ok {
		affected = 1
		basis = "新插入键一律计一"
		if value != 0 {
			n := 0
			for k := range m.vals {
				if k > key {
					n++
				}
			}
			affected += n
			basis += fmt.Sprintf("；值 %d 非零，上方 %d 个存在键前缀和变化", value, n)
		} else {
			basis += "；值为零，其他键前缀和不变"
		}
		m.vals[key] = value
		return affected, basis
	}
	if old == value {
		return 0, "改值前后相等，无任何前缀和变化"
	}
	n := 0
	for k := range m.vals {
		if k >= key {
			n++
		}
	}
	m.vals[key] = value
	return n, fmt.Sprintf("改值增量 %d，键不小于 %d 的 %d 个存在键前缀和变化", value-old, key, n)
}

func (m *naiveModel) del(key int64) (affected int, basis string) {
	old := m.vals[key]
	delete(m.vals, key)
	if old == 0 {
		return 0, "被删键值为零且被删键不计，无受影响键"
	}
	n := 0
	for k := range m.vals {
		if k > key {
			n++
		}
	}
	return n, fmt.Sprintf("被删键不计；其值 %d 非零，上方 %d 个存在键前缀和变化", old, n)
}

func formatPS(m *naiveModel) string {
	ps := m.prefixSums()
	var sb strings.Builder
	sb.WriteString("{")
	for i, k := range m.sortedKeys() {
		if i > 0 {
			sb.WriteString(" ")
		}
		fmt.Fprintf(&sb, "%d:%d", k, ps[k])
	}
	sb.WriteString("}")
	return sb.String()
}

// verifyView 校验增量视图与朴素重算逐键一致，并运行自检。
func verifyView(t *testing.T, v *View, m *naiveModel, step string) {
	t.Helper()
	entries := v.Entries()
	ps := m.prefixSums()
	if len(entries) != len(ps) {
		t.Fatalf("%s: 视图键数 %d != 朴素模型 %d", step, len(entries), len(ps))
	}
	for i, e := range entries {
		if i > 0 && entries[i-1].Key >= e.Key {
			t.Fatalf("%s: 视图键序错误: %d 后接 %d", step, entries[i-1].Key, e.Key)
		}
		want, ok := ps[e.Key]
		if !ok {
			t.Fatalf("%s: 视图多出键 %d", step, e.Key)
		}
		if e.Value != m.vals[e.Key] {
			t.Fatalf("%s: 键 %d 值 %d != 朴素模型 %d", step, e.Key, e.Value, m.vals[e.Key])
		}
		if e.PrefixSum != want {
			t.Fatalf("%s: 键 %d 前缀和 %d != 朴素重算 %d", step, e.Key, e.PrefixSum, want)
		}
		got, err := v.PrefixSum(e.Key)
		if err != nil || got != want {
			t.Fatalf("%s: PrefixSum(%d) = %d, %v；期望 %d", step, e.Key, got, err, want)
		}
	}
	if err := v.Check(); err != nil {
		t.Fatalf("%s: 自检失败: %v", step, err)
	}
}

// TestScenarioVsNaive 覆盖插入、改值、删除、零值键、改回与删后恢复，
// 每步打印输入、前缀和与判定依据，并与朴素重算比对。
func TestScenarioVsNaive(t *testing.T) {
	v, err := NewView(-100, 100, 16)
	if err != nil {
		t.Fatalf("NewView: %v", err)
	}
	m := newNaive()
	step := 0
	put := func(key, value int64) {
		step++
		want, basis := m.put(key, value)
		got, err := v.Put(key, value)
		t.Logf("步骤 %d 输入=Put(%d,%d) 受影响键数=%d 前缀和=%s 判定依据=%s",
			step, key, value, got, formatPS(m), basis)
		if err != nil {
			t.Fatalf("步骤 %d Put(%d,%d) 意外失败: %v", step, key, value, err)
		}
		if got != want {
			t.Fatalf("步骤 %d Put(%d,%d) 受影响键数 %d != 朴素重算 %d", step, key, value, got, want)
		}
		verifyView(t, v, m, fmt.Sprintf("步骤 %d", step))
	}
	del := func(key int64) {
		step++
		want, basis := m.del(key)
		got, err := v.Delete(key)
		t.Logf("步骤 %d 输入=Delete(%d) 受影响键数=%d 前缀和=%s 判定依据=%s",
			step, key, got, formatPS(m), basis)
		if err != nil {
			t.Fatalf("步骤 %d Delete(%d) 意外失败: %v", step, key, err)
		}
		if got != want {
			t.Fatalf("步骤 %d Delete(%d) 受影响键数 %d != 朴素重算 %d", step, key, got, want)
		}
		verifyView(t, v, m, fmt.Sprintf("步骤 %d", step))
	}

	put(5, 3)  // 插入
	put(2, 4)  // 插入到已有键下方
	put(9, 0)  // 零值键插入
	put(7, -2) // 负值键插入
	put(5, 10) // 改值
	put(5, 3)  // 改回，视图应逐键恢复
	put(9, 8)  // 零值键改为非零
	put(9, 0)  // 再改回零
	put(2, 4)  // 改值前后相等，受影响为 0
	del(9)     // 删除零值键
	del(5)     // 删除中间键
	put(6, 11) // 插入后再删除
	del(6)     //
	del(7)     // 删除负值键
	del(2)     // 删空
	if got := v.Len(); got != 0 {
		t.Fatalf("全部删除后 Len = %d，期望 0", got)
	}
	t.Logf("步骤 %d 全部删除后视图为空，自检通过", step+1)
}

// TestRejections 覆盖非法参数、键越界、键不存在、键数超限、和值溢出，
// 校验错误类别互不相同且任何拒绝都不改变状态（失败不留痕）。
func TestRejections(t *testing.T) {
	if _, err := NewView(10, 1, 4); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewView(10,1,4) err = %v，期望 ErrInvalidArgument", err)
	}
	if _, err := NewView(0, 10, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewView(0,10,0) err = %v，期望 ErrInvalidArgument", err)
	}
	t.Log("构造: minKey>maxKey 与 maxKeys<=0 均拒绝为 ErrInvalidArgument")

	var nilView *View
	for name, err := range map[string]error{
		"Put":       func() error { _, e := nilView.Put(1, 1); return e }(),
		"Delete":    func() error { _, e := nilView.Delete(1); return e }(),
		"PrefixSum": func() error { _, e := nilView.PrefixSum(1); return e }(),
		"Check":     nilView.Check(),
	} {
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("nil 接收者 %s err = %v，期望 ErrInvalidArgument", name, err)
		}
	}
	t.Log("非法参数: nil 接收者各操作均拒绝为 ErrInvalidArgument")

	v, err := NewView(0, 100, 3)
	if err != nil {
		t.Fatalf("NewView: %v", err)
	}
	m := newNaive()
	mustPut := func(key, value int64) {
		t.Helper()
		if _, err := v.Put(key, value); err != nil {
			t.Fatalf("Put(%d,%d): %v", key, value, err)
		}
		m.vals[key] = value
	}
	reject := func(step string, op func() error, want error) {
		t.Helper()
		before := v.Entries()
		err := op()
		if !errors.Is(err, want) {
			t.Fatalf("%s: err = %v，期望 %v", step, err, want)
		}
		if after := v.Entries(); !reflect.DeepEqual(before, after) {
			t.Fatalf("%s: 拒绝后状态改变: 前=%v 后=%v", step, before, after)
		}
		if cerr := v.Check(); cerr != nil {
			t.Fatalf("%s: 拒绝后自检失败: %v", step, cerr)
		}
		t.Logf("%s: 拒绝原因=%v（与期望类别一致），拒绝后状态不变", step, want)
	}

	mustPut(10, 5)
	mustPut(20, 7)

	reject("键越界 Put(-1,1)", func() error { _, e := v.Put(-1, 1); return e }, ErrKeyOutOfRange)
	reject("键越界 Put(101,1)", func() error { _, e := v.Put(101, 1); return e }, ErrKeyOutOfRange)
	reject("键越界 Delete(101)", func() error { _, e := v.Delete(101); return e }, ErrKeyOutOfRange)
	reject("键越界 PrefixSum(-1)", func() error { _, e := v.PrefixSum(-1); return e }, ErrKeyOutOfRange)
	reject("键不存在 Delete(30)", func() error { _, e := v.Delete(30); return e }, ErrKeyNotFound)
	reject("键不存在 PrefixSum(30)", func() error { _, e := v.PrefixSum(30); return e }, ErrKeyNotFound)

	mustPut(30, 1)
	reject("键数超限 Put(40,1)", func() error { _, e := v.Put(40, 1); return e }, ErrTooManyKeys)
	if got := v.Len(); got != 3 {
		t.Fatalf("键数超限拒绝后 Len = %d，期望 3", got)
	}
	if _, err := v.Put(10, 99); err != nil {
		t.Fatalf("已满视图改值既有键应成功: %v", err)
	}
	m.vals[10] = 99
	t.Log("键数超限只拒绝新键插入，改值既有键不受影响")
	verifyView(t, v, m, "拒绝场景结束")

	if !errors.Is(ErrInvalidArgument, ErrKeyOutOfRange) &&
		!errors.Is(ErrKeyOutOfRange, ErrKeyNotFound) &&
		!errors.Is(ErrKeyNotFound, ErrTooManyKeys) &&
		!errors.Is(ErrTooManyKeys, ErrOverflow) &&
		!errors.Is(ErrOverflow, ErrInvalidArgument) {
		t.Log("错误类别两两可区分: ErrInvalidArgument/ErrKeyOutOfRange/ErrKeyNotFound/ErrTooManyKeys/ErrOverflow")
	} else {
		t.Fatal("错误类别应当互不相同")
	}
}

// TestOverflowRejected 覆盖和值溢出的整体拒绝与状态不变。
func TestOverflowRejected(t *testing.T) {
	v, err := NewView(math.MinInt64, math.MaxInt64, 8)
	if err != nil {
		t.Fatalf("NewView: %v", err)
	}
	put := func(key, value int64) {
		t.Helper()
		if _, err := v.Put(key, value); err != nil {
			t.Fatalf("Put(%d,%d): %v", key, value, err)
		}
		t.Logf("Put(%d,%d) 成功，前缀和=%v", key, value, v.Entries())
	}
	rejectOverflow := func(step string, op func() error) {
		t.Helper()
		before := v.Entries()
		if err := op(); !errors.Is(err, ErrOverflow) {
			t.Fatalf("%s: err = %v，期望 ErrOverflow", step, err)
		}
		if after := v.Entries(); !reflect.DeepEqual(before, after) {
			t.Fatalf("%s: 溢出拒绝后状态改变", step)
		}
		t.Logf("%s: 判定依据=某存在键新前缀和超出 int64，整体拒绝为 ErrOverflow，状态不变", step)
	}

	put(1, math.MaxInt64)
	rejectOverflow("插入使上方键前缀和溢出 Put(2,1)", func() error { _, e := v.Put(2, 1); return e })
	put(2, -5) // 前缀和 MaxInt64-5，合法
	rejectOverflow("改值使自身前缀和溢出 Put(2,5)", func() error { _, e := v.Put(2, 5); return e })

	// 改值使上方键前缀和溢出：p(2)=MaxInt64，键 1 由 5 改为 6 会使 p(2) 变为 MaxInt64+1。
	w, err := NewView(math.MinInt64, math.MaxInt64, 8)
	if err != nil {
		t.Fatalf("NewView: %v", err)
	}
	if _, err := w.Put(1, 5); err != nil {
		t.Fatalf("w.Put(1,5): %v", err)
	}
	if _, err := w.Put(2, math.MaxInt64-5); err != nil {
		t.Fatalf("w.Put(2,MaxInt64-5): %v", err)
	}
	wBefore := w.Entries()
	if _, err := w.Put(1, 6); !errors.Is(err, ErrOverflow) {
		t.Fatalf("改值使上方键前缀和溢出 Put(1,6): err = %v，期望 ErrOverflow", err)
	}
	if after := w.Entries(); !reflect.DeepEqual(wBefore, after) {
		t.Fatal("改值溢出拒绝后状态改变")
	}
	t.Log("改值使上方键前缀和溢出 Put(1,6): 整体拒绝为 ErrOverflow，状态不变")

	// 删除溢出场景：p(3)=MinInt64+4，删除键 1（值 5）会使 p(3) 变为 MinInt64-1，溢出。
	u, err := NewView(math.MinInt64, math.MaxInt64, 8)
	if err != nil {
		t.Fatalf("NewView: %v", err)
	}
	if _, err := u.Put(1, 5); err != nil {
		t.Fatalf("u.Put(1,5): %v", err)
	}
	if _, err := u.Put(2, math.MinInt64); err != nil {
		t.Fatalf("u.Put(2,MinInt64): %v", err)
	}
	if _, err := u.Put(3, -1); err != nil {
		t.Fatalf("u.Put(3,-1): %v", err)
	}
	before := u.Entries()
	if _, err := u.Delete(1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("删除使上方键前缀和溢出 Delete(1): err = %v，期望 ErrOverflow", err)
	}
	if after := u.Entries(); !reflect.DeepEqual(before, after) {
		t.Fatal("删除溢出拒绝后状态改变")
	}
	t.Log("删除使上方键前缀和溢出 Delete(1): 整体拒绝为 ErrOverflow，状态不变")
}

// TestRevertRestoresView 写入改值再改回、插入后再删除，视图逐键恢复原样。
func TestRevertRestoresView(t *testing.T) {
	v, err := NewView(0, 1000, 64)
	if err != nil {
		t.Fatalf("NewView: %v", err)
	}
	seed := [][2]int64{{3, 10}, {8, -4}, {15, 7}, {42, 0}, {100, 6}}
	for _, kv := range seed {
		if _, err := v.Put(kv[0], kv[1]); err != nil {
			t.Fatalf("Put(%d,%d): %v", kv[0], kv[1], err)
		}
	}
	snapshot := v.Entries()
	t.Logf("初始视图: %v", snapshot)

	// 改值再改回。
	if _, err := v.Put(15, 999); err != nil {
		t.Fatalf("Put(15,999): %v", err)
	}
	if _, err := v.Put(15, 7); err != nil {
		t.Fatalf("Put(15,7): %v", err)
	}
	if got := v.Entries(); !reflect.DeepEqual(snapshot, got) {
		t.Fatalf("改值再改回后视图未恢复: 前=%v 后=%v", snapshot, got)
	}
	t.Logf("Put(15,999) 再 Put(15,7) 后视图逐键恢复: %v", v.Entries())

	// 插入后再删除。
	if _, err := v.Put(50, 123); err != nil {
		t.Fatalf("Put(50,123): %v", err)
	}
	if _, err := v.Delete(50); err != nil {
		t.Fatalf("Delete(50): %v", err)
	}
	if got := v.Entries(); !reflect.DeepEqual(snapshot, got) {
		t.Fatalf("插入后再删除视图未恢复: 前=%v 后=%v", snapshot, got)
	}
	if err := v.Check(); err != nil {
		t.Fatalf("恢复后自检失败: %v", err)
	}
	t.Logf("Put(50,123) 再 Delete(50) 后视图逐键恢复: %v", v.Entries())
}

// TestRandomizedVsNaive 固定种子随机操作序列，逐步与朴素重算比对，
// 保证结果可复现。
func TestRandomizedVsNaive(t *testing.T) {
	v, err := NewView(0, 199, 120)
	if err != nil {
		t.Fatalf("NewView: %v", err)
	}
	m := newNaive()
	rng := rand.New(rand.NewSource(20260930))
	for step := 1; step <= 3000; step++ {
		key := int64(rng.Intn(200))
		if rng.Intn(100) < 65 || m.vals == nil {
			var value int64
			switch rng.Intn(10) {
			case 0:
				value = 0 // 零值键
			default:
				value = int64(rng.Intn(2001) - 1000)
			}
			_, existed := m.vals[key]
			if !existed && len(m.vals) >= 120 {
				if _, err := v.Put(key, value); !errors.Is(err, ErrTooManyKeys) {
					t.Fatalf("步骤 %d Put(%d,%d): err = %v，期望 ErrTooManyKeys", step, key, value, err)
				}
				continue
			}
			want, basis := m.put(key, value)
			got, err := v.Put(key, value)
			if err != nil {
				t.Fatalf("步骤 %d Put(%d,%d): 意外错误 %v（值域已限制，不应溢出）", step, key, value, err)
			}
			if got != want {
				t.Fatalf("步骤 %d Put(%d,%d) 受影响键数 %d != 朴素重算 %d", step, key, value, got, want)
			}
			if step%500 == 0 {
				t.Logf("步骤 %d 输入=Put(%d,%d) 受影响键数=%d 判定依据=%s", step, key, value, got, basis)
			}
		} else {
			if _, ok := m.vals[key]; !ok {
				if _, err := v.Delete(key); !errors.Is(err, ErrKeyNotFound) {
					t.Fatalf("步骤 %d Delete(%d): err = %v，期望 ErrKeyNotFound", step, key, err)
				}
				continue
			}
			want, basis := m.del(key)
			got, err := v.Delete(key)
			if err != nil {
				t.Fatalf("步骤 %d Delete(%d): 意外错误 %v", step, key, err)
			}
			if got != want {
				t.Fatalf("步骤 %d Delete(%d) 受影响键数 %d != 朴素重算 %d", step, key, got, want)
			}
			if step%500 == 0 {
				t.Logf("步骤 %d 输入=Delete(%d) 受影响键数=%d 判定依据=%s", step, key, got, basis)
			}
		}
		if step%250 == 0 {
			verifyView(t, v, m, fmt.Sprintf("步骤 %d", step))
		}
	}
	verifyView(t, v, m, "随机序列结束")
	t.Logf("随机序列结束: 键数=%d 前缀和=%s", v.Len(), formatPS(m))
}

// TestConcurrentAccess 多个执行体并发调用前缀查询、视图与自检，
// 并与写入删除并发；自检在任何一致快照上都应通过。
func TestConcurrentAccess(t *testing.T) {
	v, err := NewView(0, 999, 256)
	if err != nil {
		t.Fatalf("NewView: %v", err)
	}
	for k := int64(0); k < 128; k++ {
		if _, err := v.Put(k, k%17-8); err != nil {
			t.Fatalf("Put(%d): %v", k, err)
		}
	}
	const iterations = 20000
	var wg sync.WaitGroup
	errs := make(chan error, 64)

	// 写入者：循环改值、插入、删除。
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < iterations; i++ {
				key := int64(rng.Intn(1000))
				if rng.Intn(2) == 0 {
					_, _ = v.Put(key, int64(rng.Intn(41)-20))
				} else {
					_, _ = v.Delete(key)
				}
			}
		}(int64(w) + 1)
	}
	// 读者：前缀查询、视图快照、自检。
	for r := 0; r < 6; r++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < iterations; i++ {
				switch rng.Intn(3) {
				case 0:
					_, _ = v.PrefixSum(int64(rng.Intn(1000)))
				case 1:
					entries := v.Entries()
					var run int64
					for _, e := range entries {
						run += e.Value
						if run != e.PrefixSum {
							errs <- fmt.Errorf("快照内前缀和不一致: 键 %d", e.Key)
							return
						}
					}
				case 2:
					if err := v.Check(); err != nil {
						errs <- err
						return
					}
				}
			}
		}(int64(r) + 100)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发执行中出错: %v", err)
		}
	}
	if err := v.Check(); err != nil {
		t.Fatalf("并发结束后自检失败: %v", err)
	}
	t.Logf("并发结束: 键数=%d，自检通过", v.Len())
}
