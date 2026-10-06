package baggage

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// newTestSystem 构造标准夹具：
// 区域 RA/RB；机场 A1 A2(RA)、B1 B2(RB)、X1(RA,需清关)；
// 承运人 CP(计件) CW(计重) CP2(计件,免费2件)；会员 GOLD。
func newTestSystem(t *testing.T) *System {
	t.Helper()
	s, err := NewSystem(Config{MinConn: 60, MaxConn: 240, Cutoff: 45})
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	must := func(e *Error) {
		t.Helper()
		if e != nil {
			t.Fatalf("setup: %v", e)
		}
	}
	must(s.AddAirport(Airport{Code: "A1", Region: "RA"}))
	must(s.AddAirport(Airport{Code: "A2", Region: "RA"}))
	must(s.AddAirport(Airport{Code: "B1", Region: "RB"}))
	must(s.AddAirport(Airport{Code: "B2", Region: "RB"}))
	must(s.AddAirport(Airport{Code: "X1", Region: "RA", Customs: true}))
	must(s.AddCarrier(Carrier{Code: "CP", Policy: PiecePolicy,
		FreePieces: 1, PieceFreeWeight: 200, AbsWeight: 320,
		PieceFee: 5000, OverweightFee: 3000}))
	must(s.AddCarrier(Carrier{Code: "CW", Policy: WeightPolicy,
		FreeTotalWeight: 300, AbsWeight: 500, UnitFee: 50}))
	must(s.AddCarrier(Carrier{Code: "CP2", Policy: PiecePolicy,
		FreePieces: 2, PieceFreeWeight: 200, AbsWeight: 320,
		PieceFee: 5000, OverweightFee: 3000}))
	must(s.AddTier(Tier{Name: "GOLD", ExtraPieces: 1, ExtraWeight: 100}))
	return s
}

func seg(from, to, carrier, pnr string, depart, arrive int64) Segment {
	return Segment{From: from, To: to, Carrier: carrier, PNR: pnr, Depart: depart, Arrive: arrive}
}

func mustCheckIn(t *testing.T, s *System, pnr string, pax []PassengerBags, itin []Segment, now int64) *Record {
	t.Helper()
	rec, err := s.CheckIn(pnr, pax, itin, now)
	if err != nil {
		t.Fatalf("CheckIn 失败: %v", err)
	}
	return rec
}

func expectErr(t *testing.T, err *Error, code ErrCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %v，实际成功", code)
	}
	if err.Code != code {
		t.Fatalf("期望错误 %v，实际 %v (%s)", code, err.Code, err.Msg)
	}
}

// 第一个跨区域航段不是第一段时，整条行程适用该航段承运人的额度。
func TestAllowanceFirstCrossRegionNotFirst(t *testing.T) {
	s := newTestSystem(t)
	if err := s.AddBooking("PNR1", "p1"); err != nil {
		t.Fatal(err)
	}
	// 第一段区域内（CP 计件），第二段跨区域（CW 计重）→ 全程适用 CW。
	itin := []Segment{
		seg("A1", "A2", "CP", "PNR1", 1000, 1100),
		seg("A2", "B1", "CW", "PNR1", 1160, 1300),
	}
	pax := []PassengerBags{{Passenger: "p1", Bags: []int64{400}}}
	rec := mustCheckIn(t, s, "PNR1", pax, itin, 0)
	if len(rec.Sections) != 1 {
		t.Fatalf("期望 1 段托运，实际 %d", len(rec.Sections))
	}
	if rec.Sections[0].Carrier != "CW" {
		t.Fatalf("额度承运人应为 CW，实际 %s", rec.Sections[0].Carrier)
	}
	// 计重制：400-300=100 百克 × 50 分 = 5000；若错用 CP 计件制结果不同。
	if rec.TotalFee != 5000 {
		t.Fatalf("总费用应为 5000，实际 %d", rec.TotalFee)
	}
}

