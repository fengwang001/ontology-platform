package taxipool

import (
	"sort"
	"sync"
	"time"
)

// JoinResult 描述一次入池的判定结果。
type JoinResult struct {
	Terminal      string
	Position      int // 入池后前方人数
	Priority      bool
	VoucherIssued bool
	VoucherUsed   bool
}

// DispatchResult 描述一次放行的判定结果。
type DispatchResult struct {
	Driver          string
	RequestTerminal string
	SourceTerminal  string
	Transferred     bool
	Deadline        time.Time
}

// driverState 是司机的生命周期状态。
type driverState int

const (
	stateIdle       driverState = iota // 在池外（可能持有凭证）
	stateQueued                        // 在某条队列中
	stateDispatched                    // 已放行、等待到达
	stateServing                       // 已到达、载客服务中
)

type driverInfo struct {
	id          string
	state       driverState
	terminal    string // queued/dispatched 时所在/来源候机楼
	enqAt       time.Time
	priority    bool
	deadline    time.Time // dispatched 到达时限
	transferred bool
	noShows     int
	banUntil    time.Time // 零值表示未禁入
	// 最近一次“载客离开”（完成行程）的时刻与距离，用于短途返回判定。
	lastLeftAt time.Time
	lastDist   float64
}

// Pool 是蓄车池系统，单把互斥锁串行化所有状态变更，因此并发调用的结果
// 等价于某个按锁获取顺序排列的串行历史；相同操作序列重放结果完全一致。
type Pool struct {
	cfg      *Config
	mu       sync.Mutex
	now      time.Time // 上一次被接受操作的时刻
	drivers  map[string]*driverInfo
	queues   map[string]*terminalQueue
	termIDs  []string // 有序候机楼标识，保证可复现
	vouchers *voucherLedger
}

// New 创建蓄车池；配置非法时返回包裹 ErrInvalidParam 的错误。
func New(cfg *Config) (*Pool, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(cfg.Terminals))
	queues := make(map[string]*terminalQueue, len(cfg.Terminals))
	for id, capv := range cfg.Terminals {
		ids = append(ids, id)
		queues[id] = newTerminalQueue(id, capv)
	}
	sort.Strings(ids)
	return &Pool{
		cfg:      cfg,
		drivers:  make(map[string]*driverInfo),
		queues:   queues,
		termIDs:  ids,
		vouchers: newVoucherLedger(cfg),
	}, nil
}

func (p *Pool) checkClockLocked(op string, at time.Time) error {
	if !p.now.IsZero() && at.Before(p.now) {
		return opErr(op, KindClockRewind, "at="+at.String())
	}
	return nil
}

// applyNoShowLocked 对一名已逾期的放行司机记一次爽约；达到次数即从 deadline
// 起禁入 BanDuration，并清零累计（新一轮重新累计）。
func (p *Pool) applyNoShowLocked(d *driverInfo) {
	d.noShows++
	if d.noShows >= p.cfg.NoShowLimit {
		d.banUntil = d.deadline.Add(p.cfg.BanDuration)
		d.noShows = 0
	}
	d.state = stateIdle
	d.terminal = ""
	d.priority = false
}

// finalizeNoShows 由被接受操作的时钟推进驱动：除 skip 外，放行已到期
// （deadline <= at，取等即逾期）且仍在放行中的司机一律判为爽约。
func (p *Pool) finalizeNoShows(at time.Time, skip string) {
	for _, d := range p.drivers {
		if d.id == skip || d.state != stateDispatched {
			continue
		}
		if !at.Before(d.deadline) {
			p.applyNoShowLocked(d)
		}
	}
}

func bannedNow(d *driverInfo, at time.Time) bool {
	return !d.banUntil.IsZero() && at.Before(d.banUntil)
}

