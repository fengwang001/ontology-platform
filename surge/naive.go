package surge

// 朴素模型的事件种类。该模型不维护任何增量状态，
// 只追加已被接受的原始事件；每次需要状态时从头全量重放。
const (
	evOnline   byte = 'o'
	evOffline  byte = 'f'
	evMove     byte = 'm'
	evEvaluate byte = 'e'
	evCreate   byte = 'c'
	evDispatch byte = 'd'
	evCancel   byte = 'x'
	evComplete byte = 'z'
)

type rawEvent struct {
	kind   byte
	at     TimeSec
	region RegionID
	rider  RiderID
	order  OrderID
}

// nRider 全量重放时使用的骑手记录（与正式实现的 riderState 结构无关）。
type nRider struct {
	exists    bool
	online    bool
	region    RegionID
	enteredAt TimeSec
	held      int
}

// nOrder 全量重放时使用的订单记录。
type nOrder struct {
	region     RegionID
	createdAt  TimeSec
	lockedTier Tier
	stage      byte
	rider      RiderID
	eligible   bool
}

// NaiveModel 朴素对照模型：事件日志 + 每次操作前 O(n) 全量重放。
// 它与正式实现只共享 Config/错误/Snapshot 契约类型，
// 数据结构与判定代码路径完全独立，用作差分测试的预言机。
type NaiveModel struct {
	cfg     Config
	regions map[RegionID]bool
	events  []rawEvent
}

// NewNaiveModel 构造朴素模型。
func NewNaiveModel(cfg Config) (*NaiveModel, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return &NaiveModel{cfg: cfg, regions: map[RegionID]bool{}}, nil
}

// nWorld 是一次全量重放的产物。
type nWorld struct {
	now      TimeSec
	riders   map[RiderID]*nRider
	orders   map[OrderID]*nOrder
	capacity map[RegionID]int
	pending  map[RegionID]int
	tier     map[RegionID]Tier
	downConf map[RegionID]int
	lastEval map[RegionID]TimeSec
	hasEval  map[RegionID]bool
	tierEv   []TierEvent
	subsidy  map[RiderID][]SubsidyEntry
}

func newNWorld(regions map[RegionID]bool) *nWorld {
	w := &nWorld{
		riders:   map[RiderID]*nRider{},
		orders:   map[OrderID]*nOrder{},
		capacity: map[RegionID]int{},
		pending:  map[RegionID]int{},
		tier:     map[RegionID]Tier{},
		downConf: map[RegionID]int{},
		lastEval: map[RegionID]TimeSec{},
		hasEval:  map[RegionID]bool{},
		subsidy:  map[RiderID][]SubsidyEntry{},
	}
	for r := range regions {
		w.capacity[r] = 0
		w.pending[r] = 0
		w.tier[r] = 0
	}
	return w
}

func (n *NaiveModel) getRider(w *nWorld, id RiderID) *nRider {
	rd := w.riders[id]
	if rd == nil {
		rd = &nRider{}
		w.riders[id] = rd
	}
	return rd
}

// replay 从头重放全部已接受事件，重建整个世界。
func (n *NaiveModel) replay() *nWorld {
	w := newNWorld(n.regions)
	for _, ev := range n.events {
		n.apply(w, ev)
		w.now = ev.at
	}
	return w
}

