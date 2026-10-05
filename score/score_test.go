package score

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/obligation"
	"ontology/quote"
)

func mustEngine(t *testing.T, open, close, qmin, s, g, r, k int64) *Engine {
	t.Helper()
	e, err := New(open, close, qmin, s, g, r, k)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err=%v, want errors.Is %v", err, want)
	}
}

// 题面例题：open=100, close=1100, Qmin=10, S=100, G=5, R=90，第 0 日。
func TestWorkedExample(t *testing.T) {
	run := func(withdrawAt int64) Result {
		e := mustEngine(t, 100, 1100, 10, 100, 5, 90, 3)
		must(t, e.Register(0, "mm", "X"))
		must(t, e.Quote(100, "mm", "X", 9950, 20, 10050, 20)) // 价差取等合格
		must(t, e.Fill(400, "mm", "X", quote.Bid, 15))        // t0=400
		must(t, e.Quote(405, "mm", "X", 9950, 20, 10050, 20)) // 405<=405 补记 [400,405)
		must(t, e.Fill(700, "mm", "X", quote.Ask, 15))        // t0=700
		must(t, e.Quote(706, "mm", "X", 9950, 20, 10050, 20)) // 晚 1 秒，[700,706) 整段不合格
		must(t, e.Exempt(750, "X", 800, 900))
		must(t, e.Withdraw(withdrawAt, "mm", "X"))
		res, err := e.Settle(1100, "mm", "X", 0)
		must(t, err)
		return res
	}
	res := run(1016)
	t.Logf("withdraw@1016: A=%d D=%d outcome=%v（依据：A=300+5+295+94+116=810，D=1000-100=900，810*100=81000=90*900 取等达标）", res.A, res.D, res.Outcome)
	if res.A != 810 || res.D != 900 || res.Outcome != Pass || res.Misses != 0 {
		t.Fatalf("got %+v, want A=810 D=900 pass", res)
	}
	res = run(1015)
	t.Logf("withdraw@1015: A=%d D=%d outcome=%v（依据：A=809，809*100<90*900 不达标）", res.A, res.D, res.Outcome)
	if res.A != 809 || res.D != 900 || res.Outcome != Fail || res.Misses != 1 {
		t.Fatalf("got %+v, want A=809 D=900 fail misses=1", res)
	}
}

func TestNewInvalidParams(t *testing.T) {
	cases := [][7]int64{
		{-1, 1000, 1, 1, 0, 1, 1},            // open<0
		{1000, 1000, 1, 1, 0, 1, 1},          // open==close
		{2000, 1000, 1, 1, 0, 1, 1},          // open>close
		{0, 86401, 1, 1, 0, 1, 1},            // close>86400
		{0, 1000, 0, 1, 0, 1, 1},             // Qmin<1
		{0, 1000, 1_000_000_001, 1, 0, 1, 1}, // Qmin>1e9
		{0, 1000, 1, 0, 0, 1, 1},             // S<1
		{0, 1000, 1, 10001, 0, 1, 1},         // S>10000
		{0, 1000, 1, 1, -1, 1, 1},            // G<0
		{0, 1000, 1, 1, 3601, 1, 1},          // G>3600
		{0, 1000, 1, 1, 0, 0, 1},             // R<1
		{0, 1000, 1, 1, 0, 101, 1},           // R>100
		{0, 1000, 1, 1, 0, 1, 0},             // K<1
		{0, 1000, 1, 1, 0, 1, 101},           // K>100
	}
	for i, c := range cases {
		if _, err := New(c[0], c[1], c[2], c[3], c[4], c[5], c[6]); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("case %d: err=%v, want ErrInvalidParam", i, err)
		}
	}
	if _, err := New(0, 86400, 1, 1, 0, 1, 1); err != nil {
		t.Errorf("valid config: %v", err)
	}
}

