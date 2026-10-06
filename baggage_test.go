package baggage

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		MinConnect:      60,
		MaxConnect:      240,
		CutoffLead:      45,
		TierExtraPiece:  map[Tier]int{"GOLD": 1},
		TierExtraWeight: map[Tier]Weight{"GOLD": 200},
	}
}

func newWorld(t *testing.T) (*Service, *Registry) {
	t.Helper()
	reg := NewRegistry()
	airports := []Airport{
		{Code: "PEK", Region: "CN"},
		{Code: "PVG", Region: "CN"},
		{Code: "FRA", Region: "EU", Customs: true},
		{Code: "JFK", Region: "US"},
		{Code: "LAX", Region: "US"},
	}
	for _, a := range airports {
		if err := reg.AddAirport(a); err != nil {
			t.Fatalf("add airport %s: %v", a.Code, err)
		}
	}
	// CA 计件：免费 1 件、单件免费 200、绝对 500；超件 1000、超重 300。
	mustCarrier(t, reg, Carrier{
		ID: "CA", Mode: ModePiece, FreePieces: 1, PieceFreeWeight: 200,
		AbsWeight: 500, ExtraPieceRate: 1000, OverweightRate: 300,
	})
	// CW 计重：免费总重 300、绝对 500；每百克 10。
	mustCarrier(t, reg, Carrier{
		ID: "CW", Mode: ModeWeight, FreeWeight: 300,
		AbsWeight: 500, PerUnitRate: 10,
	})
	return NewService(testConfig(), reg), reg
}

func mustCarrier(t *testing.T, reg *Registry, c Carrier) {
	t.Helper()
	if err := reg.AddCarrier(c); err != nil {
		t.Fatalf("add carrier %s: %v", c.ID, err)
	}
}

func mustPNR(t *testing.T, reg *Registry, name string, ps ...Passenger) {
	t.Helper()
	if err := reg.RegisterPNR(name, ps); err != nil {
		t.Fatalf("register pnr %s: %v", name, err)
	}
}

func segm(carrier, pnr, from, to string, dep, arr int64) Segment {
	return Segment{CarrierID: carrier, PNR: pnr, From: from, To: to,
		DepartsAt: Minute(dep), ArrivesAt: Minute(arr)}
}

func bags(pnr, pax string, ws ...int) PartyBags {
	out := PartyBags{PNR: pnr, PassengerID: pax}
	for _, w := range ws {
		out.Weights = append(out.Weights, Weight(w))
	}
	return out
}

func overIndex(t *testing.T, err error) int {
	t.Helper()
	var ow *OverweightRejectedError
	if !errors.As(err, &ow) {
		t.Fatalf("want OverweightRejectedError, got %v", err)
	}
	return ow.BagIndex
}

func joinClaims(ss []string) string { return strings.Join(ss, ",") }

var _ = sync.Mutex{}

// 第一个跨区域航段不是第一段时，额度归属取该航段承运人（此处 CW 计重）。
func TestApplicableCarrier_FirstCrossRegionNotFirstSegment(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "p"})

	itin := []Segment{
		segm("CA", "P1", "PEK", "PVG", 1000, 1200),
		segm("CW", "P1", "PVG", "JFK", 1260, 3000),
	}
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "p", 350)}, At: 900})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if rec.Consignments[0].CarrierID != "CW" || rec.Consignments[0].Mode != ModeWeight {
		t.Fatalf("applicable = %s mode %d, want CW/weight",
			rec.Consignments[0].CarrierID, rec.Consignments[0].Mode)
	}
	// 350-300=50 单位 * 10 = 500。
	if rec.TotalFee != 500 {
		t.Fatalf("fee = %d, want 500", rec.TotalFee)
	}
}

// 无跨区域航段时取第一个航段承运人。
func TestApplicableCarrier_AllSameRegion(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "p"})
	itin := []Segment{
		segm("CA", "P1", "PEK", "PVG", 1000, 1200),
		segm("CW", "P1", "PVG", "PEK", 1300, 1500),
	}
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "p", 200)}, At: 900})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if rec.Consignments[0].CarrierID != "CA" {
		t.Fatalf("applicable = %s, want CA", rec.Consignments[0].CarrierID)
	}
}

