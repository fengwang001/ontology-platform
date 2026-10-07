package payledger

import (
	"fmt"
	"math/rand"
	"testing"
)

// 随机操作描述，用于驱动生产实现与朴素模型并记录日志。
type op struct {
	kind   string
	acct   string
	auth   string
	amount int64
	final  bool
	now    int64
}

func (o op) String() string {
	switch o.kind {
	case "createAccount":
		return fmt.Sprintf("CreateAccount(%s, credit=%d, now=%d)", o.acct, o.amount, o.now)
	case "adjustCredit":
		return fmt.Sprintf("AdjustCredit(%s, newLimit=%d, now=%d)", o.acct, o.amount, o.now)
	case "authorize":
		return fmt.Sprintf("Authorize(%s, %s, amount=%d, now=%d)", o.acct, o.auth, o.amount, o.now)
	case "increment":
		return fmt.Sprintf("Increment(%s, amount=%d, now=%d)", o.auth, o.amount, o.now)
	case "capture":
		return fmt.Sprintf("Capture(%s, amount=%d, final=%v, now=%d)", o.auth, o.amount, o.final, o.now)
	case "void":
		return fmt.Sprintf("Void(%s, now=%d)", o.auth, o.now)
	case "refund":
		return fmt.Sprintf("Refund(%s, amount=%d, now=%d)", o.auth, o.amount, o.now)
	}
	return "?"
}

func applyOp(l *Ledger, o op) error {
	switch o.kind {
	case "createAccount":
		return l.CreateAccount(o.acct, o.amount, o.now)
	case "adjustCredit":
		return l.AdjustCredit(o.acct, o.amount, o.now)
	case "authorize":
		return l.Authorize(o.acct, o.auth, o.amount, o.now)
	case "increment":
		return l.Increment(o.auth, o.amount, o.now)
	case "capture":
		return l.Capture(o.auth, o.amount, o.final, o.now)
	case "void":
		return l.Void(o.auth, o.now)
	case "refund":
		return l.Refund(o.auth, o.amount, o.now)
	}
	panic("bad op")
}

func applyNaive(n *naive, o op) error {
	switch o.kind {
	case "createAccount":
		return n.createAccount(o.acct, o.amount, o.now)
	case "adjustCredit":
		return n.adjustCredit(o.acct, o.amount, o.now)
	case "authorize":
		return n.authorize(o.acct, o.auth, o.amount, o.now)
	case "increment":
		return n.increment(o.auth, o.amount, o.now)
	case "capture":
		return n.capture(o.auth, o.amount, o.final, o.now)
	case "void":
		return n.void(o.auth, o.now)
	case "refund":
		return n.refund(o.auth, o.amount, o.now)
	}
	panic("bad op")
}

func errKind(err error) int {
	if err == nil {
		return -1
	}
	return int(err.(*Error).Kind)
}

// genOps 生成一条随机操作序列：时间大多单调，偶尔回退以覆盖时钟回退。
func genOps(r *rand.Rand, nOps int) []op {
	accts := []string{"c0", "c1", "c2", "c3"}
	authIDs := []string{"a0", "a1", "a2", "a3", "a4", "a5", "a6", "a7"}
	kinds := []string{
		"authorize", "authorize", "authorize",
		"increment", "increment",
		"capture", "capture", "capture",
		"void", "refund", "refund",
		"adjustCredit", "createAccount",
	}
	ops := make([]op, 0, nOps)
	now := r.Int63n(5)
	for i := 0; i < nOps; i++ {
		// 90% 时间前进 0..2 天, 10% 回退 0..3 天（触发时钟回退路径）。
		if r.Intn(10) == 0 {
			now -= int64(r.Intn(4))
		} else {
			now += int64(r.Intn(3))
		}
		kind := kinds[r.Intn(len(kinds))]
		amount := int64(1 + r.Intn(400))
		if r.Intn(4) == 0 {
			amount = int64(1 + r.Intn(40)) // 小额, 提高边界命中率
		}
		o := op{
			kind:   kind,
			acct:   accts[r.Intn(len(accts))],
			auth:   authIDs[r.Intn(len(authIDs))],
			amount: amount,
			final:  r.Intn(3) == 0,
			now:    now,
		}
		if kind == "createAccount" || kind == "adjustCredit" {
			o.amount = int64(r.Intn(1200))
		}
		ops = append(ops, o)
	}
	return ops
}

