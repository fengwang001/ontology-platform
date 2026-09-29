package cardinality

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func key(i int) string { return fmt.Sprintf("key-%d", i) }

func mustAdd(t *testing.T, e *Estimator, k string) {
	t.Helper()
	if err := e.Add(k); err != nil {
		t.Fatalf("Add(%q) 失败: %v", k, err)
	}
	t.Logf("操作=Add 键=%q 模式=%s 估计=%d", k, e.Mode(), e.Estimate())
}

func mustRemove(t *testing.T, e *Estimator, k string) {
	t.Helper()
	if err := e.Remove(k); err != nil {
		t.Fatalf("Remove(%q) 失败: %v", k, err)
	}
	t.Logf("操作=Remove 键=%q 模式=%s 估计=%d", k, e.Mode(), e.Estimate())
}

// 小基数时显式集合精确计数，重复加入幂等。
func TestSparseExactCount(t *testing.T) {
	e, err := New(4, 8)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	for i := 0; i < 5; i++ {
		mustAdd(t, e, key(i))
	}
	mustAdd(t, e, key(0)) // 重复加入
	if got := e.Estimate(); got != 5 {
		t.Fatalf("稀疏模式估计=%d, 期望精确值 5（判定依据: 显式集合大小）", got)
	}
	if e.Mode() != ModeSparse {
		t.Fatalf("模式=%s, 期望 sparse（判定依据: 基数 5 未超过阈值 8）", e.Mode())
	}
	t.Logf("判定依据: 模式=%s 估计=%d 与显式集合大小一致", e.Mode(), e.Estimate())
}

// 阈值边界：恰好 threshold 个键保持稀疏，第 threshold+1 个键触发一次性转换。
func TestThresholdBoundary(t *testing.T) {
	const threshold = 16
	e, err := New(6, threshold)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	for i := 0; i < threshold; i++ {
		mustAdd(t, e, key(i))
	}
	if e.Mode() != ModeSparse {
		t.Fatalf("模式=%s, 期望 sparse（判定依据: 基数 %d 等于阈值 %d 未超过）", e.Mode(), threshold, threshold)
	}
	if got := e.Estimate(); got != threshold {
		t.Fatalf("阈值边界处估计=%d, 期望精确值 %d", got, threshold)
	}
	t.Logf("判定依据: 基数=%d 等于阈值=%d, 模式=%s 保持稀疏", threshold, threshold, e.Mode())

	mustAdd(t, e, key(threshold))
	if e.Mode() != ModeDense {
		t.Fatalf("模式=%s, 期望 dense（判定依据: 基数 %d 超过阈值 %d）", e.Mode(), threshold+1, threshold)
	}
	t.Logf("判定依据: 基数=%d 超过阈值=%d, 模式一次性转为 %s, 寄存器数=%d",
		threshold+1, threshold, e.Mode(), len(e.Registers()))

	// 撤回到阈值以下也永不回落。
	for i := 0; i < threshold; i++ {
		mustRemove(t, e, key(i))
	}
	if e.Mode() != ModeDense {
		t.Fatalf("撤回到 1 个键后模式=%s, 期望 dense（判定依据: 转换一次性, 永不回落）", e.Mode())
	}
	if got := e.Estimate(); got != 1 {
		t.Fatalf("稠密模式剩 1 键估计=%d, 期望 1（判定依据: 线性计数修正）", got)
	}
	t.Logf("判定依据: 撤回到 1 键后模式=%s 估计=%d, 转换一次性且估计不发散", e.Mode(), e.Estimate())
}

