package creditflow

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// memLogger 收集每步日志；测试失败时整体打印，便于人工核对
// 输入、信用、积压、缓冲与判定依据。
type memLogger struct{ buf bytes.Buffer }

func (m *memLogger) Printf(format string, args ...any) {
	fmt.Fprintf(&m.buf, format+"\n", args...)
}

func newTestChannel(t *testing.T, capacity, maxBacklog int) (*Channel, *memLogger) {
	t.Helper()
	lg := &memLogger{}
	c, err := NewChannel(capacity, maxBacklog, lg)
	if err != nil {
		t.Fatalf("NewChannel: %v", err)
	}
	return c, lg
}

func dumpLogOnFail(t *testing.T, lg *memLogger) {
	t.Helper()
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("逐步日志:\n%s", lg.buf.String())
		}
	})
}

// assertInvariant 校验：信用非负；缓冲+在途信用不超过容量；
// 已产生总数==积压+在途+已收到；积压不超过上限；消费不超过收到。
func assertInvariant(t *testing.T, c *Channel, maxBacklog int, phase string) {
	t.Helper()
	s := c.Stats()
	if s.Credit < 0 {
		t.Fatalf("[%s] 信用为负: %+v", phase, s)
	}
	if committed := s.Buffered + s.InFlight + s.Credit; committed > c.capacity {
		t.Fatalf("[%s] 缓冲+在途信用(%d) 超过容量 %d: %+v", phase, committed, c.capacity, s)
	}
	if s.Produced != s.Backlog+s.InFlight+s.Received {
		t.Fatalf("[%s] 已产生(%d) != 积压(%d)+在途(%d)+已收到(%d)",
			phase, s.Produced, s.Backlog, s.InFlight, s.Received)
	}
	if s.Backlog > maxBacklog {
		t.Fatalf("[%s] 积压 %d 超过上限 %d", phase, s.Backlog, maxBacklog)
	}
	if s.Received < s.Consumed {
		t.Fatalf("[%s] 已收到 %d < 已消费 %d", phase, s.Received, s.Consumed)
	}
}