// apply 把一条事件作用到重放世界上。
func (n *NaiveModel) apply(w *nWorld, ev rawEvent) {
	switch ev.kind {
	case evOnline:
		rd := n.getRider(w, ev.rider)
		rd.exists, rd.online = true, true
		rd.region, rd.enteredAt = ev.region, ev.at
		if rd.held < n.cfg.MaxHeld {
			w.capacity[ev.region]++
		}
	case evOffline:
		rd := w.riders[ev.rider]
		if rd.held < n.cfg.MaxHeld {
			w.capacity[rd.region]--
		}
		rd.online = false
	case evMove:
		rd := w.riders[ev.rider]
		if rd.held < n.cfg.MaxHeld {
			w.capacity[rd.region]--
			w.capacity[ev.region]++
		}
		rd.region, rd.enteredAt = ev.region, ev.at
	case evCreate:
		w.orders[ev.order] = &nOrder{
			region:     ev.region,
			createdAt:  ev.at,
			lockedTier: w.tier[ev.region],
			stage:      stagePending,
		}
		w.pending[ev.region]++
	case evDispatch:
		od := w.orders[ev.order]
		rd := w.riders[ev.rider]
		if rd.held+1 >= n.cfg.MaxHeld {
			w.capacity[od.region]--
		}
		rd.held++
		od.stage, od.rider = stageDispatched, ev.rider
		od.eligible = rd.enteredAt <= od.createdAt
		w.pending[od.region]--
	case evCancel:
		od := w.orders[ev.order]
		w.pending[od.region]--
		od.stage = stageCancelled
	case evComplete:
		od := w.orders[ev.order]
		rd := w.riders[od.rider]
		wasFull := rd.held >= n.cfg.MaxHeld
		rd.held--
		if rd.online && rd.region == od.region && wasFull {
			w.capacity[od.region]++
		}
		od.stage = stageCompleted
		amount := int64(0)
		if od.eligible {
			amount = n.cfg.Subsidies[od.lockedTier]
		}
		w.subsidy[od.rider] = append(w.subsidy[od.rider], SubsidyEntry{
			Order:      ev.order,
			Region:     od.region,
			Tier:       od.lockedTier,
			Amount:     amount,
			WaivedLate: !od.eligible,
		})
	case evEvaluate:
		old := w.tier[ev.region]
		target := targetTier(n.cfg, w.pending[ev.region], w.capacity[ev.region])
		switch {
		case target > old:
			w.tier[ev.region] = target
			w.downConf[ev.region] = 0
		case target < old:
			w.downConf[ev.region]++
			if w.downConf[ev.region] >= n.cfg.DownConfirmations {
				w.tier[ev.region] = old - 1
				w.downConf[ev.region] = 0
			}
		default:
			w.downConf[ev.region] = 0
		}
		w.lastEval[ev.region] = ev.at
		w.hasEval[ev.region] = true
		if w.tier[ev.region] != old {
			w.tierEv = append(w.tierEv, TierEvent{
				Region: ev.region, At: ev.at, FromTier: old, ToTier: w.tier[ev.region],
			})
		}
	}
}

// accept 先重放世界，执行与正式实现一致的校验，通过后再把事件落盘并重放一次。
func (n *NaiveModel) accept(ev rawEvent) (Tier, error) {
	w := n.replay()
	var err error
	switch ev.kind {
	case evOnline:
		err = n.checkOnline(w, ev)
	case evOffline:
		err = n.checkOffline(w, ev)
	case evMove:
		err = n.checkMove(w, ev)
	case evEvaluate:
		err = n.checkEvaluate(w, ev)
	case evCreate:
		err = n.checkCreate(w, ev)
	case evDispatch:
		err = n.checkDispatch(w, ev)
	case evCancel:
		err = n.checkCancel(w, ev)
	case evComplete:
		err = n.checkComplete(w, ev)
	}
	if err != nil {
		return 0, err
	}
	n.apply(w, ev)
	newTier := w.tier[ev.region]
	n.events = append(n.events, ev)
	return newTier, nil
}

func (n *NaiveModel) checkClock(w *nWorld, t TimeSec) error {
	if t < w.now {
		return errf(ErrClockRollback, "time %d earlier than accepted max %d", t, w.now)
	}
	return nil
}

func (n *NaiveModel) checkOnline(w *nWorld, ev rawEvent) error {
	if err := n.checkClock(w, ev.at); err != nil {
		return err
	}
	if !n.regions[ev.region] {
		return errf(ErrRegionNotFound, "region %q", ev.region)
	}
	if rd := w.riders[ev.rider]; rd != nil && rd.exists && rd.online {
		return errf(ErrRiderAlreadyOnline, "rider %q already online", ev.rider)
	}
	return nil
}

func (n *NaiveModel) checkOffline(w *nWorld, ev rawEvent) error {
	if err := n.checkClock(w, ev.at); err != nil {
		return err
	}
	rd := w.riders[ev.rider]
	if rd == nil || !rd.exists {
		return errf(ErrRiderNotFound, "rider %q", ev.rider)
	}
	if !rd.online {
		return errf(ErrRiderAlreadyOffline, "rider %q already offline", ev.rider)
	}
	return nil
}

func (n *NaiveModel) checkMove(w *nWorld, ev rawEvent) error {
	if err := n.checkClock(w, ev.at); err != nil {
		return err
	}
	if !n.regions[ev.region] {
		return errf(ErrRegionNotFound, "region %q", ev.region)
	}
	rd := w.riders[ev.rider]
	if rd == nil || !rd.exists {
		return errf(ErrRiderNotFound, "rider %q", ev.rider)
	}
	if !rd.online {
		return errf(ErrRiderAlreadyOffline, "rider %q is offline", ev.rider)
	}
	if rd.region == ev.region {
		return errf(ErrMoveNotNeeded, "rider %q already in region %q", ev.rider, ev.region)
	}
	return nil
}