// 中转停留恰等于最短/最长（闭区间）可直挂；超出则必须提取。
func TestConnectionBoundaries(t *testing.T) {
	cases := []struct {
		conn     int64
		sections int
	}{
		{59, 2}, {60, 1}, {240, 1}, {241, 2},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("conn=%d", c.conn), func(t *testing.T) {
			s := newTestSystem(t)
			if err := s.AddBooking("PNR1", "p1"); err != nil {
				t.Fatal(err)
			}
			itin := []Segment{
				seg("A1", "A2", "CP", "PNR1", 1000, 1100),
				seg("A2", "B1", "CP", "PNR1", 1100+c.conn, 1400),
			}
			pax := []PassengerBags{{Passenger: "p1", Bags: []int64{100}}}
			rec := mustCheckIn(t, s, "PNR1", pax, itin, 0)
			if len(rec.Sections) != c.sections {
				t.Fatalf("停留 %d 分钟期望 %d 段托运，实际 %d", c.conn, c.sections, len(rec.Sections))
			}
			if c.sections == 2 && (len(rec.Extractions) != 1 || rec.Extractions[0] != "A2") {
				t.Fatalf("提取点应为 [A2]，实际 %v", rec.Extractions)
			}
		})
	}
}

// 清关机场、同记录/不同记录对提取点的影响。
func TestExtractionReasons(t *testing.T) {
	// 清关：X1 需清关，即使同记录、停留正常也必须提取。
	t.Run("customs", func(t *testing.T) {
		s := newTestSystem(t)
		if err := s.AddBooking("PNR1", "p1"); err != nil {
			t.Fatal(err)
		}
		itin := []Segment{
			seg("A1", "X1", "CP", "PNR1", 1000, 1100),
			seg("X1", "A2", "CP", "PNR1", 1160, 1300),
		}
		pax := []PassengerBags{{Passenger: "p1", Bags: []int64{100}}}
		rec := mustCheckIn(t, s, "PNR1", pax, itin, 0)
		if len(rec.Sections) != 2 || len(rec.Extractions) != 1 || rec.Extractions[0] != "X1" {
			t.Fatalf("清关机场应产生提取点 X1，实际 sections=%d extractions=%v",
				len(rec.Sections), rec.Extractions)
		}
	})
	// 不同订座记录：停留正常、无需清关也必须提取。
	t.Run("different_pnr", func(t *testing.T) {
		s := newTestSystem(t)
		if err := s.AddBooking("PNR1", "p1"); err != nil {
			t.Fatal(err)
		}
		itin := []Segment{
			seg("A1", "A2", "CP", "PNR1", 1000, 1100),
			seg("A2", "B1", "CP", "PNR2", 1160, 1300),
		}
		pax := []PassengerBags{{Passenger: "p1", Bags: []int64{100}}}
		rec := mustCheckIn(t, s, "PNR1", pax, itin, 0)
		if len(rec.Sections) != 2 || len(rec.Extractions) != 1 || rec.Extractions[0] != "A2" {
			t.Fatalf("跨记录应产生提取点 A2，实际 sections=%d extractions=%v",
				len(rec.Sections), rec.Extractions)
		}
	})
	// 同记录、无清关、停留正常：无提取点，直挂终点为最终目的机场。
	t.Run("through", func(t *testing.T) {
		s := newTestSystem(t)
		if err := s.AddBooking("PNR1", "p1"); err != nil {
			t.Fatal(err)
		}
		itin := []Segment{
			seg("A1", "A2", "CP", "PNR1", 1000, 1100),
			seg("A2", "B1", "CP", "PNR1", 1160, 1300),
		}
		pax := []PassengerBags{{Passenger: "p1", Bags: []int64{100}}}
		rec := mustCheckIn(t, s, "PNR1", pax, itin, 0)
		if len(rec.Extractions) != 0 {
			t.Fatalf("不应有提取点，实际 %v", rec.Extractions)
		}
		if len(rec.Tags) != 1 || len(rec.Tags[0].Dests) != 1 || rec.Tags[0].Dests[0] != "B1" {
			t.Fatalf("直挂终点应为 B1，实际 %+v", rec.Tags)
		}
	})
}

