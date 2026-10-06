package surge

import (
	"math"
	"sync"
)

// 订单生命周期阶段。
const (
	stagePending    byte = 'P' // 已创建待派
	stageDispatched byte = 'D' // 已派出
	stageCompleted  byte = 'C' // 已完成
	stageCancelled  byte = 'X' // 已取消
)

// regionState 区域维度的供需账本与档位状态机。
//
// capacity 与 pending 是增量维护的 O(1) 计数：
//   - capacity 恒等于本区域在线且 held<maxHeld 的骑手数；
//   - pending 恒等于本区域已创建未派未取消的订单数。
type regionState struct {
	capacity int
	pending  int

	tier          Tier
	downConfirmed int
	lastEvalAt    TimeSec
	hasEvaluated  bool
	events        []TierEvent
}

type riderState struct {
	online    bool
	region    RegionID
	enteredAt TimeSec // 最近一次进入当前区域的时刻（仅在线时有意义）
	held      int
}

type orderState struct {
	region     RegionID
	createdAt  TimeSec
	lockedTier Tier
	stage      byte
	rider      RiderID
	// riderEligible 在派出时刻固化：骑手进入本区时刻是否不晚于订单创建时刻。
	// 固化后骑手再移动/下线不影响结算判定。
	riderEligible bool
}

// System 增量维护的正式实现。单一互斥锁串行化所有变更，
// 因而任何并发交错都等价于某个合法的串行顺序。
type System struct {
	cfg Config
	mu  sync.Mutex

	now     TimeSec
	regions map[RegionID]*regionState
	riders  map[RiderID]*riderState
	orders  map[OrderID]*orderState

	account map[RiderID][]SubsidyEntry
}

// NewSystem 构造系统并校验参数。
func NewSystem(cfg Config) (*System, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return &System{
		cfg:     cfg,
		regions: map[RegionID]*regionState{},
		riders:  map[RiderID]*riderState{},
		orders:  map[OrderID]*orderState{},
		account: map[RiderID][]SubsidyEntry{},
	}, nil
}

func validateConfig(cfg Config) error {
	if len(cfg.Thresholds) == 0 {
		return errf(ErrInvalidConfig, "thresholds must be non-empty")
	}
	prev := math.Inf(-1)
	for i, th := range cfg.Thresholds {
		if math.IsNaN(th) || math.IsInf(th, 0) || th < 0 || th <= prev {
			return errf(ErrInvalidConfig, "thresholds[%d]=%v must be strictly increasing non-negative", i, th)
		}
		prev = th
	}
	if cfg.DownConfirmations <= 0 {
		return errf(ErrInvalidConfig, "down confirmations must be positive, got %d", cfg.DownConfirmations)
	}
	if cfg.MaxHeld <= 0 {
		return errf(ErrInvalidConfig, "max held orders must be positive, got %d", cfg.MaxHeld)
	}
	if len(cfg.Subsidies) != len(cfg.Thresholds)+1 {
		return errf(ErrInvalidConfig, "subsidies length %d must equal tier count %d",
			len(cfg.Subsidies), len(cfg.Thresholds)+1)
	}
	for i, v := range cfg.Subsidies {
		if v < 0 {
			return errf(ErrInvalidConfig, "subsidies[%d] must be non-negative, got %d", i, v)
		}
	}
	if cfg.MinEvalInterval < 0 {
		return errf(ErrInvalidConfig, "min evaluate interval must be non-negative, got %d", cfg.MinEvalInterval)
	}
	if cfg.Multipliers != nil {
		if len(cfg.Multipliers) != len(cfg.Thresholds)+1 {
			return errf(ErrInvalidConfig, "multipliers length %d must equal tier count %d",
				len(cfg.Multipliers), len(cfg.Thresholds)+1)
		}
		for i, v := range cfg.Multipliers {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				return errf(ErrInvalidConfig, "multipliers[%d]=%v must be finite non-negative", i, v)
			}
		}
	}
	return nil
}

func (s *System) checkClock(t TimeSec) error {
	if t < s.now {
		return errf(ErrClockRollback, "time %d earlier than accepted max %d", t, s.now)
	}
	return nil
}

func (s *System) getRegion(id RegionID) (*regionState, error) {
	r, ok := s.regions[id]
	if !ok {
		return nil, errf(ErrRegionNotFound, "region %q", id)
	}
	return r, nil
}

