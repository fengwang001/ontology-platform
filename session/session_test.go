package session

import (
	"errors"
	"sync"
	"testing"
)

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", ctx, err)
	}
}

func errIs(got, want error) bool { return errors.Is(got, want) }

func checkConservation(t *testing.T, e *Engine, acct string, topped int64) {
	t.Helper()
	bal := e.ledger.Balance(acct)
	held := e.ledger.Held(acct, e.lastNow)
	in := e.ledger.TotalIn(acct)
	ch := e.ledger.TotalCharged(acct)
	if bal < 0 {
		t.Fatalf("balance negative: %d", bal)
	}
	if held > bal {
		t.Fatalf("held %d > balance %d", held, bal)
	}
	if in != topped || in != bal+ch {
		t.Fatalf("conservation: in=%d topped=%d bal=%d charged=%d", in, topped, bal, ch)
	}
}

// TestWorkedExample1 复现题面第一个完整示例（t=0..30 及重传）。
func TestWorkedExample1(t *testing.T) {
	e := New(20, 5, 60, 16)
	mustOK(t, e.SetRate(1, 3, 0), "setrate rg1")
	mustOK(t, e.SetRate(2, 1, 0), "setrate rg2")
	mustOK(t, e.TopUp("a", 100, 0), "topup")
	mustOK(t, e.Open("s1", "a", 0), "open s1")
	mustOK(t, e.Open("s2", "a", 0), "open s2")

	r1, err := e.Update("s1", 1, 1, 0, 50, 0)
	mustOK(t, err, "u1")
	if r1 != (Reply{Granted: 20, Final: false, Denied: false, ValidUntil: 60}) {
		t.Fatalf("u1 = %+v", r1)
	}
	if got := e.ledger.Free("a", 0); got != 40 {
		t.Fatalf("free after u1 = %d, want 40", got)
	}
	if e.Touched() != 1 {
		t.Fatalf("touched u1 = %d, want 1", e.Touched())
	}

	r2, err := e.Update("s2", 1, 1, 0, 10, 10)
	mustOK(t, err, "u2")
	if r2 != (Reply{Granted: 13, Final: true, Denied: false, ValidUntil: 70}) {
		t.Fatalf("u2 = %+v", r2)
	}
	if got := e.ledger.Free("a", 10); got != 1 {
		t.Fatalf("free after u2 = %d, want 1", got)
	}

	r3, err := e.Update("s2", 2, 2, 0, 5, 20)
	mustOK(t, err, "u3")
	if r3 != (Reply{Granted: 1, Final: true, Denied: false, ValidUntil: 80}) {
		t.Fatalf("u3 = %+v", r3)
	}

	// 释放自身预留 60 后 free=60；c=min(25,20)=20，未计费 5；余额 40。
	r4, err := e.Update("s1", 2, 1, 25, 20, 30)
	mustOK(t, err, "u4")
	if r4 != (Reply{Charged: 20, Unbilled: 5, Granted: 0, Final: true, Denied: true}) {
		t.Fatalf("u4 = %+v", r4)
	}
	if got := e.ledger.Balance("a"); got != 40 {
		t.Fatalf("balance = %d, want 40", got)
	}

	// 重传：原样应答，不比较其余参数，不重复扣费，未计费仍 5，余额仍 40。
	r4dup, err := e.Update("s1", 2, 1, 999, 999, 999)
	mustOK(t, err, "u4 retransmit")
	if r4dup != r4 {
		t.Fatalf("retransmit = %+v, want %+v", r4dup, r4)
	}
	if got := e.ledger.Balance("a"); got != 40 {
		t.Fatalf("balance after dup = %d, want 40", got)
	}
	checkConservation(t, e, "a", 100)
}