// 重新托运后，新一段行程的适用额度切换到另一承运人的制式。
func TestRecheckSwitchesPolicy(t *testing.T) {
	s := newTestSystem(t)
	if err := s.AddBooking("PNR1", "p1"); err != nil {
		t.Fatal(err)
	}
	// 两段跨区域航段分属不同记录 → B1 提取；第一段适用 CP(计件)，第二段适用 CW(计重)。
	itin := []Segment{
		seg("A1", "B1", "CP", "PNR1", 1000, 1100),
		seg("B1", "A2", "CW", "PNR2", 1160, 1300),
	}
	pax := []PassengerBags{{Passenger: "p1", Bags: []int64{250, 100}}}
	rec := mustCheckIn(t, s, "PNR1", pax, itin, 0)
	if len(rec.Sections) != 2 {
		t.Fatalf("期望 2 段托运，实际 %d", len(rec.Sections))
	}
	if rec.Sections[0].Carrier != "CP" || rec.Sections[1].Carrier != "CW" {
		t.Fatalf("两段额度承运人应为 CP/CW，实际 %s/%s",
			rec.Sections[0].Carrier, rec.Sections[1].Carrier)
	}
	// 段1 计件：2 件超 1 件免费 → 5000；250>200 超重 → 3000；计 8000。
	if rec.Sections[0].Fee != 8000 {
		t.Fatalf("段1 费用应为 8000，实际 %d", rec.Sections[0].Fee)
	}
	// 段2 计重：350-300=50 百克 × 50 = 2500。
	if rec.Sections[1].Fee != 2500 {
		t.Fatalf("段2 费用应为 2500，实际 %d", rec.Sections[1].Fee)
	}
	if rec.TotalFee != 10500 {
		t.Fatalf("总费用应为 10500，实际 %d", rec.TotalFee)
	}
}

// 计件制：同一件行李既超件又超重，两种费用叠加。
func TestPieceExtraAndOverweightStack(t *testing.T) {
	s := newTestSystem(t)
	if err := s.AddBooking("PNR1", "p1"); err != nil {
		t.Fatal(err)
	}
	itin := []Segment{seg("A1", "B1", "CP", "PNR1", 1000, 1100)}
	pax := []PassengerBags{{Passenger: "p1", Bags: []int64{250, 100}}}
	rec := mustCheckIn(t, s, "PNR1", pax, itin, 0)
	// 超件 1×5000 + 超重 1×3000 = 8000。
	if rec.TotalFee != 8000 {
		t.Fatalf("总费用应为 8000，实际 %d", rec.TotalFee)
	}
}

// 计重制免费总重量可合并共享；计件制免费件数不可合并。
func TestPoolingDifference(t *testing.T) {
	// 计重：两人 400+100=500，合并免费 300+300=600 → 不收费。
	t.Run("weight_pooled", func(t *testing.T) {
		s := newTestSystem(t)
		if err := s.AddBooking("PNR1", "p1", "p2"); err != nil {
			t.Fatal(err)
		}
		itin := []Segment{seg("A1", "B1", "CW", "PNR1", 1000, 1100)}
		pax := []PassengerBags{
			{Passenger: "p1", Bags: []int64{400}},
			{Passenger: "p2", Bags: []int64{100}},
		}
		rec := mustCheckIn(t, s, "PNR1", pax, itin, 0)
		if rec.TotalFee != 0 {
			t.Fatalf("计重合并后费用应为 0，实际 %d", rec.TotalFee)
		}
	})
	// 计件：免费件数不合并。p1 三件、p2 一件，CP2 每人免费 2 件。
	// 若可合并：4 件 ≤ 2+2 → 0；实际 p1 超 1 件 → 5000。
	t.Run("piece_not_pooled", func(t *testing.T) {
		s := newTestSystem(t)
		if err := s.AddBooking("PNR1", "p1", "p2"); err != nil {
			t.Fatal(err)
		}
		itin := []Segment{seg("A1", "B1", "CP2", "PNR1", 1000, 1100)}
		pax := []PassengerBags{
			{Passenger: "p1", Bags: []int64{100, 100, 100}},
			{Passenger: "p2", Bags: []int64{100}},
		}
		rec := mustCheckIn(t, s, "PNR1", pax, itin, 0)
		if rec.TotalFee != 5000 {
			t.Fatalf("计件不可合并，费用应为 5000，实际 %d", rec.TotalFee)
		}
	})
}

