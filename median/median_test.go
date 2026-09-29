package median

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"sync"
	"testing"
)

// naiveMedian 是朴素参照：把多重集升序排序后取中间偏下位置。
func naiveMedian(ref []int) (int, bool) {
	if len(ref) == 0 {
		return 0, false
	}
	s := append([]int(nil), ref...)
	sort.Ints(s)
	return s[(len(s)-1)/2], true
}

func logStep(t *testing.T, step int, op string, v int, ref []int) {
	t.Helper()
	s := append([]int(nil), ref...)
	sort.Ints(s)
	if m, ok := naiveMedian(ref); ok {
		idx := (len(s) - 1) / 2
		t.Logf("步骤 %d: 输入=%s(%d) | 排序后=%v | 长度=%d | 中位数=%d | 判定依据: 升序索引 %d=(长度-1)/2，偶数取偏下者",
			step, op, v, s, len(s), m, idx)
	} else {
		t.Logf("步骤 %d: 输入=%s(%d) | 排序后=%v | 长度=0 | 中位数=<空集报错> | 判定依据: 空多重集无中间位置",
			step, op, v, s)
	}
}

func TestEvenLowerMedian(t *testing.T) {
	tr := New()
	var ref []int
	seq := []int{1, 3, 2, 4}
	for i, v := range seq {
		if err := tr.Add(v); err != nil {
			t.Fatalf("add %d: %v", v, err)
		}
		ref = append(ref, v)
		logStep(t, i+1, "add", v, ref)
		got, err := tr.Median()
		if err != nil {
			t.Fatalf("median: %v", err)
		}
		want, _ := naiveMedian(ref)
		if got != want {
			t.Fatalf("长度=%d: 中位数=%d, 朴素参照=%d (偶数必须取偏下者)", len(ref), got, want)
		}
	}
	m, _ := tr.Median()
	if m != 2 {
		t.Fatalf("偶数中位 got %d want 2", m)
	}
	if err := tr.Check(); err != nil {
		t.Fatalf("self-check: %v", err)
	}
}

func TestRemoveDuplicatesOneCopy(t *testing.T) {
	tr := New()
	var ref []int
	for _, v := range []int{5, 5, 5} {
		if err := tr.Add(v); err != nil {
			t.Fatal(err)
		}
		ref = append(ref, v)
	}
	if err := tr.Remove(5); err != nil {
		t.Fatalf("remove 5: %v", err)
	}
	ref = ref[:2]
	logStep(t, 1, "remove", 5, ref)
	if tr.Len() != 2 {
		t.Fatalf("len got %d want 2", tr.Len())
	}
	m, err := tr.Median()
	if err != nil {
		t.Fatal(err)
	}
	if m != 5 {
		t.Fatalf("重复值只撤回一个后中位 got %d want 5", m)
	}
	if tr.count[5] != 2 {
		t.Fatalf("5 的有效副本数 got %d want 2", tr.count[5])
	}
	if err := tr.Remove(5); err != nil || tr.Len() != 1 {
		t.Fatalf("第二次撤回异常: err=%v len=%d", err, tr.Len())
	}
	if err := tr.Remove(5); err != nil || tr.Len() != 0 {
		t.Fatalf("第三次撤回异常: err=%v len=%d", err, tr.Len())
	}
	if err := tr.Remove(5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("空集后撤回 5: got %v, want ErrNotFound", err)
	}
	if _, err := tr.Median(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("空集中位查询: got %v, want ErrEmpty", err)
	}
}

