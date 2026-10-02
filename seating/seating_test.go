package seating

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, rows, width int, ttl int64) *Registrar {
	t.Helper()
	r, err := New(rows, width, ttl)
	if err != nil {
		t.Fatalf("New(%d, %d, %d) 意外失败: %v", rows, width, ttl, err)
	}
	return r
}

func mustHold(t *testing.T, r *Registrar, k int, now int64) HoldResult {
	t.Helper()
	res, err := r.Hold(k, now)
	if err != nil {
		t.Fatalf("Hold(%d, %d) 意外失败: %v", k, now, err)
	}
	return res
}

// injectConfirmed 直接注入已确认订单，用于精确构造座位布局。
func injectConfirmed(r *Registrar, row int, seats ...int) {
	for _, s := range seats {
		r.holds = append(r.holds, &hold{row: row, start: s, size: 1, status: holdConfirmed})
	}
}

func TestNewValidation(t *testing.T) {
	bad := []struct {
		rows, width int
		ttl         int64
	}{
		{0, 5, 1}, {27, 5, 1}, {-1, 5, 1},
		{5, 0, 1}, {5, 41, 1}, {5, -1, 1},
		{5, 5, 0}, {5, 5, -3},
	}
	for _, c := range bad {
		if _, err := New(c.rows, c.width, c.ttl); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("New(%d, %d, %d) = %v, 期望 ErrInvalidConfig", c.rows, c.width, c.ttl, err)
		}
	}
	good := []struct {
		rows, width int
		ttl         int64
	}{{1, 1, 1}, {26, 40, 1}, {1, 40, 100}, {26, 1, 7}}
	for _, c := range good {
		if _, err := New(c.rows, c.width, c.ttl); err != nil {
			t.Errorf("New(%d, %d, %d) 边界值应合法: %v", c.rows, c.width, c.ttl, err)
		}
	}
}

func TestClockRollback(t *testing.T) {
	r := mustNew(t, 1, 2, 10)
	h1 := mustHold(t, r, 1, 5)

	if _, err := r.Hold(1, 4); !errors.Is(err, ErrClockRollback) {
		t.Errorf("Hold 时钟回退 = %v, 期望 ErrClockRollback", err)
	}
	if err := r.Confirm(h1.ID, 4); !errors.Is(err, ErrClockRollback) {
		t.Errorf("Confirm 时钟回退 = %v, 期望 ErrClockRollback", err)
	}
	if err := r.Release(h1.ID, 3); !errors.Is(err, ErrClockRollback) {
		t.Errorf("Release 时钟回退 = %v, 期望 ErrClockRollback", err)
	}
	// 时钟检查优先于人数检查。
	if _, err := r.Hold(9, 4); !errors.Is(err, ErrClockRollback) {
		t.Errorf("回退且人数非法 = %v, 期望 ErrClockRollback 优先", err)
	}
	// Seats 不检查也不更新已见最大 now。
	seats := r.Seats(1000)
	if seats[0][0] != SeatFree {
		t.Errorf("Seats(1000) 中保留应已过期为空闲, 得到 %v", seats[0][0])
	}
	r.Seats(0)
	// 等于已见最大 now 允许。
	h2 := mustHold(t, r, 1, 5)
	if h2.ID != 2 {
		t.Errorf("h2.ID = %d, 期望 2", h2.ID)
	}
	if err := r.Confirm(h2.ID, 5); err != nil {
		t.Fatalf("Confirm 等于 maxNow 应成功: %v", err)
	}
}

func TestInvalidGroupSize(t *testing.T) {
	r := mustNew(t, 1, 10, 10)
	for _, k := range []int{0, -1, 9, 100} {
		if _, err := r.Hold(k, 1); !errors.Is(err, ErrInvalidGroupSize) {
			t.Errorf("Hold(%d, 1) = %v, 期望 ErrInvalidGroupSize", k, err)
		}
	}
	for _, k := range []int{1, 8} {
		fresh := mustNew(t, 1, 10, 10)
		if _, err := fresh.Hold(k, 1); err != nil {
			t.Errorf("Hold(%d, 1) 边界值应合法: %v", k, err)
		}
	}
}

