package railway

import (
	"strconv"
	"testing"
)

func reasonOf(err error) Reason {
	if err == nil {
		return ReasonOK
	}
	if e, ok := err.(*OpError); ok {
		return e.Reason
	}
	return -1
}

func allocN(n int, m map[[2]int]int) [][]int {
	a := make([][]int, n)
	for i := range a {
		a[i] = make([]int, n)
	}
	for k, v := range m {
		a[k[0]][k[1]] = v
	}
	return a
}

func spec4() TrainSpec {
	return TrainSpec{
		ID:          "T1",
		Departures:  []int{100, 200, 300, 400},
		Cars:        2,
		SeatsPerCar: 2,
		Alloc: allocN(4, map[[2]int]int{
			{0, 1}: 2, {0, 2}: 1, {0, 3}: 1,
			{1, 2}: 1, {1, 3}: 1,
			{2, 3}: 3,
		}),
		Shared: 1,
	}
}

func buyOK(t *testing.T, s *Service, now int, id string, from, to int, p string, ws bool) *Ticket {
	t.Helper()
	res, err := s.Buy(now, id, TicketDesc{from, to, p}, ws)
	if err != nil {
		t.Fatalf("buy %d->%d %s: unexpected %v", from, to, p, err)
	}
	return res.Ticket
}

func buyReason(t *testing.T, s *Service, now int, id string, from, to int, p string, ws bool, want Reason) {
	t.Helper()
	_, err := s.Buy(now, id, TicketDesc{from, to, p}, ws)
	if reasonOf(err) != want {
		t.Fatalf("buy %d->%d %s: want %s got %v", from, to, p, want, err)
	}
}

// 到站恰等于另一张票发站（左闭右开），同一座位可复用。
func TestAbutReuse(t *testing.T) {
	s := NewService(Config{AdvanceSeconds: 0, StandingRatio: 0})
	if err := s.AddTrain(spec4()); err != nil {
		t.Fatal(err)
	}
	t1 := buyOK(t, s, 10, "T1", 0, 1, "A", false)
	t2 := buyOK(t, s, 20, "T1", 1, 2, "B", false)
	if t1.Car != t2.Car || t1.No != t2.No {
		t.Fatalf("abutting tickets must share seat: %d-%d vs %d-%d", t1.Car, t1.No, t2.Car, t2.No)
	}
}

// 仅一端相接优先于全空座位。
func TestSeatSingleAbut(t *testing.T) {
	sp := spec4()
	sp.Alloc = allocN(4, nil)
	sp.Shared = 100
	s := NewService(Config{AdvanceSeconds: 0, StandingRatio: 0})
	if err := s.AddTrain(sp); err != nil {
		t.Fatal(err)
	}
	one := buyOK(t, s, 1, "T1", 0, 1, "x", false)
	if one.Car != 1 || one.No != 1 {
		t.Fatalf("first seat = %d-%d", one.Car, one.No)
	}
	got := buyOK(t, s, 2, "T1", 1, 3, "y", false)
	if got.Car != 1 || got.No != 1 {
		t.Fatalf("single-abut must win over empty seats: %d-%d", got.Car, got.No)
	}
}

// 两端相接优先于仅一端相接：构造 (1,1) 占 0-1 与 2-3 的双端坑，
// (2,1) 仅占 2-3（只右相接），空座位不相接；买 1-2 必须选 (1,1)。
func TestSeatDoubleAbut(t *testing.T) {
	sp := spec4()
	sp.Alloc = allocN(4, nil)
	sp.Shared = 100
	s := NewService(Config{AdvanceSeconds: 0, StandingRatio: 0})
	if err := s.AddTrain(sp); err != nil {
		t.Fatal(err)
	}
	buyOK(t, s, 1, "T1", 0, 1, "a", false) // (1,1):0-1
	buyOK(t, s, 2, "T1", 2, 3, "b", false) // (1,1):2-3（不相接，按序仍 1-1）
	buyOK(t, s, 3, "T1", 0, 1, "c", false) // (1,2):0-1
	buyOK(t, s, 4, "T1", 1, 3, "d", false) // (1,2):1-3（左端相接），占住其 2-3
	buyOK(t, s, 5, "T1", 2, 3, "e", false) // (2,1):2-3
	got := buyOK(t, s, 6, "T1", 1, 2, "f", false)
	if got.Car != 1 || got.No != 1 {
		t.Fatalf("double-abut (1,1) must win, got %d-%d", got.Car, got.No)
	}
}

