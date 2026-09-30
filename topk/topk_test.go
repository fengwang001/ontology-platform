package topk

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustWindow(t *testing.T, n, k int) *Window {
	t.Helper()
	w, err := NewWindow(n, k)
	if err != nil {
		t.Fatalf("NewWindow(%d, %d) 意外失败: %v", n, k, err)
	}
	return w
}

func mustApply(t *testing.T, w *Window, batch []Change) {
	t.Helper()
	t.Logf("输入变更批次: %+v", batch)
	if err := w.Apply(batch); err != nil {
		t.Fatalf("Apply(%+v) 意外失败: %v", batch, err)
	}
}

func assertTopK(t *testing.T, w *Window, want []Entry, reason string) {
	t.Helper()
	got := w.TopK()
	t.Logf("前 K 查询结果: %+v", got)
	t.Logf("判定依据: %s", reason)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("前 K 不一致: 得到 %+v, 期望 %+v", got, want)
	}
	if err := w.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	t.Logf("自检通过: 依据窗口队列重算的分值与排序视图同维护状态逐字段一致")
}

func TestSumWithinWindow(t *testing.T) {
	w := mustWindow(t, 5, 3)
	mustApply(t, w, []Change{{Key: "a", Delta: 2}, {Key: "b", Delta: 5}, {Key: "a", Delta: 3}})
	assertTopK(t, w, []Entry{{Key: "a", Score: 5}, {Key: "b", Score: 5}},
		"键 a 分值为窗口内变更之和 2+3=5；a 与 b 并列 5 分，按键名字典序 a 在 b 前")
}

func TestEvictionRevokesAndBackfills(t *testing.T) {
	w := mustWindow(t, 3, 2)
	mustApply(t, w, []Change{{Key: "a", Delta: 10}, {Key: "b", Delta: 8}, {Key: "c", Delta: 6}})
	assertTopK(t, w, []Entry{{Key: "a", Score: 10}, {Key: "b", Score: 8}},
		"窗口未满，前 2 为 a(10)、b(8)")

	mustApply(t, w, []Change{{Key: "d", Delta: 1}})
	assertTopK(t, w, []Entry{{Key: "b", Score: 8}, {Key: "c", Score: 6}},
		"窗口容量 3，加入 d 后最旧的 a(10) 滑出被撤回，c(6) 补位进入前 2")

	mustApply(t, w, []Change{{Key: "e", Delta: 9}})
	assertTopK(t, w, []Entry{{Key: "e", Score: 9}, {Key: "c", Score: 6}},
		"b(8) 滑出被撤回，新键 e(9) 进入窗口并登顶，c(6) 保持前 2")
}

func TestEvictionRemovesKeyOnlyWhenNoChangeLeft(t *testing.T) {
	w := mustWindow(t, 3, 3)
	mustApply(t, w, []Change{{Key: "a", Delta: 4}, {Key: "a", Delta: 4}, {Key: "b", Delta: 1}})
	assertTopK(t, w, []Entry{{Key: "a", Score: 8}, {Key: "b", Score: 1}},
		"a 在窗口内有两条 +4，分值为 8")

	mustApply(t, w, []Change{{Key: "c", Delta: 2}})
	assertTopK(t, w, []Entry{{Key: "a", Score: 4}, {Key: "c", Score: 2}, {Key: "b", Score: 1}},
		"a 最旧一条 +4 滑出，窗口内仍剩一条 +4，键 a 继续存在且分值降为 4")

	mustApply(t, w, []Change{{Key: "d", Delta: 3}})
	assertTopK(t, w, []Entry{{Key: "d", Score: 3}, {Key: "c", Score: 2}, {Key: "b", Score: 1}},
		"a 的最后一条变更滑出，键 a 不再存在并被撤回；d(3) 进入并登顶")
}

func TestTieBreakByKeyAscending(t *testing.T) {
	w := mustWindow(t, 10, 3)
	mustApply(t, w, []Change{
		{Key: "b", Delta: 5},
		{Key: "d", Delta: 5},
		{Key: "a", Delta: 5},
		{Key: "c", Delta: 5},
		{Key: "e", Delta: 4},
	})
	assertTopK(t, w, []Entry{{Key: "a", Score: 5}, {Key: "b", Score: 5}, {Key: "c", Score: 5}},
		"a、b、c、d 并列 5 分，按字典序升序取前 3 为 a、b、c；d 因字典序靠后落选，e(4) 分值更低")
}