func TestStaleCopyCleanup(t *testing.T) {
	tr := New()
	var ref []int

	for _, v := range []int{1, 2, 3, 4, 5, 6} {
		if err := tr.Add(v); err != nil {
			t.Fatal(err)
		}
		ref = append(ref, v)
	}
	// 撤回 1：它埋在 lo 堆顶 3 之下，作废副本登记 pending 但暂不能物理清理。
	if err := tr.Remove(1); err != nil {
		t.Fatal(err)
	}
	ref = ref[1:]
	logStep(t, 1, "remove", 1, ref)
	if tr.lo.pending[1] != 1 {
		t.Fatalf("作废副本应仍在 lo 堆中等待清理, pending=%v", tr.lo.pending)
	}
	m, _ := tr.Median()
	if want, _ := naiveMedian(ref); m != want {
		t.Fatalf("待清理期间中位 got %d want %d", m, want)
	}

	// 1 当前不存在（只剩作废副本），再撤回必须报 ErrNotFound。
	if err := tr.Remove(1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("撤回不存在的 1（作废副本仍在堆中）: got %v, want ErrNotFound", err)
	}

	for step, v := range []int{3, 2, 4, 5, 6} {
		if err := tr.Remove(v); err != nil {
			t.Fatalf("remove %d: %v", v, err)
		}
		idx := -1
		for j, x := range ref {
			if x == v {
				idx = j
				break
			}
		}
		if idx < 0 {
			t.Fatalf("参照集中缺少 %d", v)
		}
		ref = append(ref[:idx], ref[idx+1:]...)
		logStep(t, step+2, "remove", v, ref)
		if len(ref) > 0 {
			got, err := tr.Median()
			if err != nil {
				t.Fatal(err)
			}
			want, _ := naiveMedian(ref)
			if got != want {
				t.Fatalf("清理过程中位 got %d want %d", got, want)
			}
		} else if _, err := tr.Median(); !errors.Is(err, ErrEmpty) {
			t.Fatalf("抽干后中位查询 got %v want ErrEmpty", err)
		}
	}
	if len(tr.lo.pending) != 0 || len(tr.hi.pending) != 0 {
		t.Fatalf("多重集抽干后作废副本应已全部清理, lo pending=%v hi pending=%v", tr.lo.pending, tr.hi.pending)
	}
	if err := tr.Check(); err != nil {
		t.Fatalf("self-check: %v", err)
	}
}

type snapshot struct {
	loItems, hiItems []int
	loPend, hiPend   map[int]int
	count            map[int]int
	size, loV, hiV   int
}

func snap(tr *Tracker) snapshot {
	s := snapshot{
		loItems: append([]int(nil), tr.lo.items...),
		hiItems: append([]int(nil), tr.hi.items...),
		loPend:  map[int]int{},
		hiPend:  map[int]int{},
		count:   map[int]int{},
		size:    tr.size, loV: tr.lo.valid, hiV: tr.hi.valid,
	}
	for k, c := range tr.lo.pending {
		s.loPend[k] = c
	}
	for k, c := range tr.hi.pending {
		s.hiPend[k] = c
	}
	for k, c := range tr.count {
		s.count[k] = c
	}
	return s
}

func assertUnchanged(t *testing.T, before, after snapshot, reason string) {
	t.Helper()
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("拒绝（%s）后状态发生变化:\n之前=%+v\n之后=%+v", reason, before, after)
	}
}

func TestBatchRejectionLeavesNoTrace(t *testing.T) {
	tr := New()
	if err := tr.Apply(Op{OpAdd, 1}, Op{OpAdd, 2}, Op{OpAdd, 1}); err != nil {
		t.Fatal(err)
	}
	// 造出“作废副本仍在堆中”的状态：两个 1 全部撤回（其中一个作废副本被埋）。
	if err := tr.Remove(1); err != nil {
		t.Fatal(err)
	}
	if err := tr.Remove(1); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		ops  []Op
		want error
	}{
		{"空批次", nil, ErrInvalidArgument},
		{"未知操作类型", []Op{{Kind: OpKind(99), V: 1}}, ErrInvalidArgument},
		{"撤回不存在的值", []Op{{OpRemove, 7}}, ErrNotFound},
		{"作废副本在堆但值不存在", []Op{{OpRemove, 1}}, ErrNotFound},
		{"合法后接非法整体回滚", []Op{{OpAdd, 5}, {OpRemove, 7}}, ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := snap(tr)
			err := tr.Apply(tc.ops...)
			if !errors.Is(err, tc.want) {
				t.Fatalf("%s: got %v, want %v", tc.name, err, tc.want)
			}
			t.Logf("拒绝用例 %q: 输入=%v | 拒绝原因=%v | 两堆与计数保持不变", tc.name, tc.ops, err)
			assertUnchanged(t, before, snap(tr), tc.name)
		})
	}

	small, err := NewWithMax(2)
	if err != nil {
		t.Fatal(err)
	}
	before := snap(small)
	if err := small.Apply(Op{OpAdd, 1}, Op{OpAdd, 2}, Op{OpAdd, 3}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("超限: got %v want ErrLimitExceeded", err)
	}
	assertUnchanged(t, before, snap(small), "小容器超限")

	if _, err := NewWithMax(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("非法上限: got %v want ErrInvalidArgument", err)
	}
	var nilTr *Tracker
	if err := nilTr.Add(1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil 接收者: got %v", err)
	}
}