// 分配票额为零转扣共用；两者皆零报票额不足。
func TestQuotaFallback(t *testing.T) {
	s := NewService(Config{AdvanceSeconds: 0, StandingRatio: 0})
	if err := s.AddTrain(spec4()); err != nil {
		t.Fatal(err)
	}
	t1 := buyOK(t, s, 1, "T1", 2, 3, "a", false)
	buyOK(t, s, 2, "T1", 2, 3, "b", false)
	buyOK(t, s, 3, "T1", 2, 3, "c", false)
	t4 := buyOK(t, s, 4, "T1", 2, 3, "d", false)
	if t1.QuotaShared || !t4.QuotaShared {
		t.Fatalf("source t1.shared=%v t4.shared=%v", t1.QuotaShared, t4.QuotaShared)
	}
	buyReason(t, s, 5, "T1", 2, 3, "e", false, ReasonQuotaExhausted)
}

// 并入恰在截止时刻发生且不可逆；并入后退票退回共用。
func TestMergeAtDeadlineAndRefund(t *testing.T) {
	cfg := Config{AdvanceSeconds: 50, StandingRatio: 0}
	s := NewService(cfg)
	if err := s.AddTrain(spec4()); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Remaining(49, "T1", 0, 1)
	if v.Merged || v.Allocation != 2 || v.Shared != 1 {
		t.Fatalf("before deadline %+v", v)
	}
	v, _ = s.Remaining(50, "T1", 0, 1)
	if !v.Merged || v.Shared != 5 {
		t.Fatalf("at deadline %+v", v)
	}

	s2 := NewService(cfg)
	if err := s2.AddTrain(spec4()); err != nil {
		t.Fatal(err)
	}
	tk := buyOK(t, s2, 40, "T1", 0, 1, "a", false)
	buyOK(t, s2, 55, "T1", 0, 2, "b", false)
	if err := s2.Refund(56, tk.ID); err != nil {
		t.Fatal(err)
	}
	v, _ = s2.Remaining(56, "T1", 0, 1)
	if !v.Merged || v.Allocation != 0 {
		t.Fatalf("allocation must stay zero after merge %+v", v)
	}
	v2, _ := s2.Remaining(56, "T1", 0, 3)
	// 初始共用1 + 并入剩余 (1+1+1=3) =4；b 扣1 →3；退票回共用 →4。
	if v2.Shared != 4 {
		t.Fatalf("refund after merge must return to shared, got %+v", v2)
	}
}

// 无座：部分区段名额用尽；有空闲座位时请求无座仍给座位；退票释放名额。
func TestStanding(t *testing.T) {
	cfg := Config{AdvanceSeconds: 0, StandingRatio: 0.5}
	sp := spec4()
	sp.Alloc = allocN(4, nil)
	sp.Shared = 100
	s := NewService(cfg)
	if err := s.AddTrain(sp); err != nil {
		t.Fatal(err)
	}
	tk := buyOK(t, s, 1, "T1", 0, 1, "a", true)
	if tk.Standing {
		t.Fatalf("must give seat while any seat free")
	}
	buyOK(t, s, 2, "T1", 0, 1, "b", false)
	buyOK(t, s, 3, "T1", 0, 1, "c", false)
	buyOK(t, s, 4, "T1", 0, 1, "d", false)
	st1 := buyOK(t, s, 5, "T1", 0, 1, "e", false)
	st2 := buyOK(t, s, 6, "T1", 0, 3, "f", false)
	if !st1.Standing || !st2.Standing {
		t.Fatalf("expected standing tickets")
	}
	buyReason(t, s, 7, "T1", 0, 2, "g", false, ReasonNoSeat)
	ok := buyOK(t, s, 8, "T1", 1, 2, "h", false)
	if ok.Standing {
		t.Fatalf("1-2 has free seats; must give a seat")
	}
	if err := s.Refund(9, st2.ID); err != nil {
		t.Fatal(err)
	}
	again := buyOK(t, s, 10, "T1", 0, 1, "i", false)
	if !again.Standing {
		t.Fatalf("standing capacity must reopen after refund")
	}
}

