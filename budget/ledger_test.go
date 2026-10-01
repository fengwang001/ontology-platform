package budget

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func logDecision(t *testing.T, op string, out string, basis string) {
	t.Helper()
	t.Logf("输入 %s -> 输出 %s | 判定依据: %s", op, out, basis)
}

func mustRegister(t *testing.T, l *Ledger, subject string, q, p, x int64) {
	t.Helper()
	if err := l.Register(subject, q, p, x); err != nil {
		t.Fatalf("Register(%q, Q=%d, P=%d, X=%d) 失败: %v", subject, q, p, x, err)
	}
	logDecision(t, "Register("+subject+")", "ok", "参数均为正")
}

func mustReserve(t *testing.T, l *Ledger, subject string, r, now int64) string {
	t.Helper()
	id, err := l.Reserve(subject, r, now)
	if err != nil {
		t.Fatalf("Reserve(%q, r=%d, now=%d) 失败: %v", subject, r, now, err)
	}
	logDecision(t, "Reserve r="+itoa(r)+" now="+itoa(now), id, "used+inflight+r <= Q")
	return id
}

func mustSettle(t *testing.T, l *Ledger, id string, u int64) {
	t.Helper()
	if err := l.Settle(id, u); err != nil {
		t.Fatalf("Settle(%q, u=%d) 失败: %v", id, u, err)
	}
	logDecision(t, "Settle "+id+" u="+itoa(u), "ok", "u 记入预留所属周期")
}

func expectReason(t *testing.T, err error, want Reason) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望拒绝原因 %s，实际成功", want)
	}
	be, ok := err.(*Error)
	if !ok {
		t.Fatalf("错误类型不是 *budget.Error: %v", err)
	}
	if be.Reason != want {
		t.Fatalf("期望原因 %s，实际 %s (%v)", want, be.Reason, err)
	}
	logDecision(t, "op", string(be.Reason), be.Detail)
}

func itoa(v int64) string {
	return fmt.Sprintf("%d", v)
}

func queryMust(t *testing.T, l *Ledger, subject string, period, now int64) (int64, int64) {
	t.Helper()
	used, inflight, err := l.Query(subject, period, now)
	if err != nil {
		t.Fatalf("Query(%q, period=%d, now=%d) 失败: %v", subject, period, now, err)
	}
	return used, inflight
}

// 跨周期的迟到结算记入旧周期，而不占新周期额度。
func TestLateSettleCreditsOldPeriod(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "svc", 100, 10, 5)

	id := mustReserve(t, l, "svc", 40, 3) // 周期 0
	// 进入周期 1 后才结算，u 应记入周期 0。
	mustSettle(t, l, id, 40)

	used0, inflight0 := queryMust(t, l, "svc", 0, 25)
	used1, inflight1 := queryMust(t, l, "svc", 1, 25)
	logDecision(t, "Query period0/period1 @now=25",
		fmt.Sprintf("p0(used=%d,inflight=%d) p1(used=%d,inflight=%d)", used0, inflight0, used1, inflight1),
		"结算按预留发生时刻的周期入账")
	if used0 != 40 || inflight0 != 0 {
		t.Fatalf("周期0 应为 used=40 inflight=0，实际 used=%d inflight=%d", used0, inflight0)
	}
	if used1 != 0 || inflight1 != 0 {
		t.Fatalf("周期1 不应被迟到结算影响，实际 used=%d inflight=%d", used1, inflight1)
	}

	// 新周期额度完整：可再预留满 Q。
	id2 := mustReserve(t, l, "svc", 100, 15) // 周期 1
	_, inflight1 = queryMust(t, l, "svc", 1, 19)
	if inflight1 != 100 {
		t.Fatalf("周期1 在途应为 100，实际 %d", inflight1)
	}
	mustSettle(t, l, id2, 100)
}

// 过期后不再占额度，但仍可结算。
func TestSettleAfterExpiry(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "svc", 100, 10, 5)

	id := mustReserve(t, l, "svc", 60, 0) // 过期点 now=5
	// now=7 时已过期，在途应释放，可再预留 100。
	_, inflight := queryMust(t, l, "svc", 0, 7)
	logDecision(t, "Query period0 @now=7", "inflight="+itoa(inflight), "now=7 >= expiresAt=5，恰到点即过期")
	if inflight != 0 {
		t.Fatalf("过期后在途应为 0，实际 %d", inflight)
	}
	mustReserve(t, l, "svc", 100, 7)

	// 过期预留仍可结算，u 记入原周期。
	mustSettle(t, l, id, 60)
	used, _ := queryMust(t, l, "svc", 0, 8)
	if used != 60 {
		t.Fatalf("过期结算后周期0已用量应为 60，实际 %d", used)
	}
}

