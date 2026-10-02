package ontology

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, cfg Config) *Marker {
	t.Helper()
	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v): %v", cfg, err)
	}
	return m
}

// TestInputColorSemantics 覆盖 Yellow 不用 Tc、Red 输入不记红、Blind 等价 Green。
func TestInputColorSemantics(t *testing.T) {
	// 初始 Tc=100、Te=20：Yellow 输入 b=25 只能红，绝不借用 Tc。
	m := mustNew(t, baseCfg(3))
	m.te = 20
	r, _ := m.Mark(0, Yellow, 25)
	logResult(t, "COL", 0, Yellow, 25, r)
	if r.Color != Red || r.Tc != 100 || r.Te != 20 {
		t.Fatalf("Yellow input must not touch Tc, got %s Tc=%d Te=%d", r.Color, r.Tc, r.Te)
	}

	// Red 输入：红、不扣令牌、不记红（即使多次也不触发惩罚）。
	r, _ = m.Mark(0, Red, 1)
	logResult(t, "COL", 0, Red, 1, r)
	if r.Color != Red || r.Tc != 100 || r.Te != 20 || r.RedQueueLen != 1 || r.Until != 0 {
		t.Fatalf("Red input must not deduct or record, got Tc=%d Te=%d q=%d until=%d",
			r.Tc, r.Te, r.RedQueueLen, r.Until)
	}
	r, _ = m.Mark(0, Red, 1)
	logResult(t, "COL", 0, Red, 1, r)
	if r.RedQueueLen != 1 || r.Until != 0 {
		t.Fatal("Red inputs must never fill the red queue")
	}

	// Blind 与 Green 完全等价：Tc 足 -> Green。
	mb := mustNew(t, baseCfg(3))
	rg, _ := mb.Mark(0, Green, 40)
	rb, _ := mb.Mark(0, Blind, 60) // 剩 Tc=60，同样走 Green
	logResult(t, "BLD", 0, Blind, 60, rb)
	if rb.Color != Green || rg.Tc != 60 || rb.Tc != 0 {
		t.Fatalf("Blind must behave as Green, got %s Tc=%d", rb.Color, rb.Tc)
	}
	mb2 := mustNew(t, baseCfg(3))
	mb2.Mark(0, Green, 100)
	rb2, _ := mb2.Mark(0, Blind, 50) // Tc=0、Te=50 -> Yellow（降级扣 Te）
	logResult(t, "BLD", 0, Blind, 50, rb2)
	if rb2.Color != Yellow || rb2.Te != 0 {
		t.Fatalf("Blind downgrade must equal Green, got %s Te=%d", rb2.Color, rb2.Te)
	}
}

// TestOversizedPacket b 大于 CBS 与 EBS 时恒红且不扣令牌；两桶都满时溢出丢弃。
func TestOversizedPacket(t *testing.T) {
	m := mustNew(t, baseCfg(3))
	// Te 先清空，使 b=51 对 Tc(100) 可绿；这里取 b=101>CBS、>EBS。
	r, err := m.Mark(5, Yellow, 101)
	if err != nil {
		t.Fatal(err)
	}
	logResult(t, "BIG", 5, Yellow, 101, r)
	if r.Color != Red || r.Tc != 100 || r.Te != 50 {
		t.Fatalf("b>CBS,EBS must be red without deduction, got %s Tc=%d Te=%d", r.Color, r.Tc, r.Te)
	}

	// 两桶都满时继续补充：溢出全部丢弃，Tc/Te 不再增长。
	r, _ = m.Mark(1000, Yellow, 50) // Te 满 50 可付
	logResult(t, "FULL", 1000, Yellow, 50, r)
	if r.Tc != 100 || r.Te != 0 {
		t.Fatalf("after long refill buckets capped, got Tc=%d Te=%d", r.Tc, r.Te)
	}
	r, _ = m.Mark(1000, Yellow, 1)
	logResult(t, "FULL", 1000, Yellow, 1, r)
	if r.Color != Red || r.Tc != 100 || r.Te != 0 {
		t.Fatalf("Te exhausted must give red without touching Tc, got %s Tc=%d Te=%d", r.Color, r.Tc, r.Te)
	}
}

