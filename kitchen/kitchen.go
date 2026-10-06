// Package kitchen 实现商家出餐节奏与压单控制系统。
//
// 模块划分：
//   - estimate.go：制作队列与开工推定（纯函数，可精确复现）。
//   - throttle.go：压单状态机与准入阈值判定，产生可查询的进出事件。
//   - kitchen.go：订单生命周期、全局单调时钟、手动暂停与并发协调。
//   - errors.go：可程序化区分的哨兵错误。
//
// 并发：所有公开方法可并发调用，内部由单把互斥锁串行化，
// 结果等价于某个串行顺序；相同操作序列重放得到相同结果。
package kitchen

import (
	"fmt"
	"sort"
	"sync"
)

// Config 是商家厨房的构造参数，时刻与时长均为整数秒。
type Config struct {
	ParallelLimit   int   // 并行制作上限（>=1）
	EnterThreshold  int64 // 压单进入阈值
	ExitThreshold   int64 // 压单退出阈值，严格小于进入阈值
	BurstThreshold  int64 // 爆单阈值，严格大于进入阈值
	ReservationLead int64 // 预约单提前量
}

func (c Config) validate() error {
	if c.ParallelLimit < 1 ||
		c.ExitThreshold < 0 ||
		c.EnterThreshold <= c.ExitThreshold ||
		c.BurstThreshold <= c.EnterThreshold ||
		c.ReservationLead < 0 {
		return fmt.Errorf("%w: %+v", ErrInvalidArgument, c)
	}
	return nil
}

type orderState int

const (
	stateWaiting orderState = iota
	stateCooking
	stateCompleted
	stateCancelled
)

func (s orderState) String() string {
	switch s {
	case stateWaiting:
		return "waiting"
	case stateCooking:
		return "cooking"
	case stateCompleted:
		return "completed"
	case stateCancelled:
		return "cancelled"
	}
	return "unknown"
}

type order struct {
	id             string
	duration       int64
	reservation    bool
	acceptTime     int64
	targetPickup   int64 // 仅预约单：目标取货时刻
	targetStart    int64 // 仅预约单：目标开工时刻 = 目标取货时刻 - 制作时长
	seq            int64
	state          orderState
	slot           int
	startTime      int64
	promisedPickup int64
	pressured      bool
}

// AcceptResult 描述一次接单准入的结果。
type AcceptResult struct {
	Pressured      bool  // 是否按压单单受理
	PromisedPickup int64 // 承诺取货时刻
	EstimatedWait  int64 // 预计等待（预约单为距目标开工的时长）
}

// OrderInfo 是订单状态快照，用于查询与测试。
type OrderInfo struct {
	State          string
	StartTime      int64
	PromisedPickup int64
	Pressured      bool
}

// Kitchen 是单个商家的出餐节奏与压单控制系统，所有方法可并发调用。
type Kitchen struct {
	mu       sync.Mutex
	cfg      Config
	clock    int64
	clockSet bool
	paused   bool
	seq      int64

	slotBusy   []bool
	slotFreeAt []int64 // 制作位实际空出时刻（仅空闲时有意义）
	slotEstEnd []int64 // 制作中订单的推定完成时刻

	orders map[string]*order // 全部出现过的订单（含终态，用于存在性判定）
	queue  []*order          // 等待中的即时单，按接单次序
	resv   []*order          // 等待中的预约单，按 (目标开工时刻, 接单序号) 排序

	throttle throttle
	estOps   int64 // 开工推定累计调度的订单数，用于验证推定开销与历史无关
}

// NewKitchen 构造一个商家厨房；参数非法时报 ErrInvalidArgument。
func NewKitchen(cfg Config) (*Kitchen, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	k := &Kitchen{
		cfg:        cfg,
		orders:     make(map[string]*order),
		slotBusy:   make([]bool, cfg.ParallelLimit),
		slotFreeAt: make([]int64, cfg.ParallelLimit),
		slotEstEnd: make([]int64, cfg.ParallelLimit),
		throttle:   newThrottle(cfg.EnterThreshold, cfg.ExitThreshold),
	}
	return k, nil
}

// checkClock 只校验时钟，不推进；时钟仅在被接受操作的末尾推进。
func (k *Kitchen) checkClock(now int64) error {
	if k.clockSet && now < k.clock {
		return fmt.Errorf("%w: %d < %d", ErrClockRegression, now, k.clock)
	}
	return nil
}

func (k *Kitchen) acceptClock(now int64) {
	k.clock = now
	k.clockSet = true
}

