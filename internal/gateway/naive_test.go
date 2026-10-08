package gateway

// 朴素参考模型：为随机对照测试独立编写。
// 刻意使用最直接的写法（切片、线性扫描、单线程、无堆无分片），
// 与正式实现的高性能结构形成对照，用大量随机序列验证两者行为一致。

import "fmt"

type naiveCommand struct {
	id         string
	req        SubmitRequest
	status     CmdStatus
	lateAcks   int
	dispatchAt int64
	finishAt   int64
}

type naiveVehicle struct {
	hasReport bool
	maxSeq    int64
	state     StateReport
	commands  []*naiveCommand

	wakeActive    bool
	wakeStartedAt int64
	hasWakeDay    bool
	wakeDay       int64
	wakeUsed      int
}

type naiveIdem struct {
	content   string
	acceptAt  int64
	vehicleID string
	commandID string
}

type naiveGateway struct {
	cfg      Config
	clock    int64
	vehicles map[string]*naiveVehicle
	idem     map[string]naiveIdem
	cmdSeq   int
}

func newNaiveGateway(cfg Config) *naiveGateway {
	return &naiveGateway{
		cfg:      cfg,
		clock:    -1,
		vehicles: map[string]*naiveVehicle{},
		idem:     map[string]naiveIdem{},
	}
}

// --- 朴素版规则表（独立重写，不复用正式实现） ---

func naiveHasPrecond(t CmdType) bool {
	return t != CmdACOff && t != CmdFindCar
}

func naivePrecondOK(t CmdType, s StateReport, known bool, minBatt int) bool {
	if !naiveHasPrecond(t) {
		return true
	}
	if !known {
		return false
	}
	switch t {
	case CmdUnlock, CmdOpenTrunk:
		return s.SpeedKmh == 0 && s.Gear == GearPark
	case CmdLock:
		return s.SpeedKmh == 0
	case CmdACOn:
		return s.BatteryPct >= minBatt && s.Power != PowerDriving
	case CmdRemoteStart:
		return s.Gear == GearPark && s.Lock == LockAllLocked && s.BatteryPct >= minBatt
	}
	return true
}

func naiveConflicts(a, b CmdType) bool {
	if a == b {
		return true
	}
	pair := func(x, y CmdType) bool { return (a == x && b == y) || (a == y && b == x) }
	return pair(CmdUnlock, CmdLock) || pair(CmdACOn, CmdACOff) || pair(CmdUnlock, CmdRemoteStart)
}

func naiveTerminal(s CmdStatus) bool { return s >= StatusSucceeded }

// --- 内部辅助 ---

func (g *naiveGateway) known(v *naiveVehicle, now int64) bool {
	return v.hasReport && now-v.state.Time <= g.cfg.StalenessThresholdSec
}

func (g *naiveGateway) maintenance(v *naiveVehicle, now int64) {
	for _, c := range v.commands {
		if !naiveTerminal(c.status) && c.req.Time+c.req.ValiditySec <= now {
			c.status = StatusExpired
			c.finishAt = now
		}
	}
	if v.wakeActive && now > v.wakeStartedAt+g.cfg.WakeupTimeoutSec {
		for _, c := range v.commands {
			if c.status == StatusAccepted {
				c.status = StatusWakeupFailed
				c.finishAt = now
			}
		}
		v.wakeActive = false
	}
}

func (g *naiveGateway) dayOf(t int64) int64 { return g.cfg.dayOf(t) }

// --- 操作 ---

func (g *naiveGateway) register(vid string) {
	if _, ok := g.vehicles[vid]; !ok {
		g.vehicles[vid] = &naiveVehicle{}
	}
}

func (g *naiveGateway) submit(req SubmitRequest) (SubmitResult, error) {
	if err := validateSubmit(req); err != nil {
		return SubmitResult{}, err
	}
	if req.Time < g.clock {
		return SubmitResult{}, rejectf(RejectTimeRegression, "naive: 时刻回退")
	}
	v, ok := g.vehicles[req.VehicleID]
	if !ok {
		return SubmitResult{}, rejectf(RejectUnknownVehicle, "naive: 车辆未知")
	}
	key := req.Submitter + "\x00" + req.RequestID
	content := fmt.Sprintf("%s|%d|%d", req.VehicleID, req.Type, req.ValiditySec)
	if rec, ok := g.idem[key]; ok && req.Time <= rec.acceptAt+g.cfg.IdempotencyRetentionSec {
		if rec.content != content {
			return SubmitResult{}, rejectf(RejectIdempotencyConflict, "naive: 幂等冲突")
		}
		ov := g.vehicles[rec.vehicleID]
		g.maintenance(ov, g.clock)
		for _, c := range ov.commands {
			if c.id == rec.commandID {
				return SubmitResult{CommandID: c.id, Status: c.status, Duplicate: true}, nil
			}
		}
		return SubmitResult{}, fmt.Errorf("naive: 幂等记录对应的指令不存在")
	}

	g.maintenance(v, req.Time)

	for _, c := range v.commands {
		if !naiveTerminal(c.status) && naiveConflicts(req.Type, c.req.Type) {
			return SubmitResult{}, rejectf(RejectMutexConflict, "naive: 与在途指令 %s 互斥", c.req.Type)
		}
	}
	if !naivePrecondOK(req.Type, v.state, g.known(v, req.Time), g.cfg.MinBatteryPct) {
		return SubmitResult{}, rejectf(RejectPrecondition, "naive: 前置不满足")
	}
	needWake := !g.known(v, req.Time) || v.state.Power == PowerSleep
	newWake := needWake && !v.wakeActive
	day := g.dayOf(req.Time)
	used := v.wakeUsed
	if !v.hasWakeDay || v.wakeDay != day {
		used = 0
	}
	if newWake && used >= g.cfg.WakeupQuotaPerDay {
		return SubmitResult{}, rejectf(RejectWakeupQuota, "naive: 唤醒配额耗尽")
	}

	if req.Time > g.clock {
		g.clock = req.Time
	}
	g.cmdSeq++
	c := &naiveCommand{
		id:         fmt.Sprintf("cmd-%d", g.cmdSeq),
		req:        req,
		status:     StatusAccepted,
		dispatchAt: -1,
		finishAt:   -1,
	}
	v.commands = append(v.commands, c)
	if newWake {
		v.hasWakeDay = true
		v.wakeDay = day
		v.wakeUsed = used + 1
		v.wakeActive = true
		v.wakeStartedAt = req.Time
	} else if !needWake {
		// 下发前再评估（同一状态，结果相同，保持代码路径一致）。
		if !naivePrecondOK(c.req.Type, v.state, g.known(v, req.Time), g.cfg.MinBatteryPct) {
			c.status = StatusPrecondFailed
			c.finishAt = req.Time
		} else {
			c.status = StatusDispatched
			c.dispatchAt = req.Time
		}
	}
	g.idem[key] = naiveIdem{content: content, acceptAt: req.Time, vehicleID: req.VehicleID, commandID: c.id}
	return SubmitResult{CommandID: c.id, Status: c.status}, nil
}

