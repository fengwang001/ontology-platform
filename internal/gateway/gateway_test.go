package gateway

import "testing"

// TestStalenessBoundary 状态陈旧恰好等于阈值不算陈旧，超过才算。
func TestStalenessBoundary(t *testing.T) {
	g := newTestGateway(t)
	mustReport(t, g, "v1", awakeReport(1, 10)) // 上报时刻 10，阈值 100

	// t=110：距上报恰好 100，不算陈旧，前置可评估 → 受理。
	res := mustSubmit(t, g, submitReq("v1", "r1", CmdUnlock, 110, 60))
	if res.Status != StatusDispatched {
		t.Fatalf("t=110 恰好等于阈值应受理并下发, got %v", res.Status)
	}
	mustAck(t, g, "v1", res.CommandID, 110, true)

	// t=111：距上报 101 > 100，陈旧 → 状态未知 → 前置不满足。
	expectReject(t, g, submitReq("v1", "r2", CmdUnlock, 111, 60), RejectPrecondition)
}

// TestValidityExpiryBoundary 有效期末刻恰等于即过期。
func TestValidityExpiryBoundary(t *testing.T) {
	g := newTestGateway(t)
	mustReport(t, g, "v1", awakeReport(1, 0))
	res := mustSubmit(t, g, submitReq("v1", "r1", CmdFindCar, 50, 30)) // 过期时刻 80

	// t=79：未过期。
	mustReport(t, g, "v1", awakeReport(2, 79))
	if v := mustQuery(t, g, "v1", res.CommandID); v.Status != StatusDispatched {
		t.Fatalf("t=79 应在途, got %v", v.Status)
	}
	// t=80：恰等于有效期末刻，已过期。
	mustReport(t, g, "v1", awakeReport(3, 80))
	if v := mustQuery(t, g, "v1", res.CommandID); v.Status != StatusExpired {
		t.Fatalf("t=80 应已过期, got %v", v.Status)
	}
}

// TestWakeupQuotaAcrossDays 自然日跨日时唤醒配额重置（日界按秒级时区偏移）。
func TestWakeupQuotaAcrossDays(t *testing.T) {
	g := newTestGateway(t) // 配额 2/日，偏移 0，唤醒超时 50
	mustReport(t, g, "v1", sleepReport(1, 0))

	// 第 0 天：两次唤醒（各自超时失败），第三次配额耗尽。
	mustSubmit(t, g, submitReq("v1", "r1", CmdFindCar, 100, 1000))
	mustSubmit(t, g, submitReq("v1", "r2", CmdFindCar, 200, 1000)) // 触发 r1 唤醒超时
	expectReject(t, g, submitReq("v1", "r3", CmdFindCar, 300, 1000), RejectWakeupQuota)

	// 跨日（t=86400 属第 1 天）：配额重置，可再次唤醒。
	res := mustSubmit(t, g, submitReq("v1", "r4", CmdFindCar, 86400, 1000))
	if res.Status != StatusAccepted {
		t.Fatalf("跨日后应受理, got %v", res.Status)
	}
	snap, _ := g.VehicleSnapshot("v1")
	if snap.WakeUsedToday != 1 || snap.WakeDay != 1 {
		t.Fatalf("跨日后配额应为 day=1 used=1, got day=%d used=%d", snap.WakeDay, snap.WakeUsedToday)
	}
}

