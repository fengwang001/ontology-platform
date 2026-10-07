package cardinality

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 核心不变式：约束作用域上"已确认关联 + 存活进行中预留"永不超过上限。
func assertNeverOverLimit(t *testing.T, m *Manager, limit int64) {
	t.Helper()
	committed, _ := m.Committed(testScope)
	inflight := m.Stats().Inflight
	if committed+int64(inflight) > limit {
		t.Fatalf("cardinality violated: committed=%d inflight=%d limit=%d",
			committed, inflight, limit)
	}
}

// 名额恰好剩一个时 N 方严格同时争夺：恰好一方获批，其余全部得到
// 明确的名额不足拒绝；既不超卖，也不出现名额空闲却全部被拒。
func TestConcurrentSingleSlotContention(t *testing.T) {
	const n = 64
	for iter := 0; iter < 200; iter++ {
		clk := NewFakeClock(int64(iter))
		m := NewManager(time.Hour, clk)
		if err := m.EnsureScope(testScope, 1, 0); err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		var accepted int64
		results := make([]ReasonCode, n)
		wg.Add(n)
		for i := 0; i < n; i++ {
			i := i
			go func() {
				defer wg.Done()
				<-start
				d, _ := m.Reserve(ReserveRequest{
					Scope:         testScope,
					ExpectedV:     0,
					ReservationID: rid(iter, i),
				})
				results[i] = d.Reason
				if d.Accepted {
					atomic.AddInt64(&accepted, 1)
				}
			}()
		}
		close(start)
		wg.Wait()

		if accepted != 1 {
			t.Fatalf("iter %d: want exactly 1 accepted, got %d", iter, accepted)
		}
		rejected := 0
		for i, r := range results {
			if r == ReasonInflightOccupied {
				rejected++
				continue
			}
			if r != ReasonNone {
				t.Fatalf("iter %d goroutine %d unexpected reason %v", iter, i, r)
			}
		}
		if rejected != n-1 {
			t.Fatalf("iter %d: want %d inflight-occupied, got %d", iter, n-1, rejected)
		}
		assertNeverOverLimit(t, m, 1)

		// 唯一获批者可以提交，最终关联数恰好为 1。
		commits := 0
		for i := 0; i < n; i++ {
			if m.Commit(rid(iter, i)) {
				commits++
			}
		}
		if commits != 1 {
			t.Fatalf("want 1 commit, got %d", commits)
		}
		if got, _ := m.Committed(testScope); got != 1 {
			t.Fatalf("want committed 1, got %d", got)
		}
	}
}