// TestInvalidAndOrder：参数非法、时钟回退、会话/序号错误及拒绝次序。
func TestInvalidAndOrder(t *testing.T) {
	func() {
		defer func() {
			if recover() == nil {
				t.Fatalf("New(1,2,..) should panic")
			}
		}()
		New(1, 2, 1, 1)
	}()

	e := New(20, 5, 60, 1)
	mustOK(t, e.SetRate(1, 3, 10), "setrate")
	if err := e.SetRate(0, 3, 10); !errIs(err, ErrInvalid) {
		t.Fatalf("rg=0: %v", err)
	}
	if err := e.SetRate(1, 0, 10); !errIs(err, ErrInvalid) {
		t.Fatalf("price=0: %v", err)
	}
	mustOK(t, e.TopUp("a", 100, 10), "topup")
	// 参数非法优先于时钟回退：now=5 回退，但 amount=0 非法
	if err := e.TopUp("a", 0, 5); !errIs(err, ErrInvalid) {
		t.Fatalf("invalid before rewind: %v", err)
	}
	if err := e.TopUp("a", 1, 5); !errIs(err, ErrClockRewind) {
		t.Fatalf("rewind: %v", err)
	}
	mustOK(t, e.Open("s1", "a", 10), "open")
	if err := e.Open("s1", "a", 10); !errIs(err, ErrExists) {
		t.Fatalf("dup: %v", err)
	}
	if err := e.Open("s2", "a", 10); !errIs(err, ErrSessionCap) {
		t.Fatalf("cap: %v", err)
	}
	if _, err := e.Update("nope", 1, 1, 0, 1, 10); !errIs(err, ErrNoSession) {
		t.Fatalf("no session: %v", err)
	}
	// 会话不存在优先于序号错
	if _, err := e.Update("nope", 5, 1, 0, 1, 10); !errIs(err, ErrNoSession) {
		t.Fatalf("no session before seq: %v", err)
	}
	// 无记录 used>0
	if _, err := e.Update("s1", 1, 1, 3, 1, 10); !errIs(err, ErrNoGrant) {
		t.Fatalf("no grant: %v", err)
	}
	// 序号错：被拒操作不占序号，n=1 仍可处理
	if _, err := e.Update("s1", 3, 1, 0, 1, 10); !errIs(err, ErrSeq) {
		t.Fatalf("seq jump: %v", err)
	}
	if _, err := e.Update("s1", 1, 1, 0, 1, 10); err != nil {
		t.Fatalf("n=1 should process: %v", err)
	}
}

// TestRepriceSettlement：改价只影响之后的新授予，结算用授予时单价。
func TestRepriceSettlement(t *testing.T) {
	e := New(20, 5, 1000, 4)
	mustOK(t, e.SetRate(1, 2, 0), "rate=2")
	mustOK(t, e.TopUp("a", 100, 0), "topup")
	mustOK(t, e.Open("s", "a", 0), "open")
	r, _ := e.Update("s", 1, 1, 0, 10, 0) // 授予 10 预留 20
	if r.Granted != 10 {
		t.Fatalf("grant %+v", r)
	}
	mustOK(t, e.SetRate(1, 100, 50), "rate=100")
	// 结算仍按 2：释放 20 后 free=100，c=10，扣 20，余额 80
	r2, err := e.Update("s", 2, 1, 10, 0, 60)
	mustOK(t, err, "settle")
	if r2.Charged != 10 || r2.Unbilled != 0 {
		t.Fatalf("settle %+v", r2)
	}
	if bal := e.ledger.Balance("a"); bal != 80 {
		t.Fatalf("bal=%d want 80", bal)
	}
	// 新授予按 100：u=0 → Denied
	r3, _ := e.Update("s", 3, 1, 0, 1, 70)
	if !r3.Denied || !r3.Final {
		t.Fatalf("new grant at rate 100: %+v", r3)
	}
	checkConservation(t, e, "a", 100)
}