// TestWakeupQuotaDayOffset 日界按配置的秒级时区偏移。
func TestWakeupQuotaDayOffset(t *testing.T) {
	cfg := testConfig()
	cfg.DayOffsetSec = 8 * 3600 // 东八区：自然日从 t=57600 开始
	g, err := NewGateway(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mustReport(t, g, "v1", sleepReport(1, 0))
	// t=57599 仍属第 0 天，t=57600 属第 1 天。
	mustSubmit(t, g, submitReq("v1", "a", CmdFindCar, 100, 100000))
	mustSubmit(t, g, submitReq("v1", "b", CmdFindCar, 200, 100000))
	expectReject(t, g, submitReq("v1", "c", CmdFindCar, 57599, 100000), RejectWakeupQuota)
	mustSubmit(t, g, submitReq("v1", "d", CmdFindCar, 57600, 100000))
}

// TestSharedWakeupFailsTogether 共享同一次唤醒的两条指令一起失败。
func TestSharedWakeupFailsTogether(t *testing.T) {
	g := newTestGateway(t)
	mustReport(t, g, "v1", sleepReport(1, 10))

	r1 := mustSubmit(t, g, submitReq("v1", "r1", CmdFindCar, 20, 1000))
	r2 := mustSubmit(t, g, submitReq("v1", "r2", CmdACOff, 25, 1000))
	if r1.Status != StatusAccepted || r2.Status != StatusAccepted {
		t.Fatalf("两条指令应共享唤醒等待, got %v / %v", r1.Status, r2.Status)
	}
	// 共享唤醒只消耗一次配额。
	if snap, _ := g.VehicleSnapshot("v1"); snap.WakeUsedToday != 1 || !snap.WakeupActive {
		t.Fatalf("共享唤醒应只耗 1 次配额, got %+v", snap)
	}
	// 超过唤醒超时（20+50=70）仍无上线上报：两条一起以唤醒失败终结。
	mustReport(t, g, "v1", sleepReport(2, 71))
	v1 := mustQuery(t, g, "v1", r1.CommandID)
	v2 := mustQuery(t, g, "v1", r2.CommandID)
	if v1.Status != StatusWakeupFailed || v2.Status != StatusWakeupFailed {
		t.Fatalf("共享唤醒应一起失败, got %v / %v", v1.Status, v2.Status)
	}
	if snap, _ := g.VehicleSnapshot("v1"); snap.WakeupActive {
		t.Fatalf("唤醒失败后不应有进行中唤醒")
	}
}

// TestMutexMatrix 互斥矩阵全部 7x7 组合。
func TestMutexMatrix(t *testing.T) {
	types := []CmdType{CmdUnlock, CmdLock, CmdACOn, CmdACOff, CmdFindCar, CmdOpenTrunk, CmdRemoteStart}
	for _, a := range types {
		for _, b := range types {
			t.Run(a.String()+"_then_"+b.String(), func(t *testing.T) {
				g := newTestGateway(t)
				mustReport(t, g, "v1", awakeReport(1, 0))
				mustSubmit(t, g, submitReq("v1", "first", a, 10, 1000))
				_, err := g.SubmitCommand(submitReq("v1", "second", b, 20, 1000))
				if conflicts(a, b) {
					expectRejectErr(t, err, RejectMutexConflict)
				} else if err != nil {
					t.Fatalf("%s 与 %s 不互斥，应受理，实际 %v", a, b, err)
				}
			})
		}
	}
}

// TestIdempotentReplay 同键同内容重复提交返回原指令结果，不重复受理。
func TestIdempotentReplay(t *testing.T) {
	g := newTestGateway(t)
	mustReport(t, g, "v1", awakeReport(1, 0))
	r1 := mustSubmit(t, g, submitReq("v1", "k1", CmdFindCar, 10, 100))
	r2 := mustSubmit(t, g, submitReq("v1", "k1", CmdFindCar, 20, 100))
	if !r2.Duplicate || r2.CommandID != r1.CommandID {
		t.Fatalf("同键同内容应返回原指令 %s, got %+v", r1.CommandID, r2)
	}
	snap, _ := g.VehicleSnapshot("v1")
	if snap.CommandTotal != 1 {
		t.Fatalf("重复提交不应重复受理, CommandTotal=%d", snap.CommandTotal)
	}
	// 同键不同内容 → 冲突。
	req := submitReq("v1", "k1", CmdLock, 30, 100)
	expectReject(t, g, req, RejectIdempotencyConflict)
	// 不同提交者同一请求编号 → 不同幂等键。
	req2 := submitReq("v1", "k1", CmdLock, 30, 100)
	req2.Submitter = "web"
	mustSubmit(t, g, req2)
}

// TestIdempotencyReuseAfterExpiry 幂等记录过期后同一键视为新请求。
func TestIdempotencyReuseAfterExpiry(t *testing.T) {
	g := newTestGateway(t) // 保留时长 1000
	mustReport(t, g, "v1", awakeReport(1, 0))
	r1 := mustSubmit(t, g, submitReq("v1", "k1", CmdFindCar, 10, 100))
	mustAck(t, g, "v1", r1.CommandID, 20, true)

	// t=1010：恰好 10+1000，未超过保留时长，仍是重放。
	r2 := mustSubmit(t, g, submitReq("v1", "k1", CmdFindCar, 1010, 100))
	if !r2.Duplicate || r2.CommandID != r1.CommandID {
		t.Fatalf("t=1010 应仍为幂等重放, got %+v", r2)
	}
	// t=1011：超过保留时长，视为新请求，生成新指令。
	r3 := mustSubmit(t, g, submitReq("v1", "k1", CmdFindCar, 1011, 100))
	if r3.Duplicate || r3.CommandID == r1.CommandID {
		t.Fatalf("t=1011 应为新请求, got %+v", r3)
	}
}

// TestLateAck 已终结指令之后到达的回执记为迟到回执，不改变终态但可查询次数。
func TestLateAck(t *testing.T) {
	g := newTestGateway(t)
	mustReport(t, g, "v1", awakeReport(1, 0))
	res := mustSubmit(t, g, submitReq("v1", "r1", CmdFindCar, 10, 100))
	mustAck(t, g, "v1", res.CommandID, 20, true)

	late := mustAck(t, g, "v1", res.CommandID, 30, false)
	if !late.Late {
		t.Fatalf("已终结指令的回执应记为迟到回执")
	}
	late = mustAck(t, g, "v1", res.CommandID, 40, true)
	if !late.Late {
		t.Fatalf("再次回执仍为迟到回执")
	}
	v := mustQuery(t, g, "v1", res.CommandID)
	if v.Status != StatusSucceeded || v.LateAcks != 2 {
		t.Fatalf("终态不应改变且迟到回执计数为 2, got %+v", v)
	}

	// 过期指令的回执同样记为迟到回执。
	res2 := mustSubmit(t, g, submitReq("v1", "r2", CmdFindCar, 50, 10)) // 60 过期
	mustReport(t, g, "v1", awakeReport(2, 60))
	if v := mustQuery(t, g, "v1", res2.CommandID); v.Status != StatusExpired {
		t.Fatalf("应已过期, got %v", v.Status)
	}
	late = mustAck(t, g, "v1", res2.CommandID, 61, true)
	if !late.Late {
		t.Fatalf("过期指令的回执应为迟到回执")
	}
	if v := mustQuery(t, g, "v1", res2.CommandID); v.Status != StatusExpired || v.LateAcks != 1 {
		t.Fatalf("过期终态不应改变, got %+v", v)
	}
}

// TestRejectedSubmissionLeavesNoTrace 被拒绝的提交不留痕：
// 不改变状态与时钟，不消耗唤醒配额，不占用幂等键。
func TestRejectedSubmissionLeavesNoTrace(t *testing.T) {
	g := newTestGateway(t) // 唤醒配额 2/日，唤醒超时 50

	// v3：耗尽当日唤醒配额（两次唤醒均超时失败）。
	mustReport(t, g, "v3", sleepReport(1, 35))
	mustSubmit(t, g, submitReq("v3", "a", CmdFindCar, 40, 1000))
	mustSubmit(t, g, submitReq("v3", "b", CmdACOff, 45, 1000)) // 共享唤醒
	mustReport(t, g, "v3", sleepReport(2, 91))                 // 唤醒 1 超时（40+50<91）
	mustSubmit(t, g, submitReq("v3", "c", CmdFindCar, 95, 1000))
	mustReport(t, g, "v3", sleepReport(3, 146)) // 唤醒 2 超时（95+50<146）

	// v2：一条在途指令与一条幂等记录。
	mustReport(t, g, "v2", awakeReport(1, 150))
	mustSubmit(t, g, submitReq("v2", "idem", CmdFindCar, 151, 1000))

	// v1：一条在途指令（等待唤醒，唤醒进行中），车速非 0 使解锁前置不满足。
	bad := sleepReport(1, 152)
	bad.SpeedKmh = 5
	mustReport(t, g, "v1", bad)
	mustSubmit(t, g, submitReq("v1", "keep", CmdFindCar, 153, 1000))

	beforeV1, _ := g.VehicleSnapshot("v1")
	beforeV3, _ := g.VehicleSnapshot("v3")
	beforeClock := g.ClockNow()
	beforeIdem := g.IdemRecordCount()

	// 依次覆盖全部拒绝原因（时钟已推进到 160，以下 t=170 合法）。
	expectReject(t, g, submitReq("v1", "x1", CmdFindCar, 160, -5), RejectInvalidParam)
	expectReject(t, g, submitReq("v1", "x2", CmdFindCar, 100, 60), RejectTimeRegression)
	expectReject(t, g, submitReq("ghost", "x3", CmdFindCar, 160, 60), RejectUnknownVehicle)
	expectReject(t, g, submitReq("v2", "idem", CmdLock, 160, 100), RejectIdempotencyConflict)
	expectReject(t, g, submitReq("v1", "x5", CmdFindCar, 160, 60), RejectMutexConflict)
	expectReject(t, g, submitReq("v1", "x6", CmdUnlock, 160, 60), RejectPrecondition)
	expectReject(t, g, submitReq("v3", "x7", CmdFindCar, 160, 60), RejectWakeupQuota)

	if got := g.ClockNow(); got != beforeClock {
		t.Fatalf("被拒绝的提交不应改变时钟: before=%d after=%d", beforeClock, got)
	}
	if got := g.IdemRecordCount(); got != beforeIdem {
		t.Fatalf("被拒绝的提交不应占用幂等键: before=%d after=%d", beforeIdem, got)
	}
	afterV1, _ := g.VehicleSnapshot("v1")
	afterV3, _ := g.VehicleSnapshot("v3")
	if afterV1.CommandTotal != beforeV1.CommandTotal || afterV1.InFlight != beforeV1.InFlight ||
		afterV1.WakeUsedToday != beforeV1.WakeUsedToday || afterV1.WakeupActive != beforeV1.WakeupActive {
		t.Fatalf("v1 状态被被拒绝的提交改变:\nbefore=%+v\nafter=%+v", beforeV1, afterV1)
	}
	if afterV3.CommandTotal != beforeV3.CommandTotal || afterV3.WakeUsedToday != beforeV3.WakeUsedToday {
		t.Fatalf("v3 状态被被拒绝的提交改变:\nbefore=%+v\nafter=%+v", beforeV3, afterV3)
	}
	// 时钟未被推进：等于 beforeClock 的操作仍被接受。
	mustReport(t, g, "v2", awakeReport(2, beforeClock))
}

// TestRejectOrder 验证提交拒绝次序：
// 参数非法 > 时刻回退 > 车辆未知 > 幂等冲突 > 互斥冲突 > 前置不满足 > 唤醒配额耗尽。
func TestRejectOrder(t *testing.T) {
	g := newTestGateway(t)
	mustReport(t, g, "v1", awakeReport(1, 0))
	mustSubmit(t, g, submitReq("v1", "k1", CmdUnlock, 100, 1000)) // 在途 unlock，时钟=100

	// 参数非法 优先于 时刻回退。
	expectReject(t, g, submitReq("v1", "o1", CmdFindCar, 50, -1), RejectInvalidParam)
	// 时刻回退 优先于 车辆未知。
	expectReject(t, g, submitReq("ghost", "o2", CmdFindCar, 50, 60), RejectTimeRegression)
	// 车辆未知 优先于 幂等冲突（k1 内容不同但车辆未知）。
	expectReject(t, g, submitReq("ghost", "k1", CmdLock, 150, 60), RejectUnknownVehicle)
	// 幂等冲突 优先于 互斥冲突（k1 新内容为 lock，与在途 unlock 也互斥）。
	expectReject(t, g, submitReq("v1", "k1", CmdLock, 150, 60), RejectIdempotencyConflict)
	// 互斥冲突 优先于 前置不满足：让车速非 0 使 lock 前置不满足。
	fast := awakeReport(2, 160)
	fast.SpeedKmh = 10
	mustReport(t, g, "v1", fast)
	expectReject(t, g, submitReq("v1", "o3", CmdLock, 170, 60), RejectMutexConflict)
	// 前置不满足 优先于 唤醒配额耗尽：v2 休眠且配额已用尽，再发前置不满足的指令。
	mustReport(t, g, "v2", sleepReport(1, 180))
	mustSubmit(t, g, submitReq("v2", "w1", CmdFindCar, 190, 1000))
	mustReport(t, g, "v2", sleepReport(2, 241)) // 唤醒 1 超时
	mustSubmit(t, g, submitReq("v2", "w2", CmdFindCar, 250, 1000))
	mustReport(t, g, "v2", sleepReport(3, 301)) // 唤醒 2 超时，配额耗尽
	moving := sleepReport(4, 310)
	moving.SpeedKmh = 10
	mustReport(t, g, "v2", moving)
	expectReject(t, g, submitReq("v2", "o4", CmdUnlock, 320, 60), RejectPrecondition)
	// 配额耗尽最后：前置满足的寻车才轮到配额判定。
	expectReject(t, g, submitReq("v2", "o5", CmdFindCar, 330, 60), RejectWakeupQuota)
}

// TestPreconditions 逐类型验证前置条件。
func TestPreconditions(t *testing.T) {
	newWith := func(t *testing.T, mutate func(*StateReport)) *Gateway {
		g := newTestGateway(t)
		rep := awakeReport(1, 0)
		mutate(&rep)
		mustReport(t, g, "v1", rep)
		return g
	}
	cases := []struct {
		name   string
		mutate func(*StateReport)
		typ    CmdType
		want   RejectReason // 空值表示应受理；用 hasWant 区分
		ok     bool
	}{
		{"解锁_驻车静止_受理", func(r *StateReport) {}, CmdUnlock, 0, true},
		{"解锁_车速非0_拒绝", func(r *StateReport) { r.SpeedKmh = 3 }, CmdUnlock, RejectPrecondition, false},
		{"解锁_非驻车_拒绝", func(r *StateReport) { r.Gear = GearDrive }, CmdUnlock, RejectPrecondition, false},
		{"后备箱_驻车静止_受理", func(r *StateReport) {}, CmdOpenTrunk, 0, true},
		{"后备箱_非驻车_拒绝", func(r *StateReport) { r.Gear = GearNeutral }, CmdOpenTrunk, RejectPrecondition, false},
		{"上锁_静止非驻车_受理", func(r *StateReport) { r.Gear = GearDrive }, CmdLock, 0, true},
		{"上锁_车速非0_拒绝", func(r *StateReport) { r.SpeedKmh = 1 }, CmdLock, RejectPrecondition, false},
		{"开空调_电量不足_拒绝", func(r *StateReport) { r.BatteryPct = 19 }, CmdACOn, RejectPrecondition, false},
		{"开空调_电量恰等于下限_受理", func(r *StateReport) { r.BatteryPct = 20 }, CmdACOn, 0, true},
		{"开空调_行驶中_拒绝", func(r *StateReport) { r.Power = PowerDriving }, CmdACOn, RejectPrecondition, false},
		{"关空调_无条件_受理", func(r *StateReport) { r.SpeedKmh = 60; r.Power = PowerDriving }, CmdACOff, 0, true},
		{"寻车_无条件_受理", func(r *StateReport) { r.SpeedKmh = 60; r.Power = PowerDriving }, CmdFindCar, 0, true},
		{"远程启动_全锁驻车_受理", func(r *StateReport) {}, CmdRemoteStart, 0, true},
		{"远程启动_非全锁_拒绝", func(r *StateReport) { r.Lock = LockNotAllLocked }, CmdRemoteStart, RejectPrecondition, false},
		{"远程启动_非驻车_拒绝", func(r *StateReport) { r.Gear = GearReverse }, CmdRemoteStart, RejectPrecondition, false},
		{"远程启动_电量不足_拒绝", func(r *StateReport) { r.BatteryPct = 10 }, CmdRemoteStart, RejectPrecondition, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newWith(t, tc.mutate)
			_, err := g.SubmitCommand(submitReq("v1", "r1", tc.typ, 10, 60))
			if tc.ok {
				if err != nil {
					t.Fatalf("应受理, got %v", err)
				}
			} else {
				expectRejectErr(t, err, tc.want)
			}
		})
	}
}

