package gateway

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// Gateway 远程控车指令网关。
//
// 并发模型：同一车辆上的操作由该车辆持有的互斥锁串行化；不同车辆持有
// 不同的锁，互不阻塞。全局唯一的共享点是逻辑时钟（原子变量，无锁 CAS）
// 与幂等存储（按键分片加锁），二者均不会使不同车辆的操作互相阻塞。
// 所有操作的整体效果等价于某个串行顺序。
type Gateway struct {
	cfg      Config
	clock    *logicalClock
	vehicles sync.Map // string -> *vehicle
	idem     *idemStore
	cmdSeq   atomic.Int64
}

// NewGateway 创建网关。
func NewGateway(cfg Config) (*Gateway, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Gateway{
		cfg:   cfg,
		clock: newLogicalClock(),
		idem:  newIdemStore(cfg.IdempotencyRetentionSec),
	}, nil
}

// Config 返回网关配置。
func (g *Gateway) Config() Config { return g.cfg }

// ClockNow 返回网关逻辑时钟当前值（诊断用）。
func (g *Gateway) ClockNow() int64 { return g.clock.now() }

// RegisterVehicle 显式注册车辆。状态上报也会隐式注册车辆。
func (g *Gateway) RegisterVehicle(vehicleID string) error {
	if vehicleID == "" {
		return rejectf(RejectInvalidParam, "vehicle id 为空")
	}
	g.getOrCreateVehicle(vehicleID)
	return nil
}

func (g *Gateway) getVehicle(vehicleID string) (*vehicle, bool) {
	v, ok := g.vehicles.Load(vehicleID)
	if !ok {
		return nil, false
	}
	return v.(*vehicle), true
}

func (g *Gateway) getOrCreateVehicle(vehicleID string) *vehicle {
	nv := newVehicle(vehicleID, g.cfg)
	actual, _ := g.vehicles.LoadOrStore(vehicleID, nv)
	return actual.(*vehicle)
}

// SubmitCommand 受理一条控制指令。
//
// 拒绝次序：参数非法 > 时刻回退 > 车辆未知 > 幂等冲突 > 互斥冲突 >
// 前置不满足 > 唤醒配额耗尽。被拒绝的提交不改变任何状态与时钟，
// 不消耗唤醒配额，也不占用幂等键。
func (g *Gateway) SubmitCommand(req SubmitRequest) (SubmitResult, error) {
	if err := validateSubmit(req); err != nil {
		return SubmitResult{}, err
	}
	if req.Time < g.clock.now() {
		return SubmitResult{}, rejectf(RejectTimeRegression,
			"提交时刻 %d 小于已接受操作时刻 %d", req.Time, g.clock.now())
	}
	v, ok := g.getVehicle(req.VehicleID)
	if !ok {
		return SubmitResult{}, rejectf(RejectUnknownVehicle, "车辆 %q 未注册", req.VehicleID)
	}

	key := req.Submitter + "\x00" + req.RequestID
	content := fmt.Sprintf("%s|%d|%d", req.VehicleID, req.Type, req.ValiditySec)
	rec, dup, err := g.idem.acquire(key, content, req.Time)
	if err != nil {
		return SubmitResult{}, err // 幂等冲突
	}
	if dup != nil {
		// 同键同内容：返回原指令结果（状态取当前值）。
		status := g.commandStatus(dup.vehicleID, dup.commandID)
		return SubmitResult{CommandID: dup.commandID, Status: status, Duplicate: true}, nil
	}

	res, verr := v.submit(g, req)
	if verr != nil {
		g.idem.abort(key, rec)
		return SubmitResult{}, verr
	}
	g.idem.commit(rec, req.VehicleID, res.CommandID)
	return res, nil
}

// ReportState 处理车端状态上报。序号不大于已接受最大序号者丢弃。
func (g *Gateway) ReportState(vehicleID string, rep StateReport) (ReportOutcome, error) {
	if err := validateReport(vehicleID, rep); err != nil {
		return ReportDropped, err
	}
	if rep.Time < g.clock.now() {
		return ReportDropped, rejectf(RejectTimeRegression,
			"上报时刻 %d 小于已接受操作时刻 %d", rep.Time, g.clock.now())
	}
	v := g.getOrCreateVehicle(vehicleID)
	return v.report(g, rep)
}