// TestReconfigureSemantics 缩小 CBS 时截去的令牌直接丢弃，不转入超额桶。
func TestReconfigureSemantics(t *testing.T) {
	m := mustNew(t, baseCfg(3))
	m.Mark(0, Green, 100) // Tc=0, Te=50

	// t=5 按旧 CIR=10 补 50：Tc=50（未溢出）。随后 CBS 缩到 30：Tc 截到 30。
	// Te=50 超过新 EBS=10，截到 10；被截令牌不互转。
	if err := m.Reconfigure(5, 2, 30, 10); err != nil {
		t.Fatal(err)
	}
	s, _ := m.State(5)
	if s.Tc != 30 || s.Te != 10 || s.CIR != 2 || s.CBS != 30 || s.EBS != 10 {
		t.Fatalf("reconfigure truncate mismatch: Tc=%d Te=%d cfg=(%d,%d,%d)",
			s.Tc, s.Te, s.CIR, s.CBS, s.EBS)
	}
	// last 已推进到 5：再配置到 t=5 不重复补充。
	if err := m.Reconfigure(5, 100, 100, 100); err != nil {
		t.Fatal(err)
	}
	s, _ = m.State(5)
	if s.Tc != 30 || s.Te != 10 {
		t.Fatalf("same-time reconfigure must not refill, got Tc=%d Te=%d", s.Tc, s.Te)
	}

	// Reconfigure 的非法参数与回退可区分；拒绝时状态不变。
	if err := m.Reconfigure(5, 0, 100, 100); !errors.Is(err, ErrInvalidCIR) {
		t.Fatalf("want ErrInvalidCIR, got %v", err)
	}
	if err := m.Reconfigure(5, 1, 0, 100); !errors.Is(err, ErrInvalidCBS) {
		t.Fatalf("want ErrInvalidCBS, got %v", err)
	}
	if err := m.Reconfigure(5, 1, 1, -1); !errors.Is(err, ErrInvalidEBS) {
		t.Fatalf("want ErrInvalidEBS, got %v", err)
	}
	if err := m.Reconfigure(4, 1, 1, 1); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("want ErrClockRollback, got %v", err)
	}
	s, _ = m.State(5)
	if s.Tc != 30 || s.Te != 10 || s.CIR != 100 {
		t.Fatalf("rejected reconfigure must not change state, got Tc=%d Te=%d CIR=%d", s.Tc, s.Te, s.CIR)
	}
}

// TestValidationAndRollback 覆盖所有可区分错误原因；被拒绝调用不 Refill、不改状态。
func TestValidationAndRollback(t *testing.T) {
	bad := []struct {
		cfg Config
		err error
	}{
		{Config{CIR: 0, CBS: 1, EBS: 0, W: 1, K: 1, Pn: 1}, ErrInvalidCIR},
		{Config{CIR: maxCIR + 1, CBS: 1, EBS: 0, W: 1, K: 1, Pn: 1}, ErrInvalidCIR},
		{Config{CIR: 1, CBS: 0, EBS: 0, W: 1, K: 1, Pn: 1}, ErrInvalidCBS},
		{Config{CIR: 1, CBS: 1, EBS: -1, W: 1, K: 1, Pn: 1}, ErrInvalidEBS},
		{Config{CIR: 1, CBS: 1, EBS: 0, W: 0, K: 1, Pn: 1}, ErrInvalidWindow},
		{Config{CIR: 1, CBS: 1, EBS: 0, W: 1, K: 0, Pn: 1}, ErrInvalidThreshold},
		{Config{CIR: 1, CBS: 1, EBS: 0, W: 1, K: 1001, Pn: 1}, ErrInvalidThreshold},
		{Config{CIR: 1, CBS: 1, EBS: 0, W: 1, K: 1, Pn: 0}, ErrInvalidPenaltyDur},
	}
	for i, b := range bad {
		if _, err := New(b.cfg); !errors.Is(err, b.err) {
			t.Fatalf("case %d want %v, got %v", i, b.err, err)
		}
	}

	m := mustNew(t, baseCfg(3))
	m.Mark(10, Green, 1) // last=10
	if _, err := m.Mark(-1, Green, 1); !errors.Is(err, ErrInvalidNow) {
		t.Fatalf("want ErrInvalidNow, got %v", err)
	}
	if _, err := m.Mark(maxNow+1, Green, 1); !errors.Is(err, ErrInvalidNow) {
		t.Fatalf("want ErrInvalidNow, got %v", err)
	}
	if _, err := m.Mark(10, Color(99), 1); !errors.Is(err, ErrInvalidColor) {
		t.Fatalf("want ErrInvalidColor, got %v", err)
	}
	if _, err := m.Mark(10, Green, 0); !errors.Is(err, ErrInvalidBytes) {
		t.Fatalf("want ErrInvalidBytes, got %v", err)
	}
	if _, err := m.Mark(10, Green, maxB+1); !errors.Is(err, ErrInvalidBytes) {
		t.Fatalf("want ErrInvalidBytes, got %v", err)
	}
	// 参数非法优先于时钟回退。
	if _, err := m.Mark(9, Color(99), 1); !errors.Is(err, ErrInvalidColor) {
		t.Fatalf("invalid param must take precedence over rollback, got %v", err)
	}
	if _, err := m.Mark(9, Green, 1); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("want ErrClockRollback, got %v", err)
	}
	// 被拒绝调用不 Refill：在 t=11 补 1（不是从 9 补 2）。
	s, _ := m.State(11)
	if s.Last != 11 || s.Tc != 100 { // Tc 99 + 10 = 100 封顶
		t.Fatalf("rejected calls must not refill, got last=%d Tc=%d", s.Last, s.Tc)
	}
}