// 拒绝按次序只报第一个：参数非法 > 时钟回退 > 未登记 > 已暂停 > 未收盘 > 状态不符或冲突。
func TestRejectionOrder(t *testing.T) {
	newEngine := func(t *testing.T) *Engine {
		e := mustEngine(t, 100, 1100, 10, 100, 5, 90, 1)
		must(t, e.Register(100, "mm", "X"))
		must(t, e.Quote(200, "mm", "X", 9950, 20, 10050, 20)) // maxNow=200
		return e
	}
	cases := []struct {
		name string
		call func(e *Engine) error
		want error
	}{
		{"参数非法优先于时钟回退", func(e *Engine) error {
			return e.Quote(150, "mm", "X", 0, 20, 10050, 20) // bid 非法且 now 回退
		}, ErrInvalidParam},
		{"参数非法优先于未登记", func(e *Engine) error {
			return e.Quote(300, "ghost", "X", 0, 20, 10050, 20)
		}, ErrInvalidParam},
		{"时钟回退优先于未登记", func(e *Engine) error {
			return e.Quote(150, "ghost", "X", 9950, 20, 10050, 20)
		}, ErrClock},
		{"未登记优先于状态不符", func(e *Engine) error {
			return e.Withdraw(300, "ghost", "X")
		}, ErrNotRegistered},
		{"未收盘优先于结算日次序", func(e *Engine) error {
			_, err := e.Settle(500, "mm", "X", 3) // 未收盘且日号错
			return err
		}, ErrNotClosed},
		{"结算日次序状态不符", func(e *Engine) error {
			_, err := e.Settle(86400+1100, "mm", "X", 1) // day1 已收盘但应为 day 0
			return err
		}, ErrState},
		{"重复登记状态不符", func(e *Engine) error {
			return e.Register(300, "mm", "X")
		}, ErrState},
		{"撤单无报价状态不符", func(e *Engine) error {
			return e.Withdraw(300, "mm", "Y2")
		}, ErrState},
		{"成交超量状态不符", func(e *Engine) error {
			return e.Fill(300, "mm", "X", quote.Bid, 21)
		}, ErrState},
		{"成交侧非法参数", func(e *Engine) error {
			return e.Fill(300, "mm", "X", quote.Side(7), 1)
		}, ErrInvalidParam},
		{"豁免参数非法", func(e *Engine) error {
			return e.Exempt(300, "X", 250, 900) // now>from
		}, ErrInvalidParam},
		{"豁免区间非法", func(e *Engine) error {
			return e.Exempt(300, "X", 900, 900)
		}, ErrInvalidParam},
		{"豁免超上限参数非法", func(e *Engine) error {
			return e.Exempt(300, "X", 900, 1_000_000_000_001)
		}, ErrInvalidParam},
		{"恢复资格非暂停状态不符", func(e *Engine) error {
			return e.Reinstate(300, "mm", "X")
		}, ErrState},
	}
	for _, c := range cases {
		e := newEngine(t)
		must(t, e.Register(250, "mm", "Y2")) // 供未登记/状态不符区分
		err := c.call(e)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v, want %v", c.name, err, c.want)
		}
	}
}

// 被拒绝的操作不改任何状态，含时钟。
func TestRejectedOpKeepsState(t *testing.T) {
	e := mustEngine(t, 100, 1100, 10, 100, 5, 90, 3)
	must(t, e.Register(100, "mm", "X"))
	must(t, e.Quote(200, "mm", "X", 9950, 20, 10050, 20)) // maxNow=200
	// 状态不符的拒绝不推进时钟
	wantErr(t, e.Fill(500, "mm", "X", quote.Bid, 21), ErrState)
	// 时钟回退的拒绝也不推进时钟
	wantErr(t, e.Quote(150, "mm", "X", 9950, 20, 10050, 20), ErrClock)
	// 冲突的豁免不推进时钟
	must(t, e.Exempt(300, "X", 800, 900))
	wantErr(t, e.Exempt(400, "X", 850, 950), ErrConflict)
	// 时钟仍停在 300：now=350 的合法操作被接受
	must(t, e.Quote(350, "mm", "X", 9950, 20, 10050, 20))
	// 报价未被拒绝的 Fill 改变：量仍是 20
	wantErr(t, e.Fill(360, "mm", "X", quote.Bid, 21), ErrState)
	must(t, e.Fill(360, "mm", "X", quote.Bid, 20))
}

