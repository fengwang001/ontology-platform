package scheduler

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// testLogger 用 bytes.Buffer 记录输入、输出与判定依据，便于断言日志内容。
type testLogger struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *testLogger) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.buf, format+"\n", args...)
}

func (l *testLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func newTestScheduler(t *testing.T, cfg Config) (*Scheduler, *testLogger) {
	t.Helper()
	log := &testLogger{}
	s, err := New(cfg, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, log
}

func mustSubmit(t *testing.T, s *Scheduler, id string, sector int64, op Op, at int64) {
	t.Helper()
	if err := s.Submit(Request{ID: id, Sector: sector, Op: op, SubmitAt: at}); err != nil {
		t.Fatalf("Submit(%s): %v", id, err)
	}
}

func mustDispatch(t *testing.T, s *Scheduler, now int64, wantID string, wantReason Reason, wantForced bool) DispatchResult {
	t.Helper()
	res, err := s.Dispatch(now)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Req.ID != wantID || res.Reason != wantReason || res.WriteForced != wantForced {
		t.Fatalf("Dispatch = id=%s reason=%s forced=%t; want id=%s reason=%s forced=%t",
			res.Req.ID, res.Reason, res.WriteForced, wantID, wantReason, wantForced)
	}
	return res
}

// 恰等到期时长：now-submitAt == Er 即算到期。
func TestDeadlineExactlyEqual(t *testing.T) {
	s, _ := newTestScheduler(t, Config{ReadDeadline: 10, WriteDeadline: 10, WriteStarveX: 5})
	mustSubmit(t, s, "r1", 100, Read, 0)
	mustDispatch(t, s, 10, "r1", ReasonDeadline, false)
}

// 到期请求越过扫描位置：队首到期时即使扇区小于磁头位置也优先派发。
func TestDeadlineJumpsOverScanPosition(t *testing.T) {
	s, _ := newTestScheduler(t, Config{ReadDeadline: 10, WriteDeadline: 10, WriteStarveX: 5})
	mustSubmit(t, s, "w0", 50, Write, 0)
	mustDispatch(t, s, 0, "w0", ReasonScan, false)

	mustSubmit(t, s, "r1", 10, Read, 5)
	mustSubmit(t, s, "r2", 60, Read, 5)
	mustDispatch(t, s, 15, "r1", ReasonDeadline, false)
	mustDispatch(t, s, 15, "r2", ReasonDeadline, false)

	// r2 已派发，head=60。新对照组在 t=16 提交、t=17 未到期（Er=10），
	// 扫描取不小于 60 的最小扇区者 r3（sector=70），而非提交在先但更远的 r4。
	mustSubmit(t, s, "r4", 90, Read, 16)
	mustSubmit(t, s, "r3", 70, Read, 16)
	mustDispatch(t, s, 17, "r3", ReasonScan, false)
}

// 饿计数在写队列为空时不累加。
func TestStarveCountNotAccumulatedWithoutWrites(t *testing.T) {
	s, _ := newTestScheduler(t, Config{ReadDeadline: 1000, WriteDeadline: 1, WriteStarveX: 2})
	mustSubmit(t, s, "r1", 10, Read, 0)
	mustSubmit(t, s, "r2", 20, Read, 0)
	mustDispatch(t, s, 0, "r1", ReasonScan, false)
	// r1 把磁头带到 10，r2(20) 仍在扫描方向上（两次派发时写队列都为空，饿计数不变）。
	mustDispatch(t, s, 0, "r2", ReasonScan, false)

	mustSubmit(t, s, "w1", 30, Write, 1)
	mustSubmit(t, s, "r3", 40, Read, 1)
	mustDispatch(t, s, 1, "r3", ReasonScan, false)
}

// 恰满 X 后强制写：X=2 时连续两次读优先，第三次派发强制选写。
func TestForceWriteAtExactlyX(t *testing.T) {
	s, _ := newTestScheduler(t, Config{ReadDeadline: 1000, WriteDeadline: 1000, WriteStarveX: 2})
	mustSubmit(t, s, "r1", 10, Read, 0)
	mustSubmit(t, s, "r2", 20, Read, 0)
	mustSubmit(t, s, "r3", 30, Read, 0)
	mustSubmit(t, s, "r4", 40, Read, 0)
	mustSubmit(t, s, "w1", 50, Write, 0)
	mustSubmit(t, s, "w2", 60, Write, 0)

	mustDispatch(t, s, 0, "r1", ReasonScan, false)
	mustDispatch(t, s, 0, "r2", ReasonScan, false)
	mustDispatch(t, s, 0, "w1", ReasonScan, true)
	// 写派发后 starve 清零，读重新被优先；磁头已在 50，r3(30) 只能回绕。
	mustDispatch(t, s, 0, "r3", ReasonWrap, false)

	if got := s.Snapshot().StarveCount; got != 1 {
		t.Fatalf("starve after sequence = %d, want 1", got)
	}
}

// 回绕：该方向没有扇区不小于磁头位置时，取最小扇区。
func TestWrapAround(t *testing.T) {
	s, _ := newTestScheduler(t, Config{ReadDeadline: 1000, WriteDeadline: 1000, WriteStarveX: 5})
	mustSubmit(t, s, "w0", 50, Write, 0)
	mustDispatch(t, s, 0, "w0", ReasonScan, false)

	mustSubmit(t, s, "r1", 10, Read, 1)
	mustSubmit(t, s, "r2", 20, Read, 1)
	mustDispatch(t, s, 1, "r1", ReasonWrap, false)
}

// 同扇区取提交在先者。
func TestSameSectorFIFO(t *testing.T) {
	s, _ := newTestScheduler(t, Config{ReadDeadline: 1000, WriteDeadline: 1000, WriteStarveX: 5})
	mustSubmit(t, s, "r1", 10, Read, 5)
	mustSubmit(t, s, "r2", 10, Read, 5)
	mustSubmit(t, s, "r3", 10, Read, 5)
	mustDispatch(t, s, 5, "r1", ReasonScan, false)
	mustDispatch(t, s, 5, "r2", ReasonScan, false)
	mustDispatch(t, s, 5, "r3", ReasonScan, false)
}

// 取消：成功取消、取消不存在、取消已取消、取消已派发原因可区分。
func TestCancel(t *testing.T) {
	s, _ := newTestScheduler(t, Config{ReadDeadline: 1000, WriteDeadline: 1000, WriteStarveX: 5})
	mustSubmit(t, s, "r1", 10, Read, 0)
	mustSubmit(t, s, "w1", 20, Write, 0)

	if err := s.Cancel("r1", 1); err != nil {
		t.Fatalf("Cancel pending: %v", err)
	}
	if st := s.Snapshot(); len(st.ReadQueue) != 0 || st.CanceledCount != 1 {
		t.Fatalf("after cancel: reads=%d canceled=%d", len(st.ReadQueue), st.CanceledCount)
	}
	if err := s.Cancel("ghost", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Cancel unknown = %v, want ErrNotFound", err)
	}
	if err := s.Cancel("r1", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Cancel canceled = %v, want ErrNotFound", err)
	}

	mustDispatch(t, s, 2, "w1", ReasonScan, false)
	if err := s.Cancel("w1", 3); !errors.Is(err, ErrAlreadyDispatched) {
		t.Fatalf("Cancel dispatched = %v, want ErrAlreadyDispatched", err)
	}
}

// 每个请求恰好派发一次；派发后离开全部队列；全空派发被拒绝。
func TestDispatchExactlyOnce(t *testing.T) {
	s, _ := newTestScheduler(t, Config{ReadDeadline: 1000, WriteDeadline: 1000, WriteStarveX: 1})
	ids := []string{"r1", "r2", "w1", "r3", "w2"}
	mustSubmit(t, s, "r1", 1, Read, 0)
	mustSubmit(t, s, "r2", 2, Read, 0)
	mustSubmit(t, s, "w1", 3, Write, 0)
	mustSubmit(t, s, "r3", 4, Read, 0)
	mustSubmit(t, s, "w2", 5, Write, 0)

	seen := map[string]int{}
	for i := 0; i < len(ids); i++ {
		res, err := s.Dispatch(int64(i))
		if err != nil {
			t.Fatalf("Dispatch %d: %v", i, err)
		}
		seen[res.Req.ID]++
	}
	for _, id := range ids {
		if seen[id] != 1 {
			t.Fatalf("id=%s dispatched %d times, want 1", id, seen[id])
		}
	}
	st := s.Snapshot()
	if len(st.ReadQueue) != 0 || len(st.WriteQueue) != 0 || st.DispatchedCount != 5 {
		t.Fatalf("queues not drained: %+v", st)
	}
	if _, err := s.Dispatch(6); !errors.Is(err, ErrEmptyQueue) {
		t.Fatalf("Dispatch empty = %v, want ErrEmptyQueue", err)
	}
}

// 参数与时钟校验，以及多因同时成立时的报错顺序。
func TestValidation(t *testing.T) {
	if _, err := New(Config{ReadDeadline: 0, WriteDeadline: 1, WriteStarveX: 1}, nil); !errors.Is(err, ErrInvalidReadDeadline) {
		t.Fatalf("Er<=0 = %v", err)
	}
	if _, err := New(Config{ReadDeadline: 1, WriteDeadline: -1, WriteStarveX: 1}, nil); !errors.Is(err, ErrInvalidWriteDeadline) {
		t.Fatalf("Ew<=0 = %v", err)
	}
	if _, err := New(Config{ReadDeadline: 1, WriteDeadline: 1, WriteStarveX: 0}, nil); !errors.Is(err, ErrInvalidStarveX) {
		t.Fatalf("X<1 = %v", err)
	}

	s, _ := newTestScheduler(t, Config{ReadDeadline: 10, WriteDeadline: 10, WriteStarveX: 1})

	// 提交多因顺序：时钟回拨、扇区为负、标识重复。
	mustSubmit(t, s, "dup", 1, Read, 10)
	if err := s.Submit(Request{ID: "x", Sector: -1, Op: Read, SubmitAt: 9}); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("submit ordering rewind = %v", err)
	}
	if err := s.Submit(Request{ID: "dup", Sector: -1, Op: Read, SubmitAt: 10}); !errors.Is(err, ErrNegativeSector) {
		t.Fatalf("submit ordering negative-sector = %v", err)
	}
	if err := s.Submit(Request{ID: "dup", Sector: 1, Op: Read, SubmitAt: 10}); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("submit duplicate = %v", err)
	}

	// 取消多因顺序：时钟回拨优先于“不存在/已派发”。
	if err := s.Cancel("ghost", 9); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("cancel ordering rewind = %v", err)
	}
	// 派发多因顺序：时钟回拨优先于队列全空。
	if _, err := s.Dispatch(9); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("dispatch ordering rewind = %v", err)
	}

	// 被拒绝的操作不改变队列、磁头与饿计数。
	st := s.Snapshot()
	if st.LastSeenTime != 10 || len(st.ReadQueue) != 1 || st.Head != 0 || st.StarveCount != 0 {
		t.Fatalf("state changed by rejected ops: %+v", st)
	}
}

