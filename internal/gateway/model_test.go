package gateway

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// TestRandomAgainstNaiveModel 用大量随机操作序列对照正式实现与朴素模型。
// 每一步打印输入、输出与判定依据（go test -v 可见）。
func TestRandomAgainstNaiveModel(t *testing.T) {
	configs := []Config{
		{MinBatteryPct: 20, StalenessThresholdSec: 100, WakeupTimeoutSec: 50, WakeupQuotaPerDay: 2, DayOffsetSec: 0, IdempotencyRetentionSec: 1000},
		{MinBatteryPct: 50, StalenessThresholdSec: 0, WakeupTimeoutSec: 10, WakeupQuotaPerDay: 1, DayOffsetSec: 8 * 3600, IdempotencyRetentionSec: 60},
		{MinBatteryPct: 0, StalenessThresholdSec: 30, WakeupTimeoutSec: 0, WakeupQuotaPerDay: 5, DayOffsetSec: -3600, IdempotencyRetentionSec: 5},
	}
	const seeds = 40
	const opsPerSeed = 1500
	for seed := int64(0); seed < seeds; seed++ {
		cfg := configs[seed%int64(len(configs))]
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			runRandomSequence(t, cfg, seed, opsPerSeed)
		})
	}
}

type opContext struct {
	real   *Gateway
	naive  *naiveGateway
	rnd    *rand.Rand
	cur    int64 // 当前时刻基准
	maxSeq map[string]int64
	cmds   []string // 已受理指令（"vid/cmdID"）
}

func runRandomSequence(t *testing.T, cfg Config, seed int64, nOps int) {
	real, err := NewGateway(cfg)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	ctx := &opContext{
		real:   real,
		naive:  newNaiveGateway(cfg),
		rnd:    rand.New(rand.NewSource(seed)),
		maxSeq: map[string]int64{},
	}
	vehicles := []string{"v0", "v1", "v2", "v3"}

	for i := 0; i < nOps; i++ {
		ctx.advanceTime()
		kind := ctx.rnd.Intn(100)
		var desc string
		switch {
		case kind < 45:
			desc = ctx.doSubmit(t, vehicles, i)
		case kind < 70:
			desc = ctx.doReport(t, vehicles, i)
		case kind < 85:
			desc = ctx.doAck(t, i)
		case kind < 90:
			desc = ctx.doRegister(t, vehicles, i)
		default:
			desc = ctx.doQuery(t, vehicles, i)
		}
		t.Logf("op#%d %s", i, desc)
		if i%100 == 99 {
			ctx.compareSnapshots(t, vehicles, i)
		}
	}
	ctx.compareSnapshots(t, vehicles, nOps)
}

func (c *opContext) advanceTime() {
	switch r := c.rnd.Intn(100); {
	case r < 80:
		c.cur += int64(c.rnd.Intn(16))
	case r < 90:
		// 保持不变
	default:
		c.cur -= int64(c.rnd.Intn(11))
		if c.cur < 0 {
			c.cur = 0
		}
	}
}

func rejectReasonOf(err error) string {
	if err == nil {
		return "<ok>"
	}
	if re, ok := err.(*RejectError); ok {
		return re.Reason.String()
	}
	return "err:" + err.Error()
}

func (c *opContext) doSubmit(t *testing.T, vehicles []string, seq int) string {
	vid := vehicles[c.rnd.Intn(len(vehicles))]
	if c.rnd.Intn(100) < 5 {
		vid = "ghost" // 未注册车辆
	}
	validity := int64(1 + c.rnd.Intn(80))
	if c.rnd.Intn(100) < 5 {
		validity = 100000
	}
	req := SubmitRequest{
		VehicleID:   vid,
		Submitter:   fmt.Sprintf("s%d", c.rnd.Intn(3)),
		RequestID:   fmt.Sprintf("req-%d", c.rnd.Intn(8)),
		Type:        CmdType(c.rnd.Intn(CmdTypeCount)),
		Time:        c.cur,
		ValiditySec: validity,
	}
	gotR, errR := c.real.SubmitCommand(req)
	gotN, errN := c.naive.submit(req)
	if rejectReasonOf(errR) != rejectReasonOf(errN) {
		t.Fatalf("op#%d submit %+v: 拒绝原因不一致 real=%v naive=%v", seq, req, errR, errN)
	}
	if errR == nil && gotR != gotN {
		t.Fatalf("op#%d submit %+v: 结果不一致 real=%+v naive=%+v", seq, req, gotR, gotN)
	}
	if errR == nil && !gotR.Duplicate {
		c.cmds = append(c.cmds, req.VehicleID+"/"+gotR.CommandID)
	}
	return fmt.Sprintf("submit %+v => real=%+v naive=%+v reason=%s",
		req, gotR, gotN, rejectReasonOf(errR))
}

