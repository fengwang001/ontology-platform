package baggage

import (
	"fmt"
	"sync"
)

// Service 行李系统：一把互斥锁串行化全部变更操作，保证并发调用
// 等价于某个串行顺序；确定性编号保证相同输入重放结果一致。
type Service struct {
	cfg Config
	reg *Registry
	mu  sync.Mutex

	lastAccepted Minute
	active       map[string]*Record // 旅客 -> 有效记录
	byID         map[string]*Record
	nextSeq      int
}

// NewService 创建系统。
func NewService(cfg Config, reg *Registry) *Service {
	return &Service{
		cfg:          cfg,
		reg:          reg,
		active:       make(map[string]*Record),
		byID:         make(map[string]*Record),
		lastAccepted: -1,
	}
}

// CheckIn 办理托运。拒绝次序（只报最靠前一类）：
// 参数非法 > 时钟回退 > 订座/旅客不存在 > 已有记录 > 已截止 > 超重拒收。
func (s *Service) CheckIn(req CheckInRequest) (*Record, error) {
	if err := s.validateStatic(req); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if req.At < s.lastAccepted {
		return nil, ErrClockRewind
	}

	passengerOrder, tierOf, err := s.resolveParties(req)
	if err != nil {
		return nil, err
	}

	for _, id := range passengerOrder {
		if _, ok := s.active[id]; ok {
			return nil, ErrExistingRecord
		}
	}

	cutoff := req.Itinerary[0].DepartsAt - Minute(s.cfg.CutoffLead)
	if req.At >= cutoff { // 恰等于视为已截止
		return nil, ErrCutoff
	}

	bps := findBreakpoints(req.Itinerary, s.cfg, s.reg)
	legs := splitConsignments(req.Itinerary, bps)

	// 各段托运只挂与该段 PNR 同记录的旅客行李，分别确定适用承运人、分别计费。
	legBags := make([][]bagUnit, len(legs))
	global := 0
	for _, party := range req.Parties {
		for _, w := range party.Weights {
			for li, leg := range legs {
				if isPartyOnLeg(leg, party) {
					legBags[li] = append(legBags[li], bagUnit{
						globalIndex: global + 1,
						passengerID: party.PassengerID,
						tier:        tierOf[party.PassengerID],
						weight:      w,
					})
				}
			}
			global++
		}
	}

	infos := make([]ConsignmentInfo, 0, len(legs))
	var totalFee Money
	for li, leg := range legs {
		carrierID := applicableCarrierID(leg.segments, s.reg)
		carrier, _ := s.reg.carrier(carrierID)
		// 绝对上限：按全局序号顺序报第一件超限行李。
		for _, bag := range legBags[li] {
			if bag.weight > carrier.AbsWeight {
				return nil, &OverweightRejectedError{BagIndex: bag.globalIndex}
			}
		}
		fee, mode, _ := priceConsignment(leg.segments, legBags[li], s.cfg, s.reg)
		infos = append(infos, ConsignmentInfo{
			Index:     li,
			From:      leg.segments[0].From,
			To:        leg.segments[len(leg.segments)-1].To,
			PNR:       leg.pnr,
			CarrierID: carrierID,
			Mode:      mode,
			Fee:       fee,
		})
		totalFee += fee
	}

	claimsByPNR := buildClaimPoints(req.Itinerary, bps)
	bags := make([]BagInfo, 0, global)
	global = 0
	for pi, party := range req.Parties {
		claims := claimsByPNR[party.PNR]
		for _, w := range party.Weights {
			bags = append(bags, BagInfo{
				Index:        global + 1,
				Weight:       w,
				Party:        pi,
				FinalAirport: claims[len(claims)-1],
				ClaimPoints:  append([]string(nil), claims...),
			})
			global++
		}
	}

	s.nextSeq++
	rec := &Record{
		ID:           fmt.Sprintf("R%06d", s.nextSeq),
		Itinerary:    append([]Segment(nil), req.Itinerary...),
		Consignments: infos,
		Bags:         bags,
		TotalFee:     totalFee,
		CheckedInAt:  req.At,
	}
	for _, id := range passengerOrder {
		s.active[id] = rec
	}
	s.byID[rec.ID] = rec
	s.lastAccepted = req.At
	return rec, nil
}

