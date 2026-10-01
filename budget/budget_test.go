package budget

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// setup 注册主体并返回账本，失败则终止测试。
func setup(t *testing.T, name string, q, p, x int64) *Ledger {
	t.Helper()
	l := NewLedger()
	if err := l.Register(name, q, p, x); err != nil {
		t.Fatalf("Register(%s, Q=%d, P=%d, X=%d): %v", name, q, p, x, err)
	}
	t.Logf("输入 Register(%s, Q=%d, P=%d, X=%d) 输出 ok 依据: 参数均为正", name, q, p, x)
	return l
}

// mustReserve 预留必须成功，返回预留号。
func mustReserve(t *testing.T, l *Ledger, name string, tm, r int64, basis string) string {
	t.Helper()
	id, err := l.Reserve(name, tm, r)
	if err != nil {
		t.Fatalf("Reserve(%s, t=%d, r=%d): %v", name, tm, r, err)
	}
	t.Logf("输入 Reserve(%s, t=%d, r=%d) 输出 %s 依据: %s", name, tm, r, id, basis)
	return id
}

// mustSettle 结算必须成功。
func mustSettle(t *testing.T, l *Ledger, id string, u int64, basis string) {
	t.Helper()
	if err := l.Settle(id, u); err != nil {
		t.Fatalf("Settle(%s, u=%d): %v", id, u, err)
	}
	t.Logf("输入 Settle(%s, u=%d) 输出 ok 依据: %s", id, u, basis)
}

// checkQuery 校验查询结果并打印判定依据。
func checkQuery(t *testing.T, l *Ledger, name string, period, tm, wantUsed, wantInflight int64, basis string) {
	t.Helper()
	used, inflight, err := l.Query(name, period, tm)
	if err != nil {
		t.Fatalf("Query(%s, period=%d, t=%d): %v", name, period, tm, err)
	}
	t.Logf("输入 Query(%s, period=%d, t=%d) 输出 used=%d inflight=%d 期望 used=%d inflight=%d 依据: %s",
		name, period, tm, used, inflight, wantUsed, wantInflight, basis)
	if used != wantUsed || inflight != wantInflight {
		t.Fatalf("Query(%s, period=%d, t=%d) = (%d, %d), want (%d, %d)",
			name, period, tm, used, inflight, wantUsed, wantInflight)
	}
}

// TestLateSettleAcrossPeriods 跨周期迟到结算记入旧周期，不占新周期额度。
func TestLateSettleAcrossPeriods(t *testing.T) {
	// Q=100, P=10, X=5。周期0: [0,10)，周期1: [10,20)。
	l := setup(t, "svc", 100, 10, 5)

	id := mustReserve(t, l, "svc", 3, 40, "周期0: 0已用+0在途+40<=100")
	mustSettle(t, l, id, 40, "在途预留可结算，u 记入预留时刻 t=3 所在周期0")

	// 迟到结算：t=15 已属周期1，但 u 仍记入周期0。
	id2 := mustReserve(t, l, "svc", 8, 30, "周期0: 40已用+0在途+30<=100")
	mustSettle(t, l, id2, 30, "结算虽发生在周期1，u 记入预留所属周期0")

	checkQuery(t, l, "svc", 0, 15, 70, 0, "周期0已用=40+30，两笔均已结算故在途为0")
	checkQuery(t, l, "svc", 1, 15, 0, 0, "迟到结算不记入新周期1")

	// 新周期额度独立，不受旧周期已用量影响。
	mustReserve(t, l, "svc", 12, 100, "周期1: 0已用+0在途+100<=100，各周期独立核算")
	checkQuery(t, l, "svc", 1, 12, 0, 100, "新周期1在途=100，旧周期用量未带入")
}

// TestSettleAfterExpiry 过期后仍可结算，过期后不再占额度。
func TestSettleAfterExpiry(t *testing.T) {
	// Q=100, P=10, X=5。t=0 预留 60，t=5 起过期。
	l := setup(t, "svc", 100, 10, 5)

	id := mustReserve(t, l, "svc", 0, 60, "周期0: 0+0+60<=100")
	checkQuery(t, l, "svc", 0, 4, 0, 60, "t=4 < 过期点5，仍在途")

	// 过期后不再占额度，可再预留。
	mustReserve(t, l, "svc", 6, 100, "t=6 时首笔已过期不占额度: 0已用+0在途+100<=100")
	checkQuery(t, l, "svc", 0, 6, 0, 100, "过期预留不计入在途")

	// 过期预留仍可结算，u 记入预留所属周期0。
	mustSettle(t, l, id, 60, "已过期但未释放的预留仍可结算")
	checkQuery(t, l, "svc", 0, 6, 60, 100, "结算后周期0已用=60，在途只剩第二笔100")
}

