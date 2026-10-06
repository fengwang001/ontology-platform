package railway

import (
	"math"
	"sort"
	"sync"
)

// Config 为系统级配置。
type Config struct {
	// AdvanceSeconds 为预售截止提前量：发站 i 截止时刻 = departures[i] - AdvanceSeconds。
	AdvanceSeconds int
	// StandingRatio 为无座上限比例，上限 = floor(座位总数 * StandingRatio)。
	StandingRatio float64
}

// TrainSpec 描述一趟列车的静态布局与初始票额。
type TrainSpec struct {
	ID          string
	Departures  []int // 各站发车时刻（非负、严格递增）
	Cars        int   // 车厢数（每节车厢座位数相同）
	SeatsPerCar int
	// Alloc[from][to] 为发站 from 对到站 to 的分配票额，from<to；其余位置忽略。
	Alloc  [][]int
	Shared int
}

type train struct {
	spec      TrainSpec
	quota     *quotaTable
	seats     *seatMap
	lastClock int
	tickets   map[int64]*Ticket
	passenger map[string][]*Ticket
}

// Service 是线程安全的票额与席位系统。所有变更操作互斥，结果等价于某个串行顺序。
type Service struct {
	mu     sync.Mutex
	cfg    Config
	trains map[string]*train
	nextID int64
	clock  int
}

func NewService(cfg Config) *Service {
	return &Service{cfg: cfg, trains: map[string]*train{}, nextID: 1}
}

func validConfig(cfg Config) bool {
	return cfg.AdvanceSeconds >= 0 && cfg.StandingRatio >= 0 && !math.IsNaN(cfg.StandingRatio)
}