func TestExemptTouchingAndOverlap(t *testing.T) {
	e := mustEngine(t, 100, 1100, 10, 100, 5, 90, 3)
	must(t, e.Exempt(0, "X", 800, 900))
	must(t, e.Exempt(0, "X", 900, 1000)) // 首尾相接
	must(t, e.Exempt(0, "X", 700, 800))  // 首尾相接
	wantErr(t, e.Exempt(0, "X", 850, 950), ErrConflict)
	wantErr(t, e.Exempt(0, "X", 750, 801), ErrConflict)
	wantErr(t, e.Exempt(0, "X", 600, 2000), ErrConflict)
	must(t, e.Exempt(0, "X", 1000, 1100)) // 首尾相接
	// Exempt 不要求登记做市关系
	must(t, e.Exempt(0, "ghost-sym", 100, 200))
}

func TestSettleDayOrder(t *testing.T) {
	e := mustEngine(t, 100, 1100, 10, 100, 5, 90, 100)
	must(t, e.Register(86400+500, "mm", "X")) // 第 1 日登记
	// 首次结算必须是登记所在日（第 1 日）
	if _, err := e.Settle(86400+1100, "mm", "X", 0); !errors.Is(err, ErrState) {
		t.Fatalf("settle day0: %v, want ErrState", err)
	}
	res, err := e.Settle(86400+1100, "mm", "X", 1)
	must(t, err)
	// 登记当日按整个时段计应报
	if res.D != 1000 || res.A != 0 || res.Outcome != Fail {
		t.Fatalf("day1: %+v, want D=1000 A=0 fail", res)
	}
	// 跳日/重复日均状态不符（此时 day3 已收盘，排除未收盘干扰）
	if _, err := e.Settle(3*86400+1100, "mm", "X", 1); !errors.Is(err, ErrState) {
		t.Fatalf("repeat day1: %v, want ErrState", err)
	}
	if _, err := e.Settle(3*86400+1100, "mm", "X", 3); !errors.Is(err, ErrState) {
		t.Fatalf("skip day3: %v, want ErrState", err)
	}
	if _, err := e.Settle(3*86400+1100, "mm", "X", 2); err != nil {
		t.Fatalf("day2: %v", err)
	}
}

// 连续 K 日不达标即暂停，现有报价在 Settle 的 now 被清除；
// 暂停后 Quote/Withdraw/Fill/Settle 均报已暂停；Reinstate 恢复。
func TestSuspension(t *testing.T) {
	e := mustEngine(t, 0, 100, 10, 100, 5, 90, 2)
	must(t, e.Register(0, "mm", "X"))
	// day0：有报价但量不足，不合格且无宽限
	must(t, e.Quote(0, "mm", "X", 9950, 5, 10050, 5))
	res, err := e.Settle(100, "mm", "X", 0)
	must(t, err)
	if res.Outcome != Fail || res.Misses != 1 || res.Suspended {
		t.Fatalf("day0: %+v, want fail misses=1", res)
	}
	// day1：仍不合格，连续 2 日达 K，暂停并清除报价
	res, err = e.Settle(86400+100, "mm", "X", 1)
	must(t, err)
	if res.Outcome != Fail || res.Misses != 2 || !res.Suspended {
		t.Fatalf("day1: %+v, want fail misses=2 suspended", res)
	}
	// 暂停后操作均报已暂停
	wantErr(t, e.Quote(86400+200, "mm", "X", 9950, 20, 10050, 20), ErrSuspended)
	wantErr(t, e.Withdraw(86400+200, "mm", "X"), ErrSuspended)
	wantErr(t, e.Fill(86400+200, "mm", "X", quote.Bid, 1), ErrSuspended)
	if _, err := e.Settle(2*86400+100, "mm", "X", 2); !errors.Is(err, ErrSuspended) {
		t.Fatalf("settle while suspended: %v, want ErrSuspended", err)
	}
	// Reinstate 恢复，连续数清零，首个结算日为恢复所在日
	must(t, e.Reinstate(86400+300, "mm", "X"))
	// 报价已在暂停时清除：撤单报状态不符
	wantErr(t, e.Withdraw(86400+400, "mm", "X"), ErrState)
	// 首个结算日 = Reinstate 所在日（第 1 日重新结算）；拒绝不推进时钟
	if _, err := e.Settle(2*86400+100, "mm", "X", 2); !errors.Is(err, ErrState) {
		t.Fatalf("settle day2 after reinstate: %v, want ErrState", err)
	}
	// 恢复后报价可重新登记（第 2 日开盘即合格）
	must(t, e.Quote(2*86400+0, "mm", "X", 9950, 20, 10050, 20))
	res, err = e.Settle(2*86400+100, "mm", "X", 1)
	must(t, err)
	if res.Outcome != Fail || res.Misses != 1 { // 连续数已清零，重新计 1
		t.Fatalf("re-settle day1: %+v, want fail misses=1", res)
	}
	res, err = e.Settle(3*86400+100, "mm", "X", 2)
	must(t, err)
	if res.Outcome != Pass || res.A != 100 || res.Misses != 0 {
		t.Fatalf("day2: %+v, want pass A=100", res)
	}
}

