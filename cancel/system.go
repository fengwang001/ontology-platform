package cancel

import "sync"

// System 是取消责任判定与退款分摊系统。
type System struct {
	mu     sync.Mutex
	params Params
	orders map[string]*orderState
	now    int64
	ends   windowHeap // 争议窗口右端点的最小堆（惰性删除）
	seq    uint64
}

// Result 是每个被接受操作后的可复现快照。
type Result struct {
	OrderID            string
	At                 int64
	Stage              Stage
	Landed             bool // 本次操作（含到期落地）是否完成一次取消
	Pending            bool // 是否处于争议待决
	WindowEnd          int64
	Liable             Party
	Reason             string
	Ledger             Ledger
	RiderCancellations int
}

// NewSystem 校验构造参数并创建系统。
func NewSystem(p Params) (*System, error) {
	if p.DisputeWindow < 0 || p.LateTolerance < 0 ||
		p.LossBasisPoints < 0 || p.LossBasisPoints > 10000 ||
		p.MerchantPenalty < 0 || p.RiderCompensation < 0 {
		return nil, errInvalidParam
	}
	return &System{params: p, orders: map[string]*orderState{}}, nil
}

// CreateOrder 在时刻 t 创建一笔已支付订单。
func (s *System) CreateOrder(t int64, id string, spec OrderSpec) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := spec.Amounts
	if t < 0 || id == "" || a.Goods < 0 || a.Packing < 0 || a.Delivery < 0 ||
		a.Coupon < 0 || a.Coupon > a.Goods {
		return nil, errInvalidParam
	}
	if t < s.now {
		return nil, errClockBack
	}
	if _, ok := s.orders[id]; ok {
		return nil, errInvalidParam
	}
	s.now = t
	o := &orderState{id: id, amounts: a, promised: spec.PromisedTime, stage: StagePaid}
	s.orders[id] = o
	return s.snapshot(o, t), nil
}

// Accept 商家接单：已支付 -> 已接单。
func (s *System) Accept(t int64, id string) (*Result, error) {
	return s.advance(t, id, StagePaid, StageAccepted)
}

// Assign 平台派单（也用于骑手取消后的改派）：已接单 -> 已派。
func (s *System) Assign(t int64, id, riderID string) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.begin(t, id)
	if err != nil {
		return nil, err
	}
	if riderID == "" {
		return nil, errInvalidParam
	}
	if err := s.checkAdvance(o, StageAccepted); err != nil {
		return nil, err
	}
	o.stage = StageAssigned
	o.riderID = riderID
	return s.snapshot(o, t), nil
}

// Pickup 骑手取货：已派 -> 已取货；争议待决期间被拒。
func (s *System) Pickup(t int64, id, riderID string) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.begin(t, id)
	if err != nil {
		return nil, err
	}
	if err := s.checkAdvance(o, StageAssigned); err != nil {
		return nil, err
	}
	if o.pending == disputeOpen {
		return nil, errCancelPending
	}
	if riderID == "" || riderID != o.riderID {
		return nil, errUnauthorized
	}
	o.stage = StagePicked
	return s.snapshot(o, t), nil
}

// Deliver 确认送达：已取货 -> 已送达。
func (s *System) Deliver(t int64, id, riderID string) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.begin(t, id)
	if err != nil {
		return nil, err
	}
	if err := s.checkAdvance(o, StagePicked); err != nil {
		return nil, err
	}
	if riderID == "" || riderID != o.riderID {
		return nil, errUnauthorized
	}
	o.stage = StageDelivered
	return s.snapshot(o, t), nil
}

func (s *System) advance(t int64, id string, from, to Stage) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.begin(t, id)
	if err != nil {
		return nil, err
	}
	if err := s.checkAdvance(o, from); err != nil {
		return nil, err
	}
	o.stage = to
	return s.snapshot(o, t), nil
}

// UserCancel 用户发起取消。
func (s *System) UserCancel(t int64, id string) (*Result, error) {
	return s.cancel(t, id, PartyUser)
}

// MerchantCancel 商家发起取消。
func (s *System) MerchantCancel(t int64, id string) (*Result, error) {
	return s.cancel(t, id, PartyMerchant)
}

// PlatformCancel 平台发起取消。
func (s *System) PlatformCancel(t int64, id string) (*Result, error) {
	return s.cancel(t, id, PartyPlatform)
}

// RiderCancel 骑手取消：仅已派未取货允许，订单回到已接单等待改派，记一次骑手取消。
func (s *System) RiderCancel(t int64, id, riderID string) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.begin(t, id)
	if err != nil {
		return nil, err
	}
	if riderID == "" {
		return nil, errInvalidParam
	}
	if o.landed || o.stage == StageDelivered {
		return nil, errTerminal
	}
	if o.pending == disputeOpen {
		return nil, errCancelPending
	}
	if o.stage == StagePicked {
		return nil, errPickedUp
	}
	if o.stage != StageAssigned || riderID != o.riderID {
		return nil, errUnauthorized
	}
	o.stage = StageAccepted
	o.riderID = ""
	o.riderCancs++
	return s.snapshot(o, t), nil
}

func (s *System) cancel(t int64, id string, actor Party) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.begin(t, id)
	if err != nil {
		return nil, err
	}
	if o.landed || o.stage == StageDelivered {
		return nil, errTerminal
	}
	if o.pending == disputeOpen {
		return nil, errCancelPending
	}
	d := adjudicate(o, s.params, actor, t)
	if actor == PartyUser && o.stage == StagePicked && !d.Landed && !d.Pending {
		return nil, errNotCancellable
	}
	if d.Pending {
		o.pending = disputeOpen
		o.windowEnd = d.EndsAt
		s.seq++
		o.windowSeq = s.seq
		s.ends.Push(windowEnd{end: d.EndsAt, id: id, seq: s.seq})
	}
	return s.snapshot(o, t), nil
}