// 撤回回落：寄存器当前最大秩被删时回落到剩余最大秩而非清零。
func TestRemoveFallback(t *testing.T) {
	const precision = 4
	// 找两个落在同一寄存器但秩不同的键。
	type hit struct {
		key  string
		rank uint8
	}
	byReg := map[uint32][]hit{}
	var reg uint32
	var pair []hit
	for i := 0; ; i++ {
		k := key(i)
		r, rk := locate(precision, hashKey(k))
		byReg[r] = append(byReg[r], hit{k, rk})
		if len(byReg[r]) >= 2 && byReg[r][0].rank != byReg[r][1].rank {
			reg, pair = r, byReg[r][:2]
			break
		}
	}
	low, high := pair[0], pair[1]
	if low.rank > high.rank {
		low, high = high, low
	}
	t.Logf("判定依据: 寄存器=%d 低秩键=%q(秩=%d) 高秩键=%q(秩=%d)", reg, low.key, low.rank, high.key, high.rank)

	e, err := New(precision, 1)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	mustAdd(t, e, low.key)
	mustAdd(t, e, high.key)
	if got := e.Registers()[reg]; got != high.rank {
		t.Fatalf("寄存器[%d]=%d, 期望 %d（判定依据: 取最大秩）", reg, got, high.rank)
	}
	t.Logf("操作=Add 模式=%s 寄存器[%d]=%d 判定依据: 两键最大秩", e.Mode(), reg, e.Registers()[reg])

	mustRemove(t, e, high.key)
	if got := e.Registers()[reg]; got != low.rank {
		t.Fatalf("撤回高秩键后寄存器[%d]=%d, 期望回落到剩余最大秩 %d 而非清零", reg, got, low.rank)
	}
	t.Logf("操作=Remove 模式=%s 寄存器[%d]=%d 判定依据: 回落到剩余最大秩而非清零",
		e.Mode(), reg, e.Registers()[reg])

	mustRemove(t, e, low.key)
	if got := e.Registers()[reg]; got != 0 {
		t.Fatalf("寄存器清空后寄存器[%d]=%d, 期望 0（判定依据: 无剩余秩才归零）", reg, got)
	}
	if got := e.Estimate(); got != 0 {
		t.Fatalf("全部撤回后估计=%d, 期望 0（判定依据: 撤回可逆不发散）", got)
	}
	t.Logf("操作=Remove 模式=%s 寄存器[%d]=0 估计=%d 判定依据: 全部撤回后精确还原",
		e.Mode(), reg, e.Estimate())
}

// 非法参数与空键、撤回不存在的键：整体拒绝且状态不变。
func TestRejections(t *testing.T) {
	if _, err := New(MinPrecision-1, 10); !errors.Is(err, ErrInvalidPrecision) {
		t.Fatalf("精度过小 err=%v, 期望 ErrInvalidPrecision", err)
	}
	if _, err := New(MaxPrecision+1, 10); !errors.Is(err, ErrInvalidPrecision) {
		t.Fatalf("精度过大 err=%v, 期望 ErrInvalidPrecision", err)
	}
	if _, err := New(6, 0); !errors.Is(err, ErrInvalidThreshold) {
		t.Fatalf("阈值非法 err=%v, 期望 ErrInvalidThreshold", err)
	}
	t.Logf("判定依据: 非法精度/阈值分别返回 %v / %v", ErrInvalidPrecision, ErrInvalidThreshold)

	e, err := New(6, 4)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err := e.Add(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Add 空键 err=%v, 期望 ErrEmptyKey", err)
	}
	if err := e.Remove(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Remove 空键 err=%v, 期望 ErrEmptyKey", err)
	}
	for i := 0; i < 10; i++ { // 超过阈值, 进入稠密模式
		mustAdd(t, e, key(i))
	}
	beforeEst := e.Estimate()
	beforeRegs := e.Registers()
	if err := e.Remove("ghost"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("撤回不存在的键 err=%v, 期望 ErrKeyNotFound", err)
	}
	if got := e.Estimate(); got != beforeEst {
		t.Fatalf("失败撤回后估计=%d, 期望不变 %d（判定依据: 失败不改变状态）", got, beforeEst)
	}
	afterRegs := e.Registers()
	for i := range beforeRegs {
		if beforeRegs[i] != afterRegs[i] {
			t.Fatalf("失败撤回后寄存器[%d]=%d, 期望不变 %d", i, afterRegs[i], beforeRegs[i])
		}
	}
	t.Logf("操作=Remove(ghost) 模式=%s 估计=%d 判定依据: 撤回不存在的键整体拒绝, 寄存器与估计不变",
		e.Mode(), e.Estimate())
}