// AcceptInstant 受理一笔即时单。
func (k *Kitchen) AcceptInstant(now int64, id string, duration int64) (AcceptResult, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if now < 0 || duration <= 0 {
		return AcceptResult{}, fmt.Errorf("%w: now=%d duration=%d", ErrInvalidArgument, now, duration)
	}
	if err := k.checkClock(now); err != nil {
		return AcceptResult{}, err
	}
	if _, ok := k.orders[id]; ok {
		return AcceptResult{}, fmt.Errorf("%w: %s", ErrOrderExists, id)
	}
	if k.paused {
		return AcceptResult{}, ErrMerchantPaused
	}
	wait := k.estimateWait(now, duration)
	if wait >= k.cfg.BurstThreshold {
		return AcceptResult{}, fmt.Errorf("%w: 预计等待 %d >= 爆单阈值 %d", ErrBurst, wait, k.cfg.BurstThreshold)
	}
	k.acceptClock(now)
	o := &order{id: id, duration: duration, acceptTime: now, seq: k.seq, state: stateWaiting, slot: -1}
	k.seq++
	if wait >= k.cfg.EnterThreshold {
		o.pressured = true
		o.promisedPickup = now + wait + duration // 推定完成时刻
	} else {
		o.promisedPickup = now + duration + k.cfg.EnterThreshold
	}
	k.orders[id] = o
	k.queue = append(k.queue, o)
	k.throttle.onAdmit(wait, now)
	k.processStarts(now)
	return AcceptResult{Pressured: o.pressured, PromisedPickup: o.promisedPickup, EstimatedWait: wait}, nil
}

// AcceptReservation 受理一笔预约单，准入只看目标开工时刻是否不早于
// 当前时刻+预约单提前量，不受压单与爆单影响。
func (k *Kitchen) AcceptReservation(now int64, id string, duration, targetPickup int64) (AcceptResult, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if now < 0 || duration <= 0 {
		return AcceptResult{}, fmt.Errorf("%w: now=%d duration=%d", ErrInvalidArgument, now, duration)
	}
	if err := k.checkClock(now); err != nil {
		return AcceptResult{}, err
	}
	if _, ok := k.orders[id]; ok {
		return AcceptResult{}, fmt.Errorf("%w: %s", ErrOrderExists, id)
	}
	if k.paused {
		return AcceptResult{}, ErrMerchantPaused
	}
	targetStart := targetPickup - duration
	if targetStart < now+k.cfg.ReservationLead {
		return AcceptResult{}, fmt.Errorf("%w: 目标开工时刻 %d < %d", ErrReservationTooSoon, targetStart, now+k.cfg.ReservationLead)
	}
	k.acceptClock(now)
	o := &order{
		id: id, duration: duration, reservation: true, acceptTime: now,
		targetPickup: targetPickup, targetStart: targetStart,
		seq: k.seq, state: stateWaiting, slot: -1, promisedPickup: targetPickup,
	}
	k.seq++
	k.orders[id] = o
	k.insertReservation(o)
	k.processStarts(now)
	return AcceptResult{PromisedPickup: targetPickup, EstimatedWait: targetStart - now}, nil
}

// Complete 报告订单完成，允许提前完成并立即释放制作位。
func (k *Kitchen) Complete(now int64, id string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if now < 0 {
		return fmt.Errorf("%w: now=%d", ErrInvalidArgument, now)
	}
	if err := k.checkClock(now); err != nil {
		return err
	}
	o, ok := k.orders[id]
	if !ok || o.state == stateCancelled {
		return fmt.Errorf("%w: %s", ErrOrderNotFound, id)
	}
	switch o.state {
	case stateWaiting:
		return fmt.Errorf("%w: %s", ErrNotStarted, id)
	case stateCompleted:
		return fmt.Errorf("%w: %s", ErrAlreadyCompleted, id)
	}
	if now < o.startTime {
		return fmt.Errorf("%w: 完成时刻 %d 早于开工时刻 %d", ErrInvalidArgument, now, o.startTime)
	}
	k.acceptClock(now)
	o.state = stateCompleted
	k.slotBusy[o.slot] = false
	k.slotFreeAt[o.slot] = now
	k.processStarts(now)
	k.throttle.onReestimate(k.estimateWait(now, 1), now)
	return nil
}

// Cancel 取消一笔排队中的订单，立即释放其队列位置。
func (k *Kitchen) Cancel(now int64, id string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if now < 0 {
		return fmt.Errorf("%w: now=%d", ErrInvalidArgument, now)
	}
	if err := k.checkClock(now); err != nil {
		return err
	}
	o, ok := k.orders[id]
	if !ok || o.state == stateCancelled {
		return fmt.Errorf("%w: %s", ErrOrderNotFound, id)
	}
	switch o.state {
	case stateCompleted:
		return fmt.Errorf("%w: %s", ErrAlreadyCompleted, id)
	case stateCooking:
		return fmt.Errorf("%w: %s", ErrAlreadyStarted, id)
	}
	k.acceptClock(now)
	o.state = stateCancelled
	if o.reservation {
		k.resv = removeOrder(k.resv, o)
	} else {
		k.queue = removeOrder(k.queue, o)
	}
	k.processStarts(now)
	k.throttle.onReestimate(k.estimateWait(now, 1), now)
	return nil
}