// TestByteConservation 输入字节恰计入某个输出色；并校验两条令牌守恒界。
func TestByteConservation(t *testing.T) {
	m := mustNew(t, baseCfg(3))
	pkts := []struct {
		now   int64
		color Color
		b     int64
	}{
		{0, Green, 100}, {0, Green, 50}, {10, Yellow, 30}, {20, Green, 120},
		{25, Blind, 40}, {30, Red, 70}, {60, Green, 300}, {80, Yellow, 5},
	}
	var inBytes int64
	for _, p := range pkts {
		r, err := m.Mark(p.now, p.color, p.b)
		if err != nil {
			t.Fatal(err)
		}
		logResult(t, "CONS", p.now, p.color, p.b, r)
		inBytes += p.b
	}
	gc, gb, yc, yb, rc, rb := m.Counters()
	if inBytes != gb+yb+rb {
		t.Fatalf("byte conservation: in=%d != G=%d+Y=%d+R=%d", inBytes, gb, yb, rb)
	}
	if int64(len(pkts)) != gc+yc+rc {
		t.Fatalf("packet conservation: in=%d != %d+%d+%d", len(pkts), gc, yc, rc)
	}

	// 未 Reconfigure：G+Y 字节 <= CBS+EBS+CIR*last。
	s, _ := m.State(80)
	bound := m.cbs + m.ebs + m.cir*s.Last
	if gb+yb > bound {
		t.Fatalf("G+Y bytes %d exceed bound CBS+EBS+CIR*last=%d", gb+yb, bound)
	}
	// Yellow 字节 <= EBS + 历史溢入 Te 总量。
	if yb > m.ebs+m.TotalOverflowToTe() {
		t.Fatalf("Yellow bytes %d exceed EBS+overflow=%d", yb, m.ebs+m.TotalOverflowToTe())
	}
	t.Logf("counters: G(%d pkt,%d B) Y(%d pkt,%d B) R(%d pkt,%d B); bound=%d overflow=%d",
		gc, gb, yc, yb, rc, rb, bound, m.TotalOverflowToTe())
}