// TestCreditAndBacklog 覆盖信用与积压的增减、通告、自动发送与消费。
func TestCreditAndBacklog(t *testing.T) {
	c, lg := newTestChannel(t, 3, 10)
	dumpLogOnFail(t, lg)
	snd, rcv := c.Sender(), c.Receiver()

	// 初始无信用：产生的消息全部积压，一条不发。
	for i := 0; i < 5; i++ {
		if _, err := snd.Produce([]byte{byte(i)}); err != nil {
			t.Fatalf("produce %d: %v", i, err)
		}
	}
	assertInvariant(t, c, 10, "无通告时产生")
	if snd.Credit() != 0 || snd.Backlog() != 5 || rcv.InFlight() != 0 {
		t.Fatalf("无信用时应全部积压: credit=%d backlog=%d inflight=%d",
			snd.Credit(), snd.Backlog(), rcv.InFlight())
	}

	// 通告：余量=3，授予3；自动发出3条，信用归零、积压剩2。
	if g := rcv.Advertise(); g != 3 {
		t.Fatalf("首次通告应授予3，实际 %d", g)
	}
	assertInvariant(t, c, 10, "首次通告")
	if snd.Credit() != 0 || snd.Backlog() != 2 || rcv.InFlight() != 3 {
		t.Fatalf("通告后状态错误: credit=%d backlog=%d inflight=%d",
			snd.Credit(), snd.Backlog(), rcv.InFlight())
	}

	// 在途占满容量：再通告余量=3-0-(0+3)=0，什么都不做。
	if g := rcv.Advertise(); g != 0 {
		t.Fatalf("在途占满时通告应授予0，实际 %d", g)
	}

	// 在途到达接收缓冲：不触发通告。
	got := rcv.Deliver()
	if len(got) != 3 || got[0].Seq != 1 || got[2].Seq != 3 {
		t.Fatalf("应按编号收到1..3，实际 %+v", got)
	}
	assertInvariant(t, c, 10, "投递")
	if rcv.Buffer() != 3 || rcv.InFlight() != 0 {
		t.Fatalf("投递后 buffered=%d inflight=%d", rcv.Buffer(), rcv.InFlight())
	}
	if g := rcv.Advertise(); g != 0 {
		t.Fatalf("缓冲满时通告应授予0，实际 %d", g)
	}

	// 顺序消费2条（不触发通告），再通告授予2，自动发完剩余积压。
	for _, seq := range []int{1, 2} {
		if err := rcv.Consume(seq); err != nil {
			t.Fatalf("consume %d: %v", seq, err)
		}
	}
	assertInvariant(t, c, 10, "消费")
	if g := rcv.Advertise(); g != 2 {
		t.Fatalf("消费2条后通告应授予2，实际 %d", g)
	}
	assertInvariant(t, c, 10, "二次通告")
	if snd.Backlog() != 0 || snd.Credit() != 0 || rcv.InFlight() != 2 {
		t.Fatalf("剩余积压应自动发完: backlog=%d credit=%d inflight=%d",
			snd.Backlog(), snd.Credit(), rcv.InFlight())
	}

	// 剩余2条到达，连同缓冲中的 seq=3 顺序消费完毕；编号连续无重复。
	got = rcv.Deliver()
	if len(got) != 2 || got[0].Seq != 4 || got[1].Seq != 5 {
		t.Fatalf("投递编号错误: %+v", got)
	}
	for seq := 3; seq <= 5; seq++ {
		if err := rcv.Consume(seq); err != nil {
			t.Fatalf("consume %d: %v", seq, err)
		}
	}
	assertInvariant(t, c, 10, "全部消费")
	s := c.Stats()
	if s.Produced != 5 || s.Received != 5 || s.Consumed != 5 || s.Buffered != 0 {
		t.Fatalf("最终状态错误: %+v", s)
	}

	// 日志必须包含每步输入、信用、积压、缓冲与判定依据。
	for _, want := range []string{"produce seq=1", "grant", "pump", "deliver count=3", "consume seq=1", "credit=", "backlog=", "buffered=", "判定:"} {
		if !strings.Contains(lg.buf.String(), want) {
			t.Fatalf("日志缺少 %q", want)
		}
	}
}