// Pause 手动暂停接单：新即时单与新预约单一律报 ErrMerchantPaused，
// 排队中与制作中的订单照常推进；不改变压单状态记录。
func (k *Kitchen) Pause(now int64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if now < 0 {
		return fmt.Errorf("%w: now=%d", ErrInvalidArgument, now)
	}
	if err := k.checkClock(now); err != nil {
		return err
	}
	k.acceptClock(now)
	k.paused = true
	k.processStarts(now)
	return nil
}

// Resume 恢复接单，状态从当时的队列重新推定。
func (k *Kitchen) Resume(now int64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if now < 0 {
		return fmt.Errorf("%w: now=%d", ErrInvalidArgument, now)
	}
	if err := k.checkClock(now); err != nil {
		return err
	}
	k.acceptClock(now)
	k.paused = false
	k.processStarts(now)
	return nil
}

// Pressured 查询当前是否处于压单状态。
func (k *Kitchen) Pressured() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.throttle.pressured
}

// PressureEvents 返回压单进出事件的副本，进入与退出严格交替。
func (k *Kitchen) PressureEvents() []PressureEvent {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]PressureEvent(nil), k.throttle.events...)
}

// Order 查询订单状态快照。
func (k *Kitchen) Order(id string) (OrderInfo, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	o, ok := k.orders[id]
	if !ok {
		return OrderInfo{}, false
	}
	return OrderInfo{
		State:          o.state.String(),
		StartTime:      o.startTime,
		PromisedPickup: o.promisedPickup,
		Pressured:      o.pressured,
	}, true
}

// CookingCount 查询当前制作中的订单数，任何交错下都不超过并行上限。
func (k *Kitchen) CookingCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for _, busy := range k.slotBusy {
		if busy {
			n++
		}
	}
	return n
}

// EstimateOps 返回开工推定累计调度的订单数。
// 每次推定的增量只等于当时参与推定的等待订单数（含假想单），
// 与历史已完成订单数无关，可用于验证性能约束。
func (k *Kitchen) EstimateOps() int64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.estOps
}

// PreviewWait 只读推定一笔假想即时单的预计等待，不改变任何状态与时钟。
func (k *Kitchen) PreviewWait(now, duration int64) int64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.clockSet && now < k.clock {
		now = k.clock
	}
	return k.estimateWait(now, duration)
}

// estimateWait 推定一笔此刻到达、时长为 phantomDuration 的假想即时单的预计等待。
func (k *Kitchen) estimateWait(now int64, phantomDuration int64) int64 {
	waiting := make([]waitOrder, 0, len(k.queue)+len(k.resv)+1)
	for _, o := range k.queue {
		waiting = append(waiting, waitOrder{duration: o.duration, seq: o.seq})
	}
	for _, o := range k.resv {
		waiting = append(waiting, waitOrder{duration: o.duration, release: o.targetStart, resv: true, seq: o.seq})
	}
	phantom := len(waiting)
	waiting = append(waiting, waitOrder{duration: phantomDuration, seq: k.seq})
	busy := make([]int64, k.cfg.ParallelLimit)
	for i := range busy {
		switch {
		case k.slotBusy[i] && k.slotEstEnd[i] > now:
			busy[i] = k.slotEstEnd[i]
		case k.slotBusy[i]:
			busy[i] = now
		default:
			busy[i] = k.slotFreeAt[i]
		}
	}
	starts := estimateStarts(now, busy, waiting)
	k.estOps += int64(len(waiting))
	return starts[phantom] - now
}

// processStarts 在制作位空出时按既定次序开工：
// 已到达目标开工时刻的预约单优先于一切尚未开工的即时单，即时单按接单次序；
// 开工时刻取空出时刻与订单可开工时刻的较晚者。
func (k *Kitchen) processStarts(now int64) {
	for {
		slot := -1
		for i := range k.slotBusy {
			if !k.slotBusy[i] && k.slotFreeAt[i] <= now &&
				(slot < 0 || k.slotFreeAt[i] < k.slotFreeAt[slot]) {
				slot = i
			}
		}
		if slot < 0 {
			return
		}
		var next *order
		if len(k.resv) > 0 && k.resv[0].targetStart <= now {
			next = k.resv[0]
			k.resv = k.resv[1:]
		} else if len(k.queue) > 0 {
			next = k.queue[0]
			k.queue = k.queue[1:]
		} else {
			return
		}
		start := k.slotFreeAt[slot]
		if next.reservation {
			if next.targetStart > start {
				start = next.targetStart
			}
		} else if next.acceptTime > start {
			start = next.acceptTime
		}
		next.state = stateCooking
		next.startTime = start
		next.slot = slot
		k.slotBusy[slot] = true
		k.slotEstEnd[slot] = start + next.duration
	}
}

func (k *Kitchen) insertReservation(o *order) {
	i := sort.Search(len(k.resv), func(i int) bool {
		r := k.resv[i]
		return r.targetStart > o.targetStart ||
			(r.targetStart == o.targetStart && r.seq > o.seq)
	})
	k.resv = append(k.resv, nil)
	copy(k.resv[i+1:], k.resv[i:])
	k.resv[i] = o
}

func removeOrder(list []*order, o *order) []*order {
	for i, x := range list {
		if x == o {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}