// D=0 时结果为跳过，连续不达标数不变。
func TestSettleSkipWhenDZero(t *testing.T) {
	e := mustEngine(t, 0, 100, 10, 100, 5, 90, 5)
	must(t, e.Register(0, "mm", "X"))
	// day0 不达标，misses=1
	res, err := e.Settle(100, "mm", "X", 0)
	must(t, err)
	if res.Outcome != Fail || res.Misses != 1 {
		t.Fatalf("day0: %+v", res)
	}
	// day1 整个时段豁免：D=0 跳过，misses 不变
	must(t, e.Exempt(200, "X", 86400, 86400+100))
	res, err = e.Settle(86400+100, "mm", "X", 1)
	must(t, err)
	t.Logf("day1 skip: A=%d D=%d outcome=%v misses=%d（依据：时段被豁免全覆盖，D=0 跳过）", res.A, res.D, res.Outcome, res.Misses)
	if res.Outcome != Skip || res.D != 0 || res.Misses != 1 {
		t.Fatalf("day1: %+v, want skip misses=1", res)
	}
	// day2 再不达标，misses=2（跳过日未清零）
	res, err = e.Settle(2*86400+100, "mm", "X", 2)
	must(t, err)
	if res.Outcome != Fail || res.Misses != 2 {
		t.Fatalf("day2: %+v, want fail misses=2", res)
	}
}

// 豁免与不合格段相交：相交部分只影响 D，不影响 A。
func TestExemptIntersectsUnqualified(t *testing.T) {
	e := mustEngine(t, 100, 1100, 10, 100, 0, 90, 3)
	must(t, e.Register(0, "mm", "X"))
	must(t, e.Quote(100, "mm", "X", 9950, 20, 10050, 20)) // 合格
	must(t, e.Exempt(350, "X", 500, 600))                 // 落在不合格段 [400,700) 内
	must(t, e.Quote(400, "mm", "X", 9950, 20, 10200, 20)) // 价差超限，主动改宽无宽限
	must(t, e.Quote(700, "mm", "X", 9950, 20, 10050, 20)) // 恢复合格
	res, err := e.Settle(1100, "mm", "X", 0)
	must(t, err)
	t.Logf("A=%d D=%d outcome=%v（依据：A=[100,400)+[700,1100)=700，D=1000-100=900）", res.A, res.D, res.Outcome)
	if res.A != 700 || res.D != 900 || res.Outcome != Fail {
		t.Fatalf("got %+v, want A=700 D=900 fail", res)
	}
}

// 达标取等：A*100==R*D 即达标并清零连续数。
func TestPassEquality(t *testing.T) {
	e := mustEngine(t, 0, 100, 10, 100, 0, 50, 3)
	must(t, e.Register(0, "mm", "X"))
	must(t, e.Quote(0, "mm", "X", 9950, 20, 10050, 20))
	must(t, e.Withdraw(50, "mm", "X")) // [0,50) 合格，A=50=D/2，R=50 取等
	res, err := e.Settle(100, "mm", "X", 0)
	must(t, err)
	if res.A != 50 || res.D != 100 || res.Outcome != Pass {
		t.Fatalf("got %+v, want A=50 D=100 pass（取等达标）", res)
	}
}