// TestUnknownStatePrecondition 状态未知（从未上报）时：
// 有前置条件的指令按不满足处理；无前置条件的指令可受理（需唤醒）。
func TestUnknownStatePrecondition(t *testing.T) {
	g := newTestGateway(t)
	if err := g.RegisterVehicle("v1"); err != nil {
		t.Fatal(err)
	}
	expectReject(t, g, submitReq("v1", "r1", CmdUnlock, 10, 60), RejectPrecondition)
	res := mustSubmit(t, g, submitReq("v1", "r2", CmdFindCar, 20, 60))
	if res.Status != StatusAccepted {
		t.Fatalf("无前置条件指令应受理并等待唤醒, got %v", res.Status)
	}
}

// TestWakeupThenDispatch 休眠车辆唤醒完成并收到新上报后下发。
func TestWakeupThenDispatch(t *testing.T) {
	g := newTestGateway(t)
	mustReport(t, g, "v1", sleepReport(1, 10))
	res := mustSubmit(t, g, submitReq("v1", "r1", CmdUnlock, 20, 1000))
	if res.Status != StatusAccepted {
		t.Fatalf("休眠车辆受理后应等待唤醒, got %v", res.Status)
	}
	// 车端上线（唤醒完成 + 新上报）→ 重新评估通过后下发。
	mustReport(t, g, "v1", awakeReport(2, 30))
	v := mustQuery(t, g, "v1", res.CommandID)
	if v.Status != StatusDispatched || v.DispatchTime != 30 {
		t.Fatalf("唤醒完成后应下发, got %+v", v)
	}
	mustAck(t, g, "v1", res.CommandID, 40, true)
	if v := mustQuery(t, g, "v1", res.CommandID); v.Status != StatusSucceeded {
		t.Fatalf("回执成功后应终结为 succeeded, got %v", v.Status)
	}
}

