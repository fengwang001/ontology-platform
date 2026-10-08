package gateway

import (
	"container/heap"
	"fmt"
	"sync"
)

// Command 一条指令的内部表示。
type Command struct {
	id         string
	vehicleID  string
	submitter  string
	requestID  string
	typ        CmdType
	acceptAt   int64
	validity   int64
	status     CmdStatus
	lateAcks   int
	dispatchAt int64
	finishAt   int64
}

func (c *Command) expiry() int64 { return c.acceptAt + c.validity }

func (c *Command) view() CommandView {
	return CommandView{
		ID:           c.id,
		VehicleID:    c.vehicleID,
		Submitter:    c.submitter,
		RequestID:    c.requestID,
		Type:         c.typ,
		AcceptTime:   c.acceptAt,
		ValiditySec:  c.validity,
		Status:       c.status,
		LateAcks:     c.lateAcks,
		DispatchTime: c.dispatchAt,
		FinishTime:   c.finishAt,
	}
}

// expiryHeap 按过期时刻排序的最小堆，使过期处理开销只与在途指令数相关，
// 与历史指令总数无关。已终结的指令在弹出时惰性跳过。
type expiryHeap []*Command

func (h expiryHeap) Len() int { return len(h) }
func (h expiryHeap) Less(i, j int) bool {
	if h[i].expiry() != h[j].expiry() {
		return h[i].expiry() < h[j].expiry()
	}
	return h[i].id < h[j].id
}
func (h expiryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *expiryHeap) Push(x any)   { *h = append(*h, x.(*Command)) }
func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	c := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return c
}

// wakeupState 一次进行中的唤醒。共享该唤醒的在途指令记录在 waiting 中。
type wakeupState struct {
	startedAt int64
	waiting   map[*Command]struct{}
}

// vehicle 单车辆状态。所有字段仅在 v.mu 保护下访问，
// 同一车辆上的操作因此互相串行。
type vehicle struct {
	mu  sync.Mutex
	id  string
	cfg Config

	hasReport bool
	maxSeq    int64
	state     StateReport // 最近一次被接受的上报

	commands map[string]*Command // 全部指令（含已终结），按 ID 索引
	inflight [CmdTypeCount]int   // 按类型统计的在途指令数
	expiry   expiryHeap          // 在途指令的过期堆

	wake       *wakeupState // 进行中的唤醒，nil 表示无
	hasWakeDay bool
	wakeDay    int64 // 最近一次发起唤醒所属自然日
	wakeUsed   int   // 当日已消耗唤醒配额
}

func newVehicle(id string, cfg Config) *vehicle {
	return &vehicle{
		id:       id,
		cfg:      cfg,
		commands: make(map[string]*Command),
	}
}

// stateKnown 报告当前车辆状态是否已知：有上报且未陈旧。
// 最近一次上报距当前时刻超过陈旧阈值视为未知，恰等于不算陈旧。
func (v *vehicle) stateKnown(now int64) bool {
	return v.hasReport && now-v.state.Time <= v.cfg.StalenessThresholdSec
}

// needsWake 报告受理指令前是否需要先唤醒。
// 休眠车辆必须唤醒；状态未知（从未上报或已陈旧）时保守地按需要唤醒处理。
func (v *vehicle) needsWake(now int64) bool {
	if !v.stateKnown(now) {
		return true
	}
	return v.state.Power == PowerSleep
}

// maintenance 将时间驱动的事件推进到 now：过期在途指令、判定唤醒超时。
// 只依赖在途指令堆与当前唤醒，开销与历史指令总数无关。
func (v *vehicle) maintenance(now int64) {
	for len(v.expiry) > 0 && v.expiry[0].expiry() <= now {
		c := heap.Pop(&v.expiry).(*Command)
		if !c.status.Terminal() {
			v.terminate(c, StatusExpired, now)
		}
	}
	if v.wake != nil && now > v.wake.startedAt+v.cfg.WakeupTimeoutSec {
		for c := range v.wake.waiting {
			if !c.status.Terminal() {
				v.terminate(c, StatusWakeupFailed, now)
			}
		}
		v.wake = nil
	}
}

// terminate 终结指令并维护在途计数。
func (v *vehicle) terminate(c *Command, status CmdStatus, now int64) {
	c.status = status
	c.finishAt = now
	v.inflight[c.typ]--
}

// dispatchOrFail 下发指令：下发前再次评估前置条件，不满足则以前置失效终结。
func (v *vehicle) dispatchOrFail(c *Command, now int64) {
	if !precondOK(c.typ, v.state, v.stateKnown(now), v.cfg.MinBatteryPct) {
		v.terminate(c, StatusPrecondFailed, now)
		return
	}
	c.status = StatusDispatched
	c.dispatchAt = now
}