// 会员额外额度只用于本人，合并时不转移。
func TestMemberExtraNotTransferred(t *testing.T) {
	// 计重：GOLD 的 100 百克额外额度不能补贴同行人。
	// p1(GOLD) 50 → 本人抵扣后 0；p2 450；合并免费池 200+200=400（CW 改小免费额见下）。
	t.Run("weight", func(t *testing.T) {
		s := newTestSystem(t)
		if err := s.AddCarrier(Carrier{Code: "CW2", Policy: WeightPolicy,
			FreeTotalWeight: 200, AbsWeight: 500, UnitFee: 50}); err != nil {
			t.Fatal(err)
		}
		if err := s.AddBooking("PNR1", "p1", "p2"); err != nil {
			t.Fatal(err)
		}
		itin := []Segment{seg("A1", "B1", "CW2", "PNR1", 1000, 1100)}
		pax := []PassengerBags{
			{Passenger: "p1", Tier: "GOLD", Bags: []int64{50}},
			{Passenger: "p2", Bags: []int64{450}},
		}
		rec := mustCheckIn(t, s, "PNR1", pax, itin, 0)
		// 不转移：(0 + 450) - 400 = 50 百克 × 50 = 2500；若转移则 500-500=0。
		if rec.TotalFee != 2500 {
			t.Fatalf("会员额外额度不应转移，费用应为 2500，实际 %d", rec.TotalFee)
		}
	})
	// 计件：GOLD 的额外 1 件不能给同行人用。
	t.Run("piece", func(t *testing.T) {
		s := newTestSystem(t)
		if err := s.AddBooking("PNR1", "p1", "p2"); err != nil {
			t.Fatal(err)
		}
		itin := []Segment{seg("A1", "B1", "CP", "PNR1", 1000, 1100)}
		pax := []PassengerBags{
			{Passenger: "p1", Tier: "GOLD", Bags: []int64{100}},
			{Passenger: "p2", Bags: []int64{100, 100}},
		}
		rec := mustCheckIn(t, s, "PNR1", pax, itin, 0)
		// p1 免费 2 件用不完；p2 免费 1 件超 1 件 → 5000；若转移则 0。
		if rec.TotalFee != 5000 {
			t.Fatalf("会员额外件数不应转移，费用应为 5000，实际 %d", rec.TotalFee)
		}
	})
}

// 办理时刻恰等于截止时刻视为已截止；早一分钟则成功。
func TestCutoffExact(t *testing.T) {
	itin := []Segment{seg("A1", "B1", "CP", "PNR1", 1000, 1100)}
	pax := []PassengerBags{{Passenger: "p1", Bags: []int64{100}}}
	// 截止时刻 = 1000 - 45 = 955。
	t.Run("equal_is_closed", func(t *testing.T) {
		s := newTestSystem(t)
		if err := s.AddBooking("PNR1", "p1"); err != nil {
			t.Fatal(err)
		}
		_, err := s.CheckIn("PNR1", pax, itin, 955)
		expectErr(t, err, ErrCutoffPassed)
	})
	t.Run("one_minute_before", func(t *testing.T) {
		s := newTestSystem(t)
		if err := s.AddBooking("PNR1", "p1"); err != nil {
			t.Fatal(err)
		}
		mustCheckIn(t, s, "PNR1", pax, itin, 954)
	})
}