// TestOverdrawRejectsNewReservation 超额结算造成透支后拒绝新预留，剩余量报 0。
func TestOverdrawRejectsNewReservation(t *testing.T) {
	// Q=100, P=10, X=100（长过期，排除过期干扰）。
	l := setup(t, "svc", 100, 10, 100)

	id := mustReserve(t, l, "svc", 0, 80, "周期0: 0+0+80<=100")
	mustSettle(t, l, id, 150, "u=150 超过 r=80 的部分照常记入，周期0已用=150>Q=100")
	checkQuery(t, l, "svc", 0, 1, 150, 0, "透支: 已用150超过预算100")

	_, err := l.Reserve("svc", 1, 1)
	var iq *InsufficientQuotaError
	if !errors.As(err, &iq) || iq.Remaining != 0 {
		t.Fatalf("Reserve after overdraw: err=%v, want InsufficientQuotaError{Remaining:0}", err)
	}
	t.Logf("输入 Reserve(svc, t=1, r=1) 输出 %v 依据: 已用150>Q=100，剩余量透支报0", err)

	// 被拒绝的预留不改变账目。
	checkQuery(t, l, "svc", 0, 1, 150, 0, "被拒操作不改变任何账目")

	// 下一周期独立，不受影响。
	mustReserve(t, l, "svc", 10, 100, "周期1独立核算: 0+0+100<=100")
}

// TestReleaseThenSettleRejected 释放后结算被拒，释放后不再占额度。
func TestReleaseThenSettleRejected(t *testing.T) {
	l := setup(t, "svc", 100, 10, 100)

	id := mustReserve(t, l, "svc", 0, 60, "周期0: 0+0+60<=100")
	if err := l.Release(id); err != nil {
		t.Fatalf("Release(%s): %v", id, err)
	}
	t.Logf("输入 Release(%s) 输出 ok 依据: 在途预留可显式释放", id)
	checkQuery(t, l, "svc", 0, 0, 0, 0, "释放后不再占额度")

	err := l.Settle(id, 10)
	if !errors.Is(err, ErrAlreadyReleased) {
		t.Fatalf("Settle after Release: err=%v, want ErrAlreadyReleased", err)
	}
	t.Logf("输入 Settle(%s, u=10) 输出 %v 依据: 已释放的预留不可再结算", id, err)

	// 释放腾出的额度可再预留。
	mustReserve(t, l, "svc", 1, 100, "释放后周期0: 0已用+0在途+100<=100")
}

// TestExactExpiryAndPeriodBoundary 恰在过期点即过期，恰在周期边界归入新周期。
func TestExactExpiryAndPeriodBoundary(t *testing.T) {
	// Q=100, P=10, X=5。t=0 预留，过期点为 t=5。
	l := setup(t, "svc", 100, 10, 5)

	id := mustReserve(t, l, "svc", 0, 50, "周期0: 0+0+50<=100")
	checkQuery(t, l, "svc", 0, 4, 0, 50, "t=4 < 5 未过期，在途=50")
	checkQuery(t, l, "svc", 0, 5, 0, 0, "t=5 恰到过期点即过期，在途=0")

	// 恰在过期点可再占用该额度。
	mustReserve(t, l, "svc", 5, 100, "t=5 首笔已过期: 0已用+0在途+100<=100")

	// 恰在周期边界 t=10 属于周期1（10/10=1），不属周期0。
	checkQuery(t, l, "svc", 0, 9, 0, 100, "t=9 第二笔仍在途，周期0在途=100")
	mustReserve(t, l, "svc", 10, 100, "t=10 属周期1，独立核算: 0+0+100<=100")
	checkQuery(t, l, "svc", 1, 10, 0, 100, "周期边界预留记入新周期1")

	// 过期预留恰在过期点之后仍可结算。
	mustSettle(t, l, id, 50, "t>=5 已过期，但结算仍被接受并记入周期0")
	checkQuery(t, l, "svc", 0, 9, 50, 100, "周期0已用=50，在途=100")
}

// TestRegisterRejectsNonPositive 注册时 Q、P、X 非正拒绝。
func TestRegisterRejectsNonPositive(t *testing.T) {
	l := NewLedger()
	cases := []struct {
		name    string
		q, p, x int64
	}{
		{"q0", 0, 10, 5},
		{"qneg", -1, 10, 5},
		{"p0", 100, 0, 5},
		{"pneg", 100, -10, 5},
		{"x0", 100, 10, 0},
		{"xneg", 100, 10, -5},
	}
	for _, c := range cases {
		err := l.Register(c.name, c.q, c.p, c.x)
		if !errors.Is(err, ErrNonPositiveParam) {
			t.Fatalf("Register(%s, %d, %d, %d): err=%v, want ErrNonPositiveParam",
				c.name, c.q, c.p, c.x, err)
		}
		t.Logf("输入 Register(%s, Q=%d, P=%d, X=%d) 输出 %v 依据: 参数非正拒绝", c.name, c.q, c.p, c.x, err)
		if _, _, qerr := l.Query(c.name, 0, 0); !errors.Is(qerr, ErrSubjectNotRegistered) {
			t.Fatalf("rejected register must not create subject %s", c.name)
		}
	}
}