// TestProbe 覆盖探测的允许条件、效果等同通告、不保底、不记忆。
func TestProbe(t *testing.T) {
	c, lg := newTestChannel(t, 2, 10)
	dumpLogOnFail(t, lg)
	snd, rcv := c.Sender(), c.Receiver()

	// 无积压时探测：拒绝。
	if _, err := snd.Probe(); !errors.Is(err, ErrProbeNotAllowed) {
		t.Fatalf("无积压探测应 ErrProbeNotAllowed，实际 %v", err)
	}

	// 有积压、无信用：探测允许，效果等同通告（授予2，自动发送2条）。
	for i := 0; i < 3; i++ {
		if _, err := snd.Produce([]byte{byte(i)}); err != nil {
			t.Fatalf("produce: %v", err)
		}
	}
	g, err := snd.Probe()
	if err != nil || g != 2 {
		t.Fatalf("探测应授予2，实际 granted=%d err=%v", g, err)
	}
	assertInvariant(t, c, 10, "探测授予")
	if snd.Credit() != 0 || snd.Backlog() != 1 || rcv.InFlight() != 2 {
		t.Fatalf("探测后状态错误: credit=%d backlog=%d inflight=%d",
			snd.Credit(), snd.Backlog(), rcv.InFlight())
	}

	// 在途占满容量：再次探测条件满足但不保底，授予0且状态不变。
	before := c.Stats()
	if g, err = snd.Probe(); err != nil || g != 0 {
		t.Fatalf("探测不保底: granted=%d err=%v", g, err)
	}
	if c.Stats() != before {
		t.Fatalf("授予0的探测不应改变状态: before=%+v after=%+v", before, c.Stats())
	}

	// 交付占满缓冲：探测仍不保底（不记忆上一次请求）。
	rcv.Deliver()
	if g, err = snd.Probe(); err != nil || g != 0 {
		t.Fatalf("缓冲满时探测应授予0: granted=%d err=%v", g, err)
	}

	// 消费1条腾出空位，再探测：授予1，发走最后一条积压；此后无积压，探测拒绝。
	if err := rcv.Consume(1); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if g, err = snd.Probe(); err != nil || g != 1 {
		t.Fatalf("腾位后探测应授予1: granted=%d err=%v", g, err)
	}
	assertInvariant(t, c, 10, "探测补发")
	if _, err := snd.Probe(); !errors.Is(err, ErrProbeNotAllowed) {
		t.Fatalf("无积压探测应拒绝，实际 %v", err)
	}

	// 清空后通告制造剩余信用：有信用时探测同样被拒。
	rcv.Deliver()
	for seq := 2; seq <= 3; seq++ {
		if err := rcv.Consume(seq); err != nil {
			t.Fatalf("consume %d: %v", seq, err)
		}
	}
	if g := rcv.Advertise(); g != 2 {
		t.Fatalf("清空后通告应授予2，实际 %d", g)
	}
	if snd.Credit() != 2 {
		t.Fatalf("准备信用失败: credit=%d", snd.Credit())
	}
	if _, err := snd.Probe(); !errors.Is(err, ErrProbeNotAllowed) {
		t.Fatalf("有信用时探测应拒绝，实际 %v", err)
	}
	assertInvariant(t, c, 10, "探测有信用拒绝")
}

// TestInvalidInputsAndNoTrace 非法输入必须整体拒绝，且失败不留痕。
func TestInvalidInputsAndNoTrace(t *testing.T) {
	for _, tc := range []struct{ cap, backlog int }{
		{0, 5}, {3, 0}, {-1, -2}, {0, 0},
	} {
		if _, err := NewChannel(tc.cap, tc.backlog, nil); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("NewChannel(%d,%d) 应 ErrInvalidParam，实际 %v", tc.cap, tc.backlog, err)
		}
	}

	c, lg := newTestChannel(t, 2, 2)
	dumpLogOnFail(t, lg)
	snd, rcv := c.Sender(), c.Receiver()

	// 积压上限为2：产生2条后第3条拒绝，状态不变。
	for _, p := range []string{"a", "b"} {
		if _, err := snd.Produce([]byte(p)); err != nil {
			t.Fatalf("produce %s: %v", p, err)
		}
	}
	if _, err := snd.Produce([]byte("c")); !errors.Is(err, ErrBacklogOverflow) {
		t.Fatalf("积压超限应 ErrBacklogOverflow，实际 %v", err)
	}
	if snd.Backlog() != 2 {
		t.Fatalf("积压超限拒绝后 backlog=%d，应为2", snd.Backlog())
	}

	// 通告发出2条并交付，按序消费 seq=1。
	if g := rcv.Advertise(); g != 2 {
		t.Fatalf("通告应授予2，实际 %d", g)
	}
	if n := len(rcv.Deliver()); n != 2 {
		t.Fatalf("应交付2条，实际 %d", n)
	}
	if err := rcv.Consume(1); err != nil {
		t.Fatalf("consume 1: %v", err)
	}

	// 整批非法调用：拒绝前后状态必须完全一致（失败不留痕）。
	assertConsumeReject := func(seq int) {
		t.Helper()
		if err := rcv.Consume(seq); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("consume(%d) 应 ErrInvalidParam，实际 %v", seq, err)
		}
	}
	before := c.Stats()
	assertConsumeReject(0)  // 非正
	assertConsumeReject(-3) // 非正
	assertConsumeReject(1)  // 重复
	assertConsumeReject(3)  // 跳号
	assertConsumeReject(99) // 越界
	if _, err := snd.Probe(); !errors.Is(err, ErrProbeNotAllowed) {
		t.Fatalf("无积压探测应 ErrProbeNotAllowed，实际 %v", err)
	}
	if c.Stats() != before {
		t.Fatalf("非法操作改变了状态: before=%+v after=%+v", before, c.Stats())
	}

	// seq=2 合法消费后，再次非法消费仍不留痕。
	if err := rcv.Consume(2); err != nil {
		t.Fatalf("consume 2: %v", err)
	}
	before = c.Stats()
	assertConsumeReject(2)
	assertConsumeReject(3)
	assertConsumeReject(1)
	if c.Stats() != before {
		t.Fatalf("非法消费改变了状态: before=%+v after=%+v", before, c.Stats())
	}

	// 三类错误必须互不相同、可区分。
	if errors.Is(ErrInvalidParam, ErrProbeNotAllowed) ||
		errors.Is(ErrInvalidParam, ErrBacklogOverflow) ||
		errors.Is(ErrProbeNotAllowed, ErrBacklogOverflow) {
		t.Fatal("错误类别必须互不相同")
	}

	// 拒绝必须在日志中打印原因；状态不留痕。
	for _, want := range []string{"REJECT", "ErrInvalidParam", "ErrProbeNotAllowed", "ErrBacklogOverflow", "失败不留痕"} {
		if !strings.Contains(lg.buf.String(), want) {
			t.Fatalf("拒绝日志缺少 %q", want)
		}
	}
}