func (g *naiveGateway) report(vid string, rep StateReport) (ReportOutcome, error) {
	if err := validateReport(vid, rep); err != nil {
		return ReportDropped, err
	}
	if rep.Time < g.clock {
		return ReportDropped, rejectf(RejectTimeRegression, "naive: 时刻回退")
	}
	g.register(vid)
	v := g.vehicles[vid]
	g.maintenance(v, rep.Time)
	if v.hasReport && rep.Seq <= v.maxSeq {
		return ReportDropped, nil
	}
	if rep.Time > g.clock {
		g.clock = rep.Time
	}
	v.hasReport = true
	v.maxSeq = rep.Seq
	v.state = rep
	if v.wakeActive && rep.Power != PowerSleep {
		for _, c := range v.commands {
			if c.status != StatusAccepted {
				continue
			}
			if !naivePrecondOK(c.req.Type, v.state, g.known(v, rep.Time), g.cfg.MinBatteryPct) {
				c.status = StatusPrecondFailed
				c.finishAt = rep.Time
			} else {
				c.status = StatusDispatched
				c.dispatchAt = rep.Time
			}
		}
		v.wakeActive = false
	}
	return ReportAccepted, nil
}

func (g *naiveGateway) ack(vid, cmdID string, t int64, success bool) (AckResult, error) {
	if vid == "" || cmdID == "" || t < 0 {
		return AckResult{}, rejectf(RejectInvalidParam, "naive: 参数非法")
	}
	if t < g.clock {
		return AckResult{}, rejectf(RejectTimeRegression, "naive: 时刻回退")
	}
	v, ok := g.vehicles[vid]
	if !ok {
		return AckResult{}, rejectf(RejectUnknownVehicle, "naive: 车辆未知")
	}
	g.maintenance(v, t)
	var c *naiveCommand
	for _, x := range v.commands {
		if x.id == cmdID {
			c = x
			break
		}
	}
	if c == nil {
		return AckResult{}, rejectf(RejectInvalidParam, "naive: 未知指令")
	}
	if t > g.clock {
		g.clock = t
	}
	if naiveTerminal(c.status) {
		c.lateAcks++
		return AckResult{Late: true}, nil
	}
	if success {
		c.status = StatusSucceeded
	} else {
		c.status = StatusAckFailed
	}
	c.finishAt = t
	return AckResult{}, nil
}

func (g *naiveGateway) query(vid, cmdID string) (CommandView, bool) {
	v, ok := g.vehicles[vid]
	if !ok {
		return CommandView{}, false
	}
	g.maintenance(v, g.clock)
	for _, c := range v.commands {
		if c.id == cmdID {
			return CommandView{
				ID:           c.id,
				VehicleID:    vid,
				Submitter:    c.req.Submitter,
				RequestID:    c.req.RequestID,
				Type:         c.req.Type,
				AcceptTime:   c.req.Time,
				ValiditySec:  c.req.ValiditySec,
				Status:       c.status,
				LateAcks:     c.lateAcks,
				DispatchTime: c.dispatchAt,
				FinishTime:   c.finishAt,
			}, true
		}
	}
	return CommandView{}, false
}

func (g *naiveGateway) snapshot(vid string) (VehicleSnapshot, bool) {
	v, ok := g.vehicles[vid]
	if !ok {
		return VehicleSnapshot{VehicleID: vid}, false
	}
	g.maintenance(v, g.clock)
	snap := VehicleSnapshot{
		VehicleID:     vid,
		Registered:    true,
		HasReport:     v.hasReport,
		LastReport:    v.state,
		InFlightByTyp: map[CmdType]int{},
		CommandTotal:  len(v.commands),
		WakeupActive:  v.wakeActive,
		WakeDay:       v.wakeDay,
		WakeUsedToday: v.wakeUsed,
	}
	for _, c := range v.commands {
		if !naiveTerminal(c.status) {
			snap.InFlight++
			snap.InFlightByTyp[c.req.Type]++
		}
	}
	return snap, true
}