// TestReserveErrorPriority 预留按「未注册、r 非正、额度不足」顺序只报第一个原因。
func TestReserveErrorPriority(t *testing.T) {
	l := setup(t, "svc", 100, 10, 100)

	// 未注册 + r 非正同时成立时，只报未注册。
	if _, err := l.Reserve("ghost", 0, -1); !errors.Is(err, ErrSubjectNotRegistered) {
		t.Fatalf("want ErrSubjectNotRegistered, got %v", err)
	}
	t.Logf("输入 Reserve(ghost, t=0, r=-1) 输出 %v 依据: 未注册优先于 r 非正", ErrSubjectNotRegistered)

	// r 非正 + 额度不足同时成立时，只报 r 非正。
	mustReserve(t, l, "svc", 0, 100, "占满周期0额度")
	if _, err := l.Reserve("svc", 0, 0); !errors.Is(err, ErrNonPositiveAmount) {
		t.Fatalf("want ErrNonPositiveAmount, got %v", err)
	}
	t.Logf("输入 Reserve(svc, t=0, r=0) 输出 %v 依据: r 非正优先于额度不足", ErrNonPositiveAmount)

	// 额度不足时返回剩余量。
	_, err := l.Reserve("svc", 0, 1)
	var iq *InsufficientQuotaError
	if !errors.As(err, &iq) || iq.Remaining != 0 {
		t.Fatalf("want InsufficientQuotaError{Remaining:0}, got %v", err)
	}
	t.Logf("输入 Reserve(svc, t=0, r=1) 输出 %v 依据: 已用0+在途100+1>100，剩余0", err)

	// 部分占用时报准确剩余量。
	l2 := setup(t, "svc2", 100, 10, 100)
	mustReserve(t, l2, "svc2", 0, 30, "周期0: 0+0+30<=100")
	_, err = l2.Reserve("svc2", 0, 80)
	if !errors.As(err, &iq) || iq.Remaining != 70 {
		t.Fatalf("want InsufficientQuotaError{Remaining:70}, got %v", err)
	}
	t.Logf("输入 Reserve(svc2, t=0, r=80) 输出 %v 依据: 剩余=100-0-30=70<80", err)

	// 被拒预留不改变账目。
	checkQuery(t, l2, "svc2", 0, 0, 0, 30, "被拒操作不改变任何账目")
}

// TestSettleReleaseErrorPriority 结算与释放的错误优先级。
func TestSettleReleaseErrorPriority(t *testing.T) {
	l := setup(t, "svc", 100, 10, 100)

	// 预留号不存在优先于一切。
	if err := l.Settle("v999", -1); !errors.Is(err, ErrReservationNotFound) {
		t.Fatalf("want ErrReservationNotFound, got %v", err)
	}
	if err := l.Release("v999"); !errors.Is(err, ErrReservationNotFound) {
		t.Fatalf("want ErrReservationNotFound, got %v", err)
	}
	t.Logf("输入 Settle/Release(v999) 输出 %v 依据: 预留号不存在最优先", ErrReservationNotFound)

	// 已结算优先于 u 为负。
	id := mustReserve(t, l, "svc", 0, 10, "周期0: 0+0+10<=100")
	mustSettle(t, l, id, 10, "首次结算成功")
	if err := l.Settle(id, -5); !errors.Is(err, ErrAlreadySettled) {
		t.Fatalf("want ErrAlreadySettled, got %v", err)
	}
	t.Logf("输入 Settle(%s, u=-5) 输出 %v 依据: 已结算优先于 u 为负", id, ErrAlreadySettled)
	if err := l.Release(id); !errors.Is(err, ErrAlreadySettled) {
		t.Fatalf("want ErrAlreadySettled, got %v", err)
	}
	t.Logf("输入 Release(%s) 输出 %v 依据: 已结算的预留不可释放", id, ErrAlreadySettled)

	// 已释放优先于 u 为负；重复释放报已释放。
	id2 := mustReserve(t, l, "svc", 0, 10, "周期0: 10已用+0在途+10<=100")
	if err := l.Release(id2); err != nil {
		t.Fatalf("Release(%s): %v", id2, err)
	}
	if err := l.Settle(id2, -5); !errors.Is(err, ErrAlreadyReleased) {
		t.Fatalf("want ErrAlreadyReleased, got %v", err)
	}
	if err := l.Release(id2); !errors.Is(err, ErrAlreadyReleased) {
		t.Fatalf("want ErrAlreadyReleased, got %v", err)
	}
	t.Logf("输入 Settle/Release(%s) 输出 %v 依据: 已释放不可再结算或再释放", id2, ErrAlreadyReleased)

	// u 为负本身可区分，且被拒后不改变账目。
	id3 := mustReserve(t, l, "svc", 0, 10, "周期0: 10已用+0在途+10<=100")
	if err := l.Settle(id3, -1); !errors.Is(err, ErrNegativeUsage) {
		t.Fatalf("want ErrNegativeUsage, got %v", err)
	}
	t.Logf("输入 Settle(%s, u=-1) 输出 %v 依据: u 为负拒绝", id3, ErrNegativeUsage)
	checkQuery(t, l, "svc", 0, 0, 10, 10, "被拒结算不改变账目: 已用10，在途只剩第三笔10")

	// u=0 合法（非负）。
	mustSettle(t, l, id3, 0, "u=0 非负，结算成功，已用量不变")
	checkQuery(t, l, "svc", 0, 0, 10, 0, "u=0 结算后已用仍为10，在途0")
}

