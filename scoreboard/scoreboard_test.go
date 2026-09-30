package scoreboard

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
)

type recordedOp struct {
	kind   string
	length int
	c      int
	blocks []Block
	sendAt int
	rtxAt  int
	ok     bool
}

// independentInFlight 在已持有 s.mu 的前提下，用独立遍历方式重算在途字节，
// 避免直接复用实现内部的 inFlightLocked 而使不变量巡检失去意义。
func independentInFlight(s *Scoreboard) int {
	total := 0
	for _, seg := range s.segs {
		if seg.end <= s.cumulative {
			continue
		}
		covered := false
		for _, b := range s.sack {
			if b.Start <= seg.start && seg.end <= b.End {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		frags, nbytes := 0, 0
		for _, b := range s.sack {
			if b.Start >= seg.end {
				frags++
				nbytes += b.End - b.Start
			}
		}
		lost := frags >= s.d || nbytes >= (s.d-1)*s.m
		if !lost || seg.retransmitted {
			total += seg.end - seg.start
		}
	}
	return total
}

// installHook 在持锁点记录每次操作的线性化顺序（含被拒绝的操作）。
func installHook(s *Scoreboard, record func(recordedOp)) {
	s.hook = func(kind string, p1, p2 int, blocks []Block, ok bool) {
		op := recordedOp{kind: kind, c: p1, length: p2, ok: ok}
		switch kind {
		case "send":
			op.sendAt = p1
		case "rtx":
			op.rtxAt = p1
		}
		if blocks != nil {
			op.blocks = append([]Block(nil), blocks...)
		}
		record(op)
	}
}

func replayOps(t *testing.T, m, d, w int, ops []recordedOp) *Scoreboard {
	t.Helper()
	s2, _ := newTestBoard(t, m, d, w)
	for _, op := range ops {
		switch op.kind {
		case "send":
			start, err := s2.Send(op.length)
			if (err == nil) != op.ok || start != op.sendAt {
				t.Fatalf("replay send mismatch: op=%+v got start=%d err=%v", op, start, err)
			}
		case "ack":
			err := s2.Acknowledge(op.c, op.blocks)
			if (err == nil) != op.ok {
				t.Fatalf("replay ack mismatch: op=%+v err=%v", op, err)
			}
		case "rtx":
			start, err := s2.Retransmit()
			if (err == nil) != op.ok || start != op.rtxAt {
				t.Fatalf("replay rtx mismatch: op=%+v got start=%d err=%v", op, start, err)
			}
		}
	}
	return s2
}

func snapshotEqual(t *testing.T, a, b *Scoreboard) {
	t.Helper()
	if a.sentEnd != b.sentEnd || a.cumulative != b.cumulative || a.InFlight() != b.InFlight() {
		t.Fatalf("state mismatch: sentEnd %d/%d cumulative %d/%d inFlight %d/%d",
			a.sentEnd, b.sentEnd, a.cumulative, b.cumulative, a.InFlight(), b.InFlight())
	}
	if len(a.sack) != len(b.sack) {
		t.Fatalf("sack len mismatch: %v vs %v", a.sack, b.sack)
	}
	for i := range a.sack {
		if a.sack[i] != b.sack[i] {
			t.Fatalf("sack[%d] mismatch: %v vs %v", i, a.sack, b.sack)
		}
	}
	for i := range a.segs {
		if a.segs[i].retransmitted != b.segs[i].retransmitted {
			t.Fatalf("segment %d retransmitted mismatch: %v vs %v", i, a.segs[i], b.segs[i])
		}
	}
}

// TestConcurrentAndReplay：发送/确认/重传/查询并发调用；
// 在途任何时刻都等于按定义重算的值；按实际生效顺序重放到新实例，结果完全相同。
func TestConcurrentAndReplay(t *testing.T) {
	const (
		M = 10
		D = 3
		W = 200
	)
	s, _ := newTestBoard(t, M, D, W)

	var (
		mu      sync.Mutex
		ops     []recordedOp
		stopCh  = make(chan struct{})
		workers sync.WaitGroup
	)
	record := func(op recordedOp) {
		mu.Lock()
		ops = append(ops, op)
		mu.Unlock()
	}
	installHook(s, record)

	// 不变量巡检：在同一把锁内对比 InFlight 的定义式与独立遍历重算值。
	workers.Add(1)
	go func() {
		defer workers.Done()
		var violations int
		for {
			select {
			case <-stopCh:
				if violations > 0 {
					t.Errorf("in-flight invariant violated %d times", violations)
				}
				return
			default:
			}
			s.mu.Lock()
			got := s.inFlightLocked()
			want := independentInFlight(s)
			s.mu.Unlock()
			if got != want {
				violations++
			}
		}
	}()

	// 阶段一：8 个发送者各发 10 个长 10 的段，与查询并发。
	var senders sync.WaitGroup
	for g := 0; g < 8; g++ {
		senders.Add(1)
		go func() {
			defer senders.Done()
			for range 10 {
				s.Send(M)
			}
		}()
	}
	senders.Wait()

	// 阶段二：确认 / 重传 / 查询全部并发（段长均为 10，边界即 10 的倍数）。
	var mix sync.WaitGroup
	mix.Add(3)
	go func() { // 制造段0判丢：三个不相交片段
		defer mix.Done()
		s.Acknowledge(0, []Block{{10, 20}, {30, 40}, {50, 60}})
	}()
	go func() { // 持续查询
		defer mix.Done()
		for range 200 {
			s.InFlight()
		}
	}()
	go func() { // 反复尝试重传：可能无段、窗口满或成功
		defer mix.Done()
		for range 50 {
			s.Retransmit()
		}
	}()
	mix.Wait()

	// 阶段三：并发推进累计点，重传状态随之清除。
	var finish sync.WaitGroup
	finish.Add(2)
	go func() {
		defer finish.Done()
		for _, c := range []int{60, 100, 400, 800} {
			s.Acknowledge(c, []Block{{c + 10, c + 20}})
		}
	}()
	go func() {
		defer finish.Done()
		for range 100 {
			s.Retransmit()
		}
	}()
	finish.Wait()

	close(stopCh)
	workers.Wait()

	// 按实际生效顺序重放，最终状态必须完全一致（确定性）。
	mu.Lock()
	replayed := replayOps(t, M, D, W, ops)
	mu.Unlock()
	snapshotEqual(t, s, replayed)
}

type testLogger struct{ t *testing.T }

func (l testLogger) Write(p []byte) (int, error) {
	l.t.Log(string(p))
	return len(p), nil
}

func newTestBoard(t *testing.T, m, d, w int) (*Scoreboard, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	s, err := New(m, d, w, io.MultiWriter(&buf, testLogger{t}))
	if err != nil {
		t.Fatalf("New(%d,%d,%d) unexpected error: %v", m, d, w, err)
	}
	return s, &buf
}

func mustSend(t *testing.T, s *Scoreboard, length int) int {
	t.Helper()
	start, err := s.Send(length)
	if err != nil {
		t.Fatalf("Send(%d) unexpected error: %v", length, err)
	}
	return start
}

func mustAck(t *testing.T, s *Scoreboard, c int, blocks ...Block) {
	t.Helper()
	if err := s.Acknowledge(c, blocks); err != nil {
		t.Fatalf("Acknowledge(%d,%v) unexpected error: %v", c, blocks, err)
	}
}

func mustRtx(t *testing.T, s *Scoreboard) int {
	t.Helper()
	start, err := s.Retransmit()
	if err != nil {
		t.Fatalf("Retransmit unexpected error: %v", err)
	}
	return start
}

// TestFragmentCountBoundary：恰好 D 个不相交连续片段判丢，D-1 个不判丢；
// 同时验证“片段数”条件单独成立（总字节 15 < 20）。
func TestFragmentCountBoundary(t *testing.T) {
	s, _ := newTestBoard(t, 10, 3, 100)
	for range 8 {
		mustSend(t, s, 5) // 边界点：0,5,10,15,20,25,30
	}
	// D-1=2 个高于段0 [0,5) 的片段，共 10 字节 < (D-1)*M=20
	mustAck(t, s, 0, Block{10, 15}, Block{25, 30})
	if got := s.InFlight(); got != 30 {
		t.Fatalf("with 2 fragments: in-flight = %d, want 30 (40 total - 10 SACKed, no loss)", got)
	}
	if _, err := s.Retransmit(); !errors.Is(err, ErrNothingToRetransmit) {
		t.Fatalf("with 2 fragments: Retransmit err = %v, want ErrNothingToRetransmit", err)
	}
	// 恰好 D=3 个不相交片段，共 15 字节 < 20：仅片段数条件成立
	mustAck(t, s, 0, Block{5, 10}, Block{20, 25}, Block{35, 40})
	if len(s.sack) != 3 {
		t.Fatalf("sack = %v, want 3 disjoint fragments", s.sack)
	}
	if got := s.InFlight(); got != 10 {
		t.Fatalf("with 3 fragments: in-flight = %d, want 10 (40 total - 25 SACKed - 5 lost)", got)
	}
	if start := mustRtx(t, s); start != 0 {
		t.Fatalf("Retransmit start = %d, want 0", start)
	}
}

// TestByteThresholdBoundary：恰好 (D-1)*M 字节判丢，少 1 字节不判丢；
// 仅字节条件成立（1 个片段 < D）。
func TestByteThresholdBoundary(t *testing.T) {
	s, _ := newTestBoard(t, 10, 3, 100)
	mustSend(t, s, 1)
	mustSend(t, s, 9)
	mustSend(t, s, 10)
	mustSend(t, s, 1) // 边界点：0,1,10,20,21

	mustAck(t, s, 0, Block{1, 20}) // 恰好 19 个已选择确认字节
	if got := s.InFlight(); got != 2 {
		t.Fatalf("19 sack bytes: in-flight = %d, want 30 (no loss)", got)
	}
	if _, err := s.Retransmit(); !errors.Is(err, ErrNothingToRetransmit) {
		t.Fatalf("19 sack bytes: Retransmit err = %v, want ErrNothingToRetransmit", err)
	}
	mustAck(t, s, 0, Block{1, 21}) // 恰好 (D-1)*M=20 字节，1 个片段
	if got := s.InFlight(); got != 0 {
		t.Fatalf("20 sack bytes: in-flight = %d, want 0 (segment 0 lost)", got)
	}
	if start := mustRtx(t, s); start != 0 {
		t.Fatalf("Retransmit start = %d, want 0", start)
	}
	if got := s.InFlight(); got != 1 {
		t.Fatalf("after retransmit: in-flight = %d, want 1", got)
	}
}

// TestRetransmitInFlightAndWindow：重传后在途变化；在途恰好放下允许，
// 多 1 字节拒绝；无可重传段的拒绝先于在途已满。
func TestRetransmitInFlightAndWindow(t *testing.T) {
	s, _ := newTestBoard(t, 10, 3, 20)
	for range 5 {
		mustSend(t, s, 10)
	}
	if got := s.InFlight(); got != 50 {
		t.Fatalf("initial in-flight = %d, want 50", got)
	}
	// 2 个片段、共 20 字节：段0 由字节条件判丢
	mustAck(t, s, 0, Block{10, 20}, Block{30, 40})
	if got := s.InFlight(); got != 20 {
		t.Fatalf("after loss: in-flight = %d, want 20", got)
	}
	// 20+10 > W=20：在途已满
	if _, err := s.Retransmit(); !errors.Is(err, ErrWindowFull) {
		t.Fatalf("Retransmit err = %v, want ErrWindowFull", err)
	}

	// W=29：差 1 字节放不下
	s.w = 29
	if _, err := s.Retransmit(); !errors.Is(err, ErrWindowFull) {
		t.Fatalf("W=29 Retransmit err = %v, want ErrWindowFull", err)
	}

	// W=30：在途恰好放下（20+10=30）
	s.w = 30
	if start := mustRtx(t, s); start != 0 {
		t.Fatalf("Retransmit start = %d, want 0", start)
	}
	if got := s.InFlight(); got != 30 {
		t.Fatalf("after retransmit: in-flight = %d, want 30", got)
	}
	if _, err := s.Retransmit(); !errors.Is(err, ErrNothingToRetransmit) {
		t.Fatalf("second Retransmit err = %v, want ErrNothingToRetransmit", err)
	}
}

// TestCumulativeSwallow：累计点吞并 SACK 记录，包含部分越过的截断。
func TestCumulativeSwallow(t *testing.T) {
	s, _ := newTestBoard(t, 10, 3, 100)
	for range 5 {
		mustSend(t, s, 10)
	}

	// 部分越过：[10,40) 在 c=20 处截断为 [20,40)，再与 {40,50} 相邻合并
	mustAck(t, s, 0, Block{10, 40})
	mustAck(t, s, 20, Block{40, 50})
	if len(s.sack) != 1 || s.sack[0] != (Block{20, 50}) {
		t.Fatalf("sack after partial swallow = %v, want [{20 50}]", s.sack)
	}

	// 完全吞并 [20,50)：c=30 保留起点恰为 c 的 [30,50) 旧记录，并并入 {40,50}
	mustAck(t, s, 30, Block{40, 50})
	if len(s.sack) != 1 || s.sack[0] != (Block{30, 50}) {
		t.Fatalf("sack after cumulative=30 = %v, want [{30 50}]", s.sack)
	}

	// 剩余段全部被确认或选择确认：在途为 0，无段可重传
	if got := s.InFlight(); got != 0 {
		t.Fatalf("in-flight = %d, want 0", got)
	}
	if _, err := s.Retransmit(); !errors.Is(err, ErrNothingToRetransmit) {
		t.Fatalf("Retransmit after all acked err = %v, want ErrNothingToRetransmit", err)
	}
}

// TestOverlappingAdjacentBlocks：块重叠/相邻取并集后片段数按并集计算。
func TestOverlappingAdjacentBlocks(t *testing.T) {
	s, _ := newTestBoard(t, 10, 3, 100)
	for range 5 {
		mustSend(t, s, 10)
	}
	// 乱序给出块，确认不得修改调用方切片（拒绝与接受均整体生效）。
	callerBlocks := []Block{{20, 30}, {10, 20}}
	saved := append([]Block(nil), callerBlocks...)
	mustAck(t, s, 0, callerBlocks...)
	if callerBlocks[0] != saved[0] || callerBlocks[1] != saved[1] {
		t.Fatalf("caller blocks mutated: got %v, want %v", callerBlocks, saved)
	}
	if len(s.sack) != 1 || s.sack[0] != (Block{10, 30}) {
		t.Fatalf("merged adjacent sack = %v, want [{10 30}]", s.sack)
	}
	if got := s.InFlight(); got != 20 {
		t.Fatalf("in-flight = %d, want 20 (segment 0 lost by bytes)", got)
	}
	mustAck(t, s, 0, Block{20, 30}, Block{30, 40})
	if len(s.sack) != 1 || s.sack[0] != (Block{10, 40}) {
		t.Fatalf("union sack = %v, want [{10 40}]", s.sack)
	}
}

// TestInvalidConstruction：构造参数拒绝顺序。
func TestInvalidConstruction(t *testing.T) {
	if _, err := New(0, 3, 100, nil); !errors.Is(err, ErrInvalidM) {
		t.Fatalf("New(0,..) err = %v, want ErrInvalidM", err)
	}
	if _, err := New(10, 1, 100, nil); !errors.Is(err, ErrInvalidD) {
		t.Fatalf("New(.,1,.) err = %v, want ErrInvalidD", err)
	}
	if _, err := New(10, 3, 9, nil); !errors.Is(err, ErrInvalidW) {
		t.Fatalf("New(.,.,9) err = %v, want ErrInvalidW", err)
	}
}

// TestInvalidSend：长度不在 [1,M] 时拒绝且状态不变。
func TestInvalidSend(t *testing.T) {
	s, _ := newTestBoard(t, 10, 3, 100)
	for _, length := range []int{0, -1, 11} {
		if _, err := s.Send(length); !errors.Is(err, ErrInvalidLength) {
			t.Fatalf("Send(%d) err = %v, want ErrInvalidLength", length, err)
		}
	}
	if got := s.InFlight(); got != 0 {
		t.Fatalf("in-flight after rejected sends = %d, want 0", got)
	}
}

// TestInvalidAcknowledgement：各类非法 ACK 拒绝顺序与整体生效性。
func TestInvalidAcknowledgement(t *testing.T) {
	s, _ := newTestBoard(t, 10, 3, 100)
	for range 4 {
		mustSend(t, s, 10)
	}
	mustAck(t, s, 10, Block{20, 30})

	cases := []struct {
		name   string
		c      int
		blocks []Block
		want   error
	}{
		{"cumulative revert", 5, []Block{{20, 30}}, ErrCumulativeRevert},
		{"cumulative beyond sent end", 41, []Block{{20, 30}}, ErrCumulativeTooFar},
		{"cumulative not boundary", 15, []Block{{20, 30}}, ErrCumulativeBoundary},
		{"no blocks", 10, nil, ErrNoBlocks},
		{"block reversed", 10, []Block{{30, 20}}, ErrBlockReversed},
		{"block empty", 10, []Block{{20, 20}}, ErrBlockReversed},
		{"block start at cumulative", 10, []Block{{10, 20}}, ErrBlockStartAtCum},
		{"block end beyond sent end", 10, []Block{{20, 41}}, ErrBlockEndTooFar},
		{"block start not boundary", 10, []Block{{15, 20}}, ErrBlockBoundary},
		{"block end not boundary", 10, []Block{{20, 25}}, ErrBlockBoundary},
		{"second block checked in order", 10, []Block{{20, 30}, {35, 30}}, ErrBlockReversed},
		{"cumulative checked before blocks", 5, nil, ErrCumulativeRevert},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.Acknowledge(tc.c, tc.blocks); !errors.Is(err, tc.want) {
				t.Fatalf("Acknowledge(%d,%v) err = %v, want %v", tc.c, tc.blocks, err, tc.want)
			}
		})
	}
	if s.cumulative != 10 {
		t.Fatalf("cumulative after rejected acks = %d, want 10", s.cumulative)
	}
	if len(s.sack) != 1 || s.sack[0] != (Block{20, 30}) {
		t.Fatalf("sack after rejected acks = %v, want [{20 30}]", s.sack)
	}
	if got := s.InFlight(); got != 20 {
		t.Fatalf("in-flight after rejected acks = %d, want 20", got)
	}
}
