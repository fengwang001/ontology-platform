package sla

import "sync"

// stage 表示订单生命周期阶段，事件必须 accept → dispatch → ready → pickup → deliver。
type stage int

const (
	stageAccepted stage = iota
	stageDispatched
	stageReady
	stagePickedUp
	stageDelivered
)

// order 是单笔订单的全部冻结状态。接受时拷贝所需构造参数，
// 此后系统 Config 的变更不影响该订单。
type order struct {
	id string

	stage    stage
	canceled bool

	acceptedAt int64
	promisedAt int64 // 原始承诺时刻（不含任何延展）

	dispatchAt int64
	readyAt    int64
	pickupAt   int64
	deliverAt  int64

	// 接受时冻结的构造参数
	merchantPrep     int64
	platformDispatch int64
	riderPickup      int64
	userAddrExtend   int64
	extendCap        int64
	claimWindow      int64
	tiers            []Tier // 接受时冻结的延误档位

	readdressed bool
	readdressAt int64
	userExt     int64

	// 天气延展：接受时快照此前已登记事件的覆盖量（O(log W)，仅发生在接受）；
	// 此后新登记的事件由 AddWeather 直接推送（事件集合与快照互斥，不重复）。
	// 送达时冻结，之后的推送跳过已送达订单，天然不追溯。
	weatherExt int64

	adjudicated   bool
	entry         *LedgerEntry
	autoCandidate bool // 送达时延误落入最高档，供窗口结束后自动裁决
}

// System 是并发安全的履约时效承诺与超时赔付系统。
type System struct {
	mu sync.Mutex

	cfg     Config
	tiers   []Tier
	orders  map[string]*order
	weather map[string]struct{} // 已登记天气事件 ID 去重

	// weatherIdx：已登记天气事件在时间轴上的差分树。
	// 接受时 O(log W) 求一次覆盖量；之后事件靠推送，裁决路径 O(1)。
	weatherIdx *sumTreap
	// orderIdx：未送达订单按原始承诺时刻索引，天气登记时只枚举区间内订单。
	orderIdx *orderTreap

	lastAcceptAt int64 // 已接受操作的最大时刻（拒绝的接受不推进时钟）
	ledger       []LedgerEntry
}

// New 用给定构造参数创建系统。
func New(cfg Config) *System {
	if err := cfg.validate(); err != nil {
		panic(err)
	}
	return newSystem(cfg)
}

func newSystem(cfg Config) *System {
	tiers := make([]Tier, len(cfg.TierThresholds))
	for i := range tiers {
		tiers[i] = Tier{Threshold: cfg.TierThresholds[i], Payout: cfg.TierPayouts[i]}
	}
	return &System{
		cfg:        cfg,
		tiers:      tiers,
		orders:     map[string]*order{},
		weather:    map[string]struct{}{},
		weatherIdx: newSumTreap(),
		orderIdx:   newOrderTreap(),
	}
}

// NewChecked 与 New 相同，但以错误而非 panic 返回非法参数。
func NewChecked(cfg Config) (*System, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return newSystem(cfg), nil
}

// UpdateConfig 变更构造参数；不影响任何已接受订单的冻结承诺与参数快照。
func (s *System) UpdateConfig(cfg Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tiers := make([]Tier, len(cfg.TierThresholds))
	for i := range tiers {
		tiers[i] = Tier{Threshold: cfg.TierThresholds[i], Payout: cfg.TierPayouts[i]}
	}
	s.cfg = cfg
	s.tiers = tiers
	return nil
}