func TestErrorReasonsAreDistinct(t *testing.T) {
	errs := []error{ErrInvalidArgument, ErrEmpty, ErrNotFound, ErrLimitExceeded}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("错误类别 %v 与 %v 不可区分", errs[i], errs[j])
			}
		}
	}
	t.Logf("四类错误原因互不相同: %v, %v, %v, %v", errs[0], errs[1], errs[2], errs[3])
}

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewPCG(20260929, 42))
	tr := New()
	var ref []int
	for step := 1; step <= 4000; step++ {
		v := rng.IntN(8)
		var op string
		if rng.IntN(2) == 0 || len(ref) == 0 {
			op = "add"
			if err := tr.Add(v); err != nil {
				t.Fatalf("step %d add %d: %v", step, v, err)
			}
			ref = append(ref, v)
		} else {
			op = "remove"
			present := false
			for _, x := range ref {
				if x == v {
					present = true
					break
				}
			}
			if !present {
				if err := tr.Remove(v); !errors.Is(err, ErrNotFound) {
					t.Fatalf("step %d remove missing %d: got %v want ErrNotFound", step, v, err)
				}
				continue
			}
			if err := tr.Remove(v); err != nil {
				t.Fatalf("step %d remove %d: %v", step, v, err)
			}
			idx := -1
			for j, x := range ref {
				if x == v {
					idx = j
					break
				}
			}
			ref = append(ref[:idx], ref[idx+1:]...)
		}
		if step <= 20 || step%500 == 0 {
			logStep(t, step, op, v, ref)
		}
		got, err := tr.Median()
		want, ok := naiveMedian(ref)
		if ok != (err == nil) || (ok && got != want) {
			t.Fatalf("step %d (%s %d): 堆中位=(%d,%v) 朴素参照=(%d,%v)", step, op, v, got, err, want, ok)
		}
		if tr.Len() != len(ref) {
			t.Fatalf("step %d: len %d != ref %d", step, tr.Len(), len(ref))
		}
		if err := tr.Check(); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
	}
}

func TestConcurrent(t *testing.T) {
	tr := New()
	for _, v := range []int{0, 1, 2, 3, 4} {
		if err := tr.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var readerErr error
	var errMu sync.Mutex

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, mErr := tr.Median()
					if mErr != nil && !errors.Is(mErr, ErrEmpty) {
						errMu.Lock()
						readerErr = mErr
						errMu.Unlock()
						return
					}
					_ = tr.Len()
					if cErr := tr.Check(); cErr != nil {
						errMu.Lock()
						readerErr = cErr
						errMu.Unlock()
						return
					}
				}
			}
		}()
	}

	rng := rand.New(rand.NewPCG(7, 7))
	for range 2000 {
		v := rng.IntN(6)
		if rng.IntN(2) == 0 || tr.Len() == 0 {
			if err := tr.Add(v); err != nil {
				t.Fatalf("concurrent add: %v", err)
			}
		} else if err := tr.Remove(v); err != nil && !errors.Is(err, ErrNotFound) {
			t.Fatalf("concurrent remove: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	if readerErr != nil {
		t.Fatalf("并发读/自检失败: %v", readerErr)
	}
	if err := tr.Check(); err != nil {
		t.Fatalf("最终自检: %v", err)
	}
	t.Logf("并发结束: 长度=%d 中位数可与提交并发调用且无竞态", tr.Len())
}