func (n *NaiveModel) checkEvaluate(w *nWorld, ev rawEvent) error {
	if err := n.checkClock(w, ev.at); err != nil {
		return err
	}
	if !n.regions[ev.region] {
		return errf(ErrRegionNotFound, "region %q", ev.region)
	}
	if w.hasEval[ev.region] && ev.at-w.lastEval[ev.region] < TimeSec(n.cfg.MinEvalInterval) {
		return errf(ErrEvaluateTooFrequent,
			"region %q evaluated at %d, next allowed at >= %d, got %d",
			ev.region, w.lastEval[ev.region],
			w.lastEval[ev.region]+TimeSec(n.cfg.MinEvalInterval), ev.at)
	}
	return nil
}

func (n *NaiveModel) checkCreate(w *nWorld, ev rawEvent) error {
	if err := n.checkClock(w, ev.at); err != nil {
		return err
	}
	if !n.regions[ev.region] {
		return errf(ErrRegionNotFound, "region %q", ev.region)
	}
	if _, dup := w.orders[ev.order]; dup {
		return errf(ErrInvalidArgument, "order %q already exists", ev.order)
	}
	return nil
}

func (n *NaiveModel) checkDispatch(w *nWorld, ev rawEvent) error {
	if err := n.checkClock(w, ev.at); err != nil {
		return err
	}
	od := w.orders[ev.order]
	if od == nil {
		return errf(ErrOrderNotFound, "order %q", ev.order)
	}
	switch {
	case od.stage == stageCancelled:
		return errf(ErrOrderCancelled, "order %q cancelled", ev.order)
	case od.stage == stageCompleted:
		return errf(ErrOrderCompleted, "order %q completed", ev.order)
	case od.stage == stageDispatched:
		return errf(ErrOrderDispatched, "order %q already dispatched", ev.order)
	}
	rd := w.riders[ev.rider]
	if rd == nil || !rd.exists {
		return errf(ErrRiderNotFound, "rider %q", ev.rider)
	}
	if !rd.online {
		return errf(ErrRiderOffline, "rider %q offline", ev.rider)
	}
	if rd.region != od.region {
		return errf(ErrRiderWrongRegion, "rider %q in %q, order in %q", ev.rider, rd.region, od.region)
	}
	if rd.held >= n.cfg.MaxHeld {
		return errf(ErrRiderHoldFull, "rider %q holds %d/%d", ev.rider, rd.held, n.cfg.MaxHeld)
	}
	return nil
}

func (n *NaiveModel) checkCancel(w *nWorld, ev rawEvent) error {
	if err := n.checkClock(w, ev.at); err != nil {
		return err
	}
	od := w.orders[ev.order]
	if od == nil {
		return errf(ErrOrderNotFound, "order %q", ev.order)
	}
	switch {
	case od.stage == stageCancelled:
		return errf(ErrOrderCancelled, "order %q already cancelled", ev.order)
	case od.stage == stageCompleted:
		return errf(ErrOrderCompleted, "order %q already completed", ev.order)
	case od.stage == stageDispatched:
		return errf(ErrOrderDispatched, "order %q already dispatched", ev.order)
	}
	return nil
}

func (n *NaiveModel) checkComplete(w *nWorld, ev rawEvent) error {
	if err := n.checkClock(w, ev.at); err != nil {
		return err
	}
	od := w.orders[ev.order]
	if od == nil {
		return errf(ErrOrderNotFound, "order %q", ev.order)
	}
	switch {
	case od.stage == stageCancelled:
		return errf(ErrOrderCancelled, "order %q cancelled", ev.order)
	case od.stage == stageCompleted:
		return errf(ErrOrderCompleted, "order %q already completed", ev.order)
	case od.stage == stagePending:
		return errf(ErrOrderNotDispatched, "order %q not dispatched", ev.order)
	}
	return nil
}

// AddRegion 登记区域。
func (n *NaiveModel) AddRegion(id RegionID) error {
	if id == "" {
		return errf(ErrInvalidArgument, "empty region id")
	}
	if n.regions[id] {
		return errf(ErrInvalidArgument, "region %q already exists", id)
	}
	n.regions[id] = true
	return nil
}

func (n *NaiveModel) RiderOnline(t TimeSec, id RiderID, reg RegionID) error {
	if id == "" {
		return errf(ErrInvalidArgument, "empty rider id")
	}
	_, err := n.accept(rawEvent{kind: evOnline, at: t, region: reg, rider: id})
	return err
}

func (n *NaiveModel) RiderOffline(t TimeSec, id RiderID) error {
	if id == "" {
		return errf(ErrInvalidArgument, "empty rider id")
	}
	_, err := n.accept(rawEvent{kind: evOffline, at: t, rider: id})
	return err
}