// TestConcurrentReserveNeverExceedsQuota 并发预留任意交错下不超额，且与朴素记账一致。
func TestConcurrentReserveNeverExceedsQuota(t *testing.T) {
	const (
		q           = 1000
		workers     = 16
		perWorker   = 100
		reservation = 10
	)
	l := setup(t, "svc", q, 10, 100000)

	var wg sync.WaitGroup
	ids := make([][]string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id, err := l.Reserve("svc", 0, reservation)
				if err == nil {
					ids[w] = append(ids[w], id)
				} else {
					var iq *InsufficientQuotaError
					if !errors.As(err, &iq) {
						t.Errorf("unexpected error: %v", err)
					}
				}
			}
		}(w)
	}
	wg.Wait()

	total := 0
	seen := make(map[string]bool)
	for w := range ids {
		for _, id := range ids[w] {
			if seen[id] {
				t.Fatalf("duplicate reservation id %s", id)
			}
			seen[id] = true
			total += reservation
		}
	}
	t.Logf("输入 %d 协程 x %d 次 Reserve(svc, t=0, r=%d) 输出 成功%d笔 依据: 成功总额=%d<=Q=%d",
		workers, perWorker, reservation, len(seen), total, q)
	if total > q {
		t.Fatalf("concurrent reservations exceed quota: %d > %d", total, q)
	}
	if len(seen) != q/reservation {
		t.Fatalf("got %d successful reservations, want exactly %d", len(seen), q/reservation)
	}

	// 查询结果与逐笔朴素记账一致。
	used, inflight, err := l.Query("svc", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入 Query(svc, period=0, t=0) 输出 used=%d inflight=%d 依据: 等于逐笔成功预留之和", used, inflight)
	if used != 0 || inflight != int64(total) {
		t.Fatalf("Query = (%d, %d), want (0, %d)", used, inflight, total)
	}

	// 并发结算与释放：同一预留只能被结算或释放其一且至多一次。
	all := make([]string, 0, len(seen))
	for id := range seen {
		all = append(all, id)
	}
	var settleOK, releaseOK, rejected int64
	var mu sync.Mutex
	wg = sync.WaitGroup{}
	for i, id := range all {
		wg.Add(2)
		go func(id string, u int64) {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			if err := l.Settle(id, u); err == nil {
				settleOK++
			} else {
				rejected++
			}
		}(id, int64(i%5))
		go func(id string) {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			if err := l.Release(id); err == nil {
				releaseOK++
			} else {
				rejected++
			}
		}(id)
	}
	wg.Wait()
	t.Logf("输入 并发 Settle+Release 各 %d 次 输出 结算成功%d 释放成功%d 被拒%d 依据: 每笔预留仅其一成功",
		len(all), settleOK, releaseOK, rejected)
	if settleOK+releaseOK != int64(len(all)) {
		t.Fatalf("settle(%d)+release(%d) != reservations(%d)", settleOK, releaseOK, len(all))
	}
	if rejected != int64(len(all)) {
		t.Fatalf("rejected = %d, want %d (each reservation touched twice)", rejected, len(all))
	}
}

// TestSequentialIDs 预留号全局顺序生成 v1、v2……。
func TestSequentialIDs(t *testing.T) {
	l := setup(t, "a", 100, 10, 100)
	if err := l.Register("b", 100, 10, 100); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"a", "b", "a"} {
		id, err := l.Reserve(name, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("v%d", i+1)
		t.Logf("输入 Reserve(%s, t=0, r=1) 输出 %s 依据: 全局顺序第%d个", name, id, i+1)
		if id != want {
			t.Fatalf("got %s, want %s", id, want)
		}
	}
}