// 超额结算造成透支后，该周期新预留一律被拒且剩余量报 0。
func TestOverSettleOverdraftRejectsNewReserve(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "svc", 100, 10, 50)

	id := mustReserve(t, l, "svc", 80, 0)
	mustSettle(t, l, id, 150) // u 超过 r 的部分照常记入

	used, _ := queryMust(t, l, "svc", 0, 1)
	logDecision(t, "Query period0 @now=1", "used="+itoa(used), "u=150 全部入账，超过 Q=100 造成透支")
	if used != 150 {
		t.Fatalf("透支后已用量应为 150，实际 %d", used)
	}

	_, err := l.Reserve("svc", 1, 1)
	expectReason(t, err, ReasonInsufficientQuota)
	if be := err.(*Error); be.Remaining != 0 {
		t.Fatalf("透支时剩余量应报 0，实际 %d", be.Remaining)
	}

	// 下一周期不受影响。
	mustReserve(t, l, "svc", 100, 10)
}

// 释放后不再占额度，此后结算被拒；结算后释放也被拒。
func TestReleaseThenSettleRejected(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "svc", 100, 10, 50)

	id := mustReserve(t, l, "svc", 60, 0)
	if err := l.Release(id); err != nil {
		t.Fatalf("Release(%q) 失败: %v", id, err)
	}
	logDecision(t, "Release "+id, "ok", "在途预留被显式释放")

	_, inflight := queryMust(t, l, "svc", 0, 1)
	if inflight != 0 {
		t.Fatalf("释放后在途应为 0，实际 %d", inflight)
	}

	expectReason(t, l.Settle(id, 10), ReasonAlreadyReleased)
	expectReason(t, l.Release(id), ReasonAlreadyReleased)

	// 额度已还，可全额再预留。
	id2 := mustReserve(t, l, "svc", 100, 0)
	mustSettle(t, l, id2, 100)
	expectReason(t, l.Release(id2), ReasonAlreadySettled)
	expectReason(t, l.Settle(id2, 1), ReasonAlreadySettled)
}

// 恰在过期点即过期；恰在周期边界落入新周期。
func TestExpiryAndPeriodBoundary(t *testing.T) {
	l := NewLedger()
	mustRegister(t, l, "svc", 100, 10, 5)

	id := mustReserve(t, l, "svc", 60, 7) // 周期 0，过期点 now=12

	// now=11 未过期，在途 60；now=12 恰到点即过期，在途 0。
	if _, inflight := queryMust(t, l, "svc", 0, 11); inflight != 60 {
		t.Fatalf("now=11 在途应为 60，实际 %d", inflight)
	}
	if _, inflight := queryMust(t, l, "svc", 0, 12); inflight != 0 {
		t.Fatalf("now=12 恰到过期点，在途应为 0，实际 %d", inflight)
	}
	logDecision(t, "Query @now=11/12", "inflight 60 -> 0", "now >= 预留时刻+X 即过期")

	// now=9 属周期 0，now=10 恰在边界属周期 1。
	mustReserve(t, l, "svc", 40, 9)
	if _, inflight := queryMust(t, l, "svc", 0, 9); inflight != 100 {
		t.Fatalf("周期0 在途应为 100，实际 %d", inflight)
	}
	mustReserve(t, l, "svc", 100, 10) // 周期 1 额度独立
	_, inflight1 := queryMust(t, l, "svc", 1, 10)
	logDecision(t, "Reserve @now=10", "inflight(p1)="+itoa(inflight1), "周期号=floor(now/P)，边界落入新周期")
	if inflight1 != 100 {
		t.Fatalf("周期1 在途应为 100，实际 %d", inflight1)
	}

	// 周期 0 的两笔预留分别在 now=12、now=14 过期。
	if _, inflight := queryMust(t, l, "svc", 0, 12); inflight != 40 {
		t.Fatalf("now=12 周期0 在途应为 40（首笔恰过期），实际 %d", inflight)
	}
	if _, inflight := queryMust(t, l, "svc", 0, 14); inflight != 0 {
		t.Fatalf("now=14 周期0 在途应为 0，实际 %d", inflight)
	}
	logDecision(t, "Query period0 @now=14", "inflight=0", "两笔预留均已到期，不再占额度")
	_ = id
}