// 中转停留恰等于最短/最长：闭区间，可直挂。
func TestLayover_InclusiveBounds(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "p"})
	itin := []Segment{
		segm("CA", "P1", "JFK", "LAX", 1000, 1100),
		segm("CA", "P1", "LAX", "JFK", 1160, 2000), // 恰好最短 60
	}
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "p", 100)}, At: 900})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if got := rec.Bags[0].ClaimPoints; joinClaims(got) != "JFK,JFK" {
		t.Fatalf("min-bound claims = %v, want direct JFK->JFK", got)
	}
	if err := svc.Cancel(rec.ID, 950); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	itin[1].DepartsAt = 1340 // 恰好最长 240
	rec, err = svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "p", 100)}, At: 954})
	if err != nil {
		t.Fatalf("checkin max bound: %v", err)
	}
	if len(rec.Consignments) != 1 {
		t.Fatalf("max-bound consignments = %d, want 1", len(rec.Consignments))
	}
}

// 停留出界即提取，重拖后两段各自确定额度（CA 与 CW）。
func TestLayover_OutOfRangeBreaksAndReapply(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "p"})
	itin := []Segment{
		segm("CA", "P1", "PEK", "PVG", 1000, 1100),
		segm("CW", "P1", "PVG", "JFK", 1401, 3000), // 停留 301 > 240
	}
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "p", 100)}, At: 900})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if len(rec.Consignments) != 2 {
		t.Fatalf("want 2 consignments, got %d", len(rec.Consignments))
	}
	if c0, c1 := rec.Consignments[0].CarrierID, rec.Consignments[1].CarrierID; c0 != "CA" || c1 != "CW" {
		t.Fatalf("carriers = %s/%s, want CA/CW", c0, c1)
	}
	if got := rec.Bags[0].ClaimPoints; joinClaims(got) != "PEK,PVG,JFK" {
		t.Fatalf("claims = %v, want PEK,PVG,JFK", got)
	}
}

// 清关机场到达即提取；同一订座记录内多人都在此提取。
func TestCustomsBreak_SamePNR(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "a"}, Passenger{ID: "b"})
	itin := []Segment{
		segm("CA", "P1", "JFK", "FRA", 1000, 2000),
		segm("CA", "P1", "FRA", "PEK", 2100, 4000),
	}
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "a", 100), bags("P1", "b", 100)}, At: 900})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	for _, bag := range rec.Bags {
		if joinClaims(bag.ClaimPoints) != "JFK,FRA,PEK" {
			t.Fatalf("customs claims = %v, want JFK,FRA,PEK", bag.ClaimPoints)
		}
	}
	if len(rec.Consignments) != 2 {
		t.Fatalf("consignments = %d, want 2", len(rec.Consignments))
	}
}

// 跨订座记录：P1 止于 FRA，P2 始于 FRA，各自提取点不含对方段。
func TestPNRBreak_DifferentRecords(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "a"})
	mustPNR(t, reg, "P2", Passenger{ID: "c"})
	itin := []Segment{
		segm("CA", "P1", "JFK", "FRA", 1000, 2000),
		segm("CA", "P2", "FRA", "PEK", 2100, 4000),
	}
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "a", 100), bags("P2", "c", 100)}, At: 900})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	byParty := map[int][]string{}
	for _, bag := range rec.Bags {
		byParty[bag.Party] = bag.ClaimPoints
	}
	if joinClaims(byParty[0]) != "JFK,FRA" {
		t.Fatalf("P1 claims = %v, want JFK,FRA", byParty[0])
	}
	if joinClaims(byParty[1]) != "FRA,PEK" {
		t.Fatalf("P2 claims = %v, want FRA,PEK", byParty[1])
	}
}

