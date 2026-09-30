package scheduler

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// logf 统一打印测试输入、输出与判定依据。
func logf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf(format, args...)
}

func mustNew(t *testing.T, cfg Config) *Scheduler {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v) unexpected error: %v", cfg, err)
	}
	return s
}

func mustSubmit(t *testing.T, s *Scheduler, req Request) {
	t.Helper()
	if err := s.Submit(req); err != nil {
		t.Fatalf("Submit(%+v) unexpected error: %v", req, err)
	}
	logf(t, "输入 Submit %+v -> 输出 nil", req)
}

func mustDispatch(t *testing.T, s *Scheduler, now int64, wantID string, wantDir Direction, wantReason Reason, wantForced bool) DispatchResult {
	t.Helper()
	res, err := s.Dispatch(now)
	if err != nil {
		t.Fatalf("Dispatch(now=%d) unexpected error: %v", now, err)
	}
	logf(t, "输入 Dispatch(now=%d) -> 输出 id=%s dir=%s reason=%s forced=%v head=%d",
		now, res.Request.ID, res.Direction, res.Reason, res.ForcedWrite, res.HeadPosition)
	if res.Request.ID != wantID || res.Direction != wantDir || res.Reason != wantReason || res.ForcedWrite != wantForced {
		t.Fatalf("Dispatch(now=%d) = id=%s dir=%s reason=%s forced=%v; want id=%s dir=%s reason=%s forced=%v",
			now, res.Request.ID, res.Direction, res.Reason, res.ForcedWrite,
			wantID, wantDir, wantReason, wantForced)
	}
	if res.HeadPosition != res.Request.Sector {
		t.Fatalf("head %d != dispatched sector %d", res.HeadPosition, res.Request.Sector)
	}
	return res
}

// 恰等到期时长（now-submitted == Er，等于也算到期）。
func TestExactExpiry(t *testing.T) {
	s := mustNew(t, Config{ReadExpire: 5, WriteExpire: 5, WriteStarve: 2})
	mustSubmit(t, s, Request{ID: "r1", Sector: 10, Submitted: 0})
	mustSubmit(t, s, Request{ID: "r2", Sector: 1, Submitted: 0})

	// now=5：队首 r1 恰满 Er=5，判到期直接派发，跳过扇区更小的 r2。
	mustDispatch(t, s, 5, "r1", Read, Expired, false)

	snap := s.Query()
	logf(t, "输入 Query() -> 输出 head=%d starve=%d reads=%v writes=%v",
		snap.Head, snap.StarveCount, snap.ReadQueue, snap.WriteQueue)
	if snap.Head != 10 || snap.StarveCount != 0 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
}

// 到期队首可以越过扫描位置优先派发。
func TestExpiredBypassesScanPosition(t *testing.T) {
	s := mustNew(t, Config{ReadExpire: 10, WriteExpire: 10, WriteStarve: 5})
	mustSubmit(t, s, Request{ID: "old", Sector: 2, Submitted: 0})
	mustSubmit(t, s, Request{ID: "young", Sector: 8, Submitted: 9})

	// head=0；old 已到期，虽然 young 扇区更大也必须先派到期队首。
	mustDispatch(t, s, 10, "old", Read, Expired, false)
	mustDispatch(t, s, 10, "young", Read, Scan, false)
}