// 左侧孤座：座 5 已确认，空闲区 [1,4] 与 [6,10]。
// s=7 的段中心偏离最小（0），但选定后左侧留下单座 6（孤座），
// 因此第一遍应选中 s=6（偏离 2）。
func TestOrphanLeftSide(t *testing.T) {
	r := mustNew(t, 1, 10, 100)
	injectConfirmed(r, 1, 5)
	res := mustHold(t, r, 2, 0)
	if res.Row != 1 || res.Start != 6 {
		t.Errorf("Hold = 排%d 座%d, 期望 排1 座6（s=7 因左侧孤座被排除）", res.Row, res.Start)
	}
}

// 右侧孤座：座 1、8、9、10 已确认，空闲区 [2,7]，k=3。
// s=4 与 s=5 段中心偏离并列最小（1），但 s=4 选定后右侧留下单座 7（孤座），
// 因此第一遍应选中 s=5。
func TestOrphanRightSide(t *testing.T) {
	r := mustNew(t, 1, 10, 100)
	injectConfirmed(r, 1, 1, 8, 9, 10)
	res := mustHold(t, r, 3, 0)
	if res.Row != 1 || res.Start != 5 {
		t.Errorf("Hold = 排%d 座%d, 期望 排1 座5（s=4 因右侧孤座被排除）", res.Row, res.Start)
	}
}

// 紧邻墙壁的单个空闲座也算孤座：排 1 的座 4、5 已确认，空闲区 [1,3]。
// s=2 选定后左侧留下紧邻墙壁的单座 1，仍算孤座；
// s=1 右侧留下单座 3 也是孤座，故排 1 没有无孤座候选，应选到排 2。
// 排 2 中 s=3 会在右侧留下单座 5（孤座），无孤座候选 s=1 与 s=4 偏离并列，取小 s。
func TestWallAdjacentSingleSeatIsOrphan(t *testing.T) {
	r := mustNew(t, 2, 5, 100)
	injectConfirmed(r, 1, 4, 5)
	res := mustHold(t, r, 2, 0)
	if res.Row != 2 || res.Start != 1 {
		t.Errorf("Hold = 排%d 座%d, 期望 排2 座1（排1 因墙壁孤座无候选）", res.Row, res.Start)
	}
}

// 后排无孤座候选胜过前排有孤座候选：
// 排 1 仅剩 [1,3]（k=2 的两个候选都产生孤座），排 2 全空，应选排 2。
// 排 2 中 s=3 占座 3-4，右侧剩座 5-6 长度 2 非孤座，且段中心偏离为 0。
func TestBackRowNoOrphanBeatsFrontRowOrphan(t *testing.T) {
	r := mustNew(t, 2, 6, 100)
	injectConfirmed(r, 1, 4, 5, 6)
	res := mustHold(t, r, 2, 0)
	if res.Row != 2 || res.Start != 3 {
		t.Errorf("Hold = 排%d 座%d, 期望 排2 座3（排2 无孤座候选优先）", res.Row, res.Start)
	}
}

// 所有排都没有无孤座候选时才退到第二遍：
// 两排 W=3 全空，k=2 的全部候选都产生孤座，
// 退到全部候选后按排号、偏离、座号次序选排 1 座 1。
func TestFallbackToSecondPass(t *testing.T) {
	r := mustNew(t, 2, 3, 100)
	res := mustHold(t, r, 2, 0)
	if res.Row != 1 || res.Start != 1 {
		t.Errorf("Hold = 排%d 座%d, 期望 排1 座1（第二遍按次序选择）", res.Row, res.Start)
	}
}

// W 为偶数时段中心偏离并列取小 s：
// W=6, k=1 时 s=3 与 s=4 偏离同为 1，应选 s=3。
func TestDeviationTiePrefersSmallerStart(t *testing.T) {
	r := mustNew(t, 1, 6, 100)
	res := mustHold(t, r, 1, 0)
	if res.Row != 1 || res.Start != 3 {
		t.Errorf("Hold = 排%d 座%d, 期望 排1 座3（偏离并列取小 s）", res.Row, res.Start)
	}
}