// 价差取等合格与多 1 不合格（引擎级）。
func TestSpreadBoundaryEngine(t *testing.T) {
	e := mustEngine(t, 0, 100, 10, 100, 0, 1, 3)
	must(t, e.Register(0, "eq", "X"))
	must(t, e.Register(0, "plus1", "X"))
	must(t, e.Quote(0, "eq", "X", 9950, 20, 10050, 20))    // 价差取等
	must(t, e.Quote(0, "plus1", "X", 9950, 20, 10051, 20)) // 价差多 1
	eq, err := e.Settle(100, "eq", "X", 0)
	must(t, err)
	p1, err := e.Settle(100, "plus1", "X", 0)
	must(t, err)
	t.Logf("取等: A=%d；多1: A=%d（依据：(ask-bid)*20000 与 S*(ask+bid) 比较）", eq.A, p1.A)
	if eq.A != 100 || eq.Outcome != Pass {
		t.Fatalf("取等应合格: %+v", eq)
	}
	if p1.A != 0 || p1.Outcome != Fail {
		t.Fatalf("多1应不合格: %+v", p1)
	}
}

// Settle 不重放当日报价事件：replayed 恒为 0，与当日 Quote 次数无关。
func TestSettleNoReplay(t *testing.T) {
	e := mustEngine(t, 0, 86400, 1, 100, 0, 1, 100)
	must(t, e.Register(0, "few", "X"))
	must(t, e.Register(0, "many", "X"))
	for i := int64(0); i < 10; i++ {
		must(t, e.Quote(i, "few", "X", 1000, 10, 1001, 10))
	}
	for i := int64(0); i < 10000; i++ {
		must(t, e.Quote(10+i, "many", "X", 1000, 10, 1001, 10))
	}
	if got := e.accts[key{"few", "X"}].tr.Quotes(); got != 10 {
		t.Fatalf("few quotes=%d, want 10", got)
	}
	if got := e.accts[key{"many", "X"}].tr.Quotes(); got != 10000 {
		t.Fatalf("many quotes=%d, want 10000", got)
	}
	if _, err := e.Settle(86400, "few", "X", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Settle(86400, "many", "X", 0); err != nil {
		t.Fatal(err)
	}
	t.Logf("10 次与 10000 次 Quote 两档：replayed=%d（依据：增量累计，结算访问事件记录数为 0）", e.replayed)
	if e.replayed != 0 {
		t.Fatalf("replayed=%d, want 0", e.replayed)
	}
}

// 相同操作序列重放得到相同的结算结果。
func TestReplayDeterminism(t *testing.T) {
	script := func(e *Engine) []Result {
		var out []Result
		must(t, e.Register(0, "mm", "X"))
		must(t, e.Quote(100, "mm", "X", 9950, 20, 10050, 20))
		must(t, e.Fill(400, "mm", "X", quote.Bid, 15))
		must(t, e.Quote(405, "mm", "X", 9950, 20, 10050, 20))
		must(t, e.Exempt(500, "X", 800, 900))
		must(t, e.Withdraw(1016, "mm", "X"))
		for d := int64(0); d < 3; d++ {
			res, err := e.Settle(d*86400+1100, "mm", "X", d)
			must(t, err)
			out = append(out, res)
		}
		return out
	}
	e1 := mustEngine(t, 100, 1100, 10, 100, 5, 90, 3)
	e2 := mustEngine(t, 100, 1100, 10, 100, 5, 90, 3)
	r1, r2 := script(e1), script(e2)
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("day %d: %+v != %+v", i, r1[i], r2[i])
		}
	}
}

// 并发调用等价于某个串行顺序（配合 -race 验证）。
func TestConcurrent(t *testing.T) {
	e := mustEngine(t, 0, 1000, 5, 100, 5, 90, 3)
	const pairs = 8
	for i := 0; i < pairs; i++ {
		must(t, e.Register(0, fmt.Sprintf("mm%d", i), "X"))
	}
	var wg sync.WaitGroup
	for i := 0; i < pairs; i++ {
		wg.Add(1)
		go func(mm string) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = e.Quote(0, mm, "X", 1000, 10, 1001, 10)
				_ = e.Fill(0, mm, "X", quote.Bid, 5)
				_ = e.Quote(0, mm, "X", 1000, 10, 1001, 10)
				_ = e.Withdraw(0, mm, "X")
			}
		}(fmt.Sprintf("mm%d", i))
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				from := int64(2000 + i*2000 + j*100)
				_ = e.Exempt(0, "X", from, from+50)
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < pairs; i++ {
		res, err := e.Settle(1000, fmt.Sprintf("mm%d", i), "X", 0)
		must(t, err)
		if !(0 <= res.A && res.A <= res.D && res.D <= 1000) || res.Misses > 3 {
			t.Fatalf("mm%d: 不变量被破坏 %+v", i, res)
		}
	}
}