// TestPenaltyTriggerAndExpiry 覆盖 K=2 的窗口边界（99 触发 / 100 剔除）与惩罚期行为。
func TestPenaltyTriggerAndExpiry(t *testing.T) {
	m := mustNew(t, baseCfg(2))

	r, _ := m.Mark(0, Green, 1000) // b>CBS 且 >EBS：恒红、不扣令牌
	logResult(t, "K2", 0, Green, 1000, r)
	if r.Color != Red || r.Tc != 100 || r.Te != 50 || r.RedQueueLen != 1 {
		t.Fatalf("want Red Tc=100 Te=50 q=1, got %s %d %d %d", r.Color, r.Tc, r.Te, r.RedQueueLen)
	}

	// 第二个判红在 t=99：0 仍在窗口内，{0,99} 达 K，触发，until=149，s=0。
	r, _ = m.Mark(99, Green, 1000)
	logResult(t, "K2", 99, Green, 1000, r)
	if r.Color != Red || r.Until != 149 || r.Penalized || !r.InPenalty || r.RedQueueLen != 0 || r.Streak != 0 {
		t.Fatalf("want until=149 queue cleared s=0 (trigger packet not penalized, now in penalty), got until=%d penalized=%v inPenalty=%v q=%d s=%d",
			r.Until, r.Penalized, r.InPenalty, r.RedQueueLen, r.Streak)
	}

	// 惩罚期内任何输入色都红、不扣令牌、不记红。
	snap, _ := m.State(100)
	r, _ = m.Mark(100, Yellow, 1)
	logResult(t, "K2", 100, Yellow, 1, r)
	if !r.Penalized || r.Color != Red || r.Tc != snap.Tc || r.Te != snap.Te || r.RedQueueLen != 0 {
		t.Fatal("penalty packet must be red without deduction/recording")
	}
	r, _ = m.Mark(148, Red, 10)
	logResult(t, "K2", 148, Red, 10, r)
	if !r.Penalized || r.RedQueueLen != 0 {
		t.Fatal("within penalty must stay penalized without recording")
	}

	// now=149：now < until 不再成立，惩罚期结束。
	r, _ = m.Mark(149, Green, 10)
	logResult(t, "K2", 149, Green, 10, r)
	if r.Penalized || r.InPenalty {
		t.Fatal("now==until must leave the penalty period")
	}
	if r.Tc != 90 || r.Te != 50 { // 1490 补充后两桶均满，本次扣 10
		t.Fatalf("want Tc=90 Te=50 at 149, got Tc=%d Te=%d", r.Tc, r.Te)
	}

	// 另一新实例：第二个判红在 t=100，0+W<=100 恰等剔除，队列仅 1 条，不触发。
	m2 := mustNew(t, baseCfg(2))
	m2.Mark(0, Green, 1000)
	r, _ = m2.Mark(100, Green, 1000)
	logResult(t, "K2b", 100, Green, 1000, r)
	if r.Until != 0 || r.RedQueueLen != 1 {
		t.Fatalf("at t=100 record 0 must be pruned: until=%d q=%d", r.Until, r.RedQueueLen)
	}
}

// TestPenaltyEscalation 覆盖连击递增、窗口恰等回零与封顶 3（最长 8*Pn）。
func TestPenaltyEscalation(t *testing.T) {
	// 接 99 触发例：s=0, lastUntil=149。
	m := mustNew(t, baseCfg(2))
	m.Mark(0, Green, 1000)
	m.Mark(99, Green, 1000) // until=149

	m.Mark(150, Green, 1000) // 记录 150
	r, _ := m.Mark(160, Green, 1000)
	logResult(t, "ESC", 160, Green, 1000, r)
	if r.Until != 260 || r.Streak != 1 { // 160-149=11<100 -> s=1 dur=100
		t.Fatalf("want until=260 s=1, got until=%d s=%d", r.Until, r.Streak)
	}

	m.Mark(400, Green, 1000)
	r, _ = m.Mark(410, Green, 1000) // 410-260=150>=100 -> s=0 dur=50
	logResult(t, "ESC", 410, Green, 1000, r)
	if r.Until != 460 || r.Streak != 0 {
		t.Fatalf("want until=460 s=0, got until=%d s=%d", r.Until, r.Streak)
	}

	// s=1、lastUntil=260 时：trigger=359（差 99<100）s=2 dur=200 until=559；
	// trigger=360（差 100，恰等不算）s 回 0 dur=50 until=410。
	mk := func(trigger int64) (int64, int) {
		mm := mustNew(t, baseCfg(2))
		mm.Mark(0, Green, 1000)
		mm.Mark(99, Green, 1000) // until=149
		mm.Mark(150, Green, 1000)
		mm.Mark(160, Green, 1000) // until=260, s=1
		mm.Mark(trigger-1, Green, 1000)
		rr, _ := mm.Mark(trigger, Green, 1000)
		logResult(t, "BND", trigger, Green, 1000, rr)
		return rr.Until, rr.Streak
	}
	until359, s359 := mk(359)
	if until359 != 559 || s359 != 2 {
		t.Fatalf("at 359 want s=2 dur=200 until=559, got s=%d until=%d", s359, until359)
	}
	until360, s360 := mk(360)
	if until360 != 410 || s360 != 0 {
		t.Fatalf("at 360 want s=0 dur=50 until=410, got s=%d until=%d", s360, until360)
	}

	// 连击封顶 3：dur 最长 8*Pn=400，之后不再增长。
	cap := mustNew(t, baseCfg(2))
	cap.Mark(0, Green, 1000)
	cap.Mark(99, Green, 1000) // until=149 s=0
	want := []struct {
		t1, t2, until int64
		s             int
	}{
		{150, 160, 260, 1},
		{261, 262, 462, 2},
		{463, 464, 864, 3},
		{865, 866, 1266, 3},
	}
	for _, w := range want {
		cap.Mark(w.t1, Green, 1000)
		rr, _ := cap.Mark(w.t2, Green, 1000)
		logResult(t, "CAP", w.t2, Green, 1000, rr)
		if rr.Until != w.until || rr.Streak != w.s {
			t.Fatalf("t2=%d want until=%d s=%d, got until=%d s=%d", w.t2, w.until, w.s, rr.Until, rr.Streak)
		}
	}
}