// Cancel 撤销行李记录（行程变更后重新办理前必须先撤销）。
func (s *Service) Cancel(recordID string, at Minute) error {
	if recordID == "" {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if at < s.lastAccepted {
		return ErrClockRewind
	}
	rec, ok := s.byID[recordID]
	if !ok {
		return ErrNotFound
	}
	for id, cur := range s.active {
		if cur == rec {
			delete(s.active, id)
		}
	}
	delete(s.byID, recordID)
	s.lastAccepted = at
	return nil
}

// Record 按 ID 查询记录（只读，不推进时钟）。
func (s *Service) Record(recordID string) (*Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byID[recordID]
	return rec, ok
}

// validateStatic 处理拒绝次序中的“参数非法”一类（早于时钟回退）：
// 航段非空连贯、时刻单调、机场/承运人已登记、交运方件数非零重量非负。
func (s *Service) validateStatic(req CheckInRequest) error {
	if len(req.Itinerary) == 0 || len(req.Parties) == 0 {
		return ErrInvalid
	}
	for i, seg := range req.Itinerary {
		if seg.CarrierID == "" || seg.PNR == "" || seg.From == "" || seg.To == "" {
			return ErrInvalid
		}
		if _, ok := s.reg.airport(seg.From); !ok {
			return ErrInvalid
		}
		if _, ok := s.reg.airport(seg.To); !ok {
			return ErrInvalid
		}
		if _, ok := s.reg.carrier(seg.CarrierID); !ok {
			return ErrInvalid
		}
		if seg.DepartsAt >= seg.ArrivesAt {
			return ErrInvalid
		}
		if i > 0 {
			if seg.From != req.Itinerary[i-1].To {
				return ErrInvalid
			}
			if seg.DepartsAt < req.Itinerary[i-1].ArrivesAt {
				return ErrInvalid
			}
		}
	}

	seenParty := make(map[string]bool)
	for _, party := range req.Parties {
		if party.PNR == "" || party.PassengerID == "" || len(party.Weights) == 0 {
			return ErrInvalid
		}
		key := party.PNR + "\x00" + party.PassengerID
		if seenParty[key] {
			return ErrInvalid
		}
		seenParty[key] = true
		for _, w := range party.Weights {
			if w < 0 {
				return ErrInvalid
			}
		}
	}
	return nil
}

// resolveParties 处理“订座记录或旅客不存在”一类（晚于时钟回退）。
func (s *Service) resolveParties(req CheckInRequest) ([]string, map[string]Tier, error) {
	order := make([]string, 0, len(req.Parties))
	tierOf := make(map[string]Tier)
	for _, party := range req.Parties {
		members, ok := s.reg.pnrPassengers(party.PNR)
		if !ok {
			return nil, nil, ErrNotFound
		}
		tier, ok := members[party.PassengerID]
		if !ok {
			return nil, nil, ErrNotFound
		}
		homePNR, _ := s.reg.passengerPNR(party.PassengerID)
		if homePNR != party.PNR || !pnrUsed(req.Itinerary, party.PNR) {
			return nil, nil, ErrNotFound
		}
		order = append(order, party.PassengerID)
		tierOf[party.PassengerID] = tier
	}
	return order, tierOf, nil
}

func pnrUsed(segs []Segment, pnr string) bool {
	for i := range segs {
		if segs[i].PNR == pnr {
			return true
		}
	}
	return false
}

// isPartyOnLeg 旅客属于覆盖该段托运的订座记录。
// 同 PNR 在行程中可能多次出现（提取后用同记录重新托运），全部覆盖。
func isPartyOnLeg(leg consignment, party PartyBags) bool {
	return leg.pnr == party.PNR
}