// 写队列为空时派发读不累加饿计数。
func TestStarveNotCountedWhenWriteEmpty(t *testing.T) {
	s := mustNew(t, Config{ReadExpire: 100, WriteExpire: 100, WriteStarve: 1})
	mustSubmit(t, s, Request{ID: "r1", Sector: 3, Submitted: 0})
	mustDispatch(t, s, 1, "r1", Read, Scan, false)
	if got := s.Query().StarveCount; got != 0 {
		t.Fatalf("starve count = %d, want 0", got)
	}
	logf(t, "写队列为空派发读后饿计数=0，判定依据：派发读而写队列为空时饿计数不变")

	// 此后写队列到来，X=1；先读一次（饿计数变为 1），下一次必须强制写。
	mustSubmit(t, s, Request{ID: "w1", Sector: 1, Write: true, Submitted: 1})
	mustSubmit(t, s, Request{ID: "r2", Sector: 4, Submitted: 1})
	mustSubmit(t, s, Request{ID: "r3", Sector: 6, Submitted: 1})
	mustDispatch(t, s, 2, "r2", Read, Scan, false)
	// head=4，写请求扇区 1 小于磁头，强制写后在写方向内回绕。
	mustDispatch(t, s, 3, "w1", Write, WrapAround, true)
	// 强制写后饿计数清零，剩余读恢复正常扫描。
	mustDispatch(t, s, 4, "r3", Read, Scan, false)
}

// 恰满 X 次连续优先后强制选写。
func TestForcedWriteAtExactX(t *testing.T) {
	s := mustNew(t, Config{ReadExpire: 100, WriteExpire: 100, WriteStarve: 2})
	mustSubmit(t, s, Request{ID: "w", Sector: 1, Write: true, Submitted: 0})
	mustSubmit(t, s, Request{ID: "r1", Sector: 5, Submitted: 0})
	mustSubmit(t, s, Request{ID: "r2", Sector: 6, Submitted: 0})
	mustSubmit(t, s, Request{ID: "r3", Sector: 7, Submitted: 0})

	mustDispatch(t, s, 1, "r1", Read, Scan, false)
	if s.Query().StarveCount != 1 {
		t.Fatalf("starve = %d, want 1", s.Query().StarveCount)
	}
	mustDispatch(t, s, 2, "r2", Read, Scan, false)
	if s.Query().StarveCount != 2 {
		t.Fatalf("starve = %d, want 2", s.Query().StarveCount)
	}
	// 饿计数恰等于 X=2，读队列仍非空也强制选写。
	// head=6，写扇区 1 小于磁头，写方向内按回绕选取。
	mustDispatch(t, s, 3, "w", Write, WrapAround, true)
	if s.Query().StarveCount != 0 {
		t.Fatalf("starve after write = %d, want 0", s.Query().StarveCount)
	}
	// 写已派发、写队列空，恢复正常选读。
	mustDispatch(t, s, 4, "r3", Read, Scan, false)
}

// 没有扇区不小于磁头位置的请求时回绕到最小扇区。
func TestWrapAround(t *testing.T) {
	s := mustNew(t, Config{ReadExpire: 100, WriteExpire: 100, WriteStarve: 5})
	mustSubmit(t, s, Request{ID: "r2", Sector: 2, Submitted: 0})
	mustSubmit(t, s, Request{ID: "r3", Sector: 5, Submitted: 0})
	mustSubmit(t, s, Request{ID: "r1", Sector: 9, Submitted: 0})

	// head=0 时三个扇区都不小于磁头，扫描取最小扇区 2。
	mustDispatch(t, s, 1, "r2", Read, Scan, false) // head -> 2
	mustDispatch(t, s, 2, "r3", Read, Scan, false) // head -> 5
	// 仅剩扇区 9，仍不小于磁头，继续扫描。
	mustDispatch(t, s, 3, "r1", Read, Scan, false) // head -> 9

	// 再来一批全部小于当前磁头 9 的请求，必须回绕到最小扇区。
	mustSubmit(t, s, Request{ID: "b1", Sector: 4, Submitted: 3})
	mustSubmit(t, s, Request{ID: "b2", Sector: 1, Submitted: 3})
	mustDispatch(t, s, 4, "b2", Read, WrapAround, false)
	mustDispatch(t, s, 5, "b1", Read, Scan, false)
}

