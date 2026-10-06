package taxipool

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Pool 为机场出租车蓄车池。所有操作可并发调用，内部以单互斥锁串行化，
// 结果等价于按锁获得顺序的某个串行执行。
type Pool struct {
	mu        sync.Mutex
	cfg       Config
	terminals map[string]*terminal
	drivers   map[string]*Driver
	clock     int64 // 最近一次被接受操作的时刻
}

type terminal struct {
	id            string
	capacity      int
	root          *node
	size          int
	priorityCount int
}

func NewPool(cfg Config) (*Pool, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	terminals := make(map[string]*terminal, len(cfg.Terminals))
	for id, capacity := range cfg.Terminals {
		terminals[id] = &terminal{id: id, capacity: capacity}
	}
	return &Pool{cfg: cfg, terminals: terminals, drivers: map[string]*Driver{}}, nil
}

// EnterResult 描述一次成功入池的结果。
type EnterResult struct {
	Position      int  // 前方人数
	Priority      bool // 是否以优先身份入队
	VoucherIssued bool // 本次尝试是否发放了凭证
	VoucherHeld   bool // 操作结束后是否仍持有未消耗凭证
}

// DispatchResult 描述一次放行。
type DispatchResult struct {
	DriverID     string
	FromTerminal string
	Deadline     int64
	Transferred  bool // 是否跨候机楼调剂
}

// VoucherInfo 为凭证的只读视图。
type VoucherInfo struct {
	Held      bool
	IssuedAt  int64
	ExpiresAt int64
	Strikes   int
	DayCount  int // 当前自然日已发放次数
}

// DriverInfo 为司机状态的只读视图。
type DriverInfo struct {
	State       string
	Terminal    string
	Priority    bool
	EnterTS     int64
	Deadline    int64
	NoShows     int
	BannedUntil int64
}

func (p *Pool) checkClock(ts int64) error {
	if ts < p.clock {
		return ErrClockRollback
	}
	return nil
}

// EnterPool 司机入池。短途返回且及时的司机在本次尝试时发放凭证；
// 若随后入池被拒绝（满员、禁入），凭证保留且时效按发放时刻继续计算。
func (p *Pool) EnterPool(driverID, terminalID string, ts int64) (EnterResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if driverID == "" || terminalID == "" || ts < 0 {
		return EnterResult{}, ErrInvalidParam
	}
	if err := p.checkClock(ts); err != nil {
		return EnterResult{}, err
	}
	term, ok := p.terminals[terminalID]
	if !ok {
		return EnterResult{}, ErrTerminalNotFound
	}
	d, known := p.drivers[driverID]
	if !known {
		d = &Driver{id: driverID}
	}
	res := EnterResult{}
	if d.voucher != nil && ts >= d.voucher.ExpiresAt {
		d.voucher = nil // 过期即弃（恰到期视为已过期）
	}
	if d.pendingTrip != nil {
		trip := *d.pendingTrip
		d.pendingTrip = nil
		if trip.Distance <= p.cfg.ShortTripDistance && ts-trip.DepartedAt <= p.cfg.ReturnLimit && d.voucher == nil {
			day := dayOf(ts, p.cfg.DayOffsetMinutes)
			if d.voucherDay != day {
				d.voucherDay = day
				d.voucherDayCount = 0
			}
			if d.voucherDayCount < p.cfg.DailyVoucherLimit {
				d.voucherDayCount++
				d.voucher = &Voucher{IssuedAt: ts, ExpiresAt: ts + p.cfg.VoucherValidity}
				res.VoucherIssued = true
			}
		}
	}
	if !known && d.voucher != nil {
		p.drivers[driverID] = d // 凭证发放使司机进入系统（被拒时的唯一副作用）
	}
	if ts < d.bannedUntil {
		return EnterResult{}, ErrDriverBanned
	}
	if d.state == stateQueued || d.state == stateDispatched {
		return EnterResult{}, ErrDriverInQueue
	}
	if term.size >= term.capacity {
		return EnterResult{}, ErrQueueFull
	}
	priority := false
	if d.voucher != nil {
		if term.priorityCount < p.cfg.PriorityCap {
			priority = true
			d.voucher = nil // 凭证只对本次入池有效
		} else {
			d.voucher.Strikes++
			if d.voucher.Strikes >= 2 {
				d.voucher = nil // 第二次仍受名额限制，凭证作废
			}
		}
	}
	key := entryKey{priority: priority, ts: ts, driverID: driverID}
	term.root = insert(term.root, key)
	term.size++
	if priority {
		term.priorityCount++
	}
	d.state = stateQueued
	d.terminal = terminalID
	d.key = key
	p.drivers[driverID] = d
	p.clock = ts
	pos, _ := rank(term.root, key)
	res.Position = pos
	res.Priority = priority
	res.VoucherHeld = d.voucher != nil
	return res, nil
}