// 清关重拖：第一段 CA 计件、第二段 CW 计重，费用分别计算后求和；
// 且每段费用等于单独对该段办理。
func TestRecheck_SwitchesMode_FeeAndIsolation(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "p"})
	itin := []Segment{
		segm("CA", "P1", "JFK", "FRA", 1000, 2000),
		segm("CW", "P1", "FRA", "PEK", 2100, 4000),
	}
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "p", 350)}, At: 900})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if rec.Consignments[0].Mode != ModePiece || rec.Consignments[1].Mode != ModeWeight {
		t.Fatalf("modes = %d/%d, want piece/weight",
			rec.Consignments[0].Mode, rec.Consignments[1].Mode)
	}
	// 段0：免费 1 件内，350 > 200 收一次超重 300；段1：计重 (350-300)*10=500。
	if rec.Consignments[0].Fee != 300 || rec.Consignments[1].Fee != 500 || rec.TotalFee != 800 {
		t.Fatalf("fees = %d/%d total %d, want 300/500/800",
			rec.Consignments[0].Fee, rec.Consignments[1].Fee, rec.TotalFee)
	}

	// 单独办理各段，费用应一致。
	if err := svc.Cancel(rec.ID, 950); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	r0, err := svc.CheckIn(CheckInRequest{Itinerary: itin[:1],
		Parties: []PartyBags{bags("P1", "p", 350)}, At: 950})
	if err != nil || r0.TotalFee != rec.Consignments[0].Fee {
		t.Fatalf("leg0 isolated fee = %v/%d, want %d", err, r0.TotalFee, rec.Consignments[0].Fee)
	}
	if err := svc.Cancel(r0.ID, 951); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	r1, err := svc.CheckIn(CheckInRequest{Itinerary: itin[1:],
		Parties: []PartyBags{bags("P1", "p", 350)}, At: 952})
	if err != nil || r1.TotalFee != rec.Consignments[1].Fee {
		t.Fatalf("leg1 isolated fee = %v/%d, want %d", err, r1.TotalFee, rec.Consignments[1].Fee)
	}
}

// 计件制：同一件既超件又超重，两项费用叠加。
func TestPiece_BothExtraAndOverweightOnSameBag(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "p"})
	itin := []Segment{segm("CA", "P1", "PEK", "PVG", 1000, 1200)}
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		// 第 1 件 100 免费；第 2 件 250：超件 1000 + 超重（>200）300。
		Parties: []PartyBags{bags("P1", "p", 100, 250)}, At: 900})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if rec.TotalFee != 1300 {
		t.Fatalf("fee = %d, want 1300", rec.TotalFee)
	}
}

// 计件制下免费件数不可合并：每人各 1 件免费，两人各交 2 件都收超件。
func TestPiece_NoPoolingAcrossPassengers(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "a"}, Passenger{ID: "b"})
	itin := []Segment{segm("CA", "P1", "PEK", "PVG", 1000, 1200)}
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "a", 100, 100), bags("P1", "b", 100, 100)}, At: 900})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if rec.TotalFee != 2000 { // 各超 1 件 * 1000
		t.Fatalf("fee = %d, want 2000", rec.TotalFee)
	}
}

// 计重制合并共享：A 用 350（超本人 300 的 50），B 只用 100（余 200），
// 共享后合计 450 < 600，免费。
func TestWeight_PoolingShared(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "a"}, Passenger{ID: "b"})
	itin := []Segment{
		segm("CW", "P1", "PEK", "PVG", 1000, 1200),
		segm("CW", "P1", "PVG", "JFK", 1300, 3000), // 适用 CW：首个跨区域段
	}
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "a", 350), bags("P1", "b", 100)}, At: 900})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if rec.TotalFee != 0 {
		t.Fatalf("pooled fee = %d, want 0", rec.TotalFee)
	}
}

// 计重共享上限：A=500, B=100 -> 合计 600 = 600 免费；A=500,B=110 -> 10*10=100。
func TestWeight_PoolingBoundary(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "a"}, Passenger{ID: "b"})
	itin := []Segment{
		segm("CW", "P1", "PEK", "PVG", 1000, 1200),
		segm("CW", "P1", "PVG", "JFK", 1300, 3000),
	}
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "a", 500), bags("P1", "b", 100)}, At: 900})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if rec.TotalFee != 0 {
		t.Fatalf("boundary fee = %d, want 0", rec.TotalFee)
	}
	if err := svc.Cancel(rec.ID, 950); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	rec, err = svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "a", 500), bags("P1", "b", 110)}, At: 954})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if rec.TotalFee != 100 {
		t.Fatalf("over fee = %d, want 100", rec.TotalFee)
	}
}

// 会员额外额度不转移：金卡 A 有 +200，只抵扣本人；
// A 用 400（基础300+额外200 内），B 用 400（超基础 100），A 的额外余额不救 B。
func TestWeight_MemberExtraNotTransferable(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "a", Tier: "GOLD"}, Passenger{ID: "b"})
	itin := []Segment{
		segm("CW", "P1", "PEK", "PVG", 1000, 1200),
		segm("CW", "P1", "PVG", "JFK", 1300, 3000),
	}
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "a", 400), bags("P1", "b", 400)}, At: 900})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	// A: 基础用 200（400-额外200），余 100 进共享；B: 超 100，被共享池 100 抵平。
	if rec.TotalFee != 0 {
		t.Fatalf("fee = %d, want 0（A 未用基础额度可共享给 B）", rec.TotalFee)
	}

	// A=500：基础用 300 无余；B=400 超 100 无法被 A 的额外余额(0)抵消。
	if err := svc.Cancel(rec.ID, 950); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	rec, err = svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "a", 500), bags("P1", "b", 400)}, At: 954})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if rec.TotalFee != 1000 {
		t.Fatalf("fee = %d, want 1000（会员额外不转移）", rec.TotalFee)
	}
}

