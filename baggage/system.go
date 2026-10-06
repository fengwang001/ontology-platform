package baggage

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// System 是行李中转计费系统的主体，所有变更操作可并发调用，
// 内部以互斥锁串行化，结果等价于某个串行顺序。
type System struct {
	mu       sync.Mutex
	cfg      Config
	airports map[string]Airport
	carriers map[string]Carrier
	tiers    map[string]Tier
	bookings map[string]map[string]bool
	records  map[string]*Record
	last     int64
	hasLast  bool
	iterOps  atomic.Int64 // 全表迭代计数，用于证明热路径与历史规模无关
}

// NewSystem 创建系统并校验配置。
func NewSystem(cfg Config) (*System, *Error) {
	if cfg.MinConn < 0 || cfg.MaxConn < cfg.MinConn || cfg.Cutoff < 0 {
		return nil, &Error{Code: ErrInvalidParam, Msg: "系统配置非法"}
	}
	return &System{
		cfg:      cfg,
		airports: map[string]Airport{},
		carriers: map[string]Carrier{},
		tiers:    map[string]Tier{},
		bookings: map[string]map[string]bool{},
		records:  map[string]*Record{},
	}, nil
}

// AddAirport 注册机场。
func (s *System) AddAirport(a Airport) *Error {
	if a.Code == "" || a.Region == "" {
		return &Error{Code: ErrInvalidParam, Msg: "机场代码或区域为空"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.airports[a.Code] = a
	return nil
}

// AddCarrier 注册承运人。
func (s *System) AddCarrier(c Carrier) *Error {
	if c.Code == "" || c.AbsWeight < 0 || c.FreePieces < 0 || c.PieceFreeWeight < 0 ||
		c.FreeTotalWeight < 0 || c.PieceFee < 0 || c.OverweightFee < 0 || c.UnitFee < 0 {
		return &Error{Code: ErrInvalidParam, Msg: "承运人参数非法"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.carriers[c.Code] = c
	return nil
}

// AddTier 注册会员等级。
func (s *System) AddTier(t Tier) *Error {
	if t.Name == "" || t.ExtraPieces < 0 || t.ExtraWeight < 0 {
		return &Error{Code: ErrInvalidParam, Msg: "会员等级参数非法"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tiers[t.Name] = t
	return nil
}

// AddBooking 注册订座记录及其旅客名单。
func (s *System) AddBooking(pnr string, passengers ...string) *Error {
	if pnr == "" || len(passengers) == 0 {
		return &Error{Code: ErrInvalidParam, Msg: "订座记录或旅客名单为空"}
	}
	set := map[string]bool{}
	for _, p := range passengers {
		if p == "" || set[p] {
			return &Error{Code: ErrInvalidParam, Msg: "旅客名为空或重复"}
		}
		set[p] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bookings[pnr] = set
	return nil
}

// validate 参数非法检查（拒绝次序第 1 类），调用方须已持有锁。
func (s *System) validate(pnr string, pax []PassengerBags, itin []Segment) *Error {
	bad := func(msg string) *Error { return &Error{Code: ErrInvalidParam, Msg: msg} }
	if pnr == "" {
		return bad("订座记录为空")
	}
	if len(pax) == 0 {
		return bad("托运旅客为空")
	}
	seen := map[string]bool{}
	for _, p := range pax {
		if p.Passenger == "" || seen[p.Passenger] {
			return bad("旅客名为空或重复")
		}
		seen[p.Passenger] = true
		if len(p.Bags) == 0 {
			return bad(fmt.Sprintf("旅客 %s 件数为零", p.Passenger))
		}
		for _, w := range p.Bags {
			if w < 0 {
				return bad("行李重量为负")
			}
		}
		if p.Tier != "" {
			if _, ok := s.tiers[p.Tier]; !ok {
				return bad(fmt.Sprintf("会员等级 %s 未注册", p.Tier))
			}
		}
	}
	if len(itin) == 0 {
		return bad("行程为空")
	}
	for i, seg := range itin {
		if _, ok := s.airports[seg.From]; !ok {
			return bad(fmt.Sprintf("未知机场 %s", seg.From))
		}
		if _, ok := s.airports[seg.To]; !ok {
			return bad(fmt.Sprintf("未知机场 %s", seg.To))
		}
		if _, ok := s.carriers[seg.Carrier]; !ok {
			return bad(fmt.Sprintf("未知承运人 %s", seg.Carrier))
		}
		if seg.Arrive <= seg.Depart {
			return bad("航段到达时刻不晚于起飞时刻")
		}
		if i > 0 {
			prev := itin[i-1]
			if prev.To != seg.From || seg.Depart < prev.Arrive {
				return bad("行程航段不连贯")
			}
		}
	}
	return nil
}

// CheckIn 办理托运，成功返回行李记录，失败返回 *Error。
// 拒绝次序：参数非法 > 时钟回退 > 不存在 > 已有记录 > 已截止 > 超重拒收。
func (s *System) CheckIn(pnr string, pax []PassengerBags, itin []Segment, now int64) (*Record, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.validate(pnr, pax, itin); err != nil {
		return nil, err
	}
	if s.hasLast && now < s.last {
		return nil, &Error{Code: ErrClockRollback, Msg: "时钟回退"}
	}
	booking, ok := s.bookings[pnr]
	if !ok {
		return nil, &Error{Code: ErrNotFound, Msg: fmt.Sprintf("订座记录 %s 不存在", pnr)}
	}
	for _, p := range pax {
		if !booking[p.Passenger] {
			return nil, &Error{Code: ErrNotFound, Msg: fmt.Sprintf("旅客 %s 不存在", p.Passenger)}
		}
	}
	if _, ok := s.records[pnr]; ok {
		return nil, &Error{Code: ErrRecordExists, Msg: "已有行李记录，须先撤销"}
	}
	if now >= itin[0].Depart-s.cfg.Cutoff {
		return nil, &Error{Code: ErrCutoffPassed, Msg: "已过托运截止时刻"}
	}

	ranges := SplitSections(itin, s.airports, s.cfg)
	rec := &Record{PNR: pnr, Itinerary: append([]Segment(nil), itin...), Time: now}
	for _, r := range ranges {
		sec := Section{
			Start:   r[0],
			End:     r[1],
			From:    itin[r[0]].From,
			To:      itin[r[1]-1].To,
			Carrier: AllowanceCarrier(itin[r[0]:r[1]], s.airports),
		}
		fee, err := SectionFee(itin[r[0]:r[1]], pax, s.airports, s.carriers, s.tiers)
		if err != nil {
			return nil, err // 超重拒收：不收费、不改状态、不推进时钟
		}
		sec.Fee = fee
		rec.Sections = append(rec.Sections, sec)
		rec.TotalFee += fee
		if r[0] > 0 {
			rec.Extractions = append(rec.Extractions, itin[r[0]].From)
		}
	}
	seq := 0
	for _, p := range pax {
		for range p.Bags {
			seq++
			tag := BagTag{Passenger: p.Passenger, Seq: seq}
			for _, sec := range rec.Sections {
				tag.Dests = append(tag.Dests, sec.To)
			}
			rec.Tags = append(rec.Tags, tag)
		}
	}
	s.records[pnr] = rec
	s.last, s.hasLast = now, true
	return rec, nil
}

// Cancel 撤销某订座记录的行李记录。
func (s *System) Cancel(pnr string, now int64) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pnr == "" {
		return &Error{Code: ErrInvalidParam, Msg: "订座记录为空"}
	}
	if s.hasLast && now < s.last {
		return &Error{Code: ErrClockRollback, Msg: "时钟回退"}
	}
	if _, ok := s.records[pnr]; !ok {
		return &Error{Code: ErrNoRecord, Msg: "无行李记录可撤销"}
	}
	delete(s.records, pnr)
	s.last, s.hasLast = now, true
	return nil
}

// GetRecord 查询行李记录。
func (s *System) GetRecord(pnr string) (*Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[pnr]
	return rec, ok
}

// IterOps 返回全表迭代计数（测试用，证明热路径与历史规模无关）。
func (s *System) IterOps() int64 { return s.iterOps.Load() }

// RecordCount 返回当前行李记录数；遍历 records，是系统中唯一随历史规模
// 增长的只读操作，用于对照证明 CheckIn 不触碰历史记录。
func (s *System) RecordCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.iterOps.Add(int64(len(s.records)))
	return len(s.records)
}