// 同扇区取提交在先者（提交时刻相同再按提交序号）。
func TestSameSectorSubmitOrder(t *testing.T) {
	s := mustNew(t, Config{ReadExpire: 100, WriteExpire: 100, WriteStarve: 5})
	mustSubmit(t, s, Request{ID: "a", Sector: 7, Submitted: 0})
	mustSubmit(t, s, Request{ID: "b", Sector: 7, Submitted: 0})
	mustSubmit(t, s, Request{ID: "c", Sector: 7, Submitted: 5})

	mustDispatch(t, s, 10, "a", Read, Scan, false)
	mustDispatch(t, s, 11, "b", Read, Scan, false)
	mustDispatch(t, s, 12, "c", Read, Scan, false)
}

// 取消：待派发可取消；不存在与已派发原因可区分；被拒操作不改状态。
func TestCancel(t *testing.T) {
	s := mustNew(t, Config{ReadExpire: 100, WriteExpire: 100, WriteStarve: 2})
	mustSubmit(t, s, Request{ID: "r1", Sector: 3, Submitted: 0})
	mustSubmit(t, s, Request{ID: "w1", Sector: 4, Write: true, Submitted: 0})

	if err := s.Cancel("w1", 1); err != nil {
		t.Fatalf("Cancel(w1) unexpected error: %v", err)
	}
	logf(t, "输入 Cancel(id=w1, now=1) -> 输出 nil")
	if q := s.Query().WriteQueue; len(q) != 0 {
		t.Fatalf("write queue not empty after cancel: %v", q)
	}

	err := s.Cancel("ghost", 2)
	logf(t, "输入 Cancel(id=ghost, now=2) -> 输出 %v", err)
	if !errors.Is(err, ErrCancelNotFound) {
		t.Fatalf("ghost cancel = %v, want ErrCancelNotFound", err)
	}

	mustDispatch(t, s, 3, "r1", Read, Scan, false)
	err = s.Cancel("r1", 4)
	logf(t, "输入 Cancel(id=r1, now=4) -> 输出 %v", err)
	if !errors.Is(err, ErrAlreadyDispatched) {
		t.Fatalf("cancel dispatched = %v, want ErrAlreadyDispatched", err)
	}

	// 重复取消已取消请求同样报已终结，且标识不可复用。
	if err := s.Cancel("w1", 5); !errors.Is(err, ErrAlreadyDispatched) {
		t.Fatalf("re-cancel = %v, want ErrAlreadyDispatched", err)
	}
	err = s.Submit(Request{ID: "w1", Sector: 9, Write: true, Submitted: 5})
	logf(t, "输入 Submit(复用标识 w1) -> 输出 %v", err)
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("reuse id = %v, want ErrDuplicateID", err)
	}
}

// 提交参数校验与多因优先级：时钟回拨、扇区为负、标识重复。
func TestSubmitRejections(t *testing.T) {
	s := mustNew(t, Config{ReadExpire: 1, WriteExpire: 1, WriteStarve: 1})
	mustSubmit(t, s, Request{ID: "x", Sector: 0, Submitted: 10})

	// 时钟回拨优先于负扇区与重复标识。
	err := s.Submit(Request{ID: "x", Sector: -5, Submitted: 9})
	logf(t, "输入 Submit(回拨+负扇区+重复) -> 输出 %v", err)
	if !errors.Is(err, ErrClockRewind) {
		t.Fatalf("got %v, want ErrClockRewind", err)
	}
	// 时刻合法时负扇区优先于重复。
	err = s.Submit(Request{ID: "x", Sector: -5, Submitted: 10})
	logf(t, "输入 Submit(负扇区+重复) -> 输出 %v", err)
	if !errors.Is(err, ErrNegativeSector) {
		t.Fatalf("got %v, want ErrNegativeSector", err)
	}
	err = s.Submit(Request{ID: "x", Sector: 5, Submitted: 10})
	logf(t, "输入 Submit(重复标识) -> 输出 %v", err)
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("got %v, want ErrDuplicateID", err)
	}

	// 被拒操作不改变磁头、饿计数与队列。
	snap := s.Query()
	if snap.Head != 0 || len(snap.ReadQueue) != 1 || snap.StarveCount != 0 {
		t.Fatalf("state changed after rejected submits: %+v", snap)
	}
}