func (s *System) getRider(id RiderID) (*riderState, error) {
	r, ok := s.riders[id]
	if !ok {
		return nil, errf(ErrRiderNotFound, "rider %q", id)
	}
	return r, nil
}

func (s *System) getOrder(id OrderID) (*orderState, error) {
	o, ok := s.orders[id]
	if !ok {
		return nil, errf(ErrOrderNotFound, "order %q", id)
	}
	return o, nil
}

// available 骑手是否计入可用运力：在线且持单未满。
func (rd *riderState) available(maxHeld int) bool {
	return rd.online && rd.held < maxHeld
}

// targetTier 按供需比率选择目标档：比率不小于阈值的最高档；
// 任何阈值都未达到（含比率为 0）则为基础档。
func targetTier(cfg Config, pending, capacity int) Tier {
	if pending <= 0 || capacity < 0 {
		return 0
	}
	if capacity == 0 {
		return Tier(len(cfg.Thresholds)) // 0/正 = 无穷大，达到全部阈值
	}
	ratio := float64(pending) / float64(capacity)
	best := Tier(0)
	for i, th := range cfg.Thresholds {
		if ratio >= th {
			best = Tier(i + 1)
		}
	}
	return best
}

// AddRegion 登记区域。区域是静态配置，不携带操作时刻。
func (s *System) AddRegion(id RegionID) error {
	if id == "" {
		return errf(ErrInvalidArgument, "empty region id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.regions[id]; ok {
		return errf(ErrInvalidArgument, "region %q already exists", id)
	}
	s.regions[id] = &regionState{}
	return nil
}

// RiderOnline 骑手上线进入区域；下线后重新上线视为重新创建在线状态。
func (s *System) RiderOnline(t TimeSec, id RiderID, reg RegionID) error {
	if id == "" {
		return errf(ErrInvalidArgument, "empty rider id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	rg, err := s.getRegion(reg)
	if err != nil {
		return err
	}
	rd := s.riders[id]
	if rd != nil && rd.online {
		return errf(ErrRiderAlreadyOnline, "rider %q already online", id)
	}
	if rd == nil {
		rd = &riderState{}
		s.riders[id] = rd
	}
	rd.online = true
	rd.region = reg
	rd.enteredAt = t
	if rd.held < s.cfg.MaxHeld {
		rg.capacity++
	}
	s.now = t
	return nil
}

// RiderOffline 骑手下线。持单骑手必须先完成全部持单，
// 故下线时不调整持单计数，只在其仍计入运力时减一。
func (s *System) RiderOffline(t TimeSec, id RiderID) error {
	if id == "" {
		return errf(ErrInvalidArgument, "empty rider id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	rd, err := s.getRider(id)
	if err != nil {
		return err
	}
	if !rd.online {
		return errf(ErrRiderAlreadyOffline, "rider %q already offline", id)
	}
	if rg, ok := s.regions[rd.region]; ok && rd.held < s.cfg.MaxHeld {
		rg.capacity--
	}
	rd.online = false
	s.now = t
	return nil
}

// RiderMove 骑手跨区移动；移动到当前所在区域报无需移动。
func (s *System) RiderMove(t TimeSec, id RiderID, dest RegionID) error {
	if id == "" {
		return errf(ErrInvalidArgument, "empty rider id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	dst, err := s.getRegion(dest)
	if err != nil {
		return err
	}
	rd, err := s.getRider(id)
	if err != nil {
		return err
	}
	if !rd.online {
		return errf(ErrRiderAlreadyOffline, "rider %q is offline", id)
	}
	if rd.region == dest {
		return errf(ErrMoveNotNeeded, "rider %q already in region %q", id, dest)
	}
	src, err := s.getRegion(rd.region)
	if err != nil {
		return err
	}
	if rd.held < s.cfg.MaxHeld {
		src.capacity--
		dst.capacity++
	}
	rd.region = dest
	rd.enteredAt = t
	s.now = t
	return nil
}

// CreateOrder 创建待派订单，并锁定所在区域当前档位。
func (s *System) CreateOrder(t TimeSec, id OrderID, reg RegionID) error {
	if id == "" {
		return errf(ErrInvalidArgument, "empty order id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	rg, err := s.getRegion(reg)
	if err != nil {
		return err
	}
	if _, dup := s.orders[id]; dup {
		return errf(ErrInvalidArgument, "order %q already exists", id)
	}
	s.orders[id] = &orderState{
		region:     reg,
		createdAt:  t,
		lockedTier: rg.tier,
		stage:      stagePending,
	}
	rg.pending++
	s.now = t
	return nil
}

// DispatchOrder 将待派订单派给骑手。
// 资格报错次序：骑手不在线 → 骑手不在本区 → 骑手持单已满。
func (s *System) DispatchOrder(t TimeSec, oid OrderID, rid RiderID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	od, err := s.getOrder(oid)
	if err != nil {
		return err
	}
	switch {
	case od.stage == stageCancelled:
		return errf(ErrOrderCancelled, "order %q cancelled", oid)
	case od.stage == stageCompleted:
		return errf(ErrOrderCompleted, "order %q completed", oid)
	case od.stage == stageDispatched:
		return errf(ErrOrderDispatched, "order %q already dispatched", oid)
	}
	rg, err := s.getRegion(od.region)
	if err != nil {
		return err
	}
	rd, err := s.getRider(rid)
	if err != nil {
		return err
	}
	if !rd.online {
		return errf(ErrRiderOffline, "rider %q offline", rid)
	}
	if rd.region != od.region {
		return errf(ErrRiderWrongRegion, "rider %q in %q, order in %q", rid, rd.region, od.region)
	}
	if rd.held >= s.cfg.MaxHeld {
		return errf(ErrRiderHoldFull, "rider %q holds %d/%d", rid, rd.held, s.cfg.MaxHeld)
	}
	if rd.held+1 >= s.cfg.MaxHeld {
		rg.capacity--
	}
	rd.held++
	od.stage = stageDispatched
	od.rider = rid
	od.riderEligible = rd.enteredAt <= od.createdAt
	rg.pending--
	s.now = t
	return nil
}

// CancelOrder 取消订单；待派订单离开待派集合。
func (s *System) CancelOrder(t TimeSec, oid OrderID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	od, err := s.getOrder(oid)
	if err != nil {
		return err
	}
	switch {
	case od.stage == stageCancelled:
		return errf(ErrOrderCancelled, "order %q already cancelled", oid)
	case od.stage == stageCompleted:
		return errf(ErrOrderCompleted, "order %q already completed", oid)
	case od.stage == stageDispatched:
		return errf(ErrOrderDispatched, "order %q already dispatched", oid)
	}
	s.regions[od.region].pending--
	od.stage = stageCancelled
	s.now = t
	return nil
}

// CompleteOrder 骑手完成持单，释放一个持单名额并结算补贴。
// 补贴只看锁定档；骑手在订单创建时刻尚未进入本区则豁免（补贴 0）。
func (s *System) CompleteOrder(t TimeSec, oid OrderID) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return 0, err
	}
	od, err := s.getOrder(oid)
	if err != nil {
		return 0, err
	}
	switch {
	case od.stage == stageCancelled:
		return 0, errf(ErrOrderCancelled, "order %q cancelled", oid)
	case od.stage == stageCompleted:
		return 0, errf(ErrOrderCompleted, "order %q already completed", oid)
	case od.stage == stagePending:
		return 0, errf(ErrOrderNotDispatched, "order %q not dispatched", oid)
	}
	rd, err := s.getRider(od.rider)
	if err != nil {
		return 0, err
	}
	rg, err := s.getRegion(od.region)
	if err != nil {
		return 0, err
	}
	wasFull := rd.held >= s.cfg.MaxHeld
	rd.held--
	// 仅当骑手仍在本区在线时，释放名额才重新计入本区运力。
	if rd.online && rd.region == od.region && wasFull && rd.held < s.cfg.MaxHeld {
		rg.capacity++
	}
	od.stage = stageCompleted

	amount := int64(0)
	if !od.riderEligible {
		amount = 0 // 迟到补贴豁免
	} else {
		amount = s.cfg.Subsidies[od.lockedTier]
	}
	s.account[od.rider] = append(s.account[od.rider], SubsidyEntry{
		Order:      oid,
		Region:     od.region,
		Tier:       od.lockedTier,
		Amount:     amount,
		WaivedLate: !od.riderEligible,
	})
	s.now = t
	return amount, nil
}

// Evaluate 对单个区域执行一次档位评估。
func (s *System) Evaluate(t TimeSec, reg RegionID) (Tier, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return 0, err
	}
	rg, err := s.getRegion(reg)
	if err != nil {
		return 0, err
	}
	if rg.hasEvaluated && t-rg.lastEvalAt < TimeSec(s.cfg.MinEvalInterval) {
		return 0, errf(ErrEvaluateTooFrequent,
			"region %q evaluated at %d, next allowed at >= %d, got %d",
			reg, rg.lastEvalAt, rg.lastEvalAt+TimeSec(s.cfg.MinEvalInterval), t)
	}
	old := rg.tier
	target := targetTier(s.cfg, rg.pending, rg.capacity)
	switch {
	case target > old:
		rg.tier = target // 上调即时生效，可一次跨多档
		rg.downConfirmed = 0
	case target < old:
		rg.downConfirmed++
		if rg.downConfirmed >= s.cfg.DownConfirmations {
			rg.tier = old - 1 // 即使目标更低，每次也只降一档
			rg.downConfirmed = 0
		}
	default:
		rg.downConfirmed = 0
	}
	rg.lastEvalAt = t
	rg.hasEvaluated = true
	if rg.tier != old {
		rg.events = append(rg.events, TierEvent{Region: reg, At: t, FromTier: old, ToTier: rg.tier})
	}
	s.now = t
	return rg.tier, nil
}

// Multiplier 返回某档的加价倍率。
func (s *System) Multiplier(tier Tier) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return multiplierOf(s.cfg, tier)
}

func multiplierOf(cfg Config, tier Tier) (float64, error) {
	if tier < 0 || int(tier) >= len(cfg.Thresholds)+1 {
		return 0, errf(ErrInvalidArgument, "tier %d out of range [0,%d]", tier, len(cfg.Thresholds))
	}
	if cfg.Multipliers != nil {
		return cfg.Multipliers[tier], nil
	}
	if tier == 0 {
		return 1, nil
	}
	return cfg.Thresholds[tier-1], nil
}

// TierEvents 查询区域档位变化事件（按发生顺序）。
func (s *System) TierEvents(reg RegionID) ([]TierEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rg, err := s.getRegion(reg)
	if err != nil {
		return nil, err
	}
	out := make([]TierEvent, len(rg.events))
	copy(out, rg.events)
	return out, nil
}

// SubsidyTotal 查询骑手累计补贴。
func (s *System) SubsidyTotal(id RiderID) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.getRider(id); err != nil {
		return 0, err
	}
	var total int64
	for _, e := range s.account[id] {
		total += e.Amount
	}
	return total, nil
}

// SubsidyEntries 查询骑手补贴条目（按结算顺序）。
func (s *System) SubsidyEntries(id RiderID) ([]SubsidyEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.getRider(id); err != nil {
		return nil, err
	}
	out := make([]SubsidyEntry, len(s.account[id]))
	copy(out, s.account[id])
	return out, nil
}

// Snapshot 取可观测全状态（深拷贝），用于不变量与差分校验。
func (s *System) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *System) snapshotLocked() Snapshot {
	snap := Snapshot{
		Now:       s.now,
		Regions:   map[RegionID]RegionSnapshot{},
		Tiers:     map[RegionID]Tier{},
		Orders:    map[OrderID]OrderSnapshot{},
		Riders:    map[RiderID]RiderSnapshot{},
		HoldCount: map[RiderID]int{},
		Account:   map[RiderID]int64{},
	}
	for id, rg := range s.regions {
		snap.Regions[id] = RegionSnapshot{
			Capacity:       rg.capacity,
			Pending:        rg.pending,
			Tier:           rg.tier,
			DownConfirmed:  rg.downConfirmed,
			LastEvaluateAt: rg.lastEvalAt,
		}
		snap.Tiers[id] = rg.tier
	}
	for id, od := range s.orders {
		snap.Orders[id] = OrderSnapshot{
			Region:     od.region,
			CreatedAt:  od.createdAt,
			LockedTier: od.lockedTier,
			Stage:      od.stage,
			Rider:      od.rider,
		}
	}
	for id, rd := range s.riders {
		snap.Riders[id] = RiderSnapshot{
			Online:    rd.online,
			Region:    rd.region,
			EnteredAt: rd.enteredAt,
			Held:      rd.held,
		}
		snap.HoldCount[id] = rd.held
		var total int64
		for _, e := range s.account[id] {
			total += e.Amount
		}
		snap.Account[id] = total
	}
	return snap
}