// k 等于整排宽度：唯一候选 s=1，占满整排。
func TestKEqualsRowWidth(t *testing.T) {
	r := mustNew(t, 2, 8, 5)
	h1 := mustHold(t, r, 8, 0)
	if h1.Row != 1 || h1.Start != 1 {
		t.Errorf("第一次 Hold = 排%d 座%d, 期望 排1 座1", h1.Row, h1.Start)
	}
	h2 := mustHold(t, r, 8, 1)
	if h2.Row != 2 || h2.Start != 1 {
		t.Errorf("第二次 Hold = 排%d 座%d, 期望 排2 座1", h2.Row, h2.Start)
	}
	if _, err := r.Hold(8, 2); !errors.Is(err, ErrNoSeats) {
		t.Errorf("占满后 Hold = %v, 期望 ErrNoSeats", err)
	}
}

// 过期恰在 expiry 那一刻发生：now == expiry 时保留不再占座，
// Hold 可再选中其座位，而 Confirm 报已过期。
func TestExpiryBoundary(t *testing.T) {
	r := mustNew(t, 1, 4, 10)
	h1 := mustHold(t, r, 1, 0) // expiry = 10
	if h1.Start != 1 {
		t.Fatalf("h1.Start = %d, 期望 1", h1.Start)
	}

	if got := r.Seats(9)[0][0]; got != SeatHeld {
		t.Errorf("Seats(9) 座1 = %v, 期望保留中", got)
	}
	if got := r.Seats(10)[0][0]; got != SeatFree {
		t.Errorf("Seats(10) 座1 = %v, 期望空闲（恰在 expiry 过期）", got)
	}
	if err := r.Confirm(h1.ID, 10); !errors.Is(err, ErrHoldExpired) {
		t.Errorf("Confirm(expiry 时刻) = %v, 期望 ErrHoldExpired", err)
	}
	// 过期保留的座位自 expiry 起即可被后续 Hold 选中。
	h2 := mustHold(t, r, 1, 10)
	if h2.Start != 1 || h2.ID != 2 {
		t.Errorf("h2 = %+v, 期望座1、保留号2", h2)
	}
	if err := r.Confirm(h2.ID, 19); err != nil {
		t.Fatalf("Confirm(h2, 19) 应成功: %v", err)
	}
	if got := r.Seats(19)[0][0]; got != SeatConfirmed {
		t.Errorf("Seats(19) 座1 = %v, 期望已确认", got)
	}
	if err := r.Confirm(h1.ID, 19); !errors.Is(err, ErrHoldExpired) {
		t.Errorf("Confirm(h1, 19) = %v, 期望 ErrHoldExpired", err)
	}

	// expiry 前一时刻确认仍然有效。
	r2 := mustNew(t, 1, 1, 10)
	h := mustHold(t, r2, 1, 0)
	if err := r2.Confirm(h.ID, 9); err != nil {
		t.Errorf("Confirm(expiry-1) 应成功: %v", err)
	}
}

// Release 后座位立即空闲并可被再次选中。
func TestReleaseMakesSeatsAvailableImmediately(t *testing.T) {
	r := mustNew(t, 1, 4, 100)
	h1 := mustHold(t, r, 1, 0)
	if err := r.Release(h1.ID, 5); err != nil {
		t.Fatalf("Release 应成功: %v", err)
	}
	for seat := 0; seat < 4; seat++ {
		if got := r.Seats(5)[0][seat]; got != SeatFree {
			t.Fatalf("Seats(5) 座%d = %v, 期望空闲", seat+1, got)
		}
	}
	h2 := mustHold(t, r, 1, 5)
	if h2.Start != h1.Start {
		t.Errorf("释放后再次 Hold 应选中同一座位 %d, 得到 %d", h1.Start, h2.Start)
	}
	if h2.ID != 2 {
		t.Errorf("h2.ID = %d, 期望 2", h2.ID)
	}
}