// 注册参数与各类拒绝原因按优先级只报第一个。
func TestErrorPrecedence(t *testing.T) {
	l := NewLedger()

	// 注册：Q、P、X 非正拒绝。
	expectReason(t, l.Register("bad", 0, 10, 5), ReasonInvalidPeriodConfig)
	expectReason(t, l.Register("bad", 100, -1, 5), ReasonInvalidPeriodConfig)
	expectReason(t, l.Register("bad", 100, 10, 0), ReasonInvalidPeriodConfig)

	mustRegister(t, l, "svc", 100, 10, 5)
	expectReason(t, l.Register("svc", 50, 10, 5), ReasonSubjectAlreadyRegistered)

	// 预留：未注册优先于 r 非正。
	expectReason(t, func() error { _, err := l.Reserve("ghost", -1, 0); return err }(), ReasonSubjectNotRegistered)
	expectReason(t, func() error { _, err := l.Reserve("svc", 0, 0); return err }(), ReasonNonPositiveAmount)
	expectReason(t, func() error { _, err := l.Reserve("svc", -3, 0); return err }(), ReasonNonPositiveAmount)

	// 额度不足：返回剩余量。
	mustReserve(t, l, "svc", 70, 0)
	_, err := l.Reserve("svc", 31, 0)
	expectReason(t, err, ReasonInsufficientQuota)
	if be := err.(*Error); be.Remaining != 30 {
		t.Fatalf("剩余量应为 30，实际 %d", be.Remaining)
	}
	// 被拒的预留不改变账目：30 仍可预留成功。
	mustReserve(t, l, "svc", 30, 0)

	// 结算：不存在 -> 已结算 -> 已释放 -> u 为负。
	expectReason(t, l.Settle("v999", 1), ReasonReservationNotFound)
	id := mustReserve(t, l, "svc", 10, 10)
	expectReason(t, l.Settle(id, -1), ReasonNegativeUsage) // 在途时 u 为负
	mustSettle(t, l, id, 10)
	expectReason(t, l.Settle(id, -1), ReasonAlreadySettled) // 已结算优先于 u 为负

	// 释放：不存在 -> 已结算 -> 已释放。
	expectReason(t, l.Release("v999"), ReasonReservationNotFound)
	expectReason(t, l.Release(id), ReasonAlreadySettled)
	id2 := mustReserve(t, l, "svc", 10, 10)
	if err := l.Release(id2); err != nil {
		t.Fatalf("Release 失败: %v", err)
	}
	expectReason(t, l.Release(id2), ReasonAlreadyReleased)
	expectReason(t, l.Settle(id2, -1), ReasonAlreadyReleased) // 已释放优先于 u 为负

	// 查询未注册主体。
	if _, _, err := l.Query("ghost", 0, 0); err == nil {
		t.Fatal("查询未注册主体应报错")
	} else {
		expectReason(t, err, ReasonSubjectNotRegistered)
	}
}

// 并发预留不超额：成功预留总额恰好不超过 Q。
func TestConcurrentReserveNeverExceeds(t *testing.T) {
	l := NewLedger()
	const q, p, x = 100, 1000, 1000
	mustRegister(t, l, "svc", q, p, x)

	const goroutines = 64
	const r = 3
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := make([]string, 0, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := l.Reserve("svc", r, 0)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ids = append(ids, id)
			}
		}()
	}
	wg.Wait()

	// Q=100, r=3：恰好 33 笔成功（33*3=99 <= 100 < 34*3）。
	if got, want := len(ids), 33; got != want {
		t.Fatalf("成功预留数应为 %d，实际 %d", want, got)
	}
	used, inflight := queryMust(t, l, "svc", 0, 0)
	logDecision(t, "并发 64 goroutine 各预留 3",
		fmt.Sprintf("成功=%d used=%d inflight=%d", len(ids), used, inflight),
		"每次成功瞬间 used+inflight+r <= Q")
	if used+inflight > q {
		t.Fatalf("used+inflight=%d 超过 Q=%d", used+inflight, q)
	}

	// 并发结算与释放：同一预留只能被结算或释放其一且至多一次。
	var settleOK, releaseOK, rejected int64
	var mu2 sync.Mutex
	wg = sync.WaitGroup{}
	for _, id := range ids {
		for _, op := range []string{"settle", "release"} {
			wg.Add(1)
			go func(id, op string) {
				defer wg.Done()
				var err error
				if op == "settle" {
					err = l.Settle(id, r)
				} else {
					err = l.Release(id)
				}
				mu2.Lock()
				defer mu2.Unlock()
				switch {
				case err == nil && op == "settle":
					settleOK++
				case err == nil && op == "release":
					releaseOK++
				default:
					rejected++
				}
			}(id, op)
		}
	}
	wg.Wait()
	logDecision(t, "对每笔预留并发 settle+release",
		fmt.Sprintf("settleOK=%d releaseOK=%d rejected=%d", settleOK, releaseOK, rejected),
		"同一预留只能被结算或释放其一且至多一次")
	if settleOK+releaseOK != int64(len(ids)) {
		t.Fatalf("成功终态数应为 %d，实际 %d", len(ids), settleOK+releaseOK)
	}
	used, inflight = queryMust(t, l, "svc", 0, 0)
	if inflight != 0 {
		t.Fatalf("全部终态后在途应为 0，实际 %d", inflight)
	}
	if used != settleOK*r {
		t.Fatalf("已用量应为 %d，实际 %d", settleOK*r, used)
	}
}