// refModel 是独立于实现的“逐条发送”参照模型：同样的规则，
// 一次只处理一步，用于校验真实实现每一步状态与判定完全一致。
type refModel struct {
	capacity, maxBacklog int
	credit               int
	backlog, inflight    int
	buffered             int
	produced, received   int
	consumed, wantSeq    int
}

func (m *refModel) sendLoop() {
	for m.credit > 0 && m.backlog > 0 {
		m.credit--
		m.backlog--
		m.inflight++
	}
}

func (m *refModel) produce() (int, error) {
	if m.backlog >= m.maxBacklog {
		return 0, ErrBacklogOverflow
	}
	m.produced++
	m.backlog++
	m.sendLoop()
	return m.produced, nil
}

func (m *refModel) advertise() int {
	free := m.capacity - m.buffered - m.credit - m.inflight
	if free <= 0 {
		return 0
	}
	m.credit += free
	m.sendLoop()
	return free
}

func (m *refModel) probe() (int, error) {
	if m.credit != 0 || m.backlog == 0 {
		return 0, ErrProbeNotAllowed
	}
	return m.advertise(), nil
}

func (m *refModel) deliver() int {
	n := m.inflight
	m.inflight = 0
	m.buffered += n
	m.received += n
	return n
}

func (m *refModel) consume(seq int) error {
	if seq <= 0 || seq != m.wantSeq || m.buffered == 0 {
		return ErrInvalidParam
	}
	m.buffered--
	m.consumed++
	m.wantSeq++
	return nil
}

func (m *refModel) match(s Stats) bool {
	return m.credit == s.Credit && m.backlog == s.Backlog && m.inflight == s.InFlight &&
		m.buffered == s.Buffered && m.produced == s.Produced &&
		m.received == s.Received && m.consumed == s.Consumed
}