// Dispatch 上客点请求车辆：本队非空取队首；本队为空时按
// (队首入池时刻, 候机楼标识) 全序从其他队列调剂，队首为优先司机的队列不参与。
func (p *Pool) Dispatch(terminalID string, ts int64) (DispatchResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if terminalID == "" || ts < 0 {
		return DispatchResult{}, ErrInvalidParam
	}
	if err := p.checkClock(ts); err != nil {
		return DispatchResult{}, err
	}
	term, ok := p.terminals[terminalID]
	if !ok {
		return DispatchResult{}, ErrTerminalNotFound
	}
	src := term
	transferred := false
	if term.size == 0 {
		src = nil
		var best entryKey
		for _, cand := range p.terminals {
			if cand.size == 0 {
				continue
			}
			head := minNode(cand.root)
			if head.key.priority {
				continue // 调剂不得取走优先司机
			}
			if src == nil || head.key.ts < best.ts ||
				(head.key.ts == best.ts && cand.id < src.id) {
				src = cand
				best = head.key
			}
		}
		if src == nil {
			return DispatchResult{}, ErrNoCarAvailable
		}
		transferred = true
	}
	head := minNode(src.root)
	key := head.key
	src.root = deleteNode(src.root, key)
	src.size--
	if key.priority {
		src.priorityCount--
	}
	d := p.drivers[key.driverID]
	d.state = stateDispatched
	d.terminal = ""
	limit := p.cfg.ArriveLimit
	if transferred {
		limit = p.cfg.TransferLimit
	}
	d.deadline = ts + limit
	p.clock = ts
	return DispatchResult{DriverID: key.driverID, FromTerminal: src.id, Deadline: d.deadline, Transferred: transferred}, nil
}

// Arrive 司机上报到达上客点。恰在时限到期那一刻上报视为逾期，
// 返回 ErrArrivalOverdue 且不改变任何状态，须随后以 NoShow 处理。
func (p *Pool) Arrive(driverID string, ts int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if driverID == "" || ts < 0 {
		return ErrInvalidParam
	}
	if err := p.checkClock(ts); err != nil {
		return err
	}
	d := p.drivers[driverID]
	if d == nil {
		return ErrDriverNotFound
	}
	if d.state != stateDispatched {
		return ErrDriverNotDispatched
	}
	if ts >= d.deadline {
		return ErrArrivalOverdue
	}
	d.state = stateArrived
	d.deadline = 0
	p.clock = ts
	return nil
}

// NoShow 对逾期未到的放行司机记一次爽约并移出；爽约累计达到配置
// 次数时进入禁入期，禁入期恰结束那一刻起可再次入池。
func (p *Pool) NoShow(driverID string, ts int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if driverID == "" || ts < 0 {
		return ErrInvalidParam
	}
	if err := p.checkClock(ts); err != nil {
		return err
	}
	d := p.drivers[driverID]
	if d == nil {
		return ErrDriverNotFound
	}
	if d.state != stateDispatched {
		return ErrDriverNotDispatched
	}
	if ts < d.deadline {
		return ErrInvalidParam // 尚未逾期，不能按爽约处理
	}
	d.state = stateIdle
	d.deadline = 0
	d.noShows++
	if d.noShows >= p.cfg.NoShowLimit {
		d.bannedUntil = ts + p.cfg.BanDuration
	}
	p.clock = ts
	return nil
}

// CompleteTrip 登记载客离开：记录行程距离与离开时刻，作为再次入池时
// 凭证发放的依据。要求司机处于已到达状态。
func (p *Pool) CompleteTrip(driverID string, distance int64, ts int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if driverID == "" || distance < 0 || ts < 0 {
		return ErrInvalidParam
	}
	if err := p.checkClock(ts); err != nil {
		return err
	}
	d := p.drivers[driverID]
	if d == nil {
		return ErrDriverNotFound
	}
	if d.state != stateArrived {
		return ErrInvalidParam
	}
	d.state = stateIdle
	d.pendingTrip = &Trip{Distance: distance, DepartedAt: ts}
	p.clock = ts
	return nil
}