// naiveModel 是测试内的逐笔朴素记账实现，用于与 Ledger 对拍。
type naiveModel struct {
	q, p, x int64
	used    map[int64]int64
	res     map[string]*naiveRes
	counter int64
}

type naiveRes struct {
	period    int64
	amount    int64
	expiresAt int64
	state     reservationState
}

func newNaiveModel(q, p, x int64) *naiveModel {
	return &naiveModel{q: q, p: p, x: x, used: map[int64]int64{}, res: map[string]*naiveRes{}}
}

func (m *naiveModel) inflight(period, now int64) int64 {
	var sum int64
	for _, r := range m.res {
		if r.period == period && r.state == stateInflight && now < r.expiresAt {
			sum += r.amount
		}
	}
	return sum
}

func (m *naiveModel) reserve(r, now int64) (string, bool) {
	period := floorDiv(now, m.p)
	if m.used[period]+m.inflight(period, now)+r > m.q {
		return "", false
	}
	m.counter++
	id := fmt.Sprintf("v%d", m.counter)
	m.res[id] = &naiveRes{period: period, amount: r, expiresAt: now + m.x, state: stateInflight}
	return id, true
}

func (m *naiveModel) settle(id string, u int64) {
	r := m.res[id]
	r.state = stateSettled
	m.used[r.period] += u
}

func (m *naiveModel) release(id string) {
	m.res[id].state = stateReleased
}

// 随机操作序列下，Ledger 查询结果与逐笔朴素记账一致。
func TestQueryMatchesNaiveModel(t *testing.T) {
	const q, p, x = 50, 10, 7
	l := NewLedger()
	mustRegister(t, l, "svc", q, p, x)
	model := newNaiveModel(q, p, x)

	rng := rand.New(rand.NewSource(42))
	var ids []string
	for step := 0; step < 2000; step++ {
		now := int64(rng.Intn(200))
		switch rng.Intn(4) {
		case 0: // 预留
			r := int64(rng.Intn(30) + 1)
			id, err := l.Reserve("svc", r, now)
			mid, mok := model.reserve(r, now)
			if (err == nil) != mok {
				t.Fatalf("step=%d Reserve(r=%d, now=%d): ledger=%v model=%v", step, r, now, err, mok)
			}
			if err == nil {
				if id != mid {
					t.Fatalf("step=%d 预留号不一致: ledger=%s model=%s", step, id, mid)
				}
				ids = append(ids, id)
			}
		case 1: // 结算
			if len(ids) == 0 {
				continue
			}
			id := ids[rng.Intn(len(ids))]
			u := int64(rng.Intn(60))
			err := l.Settle(id, u)
			st := model.res[id].state
			if st == stateInflight {
				if err != nil {
					t.Fatalf("step=%d Settle(%s, %d) 应成功: %v", step, id, u, err)
				}
				model.settle(id, u)
			} else if err == nil {
				t.Fatalf("step=%d Settle(%s) 应被拒（状态=%v）", step, id, st)
			}
		case 2: // 释放
			if len(ids) == 0 {
				continue
			}
			id := ids[rng.Intn(len(ids))]
			err := l.Release(id)
			st := model.res[id].state
			if st == stateInflight {
				if err != nil {
					t.Fatalf("step=%d Release(%s) 应成功: %v", step, id, err)
				}
				model.release(id)
			} else if err == nil {
				t.Fatalf("step=%d Release(%s) 应被拒（状态=%v）", step, id, st)
			}
		case 3: // 查询对拍
			period := int64(rng.Intn(20))
			used, inflight, err := l.Query("svc", period, now)
			if err != nil {
				t.Fatalf("step=%d Query 失败: %v", step, err)
			}
			mu, mi := model.used[period], model.inflight(period, now)
			if used != mu || inflight != mi {
				t.Fatalf("step=%d Query(period=%d, now=%d): ledger=(%d,%d) model=(%d,%d)",
					step, period, now, used, inflight, mu, mi)
			}
		}
	}
	used, inflight, _ := l.Query("svc", 0, 0)
	logDecision(t, "2000 步随机操作（预留/结算/释放/查询）",
		fmt.Sprintf("末态 period0 used=%d inflight=%d，全程与朴素模型一致", used, inflight),
		"逐笔朴素记账对拍")
}