// 相同的提交、取消与派发序列重放得到完全相同的派发顺序；
// 且写队列持续非空时相邻两次写派发之间的读派发不超过 X 次。
func TestReplayDeterminismAndStarveBound(t *testing.T) {
	cfg := Config{ReadDeadline: 1000, WriteDeadline: 1000, WriteStarveX: 1}

	script := func(s *Scheduler) []DispatchResult {
		mustSubmit(t, s, "r1", 80, Read, 0)
		mustSubmit(t, s, "w1", 10, Write, 0)
		mustSubmit(t, s, "r2", 20, Read, 0)
		mustSubmit(t, s, "w2", 90, Write, 1)
		mustSubmit(t, s, "r3", 30, Read, 2)
		if err := s.Cancel("r2", 3); err != nil {
			t.Fatalf("cancel: %v", err)
		}
		out := make([]DispatchResult, 0, 4)
		for _, now := range []int64{4, 5, 6, 7} {
			res, err := s.Dispatch(now)
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			out = append(out, res)
		}
		return out
	}

	encode := func(rs []DispatchResult) string {
		parts := make([]string, len(rs))
		for i, r := range rs {
			parts[i] = fmt.Sprintf("%s:%s:forced=%t:head=%d:op=%s",
				r.Req.ID, r.Reason, r.WriteForced, r.Req.Sector, r.Req.Op)
		}
		return strings.Join(parts, "|")
	}

	first := func() []DispatchResult {
		s, _ := newTestScheduler(t, cfg)
		return script(s)
	}()
	want := encode(first)
	for run := 0; run < 5; run++ {
		s, _ := newTestScheduler(t, cfg)
		if got := encode(script(s)); got != want {
			t.Fatalf("replay mismatch:\nwant=%s\ngot =%s", want, got)
		}
	}

	// 校验防饿上界：相邻两次写派发之间的读派发次数 <= X（写队列持续非空）。
	readsSinceWrite := 0
	writeStillPending := true
	for _, r := range first {
		switch r.Req.Op {
		case Write:
			readsSinceWrite = 0
		case Read:
			readsSinceWrite++
		}
		if writeStillPending && readsSinceWrite > cfg.WriteStarveX {
			t.Fatalf("starve bound violated: %d reads before next write (X=%d)",
				readsSinceWrite, cfg.WriteStarveX)
		}
	}
}