// 乘车人相接允许、相交拒绝；now 恰等于发车视为已发车；时钟回退。
func TestPassengerAndClock(t *testing.T) {
	s := NewService(Config{AdvanceSeconds: 0, StandingRatio: 0})
	if err := s.AddTrain(spec4()); err != nil {
		t.Fatal(err)
	}
	buyOK(t, s, 10, "T1", 0, 1, "P", false)
	buyOK(t, s, 20, "T1", 1, 3, "P", false)
	buyReason(t, s, 30, "T1", 2, 3, "P", false, ReasonPassengerOverlap)
	buyReason(t, s, 100, "T1", 0, 1, "Q", false, ReasonDeparted)
	buyReason(t, s, 5, "T1", 2, 3, "Q", false, ReasonClockRollback)
}

// 拒绝次序相邻类别。
func TestRejectOrder(t *testing.T) {
	s := NewService(Config{AdvanceSeconds: 0, StandingRatio: 0})
	if err := s.AddTrain(spec4()); err != nil {
		t.Fatal(err)
	}
	tk := buyOK(t, s, 90, "T1", 2, 3, "P", false)
	buyReason(t, s, -5, "NOPE", 0, 0, "", false, ReasonInvalidParam)
	buyReason(t, s, 50, "NOPE", 0, 1, "x", false, ReasonClockRollback)
	buyReason(t, s, 95, "NOPE", 0, 1, "x", false, ReasonTrainNotFound)
	if got := reasonOf(s.Refund(-1, 0)); got != ReasonInvalidParam {
		t.Fatalf("refund invalid %v", got)
	}
	if got := reasonOf(s.Refund(50, 9999)); got != ReasonClockRollback {
		t.Fatalf("refund rollback %v", got)
	}
	if got := reasonOf(s.Refund(1000, 9999)); got != ReasonTicketNotFound {
		t.Fatalf("refund missing %v", got)
	}
	if err := s.Refund(91, tk.ID); err != nil {
		t.Fatal(err)
	}
	if got := reasonOf(s.Refund(1000, tk.ID)); got != ReasonTicketRefunded {
		t.Fatalf("refunded %v", got)
	}
	buyReason(t, s, 1000, "T1", 0, 1, "P", false, ReasonDeparted)

	s2 := NewService(Config{AdvanceSeconds: 0, StandingRatio: 0})
	if err := s2.AddTrain(spec4()); err != nil {
		t.Fatal(err)
	}
	buyOK(t, s2, 10, "T1", 0, 2, "P", false)
	buyReason(t, s2, 20, "T1", 0, 1, "P", false, ReasonPassengerOverlap)
}

// 被拒绝操作不推进时钟、不改票额。
func TestRejectedNoSideEffect(t *testing.T) {
	s := NewService(Config{AdvanceSeconds: 0, StandingRatio: 0})
	if err := s.AddTrain(spec4()); err != nil {
		t.Fatal(err)
	}
	buyOK(t, s, 90, "T1", 2, 3, "seed", false) // 推进时钟到 90
	v, _ := s.Remaining(90, "T1", 2, 3)
	buyReason(t, s, 1000, "T1", 0, 1, "x", false, ReasonDeparted)
	buyReason(t, s, 1, "T1", 2, 3, "x", false, ReasonClockRollback)
	v2, _ := s.Remaining(90, "T1", 2, 3)
	if v != v2 {
		t.Fatalf("state changed by rejected op: %+v vs %+v", v, v2)
	}
}

// 构造每座位的边占用位图，密度参数决定每条边占用比例。
func buildMap(nStations, nCars, perCar, densityPercent int) (*seatMap, int, int) {
	seats := make([]Seat, 0, nCars*perCar)
	for c := 1; c <= nCars; c++ {
		for k := 1; k <= perCar; k++ {
			seats = append(seats, Seat{c, k})
		}
	}
	m := newSeatMap(nStations, seats, 0)
	for i := range m.occ {
		for e := 0; e < nStations-1; e++ {
			if (i*7+e*13)%100 < densityPercent {
				m.occ[i][e] = true
			}
		}
	}
	return m, 0, nStations - 1
}

// 选座热路径仅随 座位数*站数 增长，与历史售票张数无关。
func BenchmarkPickSeat(b *testing.B) {
	cases := []struct{ stations, cars, per, density int }{
		{8, 5, 50, 20},
		{8, 20, 50, 20},
		{16, 20, 50, 60},
	}
	for _, cs := range cases {
		name := "S" + strconv.Itoa(cs.stations) + "_seats" + strconv.Itoa(cs.cars*cs.per)
		m, from, to := buildMap(cs.stations, cs.cars, cs.per, cs.density)
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = m.pickSeat(from, to)
			}
		})
	}
}