// TestTouchedInvariant：Update 考察记录数 ≤ 本次落地到期记录数 + 2，
// 与账户会话数无关（10 与 10000 两档）。
func TestTouchedInvariant(t *testing.T) {
	for _, nSess := range []int{10, 10000} {
		e := New(20, 5, 60, nSess+1)
		mustOK(t, e.SetRate(1, 1, 0), "rate")
		mustOK(t, e.TopUp("a", 1_000_000_000_000, 0), "topup")
		for i := 0; i < nSess; i++ {
			name := "s" + itoa(i)
			mustOK(t, e.Open(name, "a", 0), "open "+name)
			_, err := e.Update(name, 1, 1, 0, 1, 0) // 每会话一条记录
			mustOK(t, err, "grant "+name)
		}
		// 在 s0 上做 Update：只考察自身 1 条；到期判定是 now<e 的纯函数，
		// 不扫描其他会话，故 touched 恒为 1 ≤ 0+2。
		if _, err := e.Update("s0", 2, 1, 1, 1, 5); err != nil {
			t.Fatalf("nSess=%d update: %v", nSess, err)
		}
		if got := e.Touched(); got > 2 {
			t.Fatalf("nSess=%d touched=%d, want ≤ 2", nSess, got)
		}
		if e.Touched() != 1 {
			t.Fatalf("nSess=%d touched=%d, want exactly 1", nSess, e.Touched())
		}
		// t=60 恰等：s0 记录已结算删除；其余会话记录到期但仍保留，touched 仍 1。
		if _, err := e.Update("s1", 2, 1, 1, 1, 60); err != nil {
			t.Fatalf("nSess=%d expired update: %v", nSess, err)
		}
		if e.Touched() != 1 {
			t.Fatalf("nSess=%d touched at expiry=%d, want 1", nSess, e.Touched())
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

// TestConcurrent：并发调用下不变量始终成立，结果等价于某一串行顺序。
func TestConcurrent(t *testing.T) {
	e := New(1_000_000, 1, 1_000_000_000, 64)
	mustOK(t, e.SetRate(1, 3, 0), "rate1")
	mustOK(t, e.SetRate(2, 7, 0), "rate2")

	const accts = 50
	var wg sync.WaitGroup
	for i := 0; i < accts; i++ {
		acct := "a" + itoa(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.TopUp(acct, 1_000_000, 1_000_000)
			for s := 0; s < 2; s++ {
				sess := acct + "-s" + itoa(s)
				if err := e.Open(sess, acct, 1_000_000); err != nil {
					t.Errorf("open %s: %v", sess, err)
					return
				}
				// 每个 rg 先只授予，下一序号对同一 rg 上报（保证记录存在）
				for n := int64(1); n <= 20; n++ {
					rg := int64(1 + ((n - 1) / 2 % 2))
					var used int64
					if n%2 == 0 { // 偶数序号：结算上一轮同 rg 的授予
						used = n % 3
					}
					r, err := e.Update(sess, n, rg, used, 10, 1_000_000) // 同一时间戳：并发等价于某串行序
					if err != nil {
						t.Errorf("update %s n=%d: %v", sess, n, err)
						return
					}
					// 重传必须返回完全相同的应答
					r2, err := e.Update(sess, n, rg, used, 10, 1_000_000)
					if err != nil || r2 != r {
						t.Errorf("retransmit %s n=%d: %+v vs %+v err=%v", sess, n, r, r2, err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	for i := 0; i < accts; i++ {
		acct := "a" + itoa(i)
		checkConservation(t, e, acct, 1_000_000)
	}
}

// TestWorkedExample2：恰到期、到期后上报、改价前后单价、超用未计费、守恒。
func TestWorkedExample2(t *testing.T) {
	run := func(usedAt70 int64, reprice bool) {
		e := New(20, 5, 60, 16)
		// 严格按时间线：t=25 改价（在 t=30 结算之前）。
		mustOK(t, e.SetRate(1, 3, 0), "setrate")
		mustOK(t, e.SetRate(2, 1, 0), "setrate2")
		mustOK(t, e.TopUp("a", 100, 0), "topup")
		mustOK(t, e.Open("s1", "a", 0), "open s1")
		mustOK(t, e.Open("s2", "a", 0), "open s2")
		_, _ = e.Update("s1", 1, 1, 0, 50, 0)
		_, _ = e.Update("s2", 1, 1, 0, 10, 10)
		_, _ = e.Update("s2", 2, 2, 0, 5, 20)
		if reprice {
			mustOK(t, e.SetRate(1, 4, 25), "reprice") // 改价不影响既有记录结算
		}
		_, _ = e.Update("s1", 2, 1, 25, 20, 30) // 余额 40
		if got := e.ledger.Free("a", 69); got != 0 {
			t.Fatalf("free@69 = %d, want 0", got)
		}
		if got := e.ledger.Free("a", 70); got != 39 {
			t.Fatalf("free@70 = %d, want 39", got)
		}
		r, err := e.Update("s2", 3, 1, usedAt70, 0, 70)
		mustOK(t, err, "settle at 70")
		wantC := usedAt70
		var wantUn int64
		if wantC > 13 {
			wantC = 13
			wantUn = usedAt70 - 13
		}
		// want=0 不授予：Granted=0，Final 与 Denied 均为假
		if r != (Reply{Charged: wantC, Unbilled: wantUn}) {
			t.Fatalf("used=%d reprice=%v: got %+v", usedAt70, reprice, r)
		}
		checkConservation(t, e, "a", 100)
	}
	run(13, false) // 加款 100 = 余额 1 + 已扣 60+39
	run(15, false) // c=13，未计费 2
	run(15, true)  // 改价后结算仍按授予时单价 3
}

// TestLminBoundary：余量恰等 Lmin 不吃零头；小 1 吃掉；g 可超过 want 与 Smax。
func TestLminBoundary(t *testing.T) {
	cases := []struct {
		name     string
		top      int64
		price    int64
		want     int64
		expGrant int64
		expFinal bool
	}{
		{"d==Lmin keep", 105, 1, 100, 100, false}, // u=105,g0=100,d=5
		{"d==Lmin-1 eat", 104, 1, 100, 104, true}, // d=4 → g=104 > Smax
		{"u==want final", 50, 1, 50, 50, true},
		{"zero denied", 0, 1, 10, 0, true}, // u=0
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Smax=100：u=105→g0=100,d=5==Lmin 不吃；u=104→d=4 吃掉零头
			e := New(100, 5, 60, 4)
			mustOK(t, e.SetRate(1, tc.price, 0), "setrate")
			if tc.top > 0 {
				mustOK(t, e.TopUp("a", tc.top, 0), "topup")
			} else {
				e.ledger.Ensure("a")
			}
			mustOK(t, e.Open("s", "a", 0), "open")
			r, err := e.Update("s", 1, 1, 0, tc.want, 0)
			mustOK(t, err, "update")
			if r.Granted != tc.expGrant || r.Final != tc.expFinal {
				t.Fatalf("got %+v, want granted=%d final=%v", r, tc.expGrant, tc.expFinal)
			}
			if tc.expGrant == 0 != r.Denied {
				t.Fatalf("denied=%v want %v", r.Denied, tc.expGrant == 0)
			}
			checkConservation(t, e, "a", tc.top)
		})
	}
}

// TestCrossRgAndWantZero：贵的买不起、便宜的买得起共用余额；want=0 不授予不追零头。
func TestCrossRgAndWantZero(t *testing.T) {
	e := New(20, 5, 60, 4)
	mustOK(t, e.SetRate(1, 10, 0), "expensive")
	mustOK(t, e.SetRate(2, 1, 0), "cheap")
	mustOK(t, e.TopUp("a", 5, 0), "topup")
	mustOK(t, e.Open("s", "a", 0), "open")

	r, err := e.Update("s", 1, 1, 0, 3, 0) // u=0
	mustOK(t, err, "expensive")
	if r != (Reply{Final: true, Denied: true}) {
		t.Fatalf("expensive got %+v", r)
	}
	// want=0：Granted=0，Final/Denied 均为假，不发零头
	r, err = e.Update("s", 2, 2, 0, 0, 1)
	mustOK(t, err, "want0")
	if r != (Reply{}) {
		t.Fatalf("want0 got %+v", r)
	}
	r, _ = e.Update("s", 3, 2, 0, 10, 2) // u=5,g0=5,d=0
	if r.Granted != 5 || !r.Final || r.Denied {
		t.Fatalf("cheap got %+v", r)
	}
	// 结算 rg2 全部 5 个单位（扣 5，余额归 0），再请求即 Denied
	r, _ = e.Update("s", 4, 2, 5, 1, 3)
	if r.Charged != 5 || !r.Denied || !r.Final || r.Granted != 0 {
		t.Fatalf("after full got %+v", r)
	}
	if !r.Denied || !r.Final || r.Granted != 0 {
		t.Fatalf("after full got %+v", r)
	}
	checkConservation(t, e, "a", 5)
}

// TestCloseImplicit：Close 隐式结算未列出 rg；列出无记录且 used>0 整体拒绝。
func TestCloseImplicit(t *testing.T) {
	e := New(20, 5, 60, 4)
	mustOK(t, e.SetRate(1, 3, 0), "r1")
	mustOK(t, e.SetRate(2, 1, 0), "r2")
	mustOK(t, e.TopUp("a", 100, 0), "topup")
	mustOK(t, e.Open("s", "a", 0), "open")
	_, _ = e.Update("s", 1, 1, 0, 10, 0) // 预留 30
	_, _ = e.Update("s", 2, 2, 0, 4, 0)  // 预留 4

	// 拒绝：列出无记录 rg3 且 used>0 → 整体拒绝、会话仍在、余额不变
	bad := map[int64]int64{1: 10, 3: 2}
	if _, err := e.Close("s", 3, bad, 10); !errIs(err, ErrNoGrant) {
		t.Fatalf("close bad: %v", err)
	}
	// 被拒操作不占序号：Close n=3（仅列出 used=0 的无记录 rg，前置 NoGrant 优先触发）
	// 这里改用纯序号探测：下一次 n=3 必须可执行。
	if _, err := e.Update("s", 3, 2, 4, 0, 10); err != nil { // 结算 rg2，不授予
		t.Fatalf("seq 3 not consumable: %v", err)
	}
	// 隐式结算：rg1 未列出按 used=0（预留 30 释放但不扣费）；rg2 已在上一步结算。
	r, err := e.Close("s", 4, nil, 10)
	mustOK(t, err, "close")
	if r != (Reply{}) { // Close 本身没有新扣费
		t.Fatalf("close reply %+v, want zero reply", r)
	}
	if bal := e.ledger.Balance("a"); bal != 96 { // rg2 已扣 4；rg1 used=0 不扣
		t.Fatalf("balance after close = %d, want 96", bal)
	}
	if _, err := e.Close("s", 4, nil, 10); !errIs(err, ErrNoSession) {
		t.Fatalf("close retransmit after gone: %v", err)
	}
	if e.ledger.Held("a", 10) != 0 {
		t.Fatalf("held after close = %d, want 0", e.ledger.Held("a", 10))
	}
	checkConservation(t, e, "a", 100)
}