// TestReferenceEquivalence 用同一脚本驱动真实实现与参照模型，逐步比对。
func TestReferenceEquivalence(t *testing.T) {
	const cap, maxBacklog = 3, 6
	c, lg := newTestChannel(t, cap, maxBacklog)
	dumpLogOnFail(t, lg)
	snd, rcv := c.Sender(), c.Receiver()
	ref := &refModel{capacity: cap, maxBacklog: maxBacklog, wantSeq: 1}

	// op: P=produce, A=advertise, R=probe, D=deliver, cN=consume(N)
	script := []string{
		"P", "P", "P", "P", "R", "P",
		"A", "D", "c1", "c2", "R",
		"P", "P", "A", "D", "c3", "c4", "c5",
		"R", "P", "D", "c6", "c7", "A", "D", "c8",
		"c0", "c1", "c99", "R",
	}
	for _, op := range script {
		var refErr, gotErr error
		var refN, gotN int
		switch {
		case op == "P":
			_, refErr = ref.produce()
			_, gotErr = snd.Produce([]byte("m"))
		case op == "A":
			refN, gotN = ref.advertise(), rcv.Advertise()
		case op == "R":
			refN, refErr = ref.probe()
			gotN, gotErr = snd.Probe()
		case op == "D":
			refN, gotN = ref.deliver(), len(rcv.Deliver())
		case len(op) > 1 && op[0] == 'c':
			var seq int
			fmt.Sscan(op[1:], &seq)
			refErr, gotErr = ref.consume(seq), rcv.Consume(seq)
		}
		if !errors.Is(refErr, gotErr) {
			t.Fatalf("op=%s 错误不一致: ref=%v got=%v", op, refErr, gotErr)
		}
		if refN != gotN {
			t.Fatalf("op=%s 返回值不一致: ref=%d got=%d", op, refN, gotN)
		}
		if !ref.match(c.Stats()) {
			s := c.Stats()
			t.Fatalf("op=%s 状态不一致: ref={credit:%d backlog:%d inflight:%d buffered:%d produced:%d received:%d consumed:%d} got=%+v",
				op, ref.credit, ref.backlog, ref.inflight, ref.buffered,
				ref.produced, ref.received, ref.consumed, s)
		}
		assertInvariant(t, c, maxBacklog, "参照比对 "+op)
	}
}

// TestConcurrent 发送侧与接收侧操作由不同执行体并发调用，
// race 检测下持续抽查不变量，最终全部消息按序消费。
func TestConcurrent(t *testing.T) {
	const cap, maxBacklog, total = 4, 64, 120
	c, lg := newTestChannel(t, cap, maxBacklog)
	dumpLogOnFail(t, lg)
	snd, rcv := c.Sender(), c.Receiver()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 独立检查执行体：并发读取快照并校验不变量。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				assertInvariant(t, c, maxBacklog, "并发抽查")
			}
		}
	}()

	// 发送执行体：产生 total 条；积压满则重试（拒绝不留痕）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < total; i++ {
			for {
				_, err := snd.Produce([]byte{byte(i % 251)})
				if err == nil {
					break
				}
				if !errors.Is(err, ErrBacklogOverflow) {
					t.Errorf("produce: %v", err)
					return
				}
			}
		}
	}()

	// 通告执行体：周期性通告 + 探测（非法条件被拒是正常的）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for done := false; !done; {
			select {
			case <-stop:
				done = true
			default:
				rcv.Advertise()
				_, _ = snd.Probe()
			}
		}
	}()

	// 接收执行体：持续交付 + 按编号顺序消费到 total。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for seq := 1; seq <= total; {
			rcv.Deliver()
			if err := rcv.Consume(seq); err == nil {
				seq++
			} else if !errors.Is(err, ErrInvalidParam) {
				t.Errorf("consume %d: %v", seq, err)
				return
			}
		}
		close(stop)
	}()

	// 等待接收执行体发出停止信号后，回收其余执行体；再等全部结束。
	wg.Wait()

	s := c.Stats()
	if s.Produced != total || s.Received != total || s.Consumed != total {
		t.Fatalf("最终计数错误: %+v", s)
	}
	if s.Backlog != 0 || s.InFlight != 0 || s.Buffered != 0 {
		t.Fatalf("最终应全部排空: %+v", s)
	}
	if s.Credit < 0 || s.Buffered+s.InFlight+s.Credit > cap {
		t.Fatalf("最终信用/容量不变量被破坏: %+v", s)
	}
}