// TestPrecondFailAtDispatch 下发前再次评估前置条件，不满足以前置失效终结，
// 且不退还已消耗的唤醒配额。
func TestPrecondFailAtDispatch(t *testing.T) {
	g := newTestGateway(t)
	mustReport(t, g, "v1", sleepReport(1, 10)) // 驻车、静止、全锁
	res := mustSubmit(t, g, submitReq("v1", "r1", CmdUnlock, 20, 1000))
	// 车端上线但状态变为行驶中（车速 60）→ 下发前再评估失败。
	bad := awakeReport(2, 30)
	bad.SpeedKmh = 60
	bad.Gear = GearDrive
	bad.Power = PowerDriving
	mustReport(t, g, "v1", bad)
	v := mustQuery(t, g, "v1", res.CommandID)
	if v.Status != StatusPrecondFailed {
		t.Fatalf("下发前前置失效应以 precond_failed 终结, got %v", v.Status)
	}
	snap, _ := g.VehicleSnapshot("v1")
	if snap.WakeUsedToday != 1 {
		t.Fatalf("已消耗的唤醒配额不应退还, got %d", snap.WakeUsedToday)
	}
	if snap.WakeupActive {
		t.Fatalf("唤醒已完成，不应再有进行中唤醒")
	}
}

// TestReportSeqDrop 上报序号不大于已接受最大序号者丢弃，不改变状态与时钟。
func TestReportSeqDrop(t *testing.T) {
	g := newTestGateway(t)
	mustReport(t, g, "v1", awakeReport(5, 100))
	clock := g.ClockNow()

	out, err := g.ReportState("v1", awakeReport(5, 200))
	if err != nil || out != ReportDropped {
		t.Fatalf("同序号应丢弃, got %v, %v", out, err)
	}
	out, err = g.ReportState("v1", awakeReport(3, 200))
	if err != nil || out != ReportDropped {
		t.Fatalf("小序号应丢弃, got %v, %v", out, err)
	}
	if g.ClockNow() != clock {
		t.Fatalf("被丢弃的上报不应推进时钟: %d -> %d", clock, g.ClockNow())
	}
	snap, _ := g.VehicleSnapshot("v1")
	if snap.LastReport.Seq != 5 || snap.LastReport.Time != 100 {
		t.Fatalf("被丢弃的上报不应改变状态: %+v", snap.LastReport)
	}
	// 大序号接受。
	mustReport(t, g, "v1", awakeReport(6, 200))
}