// Confirm 与 Release 的拒绝原因按 未发出、已确认、已释放、已过期 优先判定。
func TestSettleReasonPrecedence(t *testing.T) {
	r := mustNew(t, 1, 4, 10)

	h1 := mustHold(t, r, 1, 0)
	if err := r.Confirm(h1.ID, 1); err != nil {
		t.Fatalf("Confirm(h1) 应成功: %v", err)
	}
	if err := r.Release(h1.ID, 2); !errors.Is(err, ErrHoldConfirmed) {
		t.Errorf("已确认不可释放 = %v, 期望 ErrHoldConfirmed", err)
	}
	if err := r.Confirm(h1.ID, 100); !errors.Is(err, ErrHoldConfirmed) {
		t.Errorf("已确认且已过期 = %v, 期望 ErrHoldConfirmed 优先", err)
	}
	if err := r.Release(h1.ID, 100); !errors.Is(err, ErrHoldConfirmed) {
		t.Errorf("已确认且已过期释放 = %v, 期望 ErrHoldConfirmed 优先", err)
	}

	h2 := mustHold(t, r, 1, 101)
	if err := r.Release(h2.ID, 102); err != nil {
		t.Fatalf("Release(h2) 应成功: %v", err)
	}
	if err := r.Confirm(h2.ID, 103); !errors.Is(err, ErrHoldReleased) {
		t.Errorf("已释放后确认 = %v, 期望 ErrHoldReleased", err)
	}
	if err := r.Confirm(h2.ID, 1000); !errors.Is(err, ErrHoldReleased) {
		t.Errorf("已释放且已过期 = %v, 期望 ErrHoldReleased 优先", err)
	}
	if err := r.Release(h2.ID, 1000); !errors.Is(err, ErrHoldReleased) {
		t.Errorf("已释放且已过期再释放 = %v, 期望 ErrHoldReleased 优先", err)
	}

	for _, id := range []int{0, -1, 999} {
		if err := r.Confirm(id, 1000); !errors.Is(err, ErrHoldNotFound) {
			t.Errorf("Confirm(%d) = %v, 期望 ErrHoldNotFound", id, err)
		}
		if err := r.Release(id, 1000); !errors.Is(err, ErrHoldNotFound) {
			t.Errorf("Release(%d) = %v, 期望 ErrHoldNotFound", id, err)
		}
	}
}

// 被拒绝的操作不得改变座位、保留与已见最大 now；保留号严格连续无空洞。
func TestRejectedOpsDoNotChangeState(t *testing.T) {
	r := mustNew(t, 1, 4, 10)

	h1 := mustHold(t, r, 1, 5) // maxNow = 5
	if h1.ID != 1 {
		t.Fatalf("h1.ID = %d, 期望 1", h1.ID)
	}

	// 人数非法被拒绝，不推进 maxNow。
	if _, err := r.Hold(9, 10); !errors.Is(err, ErrInvalidGroupSize) {
		t.Fatalf("Hold(9, 10) = %v, 期望 ErrInvalidGroupSize", err)
	}
	// 若 maxNow 被推进到 10，下面 now=7 会报时钟回退。
	h2 := mustHold(t, r, 1, 7)
	if h2.ID != 2 {
		t.Errorf("失败的 Hold 不得消耗保留号: h2.ID = %d, 期望 2", h2.ID)
	}

	// 保留号未发出被拒绝，不推进 maxNow，也不改变座位状态。
	before := r.Seats(8)
	if err := r.Confirm(999, 8); !errors.Is(err, ErrHoldNotFound) {
		t.Fatalf("Confirm(999, 8) = %v, 期望 ErrHoldNotFound", err)
	}
	if got := r.Seats(8); !reflect.DeepEqual(got, before) {
		t.Errorf("被拒绝的 Confirm 改变了座位状态: %v -> %v", before, got)
	}

	// 占满整排后，无座 Hold 被拒绝且不改变状态。
	mustHold(t, r, 1, 8)
	mustHold(t, r, 1, 9)
	full := r.Seats(9)
	if _, err := r.Hold(1, 10); !errors.Is(err, ErrNoSeats) {
		t.Fatalf("占满后 Hold = %v, 期望 ErrNoSeats", err)
	}
	if got := r.Seats(9); !reflect.DeepEqual(got, full) {
		t.Errorf("被拒绝的 Hold 改变了座位状态: %v -> %v", full, got)
	}
	// maxNow 仍是 9，now=9 的操作仍被接受。
	if err := r.Release(h1.ID, 9); err != nil {
		t.Fatalf("Release(h1, 9) 应成功（maxNow 未被拒绝操作推进）: %v", err)
	}
	// 释放后空位可再选，保留号继续连续。
	h5 := mustHold(t, r, 1, 9)
	if h5.ID != 5 {
		t.Errorf("h5.ID = %d, 期望 5（保留号严格连续无空洞）", h5.ID)
	}
}