func rid(iter, i int) string {
	return "r-" + itoa(iter) + "-" + itoa(i)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// 进行中请求失败（Abort）后名额释放的瞬时窗口：释放原子可见，
// 等待中的新请求恰有一个拿到名额，其余继续被明确拒绝。
func TestAbortReleaseInstantWindow(t *testing.T) {
	for iter := 0; iter < 100; iter++ {
		clk := NewFakeClock(int64(iter))
		m := NewManager(time.Hour, clk)
		if err := m.EnsureScope(testScope, 1, 0); err != nil {
			t.Fatal(err)
		}
		d, _ := m.Reserve(ReserveRequest{Scope: testScope, ExpectedV: 0, ReservationID: "holder"})
		if !d.Accepted {
			t.Fatal("holder reserve")
		}

		// 先制造一批"释放前"到达并被拒的请求，它们绝不允许被自动唤醒。
		var preRejected int64
		for i := 0; i < 16; i++ {
			dd := reserve(t, m, "pre-"+itoa(iter)+"-"+itoa(i), 0)
			if dd.Reason == ReasonInflightOccupied {
				preRejected++
			}
		}
		if preRejected != 16 {
			t.Fatalf("all pre-abort requests must be rejected, got %d", preRejected)
		}

		const w = 64
		start := make(chan struct{})
		release := make(chan struct{})
		var wg sync.WaitGroup
		var accepted int64
		wg.Add(w + 1)

		// 竞争者全部先阻塞在屏障上；中止者与它们同时放行，
		// 竞争"释放窗口"后的唯一名额。
		for i := 0; i < w; i++ {
			i := i
			go func() {
				defer wg.Done()
				<-start
				<-release
				dd, _ := m.Reserve(ReserveRequest{
					Scope:         testScope,
					ExpectedV:     0,
					ReservationID: "win-" + itoa(iter) + "-" + itoa(i),
				})
				if dd.Accepted {
					atomic.AddInt64(&accepted, 1)
				}
			}()
		}
		go func() {
			defer wg.Done()
			<-start
			<-release
			m.Abort("holder")
		}()

		close(start)
		time.Sleep(time.Millisecond)
		close(release)
		wg.Wait()

		// 调度允许两种合法串行化：中止先于部分竞争者（恰 1 人抢到），
		// 或中止排在所有竞争者之后（0 人抢到）。二者都合法，但绝不允许多于 1。
		if accepted < 0 || accepted > 1 {
			t.Fatalf("iter %d: at most one waiter may grab released slot, got %d", iter, accepted)
		}
		assertNeverOverLimit(t, m, 1)

		// 释放之后的新到达请求必须立刻感知释放：没有不确定窗口。
		// 若已有竞争者抢到名额，则看到"进行中占用"；否则自己抢到。
		dd := reserve(t, m, "late-"+itoa(iter), 0)
		if accepted == 1 {
			if dd.Reason != ReasonInflightOccupied {
				t.Fatalf("post-window arrival must see slot taken, got %v", dd.Reason)
			}
		} else if !dd.Accepted {
			t.Fatalf("released slot must be immediately visible, got %v", dd.Reason)
		}

		// 早先被拒的请求没有被自动重试：它们的 ID 从未存在过。
		for i := 0; i < 16; i++ {
			if m.Commit("pre-" + itoa(iter) + "-" + itoa(i)) {
				t.Fatal("previously rejected request was secretly retried")
			}
		}
	}
}

// 连续多轮争夺：每轮"并发预留 → 部分提交/部分中止"，
// 整个过程中及结束后关联总数始终不超上限，且最终不无故浪费名额。
func TestRepeatedRoundsNeverOverLimit(t *testing.T) {
	const limit = 7
	const rounds = 40
	const contenders = 32

	clk := NewFakeClock(0)
	m := NewManager(time.Hour, clk)
	if err := m.EnsureScope(testScope, limit, 0); err != nil {
		t.Fatal(err)
	}

	idSeq := int64(0)
	for round := 0; round < rounds; round++ {
		start := make(chan struct{})
		var wg sync.WaitGroup
		var mu sync.Mutex
		granted := make([]string, 0, contenders)
		wg.Add(contenders)
		for i := 0; i < contenders; i++ {
			go func() {
				defer wg.Done()
				<-start
				id := "round-r" + itoa(round) + "-" + itoa(int(atomic.AddInt64(&idSeq, 1)))
				committed, _ := m.Committed(testScope)
				d, _ := m.Reserve(ReserveRequest{
					Scope:         testScope,
					ExpectedV:     committed,
					ReservationID: id,
				})
				if d.Accepted {
					mu.Lock()
					granted = append(granted, id)
					mu.Unlock()
				}
			}()
		}
		close(start)
		wg.Wait()
		assertNeverOverLimit(t, m, limit)

		// 每轮提交约一半获批者，其余中止；交错终结以制造释放窗口。
		for j, id := range granted {
			if j%2 == 0 {
				if !m.Commit(id) {
					t.Fatalf("round %d commit %s", round, id)
				}
			} else {
				if !m.Abort(id) {
					t.Fatalf("round %d abort %s", round, id)
				}
			}
			assertNeverOverLimit(t, m, limit)
		}

		got, _ := m.Committed(testScope)
		if got > limit {
			t.Fatalf("round %d committed %d exceeds limit %d", round, got, limit)
		}
	}

	committed, _ := m.Committed(testScope)
	if committed != limit {
		t.Fatalf("after enough rounds total should saturate at limit, got %d", committed)
	}
	if st := m.Stats(); st.Inflight != 0 {
		t.Fatalf("no inflight should remain, got %d", st.Inflight)
	}
}