// runDifferential 对同一操作序列并行驱动两个实现，逐步比对错误类别、
// 可用额度（多个时刻）与授权快照，并按要求打印输入、输出与判定依据。
func runDifferential(t *testing.T, cfg Config, seed int64, nOps int, logEveryOp bool) {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	l := NewLedger(cfg)
	n := newNaive(cfg)

	// 预开户，使随机序列更深入覆盖成功路径；序列中仍含重复开户操作。
	for i, acct := range []string{"c0", "c1", "c2", "c3"} {
		if err := l.CreateAccount(acct, int64(300+100*i), 0); err != nil {
			t.Fatalf("预开户失败: %v", err)
		}
		if err := n.createAccount(acct, int64(300+100*i), 0); err != nil {
			t.Fatalf("预开户失败(模型): %v", err)
		}
	}
	ops := genOps(r, nOps)

	accts := []string{"c0", "c1", "c2", "c3"}
	authIDs := []string{"a0", "a1", "a2", "a3", "a4", "a5", "a6", "a7"}

	for i, o := range ops {
		errL := applyOp(l, o)
		errN := applyNaive(n, o)
		if errKind(errL) != errKind(errN) {
			t.Fatalf("第 %d 步操作结果不一致\n  输入: %s\n  生产实现: %v\n  朴素模型: %v",
				i, o, errL, errN)
		}

		// 比对多个时刻的可用额度（含过去与未来, 覆盖前向/后向查询）。
		queryNows := []int64{o.now, o.now + cfg.ExpiryDays + 1, o.now - int64(r.Intn(4))}
		for _, acct := range accts {
			for _, qn := range queryNows {
				avL, eL := l.Available(acct, qn)
				avN, eN := n.available(acct, qn)
				if errKind(eL) != errKind(eN) || avL != avN {
					t.Fatalf("第 %d 步后可用额度不一致 (acct=%s now=%d)\n  输入: %s\n  生产实现: %d,%v\n  朴素模型: %d,%v",
						i, acct, qn, o, avL, eL, avN, eN)
				}
				// 非负不变式对"不早于当前时钟"的时刻成立；更早的时刻属于
				// 回溯式求值（把后续入账与当时未过期的持有混合），不在不变式范围内。
				if eL == nil && avL < 0 && (!n.hasClock || qn >= n.clock) {
					t.Fatalf("第 %d 步后可用额度为负 (acct=%s now=%d clock=%d): %d", i, acct, qn, n.clock, avL)
				}
			}
		}
		// 比对授权快照。
		for _, id := range authIDs {
			sL, eL := l.AuthSnapshot(id, o.now)
			eN := error(nil)
			var sN AuthSnapshot
			a, ok := n.auths[id]
			if !ok {
				eN = newErr(ErrAuthNotFound, "授权不存在")
			} else {
				sN = AuthSnapshot{
					AuthID: id, AccountID: a.account,
					Status: n.status(a, o.now), RemainingHold: n.remaining(a, o.now),
					CumAuth: a.cumAuth, Captured: a.captured, Refunded: a.refunded,
					ExpiryDay: a.expiry,
				}
			}
			if errKind(eL) != errKind(eN) || sL != sN {
				t.Fatalf("第 %d 步后授权快照不一致 (auth=%s now=%d)\n  输入: %s\n  生产实现: %+v,%v\n  朴素模型: %+v,%v",
					i, id, o.now, o, sL, eL, sN, eN)
			}
		}

		if logEveryOp {
			// 判定依据：操作后关键可观察量。
			av, _ := l.Available(o.acct, o.now)
			snap, snapErr := l.AuthSnapshot(o.auth, o.now)
			t.Logf("第 %d 步\n  输入: %s\n  输出: err=%v\n  判定依据: 账户%s可用=%d, 授权快照=%+v (snapErr=%v)",
				i, o, errL, o.acct, av, snap, snapErr)
		}
	}
}

// 带完整日志的对照测试（go test -v 可见每步输入/输出/判定依据）。
func TestDifferentialLogged(t *testing.T) {
	cfgs := []Config{
		{ExpiryDays: 0, ToleranceBps: 0},
		{ExpiryDays: 3, ToleranceBps: 55},
		{ExpiryDays: 7, ToleranceBps: 1000},
	}
	for _, cfg := range cfgs {
		t.Run(fmt.Sprintf("E=%d/bps=%d", cfg.ExpiryDays, cfg.ToleranceBps), func(t *testing.T) {
			runDifferential(t, cfg, int64(cfg.ExpiryDays*100+cfg.ToleranceBps), 300, true)
		})
	}
}

// 大规模扫描：多种参数与种子, 仅在不一致时输出日志。
func TestDifferentialSweep(t *testing.T) {
	cfgs := []Config{
		{ExpiryDays: 0, ToleranceBps: 0},
		{ExpiryDays: 1, ToleranceBps: 1},
		{ExpiryDays: 3, ToleranceBps: 55},
		{ExpiryDays: 7, ToleranceBps: 1000},
		{ExpiryDays: 30, ToleranceBps: 10000},
	}
	for _, cfg := range cfgs {
		for seed := int64(0); seed < 40; seed++ {
			runDifferential(t, cfg, seed*7919+cfg.ExpiryDays, 1500, false)
		}
	}
}