// 计件制会员加件数只用于本人：金卡免费 2 件，同行普通旅客仍只有 1 件。
func TestPiece_MemberExtraOnlySelf(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "a", Tier: "GOLD"}, Passenger{ID: "b"})
	itin := []Segment{segm("CA", "P1", "PEK", "PVG", 1000, 1200)}
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "a", 100, 100), bags("P1", "b", 100, 100)}, At: 900})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	// A 免费 2 件；B 超 1 件 1000。
	if rec.TotalFee != 1000 {
		t.Fatalf("fee = %d, want 1000", rec.TotalFee)
	}
}

// 办理时刻恰等于截止：拒绝；严格早于才接受。
func TestCutoff_EqualRejected(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "p"})
	itin := []Segment{segm("CA", "P1", "PEK", "PVG", 1000, 1200)}
	if _, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "p", 100)}, At: 955}); err != ErrCutoff {
		t.Fatalf("at=cutoff err = %v, want ErrCutoff", err)
	}
	// 被拒操作不推进时钟：更早（900）仍可办。
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "p", 100)}, At: 900})
	if err != nil {
		t.Fatalf("at=900 err = %v, want success", err)
	}
	// 已有记录：再次办理报已有记录而非截止。
	if _, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "p", 100)}, At: 900}); err != ErrExistingRecord {
		t.Fatalf("duplicate err = %v, want ErrExistingRecord", err)
	}
	_ = rec
}

// 超重拒收：第一件超限行李的序号（全局输入序号）；拒收不收费、不改状态、不推进时钟。
func TestOverweight_RejectionIndexAndNoSideEffect(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "p"})
	itin := []Segment{segm("CA", "P1", "PEK", "PVG", 1000, 1200)}
	_, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "p", 100, 501)}, At: 900})
	if idx := overIndex(t, err); idx != 2 {
		t.Fatalf("over index = %d, want 2", idx)
	}
	// 时钟未推进：合法行李在 900 可办（若推进过则 900 算回退）。
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "p", 100)}, At: 900})
	if err != nil {
		t.Fatalf("after reject checkin: %v", err)
	}
	if rec.TotalFee != 0 {
		t.Fatalf("fee = %d, want 0", rec.TotalFee)
	}
}

// 时钟回退：被接受操作之后，更早时刻的操作被拒。
func TestClock_RewindRejected(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "p"})
	itin := []Segment{segm("CA", "P1", "PEK", "PVG", 2000, 2200)}
	rec, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "p", 100)}, At: 1000})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if err := svc.Cancel(rec.ID, 500); err != ErrClockRewind {
		t.Fatalf("cancel rewind err = %v, want ErrClockRewind", err)
	}
	if err := svc.Cancel(rec.ID, 1000); err != nil { // 相等允许
		t.Fatalf("cancel equal: %v", err)
	}
}