// submit 在车辆锁内受理指令。所有检查均为只读，只有时钟推进成功后
// 才落地任何变更，因此被拒绝的提交不改变任何状态、不消耗配额。
func (v *vehicle) submit(g *Gateway, req SubmitRequest) (SubmitResult, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.maintenance(req.Time)

	// 互斥冲突：与任一在途指令冲突则拒绝。
	for other := CmdType(0); other < CmdType(CmdTypeCount); other++ {
		if v.inflight[other] > 0 && conflicts(req.Type, other) {
			return SubmitResult{}, rejectf(RejectMutexConflict,
				"与在途指令 %s 互斥", other)
		}
	}

	// 前置条件：状态未知时对有前置条件的指令按不满足处理。
	if !precondOK(req.Type, v.state, v.stateKnown(req.Time), v.cfg.MinBatteryPct) {
		return SubmitResult{}, rejectf(RejectPrecondition,
			"指令 %s 前置条件不满足", req.Type)
	}

	// 唤醒：需唤醒且无进行中唤醒时，检查并准备消耗配额。
	needWake := v.needsWake(req.Time)
	newWake := needWake && v.wake == nil
	day := v.cfg.dayOf(req.Time)
	used := v.wakeUsed
	if !v.hasWakeDay || v.wakeDay != day {
		used = 0
	}
	if newWake && used >= v.cfg.WakeupQuotaPerDay {
		return SubmitResult{}, rejectf(RejectWakeupQuota,
			"当日唤醒配额 %d 已耗尽", v.cfg.WakeupQuotaPerDay)
	}

	// 提交点：时钟推进成功后方可落地变更。
	if !g.clock.tryAdvance(req.Time) {
		return SubmitResult{}, rejectf(RejectTimeRegression,
			"提交时刻 %d 发生时刻回退", req.Time)
	}

	id := fmt.Sprintf("cmd-%d", g.cmdSeq.Add(1))
	c := &Command{
		id:         id,
		vehicleID:  req.VehicleID,
		submitter:  req.Submitter,
		requestID:  req.RequestID,
		typ:        req.Type,
		acceptAt:   req.Time,
		validity:   req.ValiditySec,
		status:     StatusAccepted,
		dispatchAt: -1,
		finishAt:   -1,
	}
	v.commands[id] = c
	v.inflight[c.typ]++
	heap.Push(&v.expiry, c)

	switch {
	case newWake:
		v.hasWakeDay = true
		v.wakeDay = day
		v.wakeUsed = used + 1
		v.wake = &wakeupState{startedAt: req.Time, waiting: map[*Command]struct{}{c: {}}}
	case needWake:
		// 共享进行中的唤醒，不再消耗配额。
		v.wake.waiting[c] = struct{}{}
	default:
		v.dispatchOrFail(c, req.Time)
	}
	return SubmitResult{CommandID: id, Status: c.status}, nil
}

// report 处理状态上报。
func (v *vehicle) report(g *Gateway, rep StateReport) (ReportOutcome, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.maintenance(rep.Time)

	if v.hasReport && rep.Seq <= v.maxSeq {
		return ReportDropped, nil // 丢弃，不改变状态与时钟
	}
	if !g.clock.tryAdvance(rep.Time) {
		return ReportDropped, rejectf(RejectTimeRegression,
			"上报时刻 %d 发生时刻回退", rep.Time)
	}

	v.hasReport = true
	v.maxSeq = rep.Seq
	v.state = rep

	// 唤醒完成：收到车端上线上报后，对共享该唤醒的在途指令统一下发。
	if v.wake != nil && rep.Power != PowerSleep {
		for c := range v.wake.waiting {
			if !c.status.Terminal() {
				v.dispatchOrFail(c, rep.Time)
			}
		}
		v.wake = nil
	}
	return ReportAccepted, nil
}

// ack 处理指令回执。
func (v *vehicle) ack(g *Gateway, commandID string, t int64, success bool) (AckResult, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.maintenance(t)

	c, ok := v.commands[commandID]
	if !ok {
		return AckResult{}, rejectf(RejectInvalidParam, "未知指令 %q", commandID)
	}
	if !g.clock.tryAdvance(t) {
		return AckResult{}, rejectf(RejectTimeRegression,
			"回执时刻 %d 发生时刻回退", t)
	}
	if c.status.Terminal() {
		// 迟到回执：不改变终态，仅计数。
		c.lateAcks++
		return AckResult{Late: true}, nil
	}
	if success {
		v.terminate(c, StatusSucceeded, t)
	} else {
		v.terminate(c, StatusAckFailed, t)
	}
	return AckResult{}, nil
}

// queryCommand 查询指令快照，并按当前时钟惰性推进时间事件。
func (v *vehicle) queryCommand(now int64, commandID string) (CommandView, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.maintenance(now)
	c, ok := v.commands[commandID]
	if !ok {
		return CommandView{}, false
	}
	return c.view(), true
}

// snapshot 返回车辆诊断快照。
func (v *vehicle) snapshot(now int64) VehicleSnapshot {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.maintenance(now)
	snap := VehicleSnapshot{
		VehicleID:     v.id,
		Registered:    true,
		HasReport:     v.hasReport,
		LastReport:    v.state,
		InFlightByTyp: make(map[CmdType]int),
		CommandTotal:  len(v.commands),
		WakeupActive:  v.wake != nil,
		WakeDay:       v.wakeDay,
		WakeUsedToday: v.wakeUsed,
	}
	for t := CmdType(0); t < CmdType(CmdTypeCount); t++ {
		if v.inflight[t] > 0 {
			snap.InFlightByTyp[t] = v.inflight[t]
			snap.InFlight += v.inflight[t]
		}
	}
	return snap
}