// TestTimeRegression 所有带时刻的操作不得小于上一个被接受操作的时刻。
func TestTimeRegression(t *testing.T) {
	g := newTestGateway(t)
	mustReport(t, g, "v1", awakeReport(1, 100))
	// 提交回退。
	expectReject(t, g, submitReq("v1", "r1", CmdFindCar, 99, 60), RejectTimeRegression)
	// 上报回退。
	_, err := g.ReportState("v1", awakeReport(2, 99))
	expectRejectErr(t, err, RejectTimeRegression)
	// 回执回退：先受理一条。
	res := mustSubmit(t, g, submitReq("v1", "r2", CmdFindCar, 100, 60))
	_, err = g.Ack("v1", res.CommandID, 99, true)
	expectRejectErr(t, err, RejectTimeRegression)
	// 等于上一个被接受时刻是允许的。
	mustReport(t, g, "v1", awakeReport(2, 100))
	mustAck(t, g, "v1", res.CommandID, 100, true)
}

// TestCommandExpiresWhileWaitingWakeup 等待唤醒的指令也会到期过期。
func TestCommandExpiresWhileWaitingWakeup(t *testing.T) {
	g := newTestGateway(t)
	mustReport(t, g, "v1", sleepReport(1, 10))
	res := mustSubmit(t, g, submitReq("v1", "r1", CmdFindCar, 20, 10)) // 30 过期
	mustReport(t, g, "v1", sleepReport(2, 35))
	if v := mustQuery(t, g, "v1", res.CommandID); v.Status != StatusExpired {
		t.Fatalf("等待唤醒期间到达有效期末刻应过期, got %v", v.Status)
	}
	// 唤醒仍在进行（超时 50，20+50=70），之后超时时不再有在途指令。
	mustReport(t, g, "v1", sleepReport(3, 71))
	if snap, _ := g.VehicleSnapshot("v1"); snap.WakeupActive {
		t.Fatalf("唤醒超时后不应有进行中唤醒")
	}
}