func (s *Service) AddTrain(spec TrainSpec) error {
	if !validConfig(s.cfg) {
		return errReason(ReasonInvalidParam)
	}
	if spec.ID == "" || len(spec.Departures) < 2 || spec.Cars <= 0 || spec.SeatsPerCar <= 0 ||
		len(spec.Alloc) != len(spec.Departures) || spec.Shared < 0 {
		return errReason(ReasonInvalidParam)
	}
	n := len(spec.Departures)
	prev := -1
	for i, dep := range spec.Departures {
		if dep < 0 || dep <= prev {
			return errReason(ReasonInvalidParam)
		}
		prev = dep
		if len(spec.Alloc[i]) != n {
			return errReason(ReasonInvalidParam)
		}
		for j, v := range spec.Alloc[i] {
			if v < 0 || (i >= j && v != 0) {
				return errReason(ReasonInvalidParam)
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.trains[spec.ID]; exists {
		return errReason(ReasonInvalidParam)
	}
	seats := make([]Seat, 0, spec.Cars*spec.SeatsPerCar)
	for car := 1; car <= spec.Cars; car++ {
		for no := 1; no <= spec.SeatsPerCar; no++ {
			seats = append(seats, Seat{Car: car, No: no})
		}
	}
	sort.Slice(seats, func(i, j int) bool {
		if seats[i].Car != seats[j].Car {
			return seats[i].Car < seats[j].Car
		}
		return seats[i].No < seats[j].No
	})
	standCap := int(math.Floor(float64(len(seats)) * s.cfg.StandingRatio))
	allocCopy := make([][]int, n)
	for i := range spec.Alloc {
		allocCopy[i] = append([]int(nil), spec.Alloc[i]...)
	}
	tr := &train{
		spec:      spec,
		quota:     newQuotaTable(n, allocCopy, spec.Shared),
		seats:     newSeatMap(n, seats, standCap),
		tickets:   map[int64]*Ticket{},
		passenger: map[string][]*Ticket{},
	}
	s.trains[spec.ID] = tr
	return nil
}

// Buy 在 trainID 上购买一张车票。wantStanding 仅表达偏好：有空闲座位时一定给有座。
func (s *Service) Buy(now int, trainID string, d TicketDesc, wantStanding bool) (*BuyResult, error) {
	// 拒绝次序：参数非法 > 时钟回退 > 列车不存在 > …（购票无车票维度）。
	if now < 0 || trainID == "" || d.Passenger == "" || d.From < 0 || d.From >= d.To {
		return nil, errReason(ReasonInvalidParam)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return nil, errReason(ReasonClockRollback)
	}
	tr := s.trains[trainID]
	if tr == nil {
		return nil, errReason(ReasonTrainNotFound)
	}
	n := len(tr.spec.Departures)
	if d.To >= n {
		return nil, errReason(ReasonInvalidParam)
	}
	// 当前时刻必须严格早于发站发车时刻，恰等于视为已发车。
	if now >= tr.spec.Departures[d.From] {
		return nil, errReason(ReasonDeparted)
	}
	// 同一乘车人区段不得相交（相接允许）；无座票同样占位为一张有效票。
	if passengerIntersects(tr.passenger[d.Passenger], d.From, d.To) {
		return nil, errReason(ReasonPassengerOverlap)
	}
	// 在并入视图上判票额；只在最终成功时提交并入，保证被拒绝操作不改状态。
	newMerged, newShared := tr.quota.view(now, tr.spec.Departures, s.cfg.AdvanceSeconds)
	ok, useShared := decideQuota(tr.quota.alloc, d.From, d.To, newMerged, newShared)
	if !ok {
		return nil, errReason(ReasonQuotaExhausted)
	}
	idx := tr.seats.pickSeat(d.From, d.To)
	standing := false
	if idx < 0 {
		// 没有任何空闲座位才允许无座；wantStanding 不影响是否必须先给座位。
		if !tr.seats.standingFree(d.From, d.To) {
			return nil, errReason(ReasonNoSeat)
		}
		standing = true
	}
	// 提交：并入推进、票额扣减、座位/名额占用、时钟推进全部生效。
	tr.quota.commitBuy(newMerged, newShared, d.From, d.To, useShared)
	tk := &Ticket{
		ID:          s.nextID,
		TrainID:     trainID,
		From:        d.From,
		To:          d.To,
		Passenger:   d.Passenger,
		Standing:    standing,
		QuotaShared: useShared,
	}
	if standing {
		tr.seats.standingAdd(d.From, d.To)
	} else {
		tr.seats.occupy(idx, d.From, d.To)
		tk.Car = tr.seats.seats[idx].Car
		tk.No = tr.seats.seats[idx].No
	}
	s.nextID++
	s.clock = now
	tr.tickets[tk.ID] = tk
	tr.passenger[d.Passenger] = append(tr.passenger[d.Passenger], tk)
	return &BuyResult{Ticket: tk}, nil
}

// Refund 退还车票，座位/名额与票额按当初扣减来源退回。
func (s *Service) Refund(now int, ticketID int64) error {
	if now < 0 || ticketID <= 0 {
		return errReason(ReasonInvalidParam)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return errReason(ReasonClockRollback)
	}
	var tk *Ticket
	for _, tr := range s.trains {
		if t := tr.tickets[ticketID]; t != nil {
			tk = t
			break
		}
	}
	if tk == nil {
		return errReason(ReasonTicketNotFound)
	}
	if tk.Refunded {
		return errReason(ReasonTicketRefunded)
	}
	tr := s.trains[tk.TrainID]
	if now >= tr.spec.Departures[tk.From] {
		return errReason(ReasonDeparted)
	}
	// 退票也在当前时刻的并入视图上提交不可逆并入；之后票额按来源退回。
	newMerged, newShared := tr.quota.view(now, tr.spec.Departures, s.cfg.AdvanceSeconds)
	tr.quota.commitMerge(newMerged, newShared)
	if tk.Standing {
		tr.seats.standingRelease(tk.From, tk.To)
	} else {
		idx := seatIndex(tr.seats.seats, tk.Car, tk.No)
		tr.seats.release(idx, tk.From, tk.To)
	}
	tr.quota.refund(tk.From, tk.To, tk.QuotaShared)
	tk.Refunded = true
	tr.passenger[tk.Passenger] = removeTicket(tr.passenger[tk.Passenger], tk)
	s.clock = now
	return nil
}

// Remaining 查询某发站-到站票额视图，耗时与已售车票数无关。
func (s *Service) Remaining(now int, trainID string, from, to int) (QuotaView, error) {
	if now < 0 || trainID == "" || from < 0 || to <= from {
		return QuotaView{}, errReason(ReasonInvalidParam)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tr := s.trains[trainID]
	if tr == nil {
		return QuotaView{}, errReason(ReasonTrainNotFound)
	}
	n := len(tr.spec.Departures)
	if to >= n {
		return QuotaView{}, errReason(ReasonInvalidParam)
	}
	merged, shared := tr.quota.view(now, tr.spec.Departures, s.cfg.AdvanceSeconds)
	v := QuotaView{Shared: shared, Merged: from < merged}
	if !v.Merged {
		v.Allocation = tr.quota.alloc[from][to]
	}
	return v, nil
}

// 区段相交：[a,b) 与 [c,d) 有公共边即相交；相接（b==c 或 d==a）不相交。
func passengerIntersects(held []*Ticket, from, to int) bool {
	for _, t := range held {
		if t.Refunded {
			continue
		}
		if from < t.To && t.From < to {
			return true
		}
	}
	return false
}

func seatIndex(seats []Seat, car, no int) int {
	for i := range seats {
		if seats[i].Car == car && seats[i].No == no {
			return i
		}
	}
	return -1
}

func removeTicket(list []*Ticket, tk *Ticket) []*Ticket {
	out := list[:0]
	for _, t := range list {
		if t != tk {
			out = append(out, t)
		}
	}
	return out
}