// 拒绝次序：只报相邻类别中更靠前的一类。
func TestRejectionOrder_AdjacentPairs(t *testing.T) {
	// 对 1：参数非法 > 时钟回退（回退时刻 + 断航段 -> ErrInvalid）。
	{
		svc, reg := newWorld(t)
		mustPNR(t, reg, "P1", Passenger{ID: "p"})
		good := []Segment{segm("CA", "P1", "PEK", "PVG", 2000, 2200)}
		rec, err := svc.CheckIn(CheckInRequest{Itinerary: good,
			Parties: []PartyBags{bags("P1", "p", 100)}, At: 1000})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		bad := []Segment{segm("CA", "P1", "PEK", "JFK", 2000, 2200)} // 与后续不连贯
		bad = append(bad, segm("CA", "P1", "PVG", "PEK", 2300, 2400))
		_, err = svc.CheckIn(CheckInRequest{Itinerary: bad,
			Parties: []PartyBags{bags("P1", "p", 100)}, At: 500})
		if err != ErrInvalid {
			t.Fatalf("invalid>rewind: got %v, want ErrInvalid", err)
		}
		if err := svc.Cancel(rec.ID, 1000); err != nil {
			t.Fatalf("cleanup: %v", err)
		}
	}
	// 对 2：时钟回退 > 不存在（回退时刻 + 未知旅客 -> ErrClockRewind）。
	{
		svc, reg := newWorld(t)
		mustPNR(t, reg, "P1", Passenger{ID: "p"})
		good := []Segment{segm("CA", "P1", "PEK", "PVG", 2000, 2200)}
		if _, err := svc.CheckIn(CheckInRequest{Itinerary: good,
			Parties: []PartyBags{bags("P1", "p", 100)}, At: 1000}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		_, err := svc.CheckIn(CheckInRequest{Itinerary: good,
			Parties: []PartyBags{bags("P1", "ghost", 100)}, At: 500})
		if err != ErrClockRewind {
			t.Fatalf("rewind>notfound: got %v, want ErrClockRewind", err)
		}
	}
	// 对 3：不存在 > 已有记录（未知旅客 + 让某旅客已有记录）。
	{
		svc, reg := newWorld(t)
		mustPNR(t, reg, "P1", Passenger{ID: "p"})
		good := []Segment{segm("CA", "P1", "PEK", "PVG", 2000, 2200)}
		if _, err := svc.CheckIn(CheckInRequest{Itinerary: good,
			Parties: []PartyBags{bags("P1", "p", 100)}, At: 1000}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		_, err := svc.CheckIn(CheckInRequest{Itinerary: good,
			Parties: []PartyBags{bags("ZZZ", "p", 100)}, At: 1000})
		if err != ErrNotFound {
			t.Fatalf("notfound>existing: got %v, want ErrNotFound", err)
		}
	}
	// 对 4：已有记录 > 已截止（p 有记录且时刻已截止 -> ErrExistingRecord）。
	{
		svc, reg := newWorld(t)
		mustPNR(t, reg, "P1", Passenger{ID: "p"})
		good := []Segment{segm("CA", "P1", "PEK", "PVG", 2000, 2200)}
		if _, err := svc.CheckIn(CheckInRequest{Itinerary: good,
			Parties: []PartyBags{bags("P1", "p", 100)}, At: 1000}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		_, err := svc.CheckIn(CheckInRequest{Itinerary: good,
			Parties: []PartyBags{bags("P1", "p", 100)}, At: 1999})
		if err != ErrExistingRecord {
			t.Fatalf("existing>cutoff: got %v, want ErrExistingRecord", err)
		}
	}
	// 对 5：已截止 > 超重拒收（时刻已截止且含超限件 -> ErrCutoff）。
	{
		svc, reg := newWorld(t)
		mustPNR(t, reg, "P1", Passenger{ID: "q"})
		good := []Segment{segm("CA", "P1", "PEK", "PVG", 2000, 2200)}
		_, err := svc.CheckIn(CheckInRequest{Itinerary: good,
			Parties: []PartyBags{bags("P1", "q", 9999)}, At: 1999})
		if err != ErrCutoff {
			t.Fatalf("cutoff>overweight: got %v, want ErrCutoff", err)
		}
	}
}

// 并发：多个办理竞争同一旅客，恰一个成功，其余报已有记录；无崩溃/数据竞争。
func TestConcurrent_Serializable(t *testing.T) {
	svc, reg := newWorld(t)
	mustPNR(t, reg, "P1", Passenger{ID: "p"})
	itin := []Segment{segm("CA", "P1", "PEK", "PVG", 5000, 5200)}
	const n = 16
	var wg sync.WaitGroup
	results := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			req := CheckInRequest{Itinerary: itin,
				Parties: []PartyBags{bags("P1", "p", 100)}, At: 1000}
			rec, err := svc.CheckIn(req)
			if err == nil {
				// 抢到后立刻撤销，验证并发取消也能串行推进。
				err = svc.Cancel(rec.ID, 1000)
			}
			results[i] = err
		}()
	}
	wg.Wait()
	ok, dup := 0, 0
	for _, e := range results {
		switch e {
		case nil:
			ok++
		case ErrExistingRecord:
			dup++
		default:
			t.Fatalf("unexpected err: %v", e)
		}
	}
	if ok+dup != n || ok == 0 {
		t.Fatalf("ok=%d dup=%d, 二者之和应为 %d 且至少 1 次成功", ok, dup, n)
	}
	// 全部竞争结束后系统应空闲，可再次办理。
	if _, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
		Parties: []PartyBags{bags("P1", "p", 100)}, At: 1000}); err != nil {
		t.Fatalf("final checkin: %v", err)
	}
}