// ---- 逐秒朴素实现（随机对照用） ----

type naive struct {
	open, close, qmin, s, g, r, k int64
	wins                          map[string][][2]int64
	pairs                         map[string]*naivePair
}

type naivePair struct {
	q         quote.Quote
	hasQ      bool
	qual      bool
	grace     bool
	t0        int64
	last      int64
	qs        map[int64][]bool // 日号 -> 时段内逐秒合格标记
	misses    int64
	suspended bool
}

func newNaive(open, close, qmin, s, g, r, k int64) *naive {
	return &naive{open: open, close: close, qmin: qmin, s: s, g: g, r: r, k: k,
		wins: map[string][][2]int64{}, pairs: map[string]*naivePair{}}
}

func (n *naive) exemptAt(sym string, t int64) bool {
	for _, w := range n.wins[sym] {
		if t >= w[0] && t < w[1] {
			return true
		}
	}
	return false
}

func (n *naive) mark(p *naivePair, day, off int64) {
	sec := p.qs[day]
	if sec == nil {
		sec = make([]bool, n.close-n.open)
		p.qs[day] = sec
	}
	sec[off] = true
}

// advance 逐秒推进 [p.last, to)：时段内且非豁免的每秒按当前状态计入。
func (n *naive) advance(p *naivePair, sym string, to int64) {
	t := p.last
	for t < to {
		day := t / 86400
		s0 := day*86400 + n.open
		s1 := day*86400 + n.close
		if t < s0 {
			t = s0
			continue
		}
		if t >= s1 {
			t = s0 + 86400 // 次日开盘
			continue
		}
		if p.qual && !n.exemptAt(sym, t) {
			n.mark(p, day, t-s0)
		}
		t++
	}
	p.last = to
}

func (n *naive) register(mm, sym string, now int64) {
	n.pairs[mm+"/"+sym] = &naivePair{last: now, qs: map[int64][]bool{}}
}

func (n *naive) quote(mm, sym string, now int64, q quote.Quote) {
	p := n.pairs[mm+"/"+sym]
	n.advance(p, sym, now)
	qual := obligation.Qualified(q, true, n.qmin, n.s)
	if p.grace && qual {
		t0 := p.t0
		dayStart := (t0 / 86400) * 86400
		if now <= t0+n.g && now/86400 == t0/86400 && now < dayStart+n.close {
			for tt := t0; tt < now; tt++ { // 逐秒补记 [t0, now)
				s0 := (tt/86400)*86400 + n.open
				if tt < s0 || tt >= (tt/86400)*86400+n.close {
					continue
				}
				if !n.exemptAt(sym, tt) {
					n.mark(p, tt/86400, tt-s0)
				}
			}
		}
		p.grace = false
	}
	p.q, p.hasQ, p.qual = q, true, qual
}

func (n *naive) withdraw(mm, sym string, now int64) {
	p := n.pairs[mm+"/"+sym]
	n.advance(p, sym, now)
	p.hasQ, p.qual, p.grace = false, false, false
}

func (n *naive) fillQty(mm, sym string, now int64, side quote.Side, qty int64) {
	p := n.pairs[mm+"/"+sym]
	n.advance(p, sym, now)
	if side == quote.Bid {
		p.q.BidQty -= qty
	} else {
		p.q.AskQty -= qty
	}
	qual := obligation.Qualified(p.q, p.hasQ, n.qmin, n.s)
	if p.qual && !qual {
		p.grace, p.t0 = true, now
	}
	p.qual = qual
}

func (n *naive) exempt(sym string, from, to int64) {
	n.wins[sym] = append(n.wins[sym], [2]int64{from, to})
}