// RegisterDriver 注册司机；已存在时幂等成功（重复注册不改变任何状态）。
func (p *Pool) RegisterDriver(driver string, at time.Time) error {
	const op = "RegisterDriver"
	if driver == "" || at.IsZero() {
		return opErr(op, KindInvalidParam, "driver/at 不能为空")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkClockLocked(op, at); err != nil {
		return err
	}
	p.now = at
	if _, ok := p.drivers[driver]; !ok {
		p.drivers[driver] = &driverInfo{id: driver}
	}
	return nil
}

// Join 入池排队。若司机上一次行程为短途且在返回时限内再次入池，将发放一张
// 优先凭证；凭证在满员/禁入导致入池被拒时不消耗，但时效自发放时刻起算。
func (p *Pool) Join(driver, terminal string, at time.Time) (*JoinResult, error) {
	const op = "Join"
	if driver == "" || terminal == "" || at.IsZero() {
		return nil, opErr(op, KindInvalidParam, "driver/terminal/at 不能为空")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkClockLocked(op, at); err != nil {
		return nil, err
	}
	q := p.queues[terminal]
	if q == nil {
		return nil, opErr(op, KindTerminalNotFound, terminal)
	}
	d := p.drivers[driver]
	if d == nil {
		return nil, opErr(op, KindDriverNotFound, driver)
	}
	// 接受时钟推进：先由本操作的时钟确定性地处置所有已逾期的放行。
	p.finalizeNoShows(at, "")
	if d.state != stateIdle {
		return nil, opErr(op, KindAlreadyQueued, driver)
	}
	// 凭证评估（发放先于禁入/满员检查：被拒时凭证已发放且不消耗）。
	v, issued := p.vouchers.evaluate(evaluateParams{
		driver: driver, at: at,
		leftAt: d.lastLeftAt, distance: d.lastDist,
	})
	if issued {
		// 短途机会已用：无论后续是否被拒，该次离开记录都被消费。
		d.lastLeftAt = time.Time{}
	}
	if bannedNow(d, at) {
		return nil, opErr(op, KindDriverBanned, "禁入至 "+d.banUntil.Format(time.RFC3339Nano))
	}
	if q.full() {
		return nil, opErr(op, KindQueueFull, terminal)
	}
	// 凭证时效：入池时刻恰等于过期时刻视为已过期，按普通司机处理（凭证作废）。
	usePriority := v != nil && v.usableAt(at)
	if usePriority && q.priorityCount() >= p.cfg.PrioritySlots {
		// 优先名额已满：按普通入队，凭证第一次受限保留、第二次受限作废。
		p.vouchers.noteRestricted(driver)
		usePriority = false
	}
	if usePriority {
		p.vouchers.consume(driver)
	}
	pos := q.enqueue(driver, at, usePriority)
	d.state = stateQueued
	d.terminal = terminal
	d.enqAt = at
	d.priority = usePriority
	p.now = at
	return &JoinResult{
		Terminal:      terminal,
		Position:      pos,
		Priority:      usePriority,
		VoucherIssued: issued,
		VoucherUsed:   usePriority,
	}, nil
}

// Dispatch 为某候机楼上客点放行一辆车；本队列空时按规则跨候机楼调剂。
// 调剂只取普通司机：备选队列队首是优先司机时该队列不参与调剂。
func (p *Pool) Dispatch(requestTerminal string, at time.Time) (*DispatchResult, error) {
	const op = "Dispatch"
	if requestTerminal == "" || at.IsZero() {
		return nil, opErr(op, KindInvalidParam, "requestTerminal/at 不能为空")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkClockLocked(op, at); err != nil {
		return nil, err
	}
	rq := p.queues[requestTerminal]
	if rq == nil {
		return nil, opErr(op, KindTerminalNotFound, requestTerminal)
	}
	p.finalizeNoShows(at, "")

	source := rq
	transferred := false
	if _, ok := rq.front(); !ok {
		// 调剂：队首入池时刻最早者，并列时候机楼标识字典序最小者。
		var donor *terminalQueue
		var best time.Time
		for _, id := range p.termIDs {
			cand := p.queues[id]
			if cand == rq {
				continue
			}
			f, ok := cand.front()
			if !ok || f.priority {
				continue
			}
			if donor == nil || f.enqAt.Before(best) ||
				(f.enqAt.Equal(best) && cand.id < donor.id) {
				donor, best = cand, f.enqAt
			}
		}
		if donor == nil {
			return nil, opErr(op, KindNoTaxi, requestTerminal)
		}
		source, transferred = donor, true
	}
	entry, ok := source.popFront()
	if !ok {
		return nil, opErr(op, KindNoTaxi, requestTerminal)
	}
	d := p.drivers[entry.driver]
	limit := p.cfg.ArrivalLimit
	if transferred {
		limit = p.cfg.TransferLimit
	}
	d.state = stateDispatched
	d.terminal = source.id
	d.transferred = transferred
	d.deadline = at.Add(limit)
	d.priority = entry.priority
	p.now = at
	return &DispatchResult{
		Driver:          d.id,
		RequestTerminal: requestTerminal,
		SourceTerminal:  source.id,
		Transferred:     transferred,
		Deadline:        d.deadline,
	}, nil
}

// Arrive 上报放行司机到达上客点；恰到期那一刻上报视为逾期并记爽约。
func (p *Pool) Arrive(driver string, at time.Time) error {
	const op = "Arrive"
	if driver == "" || at.IsZero() {
		return opErr(op, KindInvalidParam, "driver/at 不能为空")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkClockLocked(op, at); err != nil {
		return err
	}
	d := p.drivers[driver]
	if d == nil {
		return opErr(op, KindDriverNotFound, driver)
	}
	// 先处置其它逾期者，使时钟推进的副作用确定；本人保留到下方专门判定，
	// 以便精确返回 KindArrivalOverdue（而非 KindNotDispatched）。
	p.finalizeNoShows(at, driver)
	if d.state != stateDispatched {
		return opErr(op, KindNotDispatched, driver)
	}
	if !at.Before(d.deadline) {
		p.applyNoShowLocked(d)
		p.now = at
		return opErr(op, KindArrivalOverdue, "deadline="+d.deadline.String())
	}
	d.state = stateServing
	d.priority = false
	p.now = at
	return nil
}

// CompleteTrip 记一次载客完成（司机离开上客点），保存离开时刻与行程距离，
// 供下一次入池判定短途返回。
func (p *Pool) CompleteTrip(driver string, at time.Time, distanceMeters float64) error {
	const op = "CompleteTrip"
	if driver == "" || at.IsZero() || distanceMeters < 0 {
		return opErr(op, KindInvalidParam, "driver/at/distance 非法")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkClockLocked(op, at); err != nil {
		return err
	}
	d := p.drivers[driver]
	if d == nil {
		return opErr(op, KindDriverNotFound, driver)
	}
	p.finalizeNoShows(at, "")
	if d.state != stateServing {
		return opErr(op, KindNotDispatched, "司机不处于服务中")
	}
	d.state = stateIdle
	d.terminal = ""
	d.lastLeftAt = at
	d.lastDist = distanceMeters
	p.now = at
	return nil
}

// Leave 司机主动离队；离队不消耗凭证也不计爽约。
// 放行后到达之前不可离队，须按到达或爽约处理。
func (p *Pool) Leave(driver string, at time.Time) error {
	const op = "Leave"
	if driver == "" || at.IsZero() {
		return opErr(op, KindInvalidParam, "driver/at 不能为空")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkClockLocked(op, at); err != nil {
		return err
	}
	d := p.drivers[driver]
	if d == nil {
		return opErr(op, KindDriverNotFound, driver)
	}
	p.finalizeNoShows(at, "")
	if d.state == stateDispatched {
		return opErr(op, KindCannotLeaveDispatched, driver)
	}
	if d.state != stateQueued {
		return opErr(op, KindNotInQueue, driver)
	}
	p.queues[d.terminal].remove(driver)
	d.state = stateIdle
	d.terminal = ""
	d.priority = false
	p.now = at
	return nil
}

// Position 查询司机在其队列中的位置（前方人数）。查询不推进时钟、不产生
// 副作用；内部只遍历该队列当前节点（步数见 LastPositionScan），与历史
// 入池总数无关，可在测试中用“先产生大量历史再查”的方式验证。
func (p *Pool) Position(driver string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.drivers[driver]
	if d == nil {
		return 0, opErr("Position", KindDriverNotFound, driver)
	}
	if d.state != stateQueued {
		return 0, opErr("Position", KindNotInQueue, driver)
	}
	pos, ok := p.queues[d.terminal].position(driver)
	if !ok {
		return 0, opErr("Position", KindNotInQueue, driver)
	}
	return pos, nil
}

// LastPositionScan 返回某队列最近一次位置查询实际遍历的节点数（验证用）。
func (p *Pool) LastPositionScan(terminal string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if q := p.queues[terminal]; q != nil {
		return q.LastPositionScan()
	}
	return 0
}

// QueueEntry 是队列快照中的一个司机。
type QueueEntry struct {
	Driver   string
	Priority bool
	EnqAt    time.Time
}

// Snapshot 返回某候机楼队列从队首到队尾的快照，供复现与对照。
func (p *Pool) Snapshot(terminal string) ([]QueueEntry, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	q := p.queues[terminal]
	if q == nil {
		return nil, opErr("Snapshot", KindTerminalNotFound, terminal)
	}
	es := q.snapshot()
	out := make([]QueueEntry, len(es))
	for i, e := range es {
		out[i] = QueueEntry{Driver: e.driver, Priority: e.priority, EnqAt: e.enqAt}
	}
	return out, nil
}

// DriverView 是司机状态的只读视图（测试/文档复现用）。
type DriverView struct {
	State         string
	Terminal      string
	NoShows       int
	BanUntil      time.Time
	HasVoucher    bool
	VoucherExpiry time.Time
}

// Inspect 返回司机当前状态视图。
func (p *Pool) Inspect(driver string) (DriverView, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.drivers[driver]
	if d == nil {
		return DriverView{}, opErr("Inspect", KindDriverNotFound, driver)
	}
	stateName := map[driverState]string{
		stateIdle: "idle", stateQueued: "queued",
		stateDispatched: "dispatched", stateServing: "serving",
	}[d.state]
	v := p.vouchers.pendingVoucher(driver)
	view := DriverView{
		State: stateName, Terminal: d.terminal, NoShows: d.noShows,
		BanUntil: d.banUntil, HasVoucher: v != nil,
	}
	if v != nil {
		view.VoucherExpiry = v.ExpiresAt
	}
	return view, nil
}

// Now 返回系统已接受的最新操作时刻。
func (p *Pool) Now() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.now
}