// ClaimPreparation 商家在争议窗口内声明已开始备餐；右端点及之后报声明超时。
func (s *System) ClaimPreparation(t int64, id string) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.begin(t, id)
	if err != nil {
		return nil, err
	}
	if o.landed || o.stage == StageDelivered {
		return nil, errTerminal
	}
	if o.pending != disputeOpen {
		return nil, errNoDispute
	}
	if t >= o.windowEnd { // 恰在右端点声明不允许
		return nil, errWindowTimeout
	}
	claimDispute(o, s.params, t)
	return s.snapshot(o, t), nil
}

// WaiveClaim 商家主动放弃备餐声明，取消立即以用户无责落地。
func (s *System) WaiveClaim(t int64, id string) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.begin(t, id)
	if err != nil {
		return nil, err
	}
	if o.landed || o.stage == StageDelivered {
		return nil, errTerminal
	}
	if o.pending != disputeOpen {
		return nil, errNoDispute
	}
	waiveDispute(o, s.params, t)
	return s.snapshot(o, t), nil
}

// Advance 推进全局时钟到 t，并让所有右端点 <= t 的争议窗口到期落地。
// 其它被接受操作只会先行落地右端点 < t 的窗口（右端点取等仍待决）。
func (s *System) Advance(t int64) ([]*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t < 0 {
		return nil, errInvalidParam
	}
	if t < s.now {
		return nil, errClockBack
	}
	s.now = t
	landed := s.sweep(t, true)
	out := make([]*Result, 0, len(landed))
	for _, o := range landed {
		out = append(out, s.snapshot(o, t))
	}
	return out, nil
}

// Stage 查询订单当前阶段（只读，不推进时钟）。
func (s *System) Stage(id string) (Stage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[id]
	if !ok {
		return StagePaid, errNoOrder
	}
	return o.stage, nil
}

// begin 执行拒绝次序的公共前缀：参数 -> 时钟 -> 订单存在 -> 到期窗口落地。
func (s *System) begin(t int64, id string) (*orderState, error) {
	if t < 0 || id == "" {
		return nil, errInvalidParam
	}
	if t < s.now {
		return nil, errClockBack
	}
	o, ok := s.orders[id]
	if !ok {
		return nil, errNoOrder
	}
	s.now = t
	s.sweep(t, false) // 严格 end < t：右端点取等时争议仍待决
	return o, nil
}

func (s *System) checkAdvance(o *orderState, want Stage) error {
	if o.landed || o.stage == StageDelivered {
		return errTerminal
	}
	if o.stage != want {
		return errStageOrder
	}
	return nil
}

// sweep 弹出所有到期窗口。inclusive 为 false 时仅处理 end < t（普通操作），
// 为 true 时处理 end <= t（Advance 显式到期）。每条窗口记录至多弹出一次，
// 已被声明/放弃/其它方式终结的订单条目在弹出时惰性跳过。
func (s *System) sweep(t int64, inclusive bool) []*orderState {
	var landed []*orderState
	for s.ends.Len() > 0 {
		top := s.ends[0]
		if top.end > t || (!inclusive && top.end == t) {
			break
		}
		s.ends.Pop()
		o := s.orders[top.id]
		if o == nil || o.pending != disputeOpen || o.windowSeq != top.seq {
			continue
		}
		expireDispute(o, s.params)
		landed = append(landed, o)
	}
	return landed
}

func (s *System) snapshot(o *orderState, at int64) *Result {
	r := &Result{
		OrderID: o.id, At: at, Stage: o.stage,
		Landed: o.landed, Liable: o.liable, Reason: o.reason,
		Ledger: o.ledger, RiderCancellations: o.riderCancs,
	}
	if o.pending == disputeOpen {
		r.Pending = true
		r.WindowEnd = o.windowEnd
	}
	return r
}

// windowEnd 是争议到期堆条目。
type windowEnd struct {
	end int64
	id  string
	seq uint64
}

// windowHeap 按 end 排序的最小堆，支持 O(log n) 插入与到期弹出。
type windowHeap []windowEnd

func (h windowHeap) Len() int { return len(h) }
func (h windowHeap) Less(i, j int) bool {
	if h[i].end != h[j].end {
		return h[i].end < h[j].end
	}
	if h[i].seq != h[j].seq {
		return h[i].seq < h[j].seq
	}
	return h[i].id < h[j].id
}
func (h windowHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *windowHeap) Push(x windowEnd) {
	*h = append(*h, x)
	h.up(len(*h) - 1)
}

func (h *windowHeap) Pop() windowEnd {
	old := *h
	n := len(old)
	top := old[0]
	old[0] = old[n-1]
	*h = old[:n-1]
	if n > 1 {
		h.down(0)
	}
	return top
}

func (h windowHeap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !h.Less(i, parent) {
			break
		}
		h.Swap(i, parent)
		i = parent
	}
}

func (h windowHeap) down(i int) {
	n := len(h)
	for {
		left := 2*i + 1
		if left >= n {
			break
		}
		j := left
		if right := left + 1; right < n && h.Less(right, left) {
			j = right
		}
		if !h.Less(j, i) {
			break
		}
		h.Swap(i, j)
		i = j
	}
}