// 取消与派发：时钟回拨优先于其余原因；队列全空派发被拒绝。
func TestCancelDispatchClockFirst(t *testing.T) {
	s := mustNew(t, Config{ReadExpire: 100, WriteExpire: 100, WriteStarve: 1})
	mustSubmit(t, s, Request{ID: "r", Sector: 1, Submitted: 5})

	if err := s.Cancel("missing", 4); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("cancel clock = %v, want ErrClockRewind", err)
	}
	if _, err := s.Dispatch(4); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("dispatch clock = %v, want ErrClockRewind", err)
	}

	mustDispatch(t, s, 6, "r", Read, Scan, false)
	if _, err := s.Dispatch(7); !errors.Is(err, ErrNoPendingRequests) {
		t.Fatalf("empty dispatch = %v, want ErrNoPendingRequests", err)
	}
	if snap := s.Query(); snap.Head != 1 || snap.StarveCount != 0 {
		t.Fatalf("state changed on empty dispatch: %+v", snap)
	}
}

func TestInvalidConfig(t *testing.T) {
	cases := []Config{
		{ReadExpire: 0, WriteExpire: 1, WriteStarve: 1},
		{ReadExpire: 1, WriteExpire: -1, WriteStarve: 1},
		{ReadExpire: 1, WriteExpire: 1, WriteStarve: 0},
	}
	for i, cfg := range cases {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d New(%+v) = %v, want ErrInvalidConfig", i, cfg, err)
		}
	}
}

// 写到期同样按队首判定（恰等也算到期），且可越过扫描位置。
func TestWriteExpiry(t *testing.T) {
	s := mustNew(t, Config{ReadExpire: 100, WriteExpire: 4, WriteStarve: 10})
	mustSubmit(t, s, Request{ID: "w1", Sector: 9, Write: true, Submitted: 0})
	mustSubmit(t, s, Request{ID: "w2", Sector: 1, Write: true, Submitted: 0})
	mustDispatch(t, s, 4, "w1", Write, Expired, false)
	// w2 同样等待满 Ew=4（5-0>=4 且恰为队首），按到期派发。
	mustDispatch(t, s, 5, "w2", Write, Expired, false)
}