// 拒绝次序的每一对相邻类别：构造同时满足两类条件的请求，只报更靠前的一类。
func TestRejectOrderAdjacentPairs(t *testing.T) {
	itin := []Segment{seg("A1", "B1", "CP", "PNR1", 1000, 1100)}
	pax := []PassengerBags{{Passenger: "p1", Bags: []int64{100}}}

	// 参数非法 > 时钟回退：重量为负且时刻回退，报参数非法。
	t.Run("invalid_before_rollback", func(t *testing.T) {
		s := newTestSystem(t)
		if err := s.AddBooking("PNR1", "p1"); err != nil {
			t.Fatal(err)
		}
		mustCheckIn(t, s, "PNR1", pax, itin, 100)
		bad := []PassengerBags{{Passenger: "p1", Bags: []int64{-1}}}
		_, err := s.CheckIn("PNR1", bad, itin, 50)
		expectErr(t, err, ErrInvalidParam)
	})
	// 时钟回退 > 不存在：订座记录不存在且时刻回退，报时钟回退。
	t.Run("rollback_before_notfound", func(t *testing.T) {
		s := newTestSystem(t)
		if err := s.AddBooking("PNR1", "p1"); err != nil {
			t.Fatal(err)
		}
		mustCheckIn(t, s, "PNR1", pax, itin, 100)
		_, err := s.CheckIn("GHOST", pax, itin, 50)
		expectErr(t, err, ErrClockRollback)
	})
	// 不存在 > 已有记录：记录已存在但旅客不存在，报不存在。
	t.Run("notfound_before_exists", func(t *testing.T) {
		s := newTestSystem(t)
		if err := s.AddBooking("PNR1", "p1"); err != nil {
			t.Fatal(err)
		}
		mustCheckIn(t, s, "PNR1", pax, itin, 0)
		ghost := []PassengerBags{{Passenger: "nobody", Bags: []int64{100}}}
		_, err := s.CheckIn("PNR1", ghost, itin, 1)
		expectErr(t, err, ErrNotFound)
	})
	// 已有记录 > 已截止：记录已存在且已过截止，报已有记录。
	t.Run("exists_before_cutoff", func(t *testing.T) {
		s := newTestSystem(t)
		if err := s.AddBooking("PNR1", "p1"); err != nil {
			t.Fatal(err)
		}
		mustCheckIn(t, s, "PNR1", pax, itin, 0)
		_, err := s.CheckIn("PNR1", pax, itin, 955)
		expectErr(t, err, ErrRecordExists)
	})
	// 已截止 > 超重拒收：已过截止且有超限行李，报已截止。
	t.Run("cutoff_before_overweight", func(t *testing.T) {
		s := newTestSystem(t)
		if err := s.AddBooking("PNR1", "p1"); err != nil {
			t.Fatal(err)
		}
		heavy := []PassengerBags{{Passenger: "p1", Bags: []int64{999}}}
		_, err := s.CheckIn("PNR1", heavy, itin, 955)
		expectErr(t, err, ErrCutoffPassed)
	})
}

// 超重拒收报出第一件超限行李的全局序号，且不产生记录、不推进时钟。
func TestOverweightReject(t *testing.T) {
	s := newTestSystem(t)
	if err := s.AddBooking("PNR1", "p1", "p2"); err != nil {
		t.Fatal(err)
	}
	itin := []Segment{seg("A1", "B1", "CP", "PNR1", 1000, 1100)}
	pax := []PassengerBags{
		{Passenger: "p1", Bags: []int64{100}},
		{Passenger: "p2", Bags: []int64{100, 321}}, // 第 3 件超过绝对上限 320
	}
	_, err := s.CheckIn("PNR1", pax, itin, 100)
	if err == nil || err.Code != ErrOverweight || err.BagSeq != 3 {
		t.Fatalf("期望 ErrOverweight 且 BagSeq=3，实际 %+v", err)
	}
	if _, ok := s.GetRecord("PNR1"); ok {
		t.Fatal("拒收不应生成行李记录")
	}
	// 时钟未推进：以更早时刻办理仍被接受（而非报时钟回退）。
	mustCheckIn(t, s, "PNR1", pax[:1], itin, 50)
}