// 并发调用提交、取消、派发与查询：不死锁、无竞态，每个请求终态唯一。
func TestConcurrent(t *testing.T) {
	s, _ := newTestScheduler(t, Config{ReadDeadline: 1000, WriteDeadline: 1000, WriteStarveX: 3})

	const writers = 8
	const per = 50
	var submitWG sync.WaitGroup
	var wg sync.WaitGroup

	for g := 0; g < writers; g++ {
		submitWG.Add(1)
		go func(g int) {
			defer submitWG.Done()
			for i := 0; i < per; i++ {
				id := fmt.Sprintf("g%d-%d", g, i)
				op := Read
				if (g+i)%2 == 0 {
					op = Write
				}
				if err := s.Submit(Request{ID: id, Sector: int64((g*per + i) % 200), Op: op, SubmitAt: 0}); err != nil {
					t.Errorf("submit %s: %v", id, err)
				}
			}
		}(g)
	}

	dispatched := make(chan string, writers*per)

	// 取消者与提交者并发：只可能得到“不存在/已派发”，二者都是合法竞争结果。
	for g := 0; g < 4; g++ {
		submitWG.Add(1)
		go func(g int) {
			defer submitWG.Done()
			for i := 0; i < per; i++ {
				switch err := s.Cancel(fmt.Sprintf("g%d-%d", g, i), 0); {
				case err == nil, errors.Is(err, ErrNotFound), errors.Is(err, ErrAlreadyDispatched):
				default:
					t.Errorf("cancel: %v", err)
				}
			}
		}(g)
	}

	submitWG.Wait()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			res, err := s.Dispatch(1)
			if err != nil {
				if errors.Is(err, ErrEmptyQueue) {
					return
				}
				t.Errorf("dispatch: %v", err)
				return
			}
			dispatched <- res.Req.ID
		}
	}()

	for c := 0; c < 4; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < writers*per; i++ {
				_ = s.Snapshot()
			}
		}()
	}

	wg.Wait()
	close(dispatched)

	seen := map[string]int{}
	total := 0
	canceled := s.Snapshot().CanceledCount
	for id := range dispatched {
		seen[id]++
		total++
	}
	if total+int(canceled) != writers*per {
		t.Fatalf("dispatched(%d)+canceled(%d) != %d", total, canceled, writers*per)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("id=%s dispatched %d times", id, n)
		}
	}
}

// 日志打印输入、输出与判定依据。
func TestLoggingContainsInputOutputAndReason(t *testing.T) {
	s, log := newTestScheduler(t, Config{ReadDeadline: 5, WriteDeadline: 5, WriteStarveX: 2})
	mustSubmit(t, s, "r1", 10, Read, 0)
	mustDispatch(t, s, 5, "r1", ReasonDeadline, false)

	out := log.String()
	for _, want := range []string{"submit", "id=\"r1\"", "op=read", "dispatch", "reason=deadline"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
}
