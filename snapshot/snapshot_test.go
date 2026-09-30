package snapshot

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
)

func mustNew(t *testing.T, n int) *Snapshot {
	t.Helper()
	s, err := New(n)
	if err != nil {
		t.Fatalf("New(%d) 返回意外错误: %v", n, err)
	}
	return s
}

func TestNewValidation(t *testing.T) {
	for _, n := range []int{-1, 0, MaxN + 1, 100} {
		s, err := New(n)
		t.Logf("输入 n=%d -> 输出 err=%v; 判定: 越界必须报 ErrInvalidN 且不产生对象", n, err)
		if !errors.Is(err, ErrInvalidN) {
			t.Fatalf("New(%d) 应返回 ErrInvalidN, 实际 %v", n, err)
		}
		if s != nil {
			t.Fatalf("New(%d) 被拒绝时不应返回对象", n)
		}
	}
	for _, n := range []int{1, MaxN} {
		s := mustNew(t, n)
		if s.N() != n {
			t.Fatalf("N()=%d, 期望 %d", s.N(), n)
		}
		got := s.Scan()
		t.Logf("输入 n=%d -> 初始快照 %v; 判定: 长度 n 且全为初值 0", n, got)
		if len(got) != n {
			t.Fatalf("快照长度 %d, 期望 %d", len(got), n)
		}
		for i, v := range got {
			if v != 0 {
				t.Fatalf("单元 %d 初值 %d, 期望 0", i, v)
			}
		}
	}
}

func TestUpdateValidationOrderAndAtomicity(t *testing.T) {
	s := mustNew(t, 4)

	err := s.Update(0, -1, 7)
	t.Logf("输入 Update(writer=0, cell=-1, 7) -> err=%v; 判定: 下标越界报 ErrCellOutOfRange", err)
	if !errors.Is(err, ErrCellOutOfRange) {
		t.Fatalf("期望 ErrCellOutOfRange, 实际 %v", err)
	}

	err = s.Update(0, 4, 7)
	t.Logf("输入 Update(writer=0, cell=4, 7) -> err=%v; 判定: 下标越界报 ErrCellOutOfRange", err)
	if !errors.Is(err, ErrCellOutOfRange) {
		t.Fatalf("期望 ErrCellOutOfRange, 实际 %v", err)
	}

	err = s.Update(2, 1, 7)
	t.Logf("输入 Update(writer=2, cell=1, 7) -> err=%v; 判定: 编号不符报 ErrWriterMismatch", err)
	if !errors.Is(err, ErrWriterMismatch) {
		t.Fatalf("期望 ErrWriterMismatch, 实际 %v", err)
	}

	err = s.Update(3, 9, 7)
	t.Logf("输入 Update(writer=3, cell=9, 7) -> err=%v; 判定: 两类错误并存时只报第一个(下标越界)", err)
	if !errors.Is(err, ErrCellOutOfRange) {
		t.Fatalf("两类错误并存时应报 ErrCellOutOfRange, 实际 %v", err)
	}

	got := s.Scan()
	t.Logf("全部被拒后快照 %v; 判定: 被拒绝的操作不得改变任何状态(仍全为 0)", got)
	for i, v := range got {
		if v != 0 {
			t.Fatalf("被拒绝的更新改变了状态: 单元 %d = %d", i, v)
		}
	}
}

func TestSingleCell(t *testing.T) {
	s := mustNew(t, 1)
	if err := s.Update(0, 0, 42); err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	got := s.Scan()
	t.Logf("n=1, 写入 42 后快照 %v; 判定: 单元 0 必须为 42", got)
	if len(got) != 1 || got[0] != 42 {
		t.Fatalf("期望 [42], 实际 %v", got)
	}
	if mc := s.MaxCollects(); mc > 2*1+1 {
		t.Fatalf("n=1 时收集次数 %d 超过上界 %d", mc, 2*1+1)
	}
}

func TestReadAfterWriteVisible(t *testing.T) {
	s := mustNew(t, 8)
	for i := 0; i < s.N(); i++ {
		want := int64(100 + i)
		if err := s.Update(i, i, want); err != nil {
			t.Fatalf("Update 失败: %v", err)
		}
		got := s.Scan()
		t.Logf("写者 %d 写入 %d 返回后立即快照 -> 单元值 %d; 判定: 必须看到该值或更新的值", i, want, got[i])
		if got[i] < want {
			t.Fatalf("更新返回后的快照未看到该值: 单元 %d = %d, 期望 >= %d", i, got[i], want)
		}
	}
}

func TestCausalChain(t *testing.T) {
	const n = MaxN
	s := mustNew(t, n)

	if err := s.Update(0, 0, 1); err != nil {
		t.Fatalf("Update 失败: %v", err)
	}

	var mu sync.Mutex
	var observed [][]int64
	stop := make(chan struct{})
	var watchers sync.WaitGroup
	for w := 0; w < 2; w++ {
		watchers.Add(1)
		go func() {
			defer watchers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				v := s.Scan()
				mu.Lock()
				observed = append(observed, v)
				mu.Unlock()
			}
		}()
	}

	var chain sync.WaitGroup
	for i := 1; i < n; i++ {
		chain.Add(1)
		go func(i int) {
			defer chain.Done()
			for {
				v := s.Scan()
				if v[i-1] >= 1 {
					break
				}
			}
			if err := s.Update(i, i, 1); err != nil {
				t.Errorf("写者 %d 更新失败: %v", i, err)
			}
		}(i)
	}
	chain.Wait()
	close(stop)
	watchers.Wait()

	final := s.Scan()
	t.Logf("因果链: 写者0写1 -> 写者i见到单元%d为1后才写1; 传播期间共采集 %d 个快照; 最终快照 %v", n-1, len(observed), final)
	for i, v := range final {
		if v != 1 {
			t.Fatalf("链完成后单元 %d = %d, 期望 1", i, v)
		}
	}
	checked := 0
	for _, v := range observed {
		for i := 1; i < n; i++ {
			if v[i] >= 1 && v[i-1] < 1 {
				t.Fatalf("违反因果性: 快照 %v 中单元 %d 已新(%d)而单元 %d 仍旧(%d)", v, i, v[i], i-1, v[i-1])
			}
		}
		checked++
	}
	t.Logf("判定依据: 任意快照若单元 i 见到新值, 则其因果前驱 i-1 必为新值; %d 个快照全部满足", checked)
}