// 并发调用：每个请求恰好被派发一次或被取消一次。
func TestConcurrentExactlyOnce(t *testing.T) {
	s := mustNew(t, Config{ReadExpire: 50, WriteExpire: 50, WriteStarve: 3})
	const n = 200

	// 先串行提交（保证时刻单调），再并发派发与取消竞争。
	for i := 0; i < n; i++ {
		mustSubmit(t, s, Request{
			ID:        string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + itoa(i),
			Sector:    int64(i * 7 % 137),
			Write:     i%3 == 0,
			Submitted: int64(i),
		})
	}

	var dispatched, cancelled sync.Map
	var wg sync.WaitGroup
	start := make(chan struct{})
	cancelDone := make(chan struct{})
	// 所有操作共享单调时钟：取消与派发并发交错时只增不减。
	var clock atomic.Int64
	clock.Store(n)
	// tickDispatch 在锁竞争导致的时钟回拨时用更新的时刻重试。
	tickDispatch := func() (DispatchResult, bool) {
		for attempt := 0; attempt < 64; attempt++ {
			res, err := s.Dispatch(clock.Add(1))
			if errors.Is(err, ErrClockRewind) {
				continue
			}
			if err != nil {
				return DispatchResult{}, false
			}
			return res, true
		}
		return DispatchResult{}, false
	}
	tickCancel := func(id string) error {
		for attempt := 0; attempt < 64; attempt++ {
			err := s.Cancel(id, clock.Add(1))
			if errors.Is(err, ErrClockRewind) {
				continue
			}
			return err
		}
		return ErrClockRewind
	}

	// 派发者：持续派发直到两队列清空。
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for {
			res, ok := tickDispatch()
			if !ok {
				// 空队列可能是取消者连续取消造成的短暂窗口：
				// 先等取消者全部结束，再做一次最终排空，确认无误后退出。
				<-cancelDone
				res, ok = tickDispatch()
				if !ok {
					return
				}
			}
			if _, loaded := dispatched.LoadOrStore(res.Request.ID, struct{}{}); loaded {
				t.Errorf("request %s dispatched twice", res.Request.ID)
			}
		}
	}()

	// 取消者与派发者竞争；无论成功与否，同一请求不得既被派发又被取消。
	cancelErr := make(chan error, n)
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < n; i += 2 {
			id := string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + itoa(i)
			err := tickCancel(id)
			if err == nil {
				cancelled.Store(id, struct{}{})
			} else if !errors.Is(err, ErrAlreadyDispatched) && !errors.Is(err, ErrCancelNotFound) {
				cancelErr <- err
			}
		}
		close(cancelDone)
	}()

	close(start)
	wg.Wait()
	close(cancelErr)
	for err := range cancelErr {
		t.Fatalf("unexpected cancel error: %v", err)
	}

	count := 0
	for i := 0; i < n; i++ {
		id := string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + itoa(i)
		_, d := dispatched.Load(id)
		_, c := cancelled.Load(id)
		if i%2 == 1 {
			// 取消者只尝试偶数下标；奇数下标必然由派发者派发。
			if !d {
				t.Fatalf("request %s not dispatched", id)
			}
			count++
			continue
		}
		if d == c { // 两者同真或同假
			t.Fatalf("request %s dispatched=%v cancelled=%v, want exactly one", id, d, c)
		}
		count++
	}
	if snap := s.Query(); len(snap.ReadQueue)+len(snap.WriteQueue) != 0 {
		t.Fatalf("queues not drained: %+v", snap)
	}
	logf(t, "并发结束：%d 个请求恰好派发一次或取消一次，两队列已清空", count)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// 相同序列重放必须得到完全相同的派发顺序与依据。
func TestDeterministicReplay(t *testing.T) {
	cfg := Config{ReadExpire: 7, WriteExpire: 9, WriteStarve: 2}
	build := func() ([]Request, []int64) {
		reqs := []Request{
			{ID: "w1", Sector: 11, Write: true, Submitted: 0},
			{ID: "r1", Sector: 3, Submitted: 0},
			{ID: "r2", Sector: 8, Submitted: 1},
			{ID: "r3", Sector: 2, Submitted: 2},
			{ID: "w2", Sector: 1, Write: true, Submitted: 2},
			{ID: "r4", Sector: 8, Submitted: 2},
		}
		times := []int64{3, 4, 5, 9, 10, 11}
		return reqs, times
	}

	run := func() []DispatchResult {
		s := mustNew(t, cfg)
		reqs, times := build()
		for _, req := range reqs {
			mustSubmit(t, s, req)
		}
		var got []DispatchResult
		for _, now := range times {
			res, err := s.Dispatch(now)
			if err != nil {
				t.Fatalf("dispatch at %d: %v", now, err)
			}
			got = append(got, res)
		}
		return got
	}

	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatalf("replay length mismatch: %d vs %d", len(first), len(second))
	}
	for i := range first {
		a, b := first[i], second[i]
		if a.Request.ID != b.Request.ID || a.Direction != b.Direction ||
			a.Reason != b.Reason || a.ForcedWrite != b.ForcedWrite || a.HeadPosition != b.HeadPosition {
			t.Fatalf("replay differs at %d: %+v vs %+v", i, a, b)
		}
		logf(t, "重放第 %d 次派发一致：id=%s dir=%s reason=%s forced=%v head=%d",
			i+1, a.Request.ID, a.Direction, a.Reason, a.ForcedWrite, a.HeadPosition)
	}
}