// Ack 处理指令回执。已终结指令的回执记为迟到回执，仅计数。
func (g *Gateway) Ack(vehicleID, commandID string, t int64, success bool) (AckResult, error) {
	if vehicleID == "" || commandID == "" {
		return AckResult{}, rejectf(RejectInvalidParam, "vehicle id 或 command id 为空")
	}
	if t < 0 {
		return AckResult{}, rejectf(RejectInvalidParam, "时刻为负: %d", t)
	}
	if t < g.clock.now() {
		return AckResult{}, rejectf(RejectTimeRegression,
			"回执时刻 %d 小于已接受操作时刻 %d", t, g.clock.now())
	}
	v, ok := g.getVehicle(vehicleID)
	if !ok {
		return AckResult{}, rejectf(RejectUnknownVehicle, "车辆 %q 未注册", vehicleID)
	}
	return v.ack(g, commandID, t, success)
}

// QueryCommand 查询指令快照。查询会按当前时钟触发惰性时间推进
// （过期、唤醒超时），但不改变时钟本身。
func (g *Gateway) QueryCommand(vehicleID, commandID string) (CommandView, bool) {
	v, ok := g.getVehicle(vehicleID)
	if !ok {
		return CommandView{}, false
	}
	return v.queryCommand(g.clock.now(), commandID)
}

// VehicleSnapshot 返回车辆诊断快照。
func (g *Gateway) VehicleSnapshot(vehicleID string) (VehicleSnapshot, bool) {
	v, ok := g.getVehicle(vehicleID)
	if !ok {
		return VehicleSnapshot{VehicleID: vehicleID}, false
	}
	return v.snapshot(g.clock.now()), true
}

// IdemRecordCount 返回当前幂等记录数（诊断与测试用）。
func (g *Gateway) IdemRecordCount() int { return g.idem.count() }

func (g *Gateway) commandStatus(vehicleID, commandID string) CmdStatus {
	if view, ok := g.QueryCommand(vehicleID, commandID); ok {
		return view.Status
	}
	return StatusExpired // 不会到达：幂等记录与指令同生命周期
}

func validateSubmit(req SubmitRequest) error {
	if req.VehicleID == "" {
		return rejectf(RejectInvalidParam, "vehicle id 为空")
	}
	if req.Submitter == "" {
		return rejectf(RejectInvalidParam, "submitter 为空")
	}
	if req.RequestID == "" {
		return rejectf(RejectInvalidParam, "request id 为空")
	}
	if req.Type < 0 || req.Type >= CmdType(CmdTypeCount) {
		return rejectf(RejectInvalidParam, "未知指令类型: %d", int(req.Type))
	}
	if req.Time < 0 {
		return rejectf(RejectInvalidParam, "时刻为负: %d", req.Time)
	}
	if req.ValiditySec <= 0 {
		return rejectf(RejectInvalidParam, "有效期须为正: %d", req.ValiditySec)
	}
	return nil
}

func validateReport(vehicleID string, rep StateReport) error {
	if vehicleID == "" {
		return rejectf(RejectInvalidParam, "vehicle id 为空")
	}
	if rep.Seq < 0 {
		return rejectf(RejectInvalidParam, "序号为负: %d", rep.Seq)
	}
	if rep.Time < 0 {
		return rejectf(RejectInvalidParam, "时刻为负: %d", rep.Time)
	}
	if rep.Gear < GearPark || rep.Gear > GearDrive {
		return rejectf(RejectInvalidParam, "未知档位: %d", int(rep.Gear))
	}
	if rep.SpeedKmh < 0 {
		return rejectf(RejectInvalidParam, "车速为负: %d", rep.SpeedKmh)
	}
	if rep.Power < PowerSleep || rep.Power > PowerDriving {
		return rejectf(RejectInvalidParam, "未知电源状态: %d", int(rep.Power))
	}
	if rep.Lock < LockAllLocked || rep.Lock > LockNotAllLocked {
		return rejectf(RejectInvalidParam, "未知锁状态: %d", int(rep.Lock))
	}
	if rep.BatteryPct < 0 || rep.BatteryPct > 100 {
		return rejectf(RejectInvalidParam, "电量越界: %d", rep.BatteryPct)
	}
	return nil
}

// IsReject 判断错误是否为拒绝错误，并返回原因。
func IsReject(err error) (RejectReason, bool) {
	var re *RejectError
	if errors.As(err, &re) {
		return re.Reason, true
	}
	return 0, false
}