// TestAckUnknownCommand 未知指令回执按参数非法拒绝。
func TestAckUnknownCommand(t *testing.T) {
	g := newTestGateway(t)
	mustReport(t, g, "v1", awakeReport(1, 0))
	_, err := g.Ack("v1", "cmd-999", 10, true)
	expectRejectErr(t, err, RejectInvalidParam)
	_, err = g.Ack("ghost", "cmd-1", 10, true)
	expectRejectErr(t, err, RejectUnknownVehicle)
}

// TestInvalidParams 参数非法校验。
func TestInvalidParams(t *testing.T) {
	g := newTestGateway(t)
	mustReport(t, g, "v1", awakeReport(1, 0))
	expectReject(t, g, submitReq("", "r", CmdFindCar, 10, 60), RejectInvalidParam)
	expectReject(t, g, submitReq("v1", "", CmdFindCar, 10, 60), RejectInvalidParam)
	bad := submitReq("v1", "r", CmdFindCar, -1, 60)
	expectReject(t, g, bad, RejectInvalidParam)
	bad = submitReq("v1", "r", CmdFindCar, 10, 0)
	expectReject(t, g, bad, RejectInvalidParam)
	bad = submitReq("v1", "r", CmdType(99), 10, 60)
	expectReject(t, g, bad, RejectInvalidParam)
	// 上报参数。
	rep := awakeReport(2, 10)
	rep.BatteryPct = 101
	_, err := g.ReportState("v1", rep)
	expectRejectErr(t, err, RejectInvalidParam)
	rep = awakeReport(2, 10)
	rep.Seq = -1
	_, err = g.ReportState("v1", rep)
	expectRejectErr(t, err, RejectInvalidParam)
}