// Leave 司机主动离队：不消耗凭证、不计爽约。放行后到达之前不可离队
// （此时司机不在任何队列中，按司机不存在处理）。
func (p *Pool) Leave(driverID string, ts int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if driverID == "" || ts < 0 {
		return ErrInvalidParam
	}
	if err := p.checkClock(ts); err != nil {
		return err
	}
	d := p.drivers[driverID]
	if d == nil || d.state != stateQueued {
		return ErrDriverNotFound
	}
	term := p.terminals[d.terminal]
	term.root = deleteNode(term.root, d.key)
	term.size--
	if d.key.priority {
		term.priorityCount--
	}
	d.state = stateIdle
	d.terminal = ""
	p.clock = ts
	return nil
}

// Position 查询司机在其队列中的位置（前方人数）。
func (p *Pool) Position(driverID string) (int, error) {
	pos, _, err := p.PositionWithCost(driverID)
	return pos, err
}

// PositionWithCost 同 Position，并返回秩查询访问的节点数，
// 用于验证查询开销只取决于当前队列规模而非历史入池总数。
func (p *Pool) PositionWithCost(driverID string) (position, visits int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.drivers[driverID]
	if d == nil || d.state != stateQueued {
		return 0, 0, ErrDriverNotFound
	}
	pos, visits := rank(p.terminals[d.terminal].root, d.key)
	return pos, visits, nil
}

// QueueSnapshot 返回队列当前次序（司机标识，队首在前）。
func (p *Pool) QueueSnapshot(terminalID string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	term, ok := p.terminals[terminalID]
	if !ok {
		return nil, ErrTerminalNotFound
	}
	out := make([]string, 0, term.size)
	inorder(term.root, func(k entryKey) {
		out = append(out, k.driverID)
	})
	return out, nil
}

// VoucherInfo 查询司机凭证状态。
func (p *Pool) VoucherInfo(driverID string) (VoucherInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.drivers[driverID]
	if d == nil {
		return VoucherInfo{}, ErrDriverNotFound
	}
	info := VoucherInfo{DayCount: d.voucherDayCount}
	if d.voucher != nil {
		info.Held = true
		info.IssuedAt = d.voucher.IssuedAt
		info.ExpiresAt = d.voucher.ExpiresAt
		info.Strikes = d.voucher.Strikes
	}
	return info, nil
}

// InspectDriver 查询司机状态。
func (p *Pool) InspectDriver(driverID string) (DriverInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.drivers[driverID]
	if d == nil {
		return DriverInfo{}, ErrDriverNotFound
	}
	info := DriverInfo{
		State:       d.state.String(),
		Terminal:    d.terminal,
		Deadline:    d.deadline,
		NoShows:     d.noShows,
		BannedUntil: d.bannedUntil,
	}
	if d.state == stateQueued {
		info.Priority = d.key.priority
		info.EnterTS = d.key.ts
	}
	return info, nil
}

// Clock 返回最近一次被接受操作的时刻。
func (p *Pool) Clock() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.clock
}

// debugState 输出全部可观测状态的确定性文本，用于重放一致性与模型对照。
func (p *Pool) debugState() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "clock=%d", p.clock)
	termIDs := make([]string, 0, len(p.terminals))
	for id := range p.terminals {
		termIDs = append(termIDs, id)
	}
	sort.Strings(termIDs)
	for _, id := range termIDs {
		t := p.terminals[id]
		fmt.Fprintf(&b, " | %s[%d/%d pri=%d]:", id, t.size, t.capacity, t.priorityCount)
		inorder(t.root, func(k entryKey) {
			mark := "n"
			if k.priority {
				mark = "P"
			}
			fmt.Fprintf(&b, " %s:%s@%d", k.driverID, mark, k.ts)
		})
	}
	driverIDs := make([]string, 0, len(p.drivers))
	for id := range p.drivers {
		driverIDs = append(driverIDs, id)
	}
	sort.Strings(driverIDs)
	for _, id := range driverIDs {
		d := p.drivers[id]
		fmt.Fprintf(&b, " | %s:%s", id, d.state)
		if d.state == stateQueued {
			fmt.Fprintf(&b, "@%s", d.terminal)
		}
		if d.state == stateDispatched {
			fmt.Fprintf(&b, " dl=%d", d.deadline)
		}
		fmt.Fprintf(&b, " ns=%d ban=%d day=%d:%d", d.noShows, d.bannedUntil, d.voucherDay, d.voucherDayCount)
		if d.voucher != nil {
			fmt.Fprintf(&b, " v=(%d,%d,s%d)", d.voucher.IssuedAt, d.voucher.ExpiresAt, d.voucher.Strikes)
		}
		if d.pendingTrip != nil {
			fmt.Fprintf(&b, " trip=(%d,%d)", d.pendingTrip.Distance, d.pendingTrip.DepartedAt)
		}
	}
	return b.String()
}