func TestNegativeScores(t *testing.T) {
	w := mustWindow(t, 5, 3)
	mustApply(t, w, []Change{{Key: "a", Delta: -3}, {Key: "b", Delta: -1}, {Key: "c", Delta: -2}})
	assertTopK(t, w, []Entry{{Key: "b", Score: -1}, {Key: "c", Score: -2}, {Key: "a", Score: -3}},
		"负分值同样按降序排列：-1 > -2 > -3")

	mustApply(t, w, []Change{{Key: "a", Delta: 10}})
	assertTopK(t, w, []Entry{{Key: "a", Score: 7}, {Key: "b", Score: -1}, {Key: "c", Score: -2}},
		"a 追加 +10 后分值为 -3+10=7，由垫底升至榜首")
}

func TestZeroDeltaChangeKeepsKey(t *testing.T) {
	w := mustWindow(t, 2, 2)
	mustApply(t, w, []Change{{Key: "z", Delta: 0}})
	assertTopK(t, w, []Entry{{Key: "z", Score: 0}},
		"分值为 0 的变更仍使键存在：键在窗口内至少有一条变更即算存在")
}

func TestInvalidWindowParams(t *testing.T) {
	cases := []struct {
		name    string
		n, k    int
		wantErr error
	}{
		{"窗口为零", 0, 1, ErrNonPositiveWindow},
		{"窗口为负", -2, 1, ErrNonPositiveWindow},
		{"前K为零", 3, 0, ErrNonPositiveK},
		{"前K为负", 3, -1, ErrNonPositiveK},
		{"前K超过窗口", 2, 3, ErrKExceedsWindow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewWindow(tc.n, tc.k)
			t.Logf("输入: n=%d k=%d, 得到错误: %v", tc.n, tc.k, err)
			t.Logf("判定依据: 错误须可区分且与 %v 匹配", tc.wantErr)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("期望错误 %v, 得到 %v", tc.wantErr, err)
			}
		})
	}
}

func TestEmptyKeyRejectsWholeBatch(t *testing.T) {
	w := mustWindow(t, 3, 2)
	mustApply(t, w, []Change{{Key: "a", Delta: 5}})
	before := w.TopK()
	beforeLen := w.Len()

	bad := []Change{{Key: "b", Delta: 1}, {Key: "", Delta: 2}, {Key: "c", Delta: 3}}
	err := w.Apply(bad)
	t.Logf("输入非法批次: %+v, 得到错误: %v", bad, err)
	t.Logf("判定依据: 错误须匹配 ErrEmptyKey，且整批不生效，窗口与分值不变")
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("期望错误 %v, 得到 %v", ErrEmptyKey, err)
	}
	if got := w.TopK(); !reflect.DeepEqual(got, before) {
		t.Fatalf("非法批次后前 K 发生变化: 之前 %+v, 之后 %+v", before, got)
	}
	if got := w.Len(); got != beforeLen {
		t.Fatalf("非法批次后窗口长度发生变化: 之前 %d, 之后 %d", beforeLen, got)
	}
	t.Logf("拒绝后前 K 仍为 %+v, 窗口长度仍为 %d, 整批未生效", before, beforeLen)
}

func TestConcurrentReadOnlyConsistency(t *testing.T) {
	w := mustWindow(t, 64, 10)
	batch := []Change{
		{Key: "alpha", Delta: 7},
		{Key: "beta", Delta: -3},
		{Key: "gamma", Delta: 7},
		{Key: "alpha", Delta: 1},
		{Key: "delta", Delta: 0},
	}
	mustApply(t, w, batch)
	baseline := w.TopK()
	t.Logf("基线前 K: %+v", baseline)
	t.Logf("判定依据: 并发只读下每次 TopK 结果须与基线逐字段一致，SelfCheck 须全部通过")

	const goroutines = 8
	const iterations = 200
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if got := w.TopK(); !reflect.DeepEqual(got, baseline) {
					t.Errorf("并发读取不一致: 得到 %+v, 基线 %+v", got, baseline)
					return
				}
				if err := w.SelfCheck(); err != nil {
					t.Errorf("并发自检失败: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	t.Logf("%d 个 goroutine 各执行 %d 次 TopK 与 SelfCheck，结果均与基线逐字段一致", goroutines, iterations)
}