func (c *opContext) doReport(t *testing.T, vehicles []string, seq int) string {
	vid := vehicles[c.rnd.Intn(len(vehicles))]
	base := c.maxSeq[vid]
	s := base + int64(c.rnd.Intn(5)) - 2
	if s < 0 {
		s = 0
	}
	powers := []PowerState{PowerSleep, PowerAwake, PowerAwake, PowerDriving}
	speeds := []int{0, 0, 5, 60}
	rep := StateReport{
		Seq:        s,
		Time:       c.cur,
		Gear:       Gear(c.rnd.Intn(4)),
		SpeedKmh:   speeds[c.rnd.Intn(len(speeds))],
		Power:      powers[c.rnd.Intn(len(powers))],
		Lock:       DoorLock(c.rnd.Intn(2)),
		BatteryPct: c.rnd.Intn(101),
	}
	gotR, errR := c.real.ReportState(vid, rep)
	gotN, errN := c.naive.report(vid, rep)
	if rejectReasonOf(errR) != rejectReasonOf(errN) || gotR != gotN {
		t.Fatalf("op#%d report %s %+v: 不一致 real=(%v,%v) naive=(%v,%v)",
			seq, vid, rep, gotR, errR, gotN, errN)
	}
	if errR == nil && gotR == ReportAccepted && s >= c.maxSeq[vid] {
		c.maxSeq[vid] = s
	}
	return fmt.Sprintf("report %s %+v => real=%v naive=%v reason=%s",
		vid, rep, gotR, gotN, rejectReasonOf(errR))
}

func (c *opContext) doAck(t *testing.T, seq int) string {
	vid, cmdID := "v0", "cmd-999"
	if len(c.cmds) > 0 && c.rnd.Intn(100) < 85 {
		pick := c.cmds[c.rnd.Intn(len(c.cmds))]
		vid = pick[:len(pick)-len(cmdIDFromPath(pick))-1]
		cmdID = cmdIDFromPath(pick)
	} else if c.rnd.Intn(100) < 50 {
		vid = "v1"
		cmdID = fmt.Sprintf("cmd-%d", c.rnd.Intn(20))
	}
	success := c.rnd.Intn(100) < 70
	gotR, errR := c.real.Ack(vid, cmdID, c.cur, success)
	gotN, errN := c.naive.ack(vid, cmdID, c.cur, success)
	if rejectReasonOf(errR) != rejectReasonOf(errN) || gotR != gotN {
		t.Fatalf("op#%d ack %s/%s t=%d ok=%v: 不一致 real=(%+v,%v) naive=(%+v,%v)",
			seq, vid, cmdID, c.cur, success, gotR, errR, gotN, errN)
	}
	return fmt.Sprintf("ack %s/%s t=%d ok=%v => real=%+v naive=%+v reason=%s",
		vid, cmdID, c.cur, success, gotR, gotN, rejectReasonOf(errR))
}

func cmdIDFromPath(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

func (c *opContext) doRegister(t *testing.T, vehicles []string, seq int) string {
	vid := vehicles[c.rnd.Intn(len(vehicles))]
	errR := c.real.RegisterVehicle(vid)
	c.naive.register(vid)
	if errR != nil {
		t.Fatalf("op#%d register %s: %v", seq, vid, errR)
	}
	return fmt.Sprintf("register %s => ok", vid)
}

func (c *opContext) doQuery(t *testing.T, vehicles []string, seq int) string {
	vid := vehicles[c.rnd.Intn(len(vehicles))]
	cmdID := fmt.Sprintf("cmd-%d", c.rnd.Intn(20))
	if len(c.cmds) > 0 && c.rnd.Intn(100) < 80 {
		pick := c.cmds[c.rnd.Intn(len(c.cmds))]
		vid = pick[:len(pick)-len(cmdIDFromPath(pick))-1]
		cmdID = cmdIDFromPath(pick)
	}
	gotR, okR := c.real.QueryCommand(vid, cmdID)
	gotN, okN := c.naive.query(vid, cmdID)
	if okR != okN || gotR != gotN {
		t.Fatalf("op#%d query %s/%s: 不一致 real=(%+v,%v) naive=(%+v,%v)",
			seq, vid, cmdID, gotR, okR, gotN, okN)
	}
	return fmt.Sprintf("query %s/%s => real=%+v naive=%+v", vid, cmdID, gotR, gotN)
}

func (c *opContext) compareSnapshots(t *testing.T, vehicles []string, seq int) {
	t.Helper()
	for _, vid := range vehicles {
		snapR, okR := c.real.VehicleSnapshot(vid)
		snapN, okN := c.naive.snapshot(vid)
		if okR != okN {
			t.Fatalf("op#%d snapshot %s: 注册状态不一致 real=%v naive=%v", seq, vid, okR, okN)
		}
		if !okR {
			continue
		}
		if snapR.InFlight != snapN.InFlight ||
			!reflect.DeepEqual(snapR.InFlightByTyp, snapN.InFlightByTyp) ||
			snapR.CommandTotal != snapN.CommandTotal ||
			snapR.WakeupActive != snapN.WakeupActive ||
			snapR.WakeDay != snapN.WakeDay ||
			snapR.WakeUsedToday != snapN.WakeUsedToday ||
			snapR.HasReport != snapN.HasReport ||
			snapR.LastReport != snapN.LastReport {
			t.Fatalf("op#%d snapshot %s 不一致:\nreal=%+v\nnaive=%+v", seq, vid, snapR, snapN)
		}
	}
	if c.real.ClockNow() != c.naive.clock {
		t.Fatalf("op#%d 时钟不一致: real=%d naive=%d", seq, c.real.ClockNow(), c.naive.clock)
	}
	if c.real.IdemRecordCount() != len(c.naive.idem) {
		t.Fatalf("op#%d 幂等记录数不一致: real=%d naive=%d", seq, c.real.IdemRecordCount(), len(c.naive.idem))
	}
	t.Logf("op#%d 快照一致: clock=%d idem=%d", seq, c.real.ClockNow(), c.real.IdemRecordCount())
}