// 并发加入互不相同的键, 结果与串行逐条加入一致。
func TestConcurrentAddsMatchSerial(t *testing.T) {
	const (
		precision = 10
		n         = 4000
		workers   = 8
	)
	serial, err := New(precision, 8)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	for i := 0; i < n; i++ {
		mustAdd(t, serial, key(i))
	}

	concurrent, err := New(precision, 8)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < n; i += workers {
				if err := concurrent.Add(key(i)); err != nil {
					t.Errorf("并发 Add 失败: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()

	if got, want := concurrent.Estimate(), serial.Estimate(); got != want {
		t.Fatalf("并发估计=%d, 串行估计=%d（判定依据: 确定哈希下加入顺序无关）", got, want)
	}
	cr, sr := concurrent.Registers(), serial.Registers()
	for i := range cr {
		if cr[i] != sr[i] {
			t.Fatalf("寄存器[%d] 并发=%d 串行=%d（判定依据: 寄存器应逐位一致）", i, cr[i], sr[i])
		}
	}
	if err := concurrent.Verify(); err != nil {
		t.Fatalf("并发加入后自检失败: %v", err)
	}
	t.Logf("判定依据: 模式=%s 并发与串行估计均为 %d, %d 个寄存器逐位一致, 自检通过",
		concurrent.Mode(), concurrent.Estimate(), len(cr))
}

// 并发撤回后估计精确还原到追加前的快照。
func TestConcurrentRemovalsRestore(t *testing.T) {
	const (
		precision = 10
		base      = 500
		extra     = 2000
		workers   = 8
	)
	e, err := New(precision, 8)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	for i := 0; i < base; i++ {
		mustAdd(t, e, key(i))
	}
	snapEst := e.Estimate()
	snapRegs := e.Registers()
	t.Logf("快照: 模式=%s 估计=%d（基数 %d）", e.Mode(), snapEst, base)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := base + w; i < base+extra; i += workers {
				if err := e.Add(key(i)); err != nil {
					t.Errorf("并发 Add 失败: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
	t.Logf("并发追加 %d 键后: 估计=%d", extra, e.Estimate())

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := base + w; i < base+extra; i += workers {
				if err := e.Remove(key(i)); err != nil {
					t.Errorf("并发 Remove 失败: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()

	if got := e.Estimate(); got != snapEst {
		t.Fatalf("并发撤回后估计=%d, 期望精确还原到快照 %d（判定依据: 撤回可逆不发散）", got, snapEst)
	}
	regs := e.Registers()
	for i := range regs {
		if regs[i] != snapRegs[i] {
			t.Fatalf("并发撤回后寄存器[%d]=%d, 期望还原到 %d", i, regs[i], snapRegs[i])
		}
	}
	if err := e.Verify(); err != nil {
		t.Fatalf("并发撤回后自检失败: %v", err)
	}
	t.Logf("判定依据: 模式=%s 估计=%d 与快照一致, %d 个寄存器逐位还原, 自检通过",
		e.Mode(), e.Estimate(), len(regs))
}

// 稠密模式估计精度在固定公式误差范围内, 且自检通过。
func TestDenseEstimateAccuracy(t *testing.T) {
	const (
		precision = 12
		n         = 50000
	)
	e, err := New(precision, 1)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	for i := 0; i < n; i++ {
		if err := e.Add(key(i)); err != nil {
			t.Fatalf("Add 失败: %v", err)
		}
	}
	got := e.Estimate()
	rel := float64(got) / n
	if rel < 0.95 || rel > 1.05 {
		t.Fatalf("估计=%d 相对误差 %.2f%% 超出 ±5%%（判定依据: m=%d 时 sigma≈%.2f%%）",
			got, (rel-1)*100, 1<<precision, 104/float64(uint(1)<<(precision/2)))
	}
	if err := e.Verify(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	t.Logf("判定依据: 模式=%s 真实基数=%d 估计=%d 相对误差=%.2f%%, 自检通过",
		e.Mode(), n, got, (rel-1)*100)
}

// 稠密模式下全部撤回后估计归零, 可复现且不发散。
func TestDenseRemoveAllReturnsToZero(t *testing.T) {
	e, err := New(8, 4)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	const n = 1000
	for i := 0; i < n; i++ {
		mustAdd(t, e, key(i))
	}
	for i := 0; i < n; i++ {
		mustRemove(t, e, key(i))
	}
	if got := e.Estimate(); got != 0 {
		t.Fatalf("全部撤回后估计=%d, 期望 0（判定依据: 撤回完全可逆）", got)
	}
	if err := e.Verify(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	t.Logf("判定依据: 模式=%s 全部 %d 键撤回后估计=%d, 寄存器全零, 自检通过",
		e.Mode(), n, e.Estimate())
}
