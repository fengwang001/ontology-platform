package booking

import (
	"runtime"
	"sync"
	"testing"
)

// 非导出计数器验证：每个判定枚举的子集数恰为 2^(C-|mask|)。
func TestSubsetCounters(t *testing.T) {
	for _, tc := range []struct {
		c    int
		mask uint32
	}{
		{4, 0b0001}, {4, 0b0110}, {4, 0b1111},
		{10, 0b0000000001}, {10, 0b0000011111}, {10, 0b1111111111},
	} {
		stocks := make([]int64, tc.c)
		for i := range stocks {
			stocks[i] = 1 << 30
		}
		want := 1 << uint(tc.c-popcount(tc.mask))

		// 1) Book 可行：检查 T 包含 mask 的全部超集。
		b := mustNew(t, tc.c, stocks, 1000)
		b.ResetCounters()
		r := b.Book("x", tc.mask, 1)
		if r.SubsetsChecked != want || b.CounterSubsets() != int64(want) || b.CounterCalls() != 1 {
			t.Fatalf("c=%d mask=%b: Book subsets=%d counter=(%d,%d) want %d",
				tc.c, tc.mask, r.SubsetsChecked, b.CounterSubsets(), b.CounterCalls(), want)
		}

		// 2) Book 不可行进候补：同样枚举全部超集。
		b2 := mustNew(t, tc.c, stocks, 1000)
		b2.Book("occ", tc.mask, 1<<40) // 占满 mask 每个单元（rho=1000 时容量极大，改用比例）
		_ = b2

		// rho=100：occ 占满各单元容量，w=1 在任意相关子集上都超额 -> 候补。
		b3 := mustNew(t, tc.c, stocks, 100)
		b3.Book("occ", tc.mask, sumOf(stocks, tc.mask))
		b3.ResetCounters()
		if wr := b3.Book("w", tc.mask, 1); wr.OK {
			t.Fatalf("c=%d mask=%b: w should wait", tc.c, tc.mask)
		}
		if got := b3.CounterSubsets(); got != int64(want) {
			t.Fatalf("waiting subsets=%d want %d", got, want)
		}

		// 3) Cancel 触发递补，逐项枚举其 mask 的超集。
		total := len(b3.st.supersets[tc.mask])
		b3.ResetCounters()
		cr := b3.Cancel("occ")
		if cr.SubsetsChecked != total || b3.CounterSubsets() != int64(total) {
			t.Fatalf("promotion subsets=%d counter=%d want %d",
				cr.SubsetsChecked, b3.CounterSubsets(), total)
		}
		if len(cr.Promoted) != 1 || cr.Promoted[0] != "w" {
			t.Fatalf("promoted=%v", cr.Promoted)
		}

		// 4) Resize 变大：枚举超集。
		b4 := mustNew(t, tc.c, stocks, 100)
		b4.Book("r", tc.mask, sumOf(stocks, tc.mask))
		b4.ResetCounters()
		rr := b4.Resize("r", sumOf(stocks, tc.mask)+1)
		if rr.OK || rr.SubsetsChecked != want {
			t.Fatalf("resize subsets=%d ok=%v want %d", rr.SubsetsChecked, rr.OK, want)
		}
	}
}

func sumOf(stocks []int64, mask uint32) int64 {
	var s int64
	for j := range stocks {
		if mask&(1<<uint(j)) != 0 {
			s += stocks[j]
		}
	}
	return s
}

// C=4 与 C=10 两档：全部 2^C-1 个非空子集容量自洽，且状态始终可行。
func TestFullEnumerationC4C10(t *testing.T) {
	for _, c := range []int{4, 10} {
		stocks := make([]int64, c)
		for i := range stocks {
			stocks[i] = int64(1 + (i*7)%50)
		}
		b := mustNew(t, c, stocks, 120)
		var booked, waiting int
		for m := 1; m < 1<<uint(c); m++ {
			r := b.Book(idOf(m), uint32(m), 1)
			if r.OK {
				booked++
			} else {
				waiting++
			}
		}
		if !b.Feasible() {
			t.Fatalf("c=%d state infeasible (booked=%d waiting=%d)", c, booked, waiting)
		}
		// 扫描后不变量：每个候补项当前加入都不可行。
		for _, w := range b.WaitingContracts() {
			if b.waitingWouldFit(w.Mask, w.Qty) {
				t.Fatalf("c=%d waiting %s feasible after sweep", c, w.ID)
			}
		}
	}
}

func idOf(m int) string {
	return "m" + itoa(m)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// 并发调用：结果等价于某个串行顺序，且任意时刻观测到的状态可行。
func TestConcurrent(t *testing.T) {
	b := mustNew(t, 3, []int64{100, 100, 100}, 100)
	var obsWG, mutWG sync.WaitGroup
	stop := make(chan struct{})
	var observerErrors []string
	var errMu sync.Mutex
	report := func(msg string) {
		errMu.Lock()
		observerErrors = append(observerErrors, msg)
		errMu.Unlock()
	}

	observers := 8
	obsWG.Add(observers)
	for i := 0; i < observers; i++ {
		go func() {
			defer obsWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if !b.consistentSnapshot() {
						report("observed inconsistent intermediate state")
					}
					runtime.Gosched()
				}
			}
		}()
	}

	mutators := 8
	mutWG.Add(mutators)
	for g := 0; g < mutators; g++ {
		go func(g int) {
			defer mutWG.Done()
			for k := 0; k < 300; k++ {
				id := idOf(g*1000 + k)
				r := b.Book(id, uint32(1<<(k%3)), int64(1+(k%40)))
				if !r.OK && r.Phase != PhaseWaiting {
					continue
				}
				switch k % 4 {
				case 0:
					b.Cancel(id)
				case 1:
					b.Resize(id, int64(1+(k%30)))
				case 2:
					b.Retarget(id, uint32(1<<((k+1)%3)))
				}
			}
		}(g)
	}
	mutWG.Wait()
	close(stop)
	obsWG.Wait()
	for _, msg := range observerErrors {
		t.Error(msg)
	}
	if !b.Feasible() {
		t.Fatal("final state infeasible")
	}
}

// Retarget 成功时检查的子集恰为 2^(C-|m2|) - 2^(C-|m1∪m2|)。
func TestRetargetSubsetCount(t *testing.T) {
	for _, tc := range []struct {
		c                int
		oldMask, newMask uint32
	}{
		{4, 0b0001, 0b0010}, // 不相交
		{4, 0b0011, 0b1101}, // 交叉
		{4, 0b0001, 0b0011}, // 纯扩大
		{4, 0b1100, 0b0100}, // 纯缩小
		{10, 0b0000000001, 0b0000000010},
		{10, 0b0000001111, 0b0001111000},
	} {
		stocks := make([]int64, tc.c)
		for i := range stocks {
			stocks[i] = 1 << 30
		}
		b := mustNew(t, tc.c, stocks, 1000)
		if r := b.Book("a", tc.oldMask, 1); !r.OK {
			t.Fatal(r.Reason)
		}
		b.ResetCounters()
		r := b.Retarget("a", tc.newMask)
		if !r.OK {
			t.Fatalf("c=%d %b->%b retarget: %v", tc.c, tc.oldMask, tc.newMask, r.Reason)
		}
		want := 1<<uint(tc.c-popcount(tc.newMask)) -
			1<<uint(tc.c-popcount(tc.oldMask|tc.newMask))
		if r.SubsetsChecked != want {
			t.Fatalf("c=%d %b->%b: retarget feasibility subsets=%d want %d",
				tc.c, tc.oldMask, tc.newMask, r.SubsetsChecked, want)
		}
	}
}