func TestMonotonicity(t *testing.T) {
	s := mustNew(t, 6)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < s.N(); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for v := int64(1); ; v++ {
				select {
				case <-stop:
					return
				default:
				}
				if err := s.Update(i, i, v); err != nil {
					t.Errorf("Update 失败: %v", err)
					return
				}
			}
		}(i)
	}
	pairs := 0
	for k := 0; k < 500; k++ {
		a := s.Scan()
		b := s.Scan()
		for i := 0; i < s.N(); i++ {
			if a[i] > b[i] {
				t.Fatalf("违反单调性: A 先于 B 完成, 但 A[%d]=%d > B[%d]=%d", i, a[i], i, b[i])
			}
		}
		pairs++
	}
	close(stop)
	wg.Wait()
	t.Logf("判定依据: 各单元只写递增值时, 先完成的快照逐分量不大于后开始的快照; %d 对快照全部满足", pairs)
}

func TestCollectBoundUnderChurn(t *testing.T) {
	const n = MaxN
	s := mustNew(t, n)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for v := int64(1); ; v++ {
				select {
				case <-stop:
					return
				default:
				}
				if err := s.Update(i, i, v); err != nil {
					t.Errorf("Update 失败: %v", err)
					return
				}
			}
		}(i)
	}
	const scans = 3000
	for k := 0; k < scans; k++ {
		_ = s.Scan()
	}
	close(stop)
	wg.Wait()
	mc := s.MaxCollects()
	t.Logf("输入: %d 个写者持续更新 + %d 次快照读; 输出: 单次收集次数最大值=%d; 判定: 必须 <= 2n+1=%d", n, scans, mc, 2*n+1)
	if mc > 2*n+1 {
		t.Fatalf("收集次数最大值 %d 超过上界 %d", mc, 2*n+1)
	}
	if mc == 0 {
		t.Fatalf("统计未生效: MaxCollects=0")
	}
}

type writeEvent struct {
	value int64
	begin int64
	end   int64
}

func TestConcurrentExplainability(t *testing.T) {
	const (
		n           = 8
		writesEach  = 300
		readerCount = 4
		scansEach   = 400
	)
	s := mustNew(t, n)
	var clock atomic.Int64
	logs := make([][]writeEvent, n)

	var mu sync.Mutex
	var vectors [][]int64

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for k := int64(1); k <= writesEach; k++ {
				value := int64(i+1)*1_000_000_000 + k
				begin := clock.Add(1)
				if err := s.Update(i, i, value); err != nil {
					t.Errorf("Update 失败: %v", err)
					return
				}
				end := clock.Add(1)
				logs[i] = append(logs[i], writeEvent{value: value, begin: begin, end: end})
			}
		}(i)
	}
	for r := 0; r < readerCount; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < scansEach; k++ {
				v := s.Scan()
				mu.Lock()
				vectors = append(vectors, v)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	index := make([]map[int64]int, n)
	for i := 0; i < n; i++ {
		index[i] = make(map[int64]int, len(logs[i]))
		for k, ev := range logs[i] {
			index[i][ev.value] = k
		}
	}

	t.Logf("输入: %d 个写者各写 %d 个递增值, %d 个读者各读 %d 次; 输出: %d 个快照向量", n, writesEach, readerCount, scansEach, len(vectors))
	for _, v := range vectors {
		overwriteEnd := make([]int64, n)
		beginAt := make([]int64, n)
		for i := 0; i < n; i++ {
			if v[i] == 0 {
				beginAt[i] = 0
				if len(logs[i]) > 0 {
					overwriteEnd[i] = logs[i][0].end
				} else {
					overwriteEnd[i] = math.MaxInt64
				}
				continue
			}
			k, ok := index[i][v[i]]
			if !ok {
				t.Fatalf("快照分量 %d 不是单元 %d 曾被写入的值 (向量 %v)", v[i], i, v)
			}
			beginAt[i] = logs[i][k].begin
			if k+1 < len(logs[i]) {
				overwriteEnd[i] = logs[i][k+1].end
			} else {
				overwriteEnd[i] = math.MaxInt64
			}
		}
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if overwriteEnd[i] < beginAt[j] {
					t.Fatalf("快照 %v 不可解释: 单元 %d 的值在时刻 %d 已被覆盖, 而单元 %d 的值直到时刻 %d 才写入, 不存在同时取到两者的时刻", v, i, overwriteEnd[i], j, beginAt[j])
				}
			}
		}
	}
	t.Logf("判定依据: 向量可解释当且仅当不存在单元 i,j 使 i 的旧值被覆盖先于 j 的值写入; %d 个向量全部可解释", len(vectors))
	if mc := s.MaxCollects(); mc > 2*n+1 {
		t.Fatalf("并发压力下收集次数最大值 %d 超过上界 %d", mc, 2*n+1)
	}
}