// Accept 接受订单：校验参数 → 时钟回退 → 冻结承诺时刻。
func (s *System) Accept(orderID string, at int64) error {
	if orderID == "" || at < 0 {
		return errf(CodeInvalidParam, "bad accept arguments")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if at < s.lastAcceptAt {
		return errf(CodeClockRollback, "accept clock went backwards")
	}
	if _, ok := s.orders[orderID]; ok {
		return errf(CodeInvalidParam, "order already exists")
	}
	promised, ok := addInt64(at, s.cfg.PromiseDuration)
	if !ok {
		return errf(CodeInvalidParam, "promise time overflow")
	}
	o := &order{
		id:               orderID,
		stage:            stageAccepted,
		acceptedAt:       at,
		promisedAt:       promised,
		merchantPrep:     s.cfg.MerchantPrep,
		platformDispatch: s.cfg.PlatformDispatch,
		riderPickup:      s.cfg.RiderPickup,
		userAddrExtend:   s.cfg.UserAddrExtend,
		extendCap:        s.cfg.ExtendCap,
		claimWindow:      s.cfg.ClaimWindow,
		tiers:            append([]Tier(nil), s.tiers...),
		weatherExt:       s.weatherIdx.prefixSum(promised),
	}
	s.orders[orderID] = o
	s.orderIdx.insert(o)
	s.lastAcceptAt = at
	return nil
}

// Dispatch 登记派单事件。
func (s *System) Dispatch(orderID string, at int64) error {
	if at < 0 {
		return errf(CodeInvalidParam, "negative timestamp")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.lookupLive(orderID)
	if err != nil {
		return err
	}
	if o.stage != stageAccepted {
		return errf(CodeEventOrder, "dispatch out of order")
	}
	o.stage, o.dispatchAt = stageDispatched, at
	return nil
}

// Ready 登记商家出餐就绪事件。
func (s *System) Ready(orderID string, at int64) error {
	if at < 0 {
		return errf(CodeInvalidParam, "negative timestamp")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.lookupLive(orderID)
	if err != nil {
		return err
	}
	if o.stage != stageDispatched {
		return errf(CodeEventOrder, "ready out of order")
	}
	o.stage, o.readyAt = stageReady, at
	return nil
}

// Pickup 登记骑手取货事件。
func (s *System) Pickup(orderID string, at int64) error {
	if at < 0 {
		return errf(CodeInvalidParam, "negative timestamp")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.lookupLive(orderID)
	if err != nil {
		return err
	}
	if o.stage != stageReady {
		return errf(CodeEventOrder, "pickup out of order")
	}
	o.stage, o.pickupAt = stagePickedUp, at
	return nil
}

// Deliver 登记送达事件，并快照“送达前已登记”的天气延展。
func (s *System) Deliver(orderID string, at int64) error {
	if at < 0 {
		return errf(CodeInvalidParam, "negative timestamp")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.lookupLive(orderID)
	if err != nil {
		return err
	}
	if o.stage != stagePickedUp {
		return errf(CodeEventOrder, "deliver out of order")
	}
	o.stage, o.deliverAt = stageDelivered, at
	// 天气延展由 AddWeather 推送，送达后不再有推送（枚举跳过已送达），
	// 故此处直接冻结，裁决路径 O(1)，与天气事件总数无关。
	delay := at - o.extendedPromise()
	if delay >= o.tiers[len(o.tiers)-1].Threshold {
		o.autoCandidate = true
	}
	return nil
}

// Cancel 取消订单（只允许在送达前）。
func (s *System) Cancel(orderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.lookupForClaim(orderID)
	if err != nil {
		return err
	}
	if o.stage == stageDelivered {
		return errf(CodeEventOrder, "cannot cancel delivered order")
	}
	o.canceled = true
	return nil
}

// ChangeAddress 用户改址：送达前限一次。
func (s *System) ChangeAddress(orderID string, at int64) error {
	if at < 0 {
		return errf(CodeInvalidParam, "negative timestamp")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.lookupLive(orderID)
	if err != nil {
		return err
	}
	if o.stage == stageDelivered {
		return errf(CodeEventOrder, "cannot change address after delivery")
	}
	if o.readdressed {
		return errf(CodeAlreadyReaddressed, "address already changed")
	}
	o.readdressed = true
	o.readdressAt = at
	o.userExt = o.userAddrExtend
	return nil
}

// AddWeather 登记天气事件，左闭右开 [startAt, endAt)，带延展量。
// 对所有尚未送达且原始承诺时刻落在区间内的订单即时生效；
// 已送达订单的天气快照已在送达时冻结，故天然不追溯。
func (s *System) AddWeather(weatherID string, startAt, endAt, extend int64) error {
	if weatherID == "" || startAt < 0 || endAt < 0 || extend < 0 || startAt >= endAt {
		return errf(CodeInvalidParam, "bad weather arguments")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.weather[weatherID]; dup {
		return errf(CodeInvalidParam, "weather event already registered")
	}
	if _, ok := addInt64(startAt, extend); !ok {
		return errf(CodeInvalidParam, "overflow")
	}
	s.weather[weatherID] = struct{}{}
	s.weatherIdx.add(startAt, extend)
	s.weatherIdx.add(endAt, -extend)
	// 推送给区间内未送达订单：接受前已登记的事件已计入其快照，
	// 那些订单不会被本次事件重复计算（本事件在其接受之后才存在）。
	// 枚举只触碰原始承诺时刻落在区间内的订单，与其他订单数量无关。
	s.orderIdx.forEachIn(startAt, endAt, func(o *order) {
		if o.canceled || o.stage == stageDelivered {
			return
		}
		o.weatherExt += extend
	})
	return nil
}

func (s *System) lookupLive(id string) (*order, error) {
	o, ok := s.orders[id]
	if !ok {
		return nil, errf(CodeOrderNotFound, "order not found")
	}
	if o.canceled {
		return nil, errf(CodeOrderCanceled, "order canceled")
	}
	return o, nil
}

// lookupForClaim 不把“已送达”视为状态错误，但仍拒绝不存在与已取消。
func (s *System) lookupForClaim(id string) (*order, error) {
	o, ok := s.orders[id]
	if !ok {
		return nil, errf(CodeOrderNotFound, "order not found")
	}
	if o.canceled {
		return nil, errf(CodeOrderCanceled, "order canceled")
	}
	return o, nil
}

// extendedPromise 计算截断到累计上限后的延展承诺时刻。
func (o *order) extendedPromise() int64 {
	ext := o.userExt + o.weatherExt
	if ext > o.extendCap {
		ext = o.extendCap
	}
	p, _ := addInt64(o.promisedAt, ext)
	return p
}