func (n *NaiveModel) RiderMove(t TimeSec, id RiderID, dest RegionID) error {
	if id == "" {
		return errf(ErrInvalidArgument, "empty rider id")
	}
	_, err := n.accept(rawEvent{kind: evMove, at: t, region: dest, rider: id})
	return err
}

func (n *NaiveModel) Evaluate(t TimeSec, reg RegionID) (Tier, error) {
	return n.accept(rawEvent{kind: evEvaluate, at: t, region: reg})
}

func (n *NaiveModel) CreateOrder(t TimeSec, id OrderID, reg RegionID) error {
	if id == "" {
		return errf(ErrInvalidArgument, "empty order id")
	}
	_, err := n.accept(rawEvent{kind: evCreate, at: t, region: reg, order: id})
	return err
}

func (n *NaiveModel) DispatchOrder(t TimeSec, oid OrderID, rid RiderID) error {
	_, err := n.accept(rawEvent{kind: evDispatch, at: t, rider: rid, order: oid})
	return err
}

func (n *NaiveModel) CancelOrder(t TimeSec, oid OrderID) error {
	_, err := n.accept(rawEvent{kind: evCancel, at: t, order: oid})
	return err
}

func (n *NaiveModel) CompleteOrder(t TimeSec, oid OrderID) (int64, error) {
	w := n.replay()
	ev := rawEvent{kind: evComplete, at: t, order: oid}
	if err := n.checkComplete(w, ev); err != nil {
		return 0, err
	}
	od := w.orders[oid]
	n.apply(w, ev)
	n.events = append(n.events, ev)
	amount := int64(0)
	if od.eligible {
		amount = n.cfg.Subsidies[od.lockedTier]
	}
	return amount, nil
}

// Multiplier 返回某档加价倍率。
func (n *NaiveModel) Multiplier(tier Tier) (float64, error) {
	return multiplierOf(n.cfg, tier)
}

// TierEvents 通过重放收集区域档位事件。
func (n *NaiveModel) TierEvents(reg RegionID) ([]TierEvent, error) {
	if !n.regions[reg] {
		return nil, errf(ErrRegionNotFound, "region %q", reg)
	}
	w := n.replay()
	var out []TierEvent
	for _, e := range w.tierEv {
		if e.Region == reg {
			out = append(out, e)
		}
	}
	return out, nil
}

// SubsidyEntries 通过重放收集骑手补贴条目。
func (n *NaiveModel) SubsidyEntries(id RiderID) ([]SubsidyEntry, error) {
	w := n.replay()
	rd := w.riders[id]
	if rd == nil || !rd.exists {
		return nil, errf(ErrRiderNotFound, "rider %q", id)
	}
	out := make([]SubsidyEntry, len(w.subsidy[id]))
	copy(out, w.subsidy[id])
	return out, nil
}

// SubsidyTotal 通过重放汇总骑手补贴。
func (n *NaiveModel) SubsidyTotal(id RiderID) (int64, error) {
	entries, err := n.SubsidyEntries(id)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		total += e.Amount
	}
	return total, nil
}

// Snapshot 全量重放后导出可观测状态。
func (n *NaiveModel) Snapshot() Snapshot {
	w := n.replay()
	snap := Snapshot{
		Now:       w.now,
		Regions:   map[RegionID]RegionSnapshot{},
		Tiers:     map[RegionID]Tier{},
		Orders:    map[OrderID]OrderSnapshot{},
		Riders:    map[RiderID]RiderSnapshot{},
		HoldCount: map[RiderID]int{},
		Account:   map[RiderID]int64{},
	}
	for r := range n.regions {
		snap.Regions[r] = RegionSnapshot{
			Capacity:       w.capacity[r],
			Pending:        w.pending[r],
			Tier:           w.tier[r],
			DownConfirmed:  w.downConf[r],
			LastEvaluateAt: w.lastEval[r],
		}
		snap.Tiers[r] = w.tier[r]
	}
	for id, od := range w.orders {
		snap.Orders[id] = OrderSnapshot{
			Region:     od.region,
			CreatedAt:  od.createdAt,
			LockedTier: od.lockedTier,
			Stage:      od.stage,
			Rider:      od.rider,
		}
	}
	for id, rd := range w.riders {
		if !rd.exists {
			continue
		}
		snap.Riders[id] = RiderSnapshot{
			Online:    rd.online,
			Region:    rd.region,
			EnteredAt: rd.enteredAt,
			Held:      rd.held,
		}
		snap.HoldCount[id] = rd.held
		var total int64
		for _, e := range w.subsidy[id] {
			total += e.Amount
		}
		snap.Account[id] = total
	}
	return snap
}