// 确定性重放：相同输入序列在全新系统上得到相同额度、提取点与费用。
func TestDeterministicReplay(t *testing.T) {
	play := func(svc *Service) (*Record, error) {
		itin := []Segment{
			segm("CA", "P1", "JFK", "FRA", 1000, 2000),
			segm("CW", "P1", "FRA", "PEK", 2100, 4000),
		}
		return svc.CheckIn(CheckInRequest{Itinerary: itin,
			Parties: []PartyBags{bags("P1", "p", 100, 250)}, At: 900})
	}
	s1, reg1 := newWorld(t)
	mustPNR(t, reg1, "P1", Passenger{ID: "p"})
	r1, err := play(s1)
	if err != nil {
		t.Fatalf("play1: %v", err)
	}
	s2, reg2 := newWorld(t)
	mustPNR(t, reg2, "P1", Passenger{ID: "p"})
	r2, err := play(s2)
	if err != nil {
		t.Fatalf("play2: %v", err)
	}
	if r1.TotalFee != r2.TotalFee || r1.ID != r2.ID ||
		len(r1.Consignments) != len(r2.Consignments) {
		t.Fatalf("replay mismatch: %+v vs %+v", r1, r2)
	}
	for i := range r1.Consignments {
		a, b := r1.Consignments[i], r2.Consignments[i]
		if a.CarrierID != b.CarrierID || a.Fee != b.Fee {
			t.Fatalf("consignment %d mismatch", i)
		}
	}
	for i := range r1.Bags {
		if joinClaims(r1.Bags[i].ClaimPoints) != joinClaims(r2.Bags[i].ClaimPoints) {
			t.Fatalf("bag %d claims mismatch: %v vs %v", i, r1.Bags[i].ClaimPoints, r2.Bags[i].ClaimPoints)
		}
	}
}

// 性能证据：在 N 个无关机场与 M 名其他旅客的有效记录下办理，
// ns/op 不随 N、M 增长（查表均 O(1)）。
func BenchmarkCheckInScaling(b *testing.B) {
	for _, m := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("history=%d", m), func(b *testing.B) {
			reg := NewRegistry()
			_ = reg.AddAirport(Airport{Code: "AX0", Region: "CN"})
			_ = reg.AddAirport(Airport{Code: "AX1", Region: "CN"})
			for i := 0; i < 10000; i++ {
				_ = reg.AddAirport(Airport{Code: fmt.Sprintf("X%05d", i), Region: "EU"})
			}
			_ = reg.AddCarrier(Carrier{ID: "CA", Mode: ModePiece, FreePieces: 1,
				PieceFreeWeight: 200, AbsWeight: 500, ExtraPieceRate: 1000, OverweightRate: 300})
			svc := NewService(testConfig(), reg)
			// 预填 M 名其他旅客的有效记录。
			for i := 0; i < m; i++ {
				pnr := fmt.Sprintf("H%05d", i)
				pax := "h" + pnr
				if err := reg.RegisterPNR(pnr, []Passenger{{ID: pax}}); err != nil {
					b.Fatal(err)
				}
				itin := []Segment{segm("CA", pnr, "AX0", "AX1", int64(2_000_000+i*1000),
					int64(2_000_200+i*1000))}
				if _, err := svc.CheckIn(CheckInRequest{Itinerary: itin,
					Parties: []PartyBags{bags(pnr, pax, 100)}, At: Minute(1_000_000 + i)}); err != nil {
					b.Fatal(err)
				}
			}
			if err := reg.RegisterPNR("P1", []Passenger{{ID: "p"}}); err != nil {
				b.Fatal(err)
			}
			itin := []Segment{segm("CA", "P1", "AX0", "AX1", 9_000_000, 9_000_200)}
			req := CheckInRequest{Itinerary: itin,
				Parties: []PartyBags{bags("P1", "p", 100)}}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req.At = Minute(8_000_000 + i*2)
				rec, err := svc.CheckIn(req)
				if err != nil {
					b.Fatal(err)
				}
				if err := svc.Cancel(rec.ID, req.At+1); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