func baseCfg(k int) Config {
	return Config{CIR: 10, CBS: 100, EBS: 50, W: 100, K: k, Pn: 50}
}

func logResult(t *testing.T, name string, now int64, c Color, b int64, r Result) {
	t.Helper()
	t.Logf("%s: Mark(now=%d, in=%s, b=%d) -> out=%s penalized=%v Tc=%d Te=%d inPenalty=%v until=%d queue=%d s=%d | %s",
		name, now, c, b, r.Color, r.Penalized, r.Tc, r.Te, r.InPenalty, r.Until, r.RedQueueLen, r.Streak, r.Reason)
}

// TestSpecExample 对应题目给出的逐步示例（K=3）。
func TestSpecExample(t *testing.T) {
	m := mustNew(t, baseCfg(3))

	r, err := m.Mark(0, Green, 100)
	if err != nil {
		t.Fatal(err)
	}
	logResult(t, "K3", 0, Green, 100, r)
	if r.Color != Green || r.Tc != 0 || r.Te != 50 {
		t.Fatalf("want Green Tc=0 Te=50, got %s Tc=%d Te=%d", r.Color, r.Tc, r.Te)
	}

	r, err = m.Mark(0, Green, 60)
	if err != nil {
		t.Fatal(err)
	}
	logResult(t, "K3", 0, Green, 60, r)
	if r.Color != Red || r.Tc != 0 || r.Te != 50 || r.RedQueueLen != 1 {
		t.Fatalf("want Red Tc=0 Te=50 queue={0}, got %s Tc=%d Te=%d q=%d", r.Color, r.Tc, r.Te, r.RedQueueLen)
	}

	r, err = m.Mark(0, Green, 50)
	if err != nil {
		t.Fatal(err)
	}
	logResult(t, "K3", 0, Green, 50, r)
	if r.Color != Yellow || r.Te != 0 || r.Tc != 0 {
		t.Fatalf("want Yellow Te=0, got %s Tc=%d Te=%d", r.Color, r.Tc, r.Te)
	}

	r, err = m.Mark(10, Green, 80)
	if err != nil {
		t.Fatal(err)
	}
	logResult(t, "K3", 10, Green, 80, r)
	if r.Color != Green || r.Tc != 20 || r.Te != 0 {
		t.Fatalf("want Green Tc=20 Te=0, got %s Tc=%d Te=%d", r.Color, r.Tc, r.Te)
	}

	// now=30：add=20*10=200，Tc=220 -> 溢出 120，Tc=100，Te=min(50,0+120)=50。
	r, err = m.Mark(30, Yellow, 30)
	if err != nil {
		t.Fatal(err)
	}
	logResult(t, "K3", 30, Yellow, 30, r)
	if r.Color != Yellow || r.Tc != 100 || r.Te != 20 {
		t.Fatalf("want Yellow Tc=100 Te=20, got %s Tc=%d Te=%d", r.Color, r.Tc, r.Te)
	}

	// Yellow 输入 Te=20 < 25 判红，不降级使用 Tc，并记红。
	r, err = m.Mark(30, Yellow, 25)
	if err != nil {
		t.Fatal(err)
	}
	logResult(t, "K3", 30, Yellow, 25, r)
	if r.Color != Red || r.Tc != 100 || r.Te != 20 || r.RedQueueLen != 2 {
		t.Fatalf("want Red Tc=100 Te=20 queue len 2, got %s Tc=%d Te=%d q=%d", r.Color, r.Tc, r.Te, r.RedQueueLen)
	}

	// 同刻 Green 25：Tc 充足走绿。
	r, err = m.Mark(30, Green, 25)
	if err != nil {
		t.Fatal(err)
	}
	logResult(t, "K3", 30, Green, 25, r)
	if r.Color != Green || r.Tc != 75 || r.Te != 20 {
		t.Fatalf("want Green Tc=75 Te=20, got %s Tc=%d Te=%d", r.Color, r.Tc, r.Te)
	}
}