func (n *naive) settle(mm, sym string, now, day int64) Result {
	p := n.pairs[mm+"/"+sym]
	n.advance(p, sym, now)
	var a int64
	for _, v := range p.qs[day] {
		if v {
			a++
		}
	}
	var ex int64 // 逐秒统计当日时段内豁免
	for t := day*86400 + n.open; t < day*86400+n.close; t++ {
		if n.exemptAt(sym, t) {
			ex++
		}
	}
	d := (n.close - n.open) - ex
	res := Result{A: a, D: d}
	switch {
	case d == 0:
		res.Outcome = Skip
	case a*100 >= n.r*d:
		res.Outcome = Pass
		p.misses = 0
	default:
		res.Outcome = Fail
		p.misses++
		if p.misses >= n.k {
			p.suspended = true
			p.hasQ, p.qual, p.grace = false, false, false
		}
	}
	res.Misses = p.misses
	res.Suspended = p.suspended
	return res
}

// 1000 组随机操作序列与逐秒朴素实现对照。
func TestRandomAgainstNaive(t *testing.T) {
	for trial := 0; trial < 1000; trial++ {
		rng := rand.New(rand.NewSource(int64(trial)))
		open := rng.Int63n(500)
		close_ := open + 1 + rng.Int63n(1500)
		qmin := 1 + rng.Int63n(20)
		spread := 1 + rng.Int63n(200)
		grace := rng.Int63n(61)
		rate := 1 + rng.Int63n(100)
		numDays := 1 + rng.Intn(3)
		numPairs := 1 + rng.Intn(3)
		t.Logf("trial=%d 输入: open=%d close=%d Qmin=%d S=%d G=%d R=%d K=100 days=%d pairs=%d",
			trial, open, close_, qmin, spread, grace, rate, numDays, numPairs)

		e := mustEngine(t, open, close_, qmin, spread, grace, rate, 100)
		nv := newNaive(open, close_, qmin, spread, grace, rate, 100)

		type pair struct{ mm, sym string }
		pairs := make([]pair, numPairs)
		for i := range pairs {
			pairs[i] = pair{fmt.Sprintf("mm%d", i), fmt.Sprintf("s%d", i%2)}
			must(t, e.Register(0, pairs[i].mm, pairs[i].sym))
			nv.register(pairs[i].mm, pairs[i].sym, 0)
		}

		type mirror struct {
			hasQ             bool
			bid, ask, bq, aq int64
		}
		mirrors := make([]mirror, numPairs)
		genWins := map[string][][2]int64{}

		qualOf := func(m mirror) bool {
			return obligation.Qualified(quote.Quote{Bid: m.bid, BidQty: m.bq, Ask: m.ask, AskQty: m.aq},
				m.hasQ, qmin, spread)
		}
		// 价差恰取边界的合格报价
		boundaryQuote := func() (bid, bq, ask, aq int64) {
			bid = 1 + rng.Int63n(10000)
			delta := int64(1)
			if den := int64(20000) - spread; den > 0 {
				if num := 2 * spread * bid; num%den == 0 && num/den >= 1 {
					delta = num / den
				}
			}
			return bid, qmin, bid + delta, qmin
		}
		randQuote := func() (bid, bq, ask, aq int64) {
			bid = 1 + rng.Int63n(10000)
			exact := int64(-1)
			if den := int64(20000) - spread; den > 0 {
				if num := 2 * spread * bid; num%den == 0 && num/den >= 1 {
					exact = num / den
				}
			}
			var delta int64
			switch rng.Intn(4) {
			case 0:
				if exact > 0 {
					delta = exact // 取等合格
				} else {
					delta = 1 + rng.Int63n(100)
				}
			case 1:
				if exact > 0 {
					delta = exact + 1 // 多 1 不合格
				} else {
					delta = 1 + rng.Int63n(100)
				}
			case 2:
				if exact > 1 {
					delta = exact - 1
				} else {
					delta = 1 + rng.Int63n(100)
				}
			default:
				delta = 1 + rng.Int63n(30000)
			}
			qty := func() int64 {
				switch rng.Intn(5) {
				case 0:
					return 0
				case 1:
					return qmin - 1
				case 2:
					return qmin
				case 3:
					return qmin + 1
				default:
					return rng.Int63n(200)
				}
			}
			return bid, qty(), bid + delta, qty()
		}

		cursor := int64(0)
		for d := 0; d < numDays; d++ {
			dayBase := int64(d) * 86400
			if cursor < dayBase {
				cursor = dayBase
			}
			limit := dayBase + close_ + 20
			for i, n := 0, rng.Intn(25); i < n; i++ {
				cursor += rng.Int63n(40)
				if cursor > limit {
					break
				}
				pi := rng.Intn(numPairs)
				pr := pairs[pi]
				m := &mirrors[pi]
				switch x := rng.Float64(); {
				case x < 0.5: // Quote
					bid, bq, ask, aq := randQuote()
					must(t, e.Quote(cursor, pr.mm, pr.sym, bid, bq, ask, aq))
					nv.quote(pr.mm, pr.sym, cursor, quote.Quote{Bid: bid, BidQty: bq, Ask: ask, AskQty: aq})
					m.bid, m.bq, m.ask, m.aq, m.hasQ = bid, bq, ask, aq, true
				case x < 0.7 && m.hasQ: // Fill
					side := quote.Side(rng.Intn(2))
					cur := m.bq
					if side == quote.Ask {
						cur = m.aq
					}
					var qty int64
					switch rng.Intn(4) {
					case 0:
						qty = 0
					case 1:
						qty = cur
					case 2:
						if cur >= qmin {
							qty = cur - qmin + 1 // 恰好打到 Qmin-1
						} else {
							qty = cur
						}
					default:
						qty = rng.Int63n(cur + 1)
					}
					wasQual := qualOf(*m)
					must(t, e.Fill(cursor, pr.mm, pr.sym, side, qty))
					nv.fillQty(pr.mm, pr.sym, cursor, side, qty)
					if side == quote.Bid {
						m.bq -= qty
					} else {
						m.aq -= qty
					}
					// 成交打落合格状态后，在宽限边界附近补单（覆盖 t0+G 取等/晚 1/晚 2）
					if wasQual && !qualOf(*m) && rng.Float64() < 0.7 {
						cursor += rng.Int63n(grace + 3)
						bid, bq, ask, aq := boundaryQuote()
						must(t, e.Quote(cursor, pr.mm, pr.sym, bid, bq, ask, aq))
						nv.quote(pr.mm, pr.sym, cursor, quote.Quote{Bid: bid, BidQty: bq, Ask: ask, AskQty: aq})
						m.bid, m.bq, m.ask, m.aq = bid, bq, ask, aq
					}
				case x < 0.85 && m.hasQ: // Withdraw
					must(t, e.Withdraw(cursor, pr.mm, pr.sym))
					nv.withdraw(pr.mm, pr.sym, cursor)
					m.hasQ = false
				default: // Exempt
					sym := pairs[rng.Intn(numPairs)].sym
					from := cursor + rng.Int63n(200)
					to := from + 1 + rng.Int63n(400)
					conflict := false
					for _, w := range genWins[sym] {
						if from < w[1] && w[0] < to {
							conflict = true
							break
						}
					}
					if conflict {
						continue
					}
					must(t, e.Exempt(cursor, sym, from, to))
					nv.exempt(sym, from, to)
					genWins[sym] = append(genWins[sym], [2]int64{from, to})
				}
			}
		}

		for d := 0; d < numDays; d++ {
			for _, pr := range pairs {
				if need := int64(d)*86400 + close_; cursor < need {
					cursor = need
				}
				got, err := e.Settle(cursor, pr.mm, pr.sym, int64(d))
				must(t, err)
				want := nv.settle(pr.mm, pr.sym, cursor, int64(d))
				t.Logf("trial=%d day=%d %s/%s 输出: A=%d D=%d outcome=%v misses=%d（判定依据：逐秒朴素模拟 A=%d D=%d %v）",
					trial, d, pr.mm, pr.sym, got.A, got.D, got.Outcome, got.Misses, want.A, want.D, want.Outcome)
				if got != want {
					t.Fatalf("trial %d day %d %s/%s: got %+v, naive want %+v",
						trial, d, pr.mm, pr.sym, got, want)
				}
				if !(0 <= got.A && got.A <= got.D && got.D <= close_-open) {
					t.Fatalf("trial %d day %d: 不变量 0<=A<=D<=close-open 被破坏: %+v", trial, d, got)
				}
				if got.Misses > 100 {
					t.Fatalf("trial %d day %d: 连续不达标数超过 K: %+v", trial, d, got)
				}
			}
		}
		if e.replayed != 0 {
			t.Fatalf("trial %d: replayed=%d, want 0", trial, e.replayed)
		}
	}
}