// 行程变更后须先撤销原记录才能重新办理。
func TestCancelAndRecheck(t *testing.T) {
	s := newTestSystem(t)
	if err := s.AddBooking("PNR1", "p1"); err != nil {
		t.Fatal(err)
	}
	itin := []Segment{seg("A1", "B1", "CP", "PNR1", 1000, 1100)}
	pax := []PassengerBags{{Passenger: "p1", Bags: []int64{100}}}
	mustCheckIn(t, s, "PNR1", pax, itin, 0)

	_, err := s.CheckIn("PNR1", pax, itin, 1)
	expectErr(t, err, ErrRecordExists)

	if e := s.Cancel("PNR1", 2); e != nil {
		t.Fatalf("撤销失败: %v", e)
	}
	if e := s.Cancel("PNR1", 3); e == nil || e.Code != ErrNoRecord {
		t.Fatalf("重复撤销应报无记录，实际 %v", e)
	}
	// 撤销后以新行程重新办理。
	itin2 := []Segment{seg("A1", "A2", "CW", "PNR1", 1000, 1100)}
	mustCheckIn(t, s, "PNR1", pax, itin2, 4)
	// 撤销时刻回退。
	if e := s.Cancel("PNR1", 3); e == nil || e.Code != ErrClockRollback {
		t.Fatalf("撤销时钟回退应报错，实际 %v", e)
	}
}

// 并发办理等价于某个串行顺序：同一记录只有一次成功，不同记录全部成功。
func TestConcurrentCheckIn(t *testing.T) {
	s := newTestSystem(t)
	itin := []Segment{seg("A1", "B1", "CP", "PNR1", 1000, 1100)}
	pax := []PassengerBags{{Passenger: "p1", Bags: []int64{100}}}
	if err := s.AddBooking("PNR1", "p1"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		pnr := fmt.Sprintf("PNR-X%d", i)
		if err := s.AddBooking(pnr, "p1"); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	var okCount atomic.Int64
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.CheckIn("PNR1", pax, itin, 10); err == nil {
				okCount.Add(1)
			}
		}()
	}
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pnr := fmt.Sprintf("PNR-X%d", i)
			if _, err := s.CheckIn(pnr, pax, itin, 10); err != nil {
				t.Errorf("并发办理 %s 失败: %v", pnr, err)
			}
		}(i)
	}
	wg.Wait()
	if okCount.Load() != 1 {
		t.Fatalf("同一订座记录并发办理应恰有一次成功，实际 %d", okCount.Load())
	}
	if n := s.RecordCount(); n != 33 {
		t.Fatalf("应生成 33 条记录，实际 %d", n)
	}
}

// 相同输入重放得到相同结论。
func TestReplayDeterministic(t *testing.T) {
	build := func() *Record {
		s := newTestSystem(t)
		if err := s.AddBooking("PNR1", "p1", "p2"); err != nil {
			t.Fatal(err)
		}
		itin := []Segment{
			seg("A1", "X1", "CP", "PNR1", 1000, 1100),
			seg("X1", "B1", "CW", "PNR2", 1300, 1400),
		}
		pax := []PassengerBags{
			{Passenger: "p1", Tier: "GOLD", Bags: []int64{250, 100}},
			{Passenger: "p2", Bags: []int64{300}},
		}
		return mustCheckIn(t, s, "PNR1", pax, itin, 7)
	}
	r1, r2 := build(), build()
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("重放结果不一致:\n%+v\n%+v", r1, r2)
	}
}

// 热路径开销与历史记录数、机场总数无关：
// 办理过程的全表迭代计数恒为 0（仅 RecordCount 会增加它）。
func TestCostIndependentOfHistory(t *testing.T) {
	s := newTestSystem(t)
	itin := []Segment{seg("A1", "B1", "CP", "PNR1", 1000, 1100)}
	pax := []PassengerBags{{Passenger: "p1", Bags: []int64{100}}}
	for i := 0; i < 500; i++ {
		pnr := fmt.Sprintf("PNR-%d", i)
		if err := s.AddBooking(pnr, "p1"); err != nil {
			t.Fatal(err)
		}
		mustCheckIn(t, s, pnr, pax, itin, int64(i))
	}
	if ops := s.IterOps(); ops != 0 {
		t.Fatalf("办理路径不应出现全表迭代，实际 %d 次", ops)
	}
	if n := s.RecordCount(); n != 500 {
		t.Fatalf("记录数应为 500，实际 %d", n)
	}
	if ops := s.IterOps(); ops != 500 {
		t.Fatalf("RecordCount 应迭代 500 次，实际 %d", ops)
	}
}
