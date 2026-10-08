package gateway

import "testing"

// testConfig 返回便于构造边界的测试配置。
func testConfig() Config {
	return Config{
		MinBatteryPct:           20,
		StalenessThresholdSec:   100,
		WakeupTimeoutSec:        50,
		WakeupQuotaPerDay:       2,
		DayOffsetSec:            0,
		IdempotencyRetentionSec: 1000,
	}
}

func newTestGateway(t *testing.T) *Gateway {
	t.Helper()
	g, err := NewGateway(testConfig())
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	return g
}

// awakeReport 构造一条"唤醒、驻车、全锁、静止、80% 电量"的上报。
func awakeReport(seq, tm int64) StateReport {
	return StateReport{
		Seq: seq, Time: tm,
		Gear: GearPark, SpeedKmh: 0, Power: PowerAwake,
		Lock: LockAllLocked, BatteryPct: 80,
	}
}

// sleepReport 构造一条休眠上报。
func sleepReport(seq, tm int64) StateReport {
	r := awakeReport(seq, tm)
	r.Power = PowerSleep
	return r
}

// submitReq 构造提交请求，提交者固定为 "app"。
func submitReq(vid, requestID string, typ CmdType, tm, validity int64) SubmitRequest {
	return SubmitRequest{
		VehicleID:   vid,
		Submitter:   "app",
		RequestID:   requestID,
		Type:        typ,
		Time:        tm,
		ValiditySec: validity,
	}
}

func mustReport(t *testing.T, g *Gateway, vid string, rep StateReport) {
	t.Helper()
	out, err := g.ReportState(vid, rep)
	if err != nil {
		t.Fatalf("ReportState(%s, %+v): %v", vid, rep, err)
	}
	if out != ReportAccepted {
		t.Fatalf("ReportState(%s, %+v) = %v, want accepted", vid, rep, out)
	}
}

func mustSubmit(t *testing.T, g *Gateway, req SubmitRequest) SubmitResult {
	t.Helper()
	res, err := g.SubmitCommand(req)
	if err != nil {
		t.Fatalf("SubmitCommand(%+v): %v", req, err)
	}
	return res
}

// expectReject 断言提交被以指定原因拒绝。
func expectReject(t *testing.T, g *Gateway, req SubmitRequest, want RejectReason) {
	t.Helper()
	_, err := g.SubmitCommand(req)
	expectRejectErr(t, err, want)
}

func expectRejectErr(t *testing.T, err error, want RejectReason) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望拒绝 %v，实际成功", want)
	}
	re, ok := err.(*RejectError)
	if !ok {
		t.Fatalf("错误类型 %T (%v)，期望 *RejectError", err, err)
	}
	if re.Reason != want {
		t.Fatalf("拒绝原因 %v，期望 %v", re.Reason, want)
	}
}

func mustQuery(t *testing.T, g *Gateway, vid, cmdID string) CommandView {
	t.Helper()
	view, ok := g.QueryCommand(vid, cmdID)
	if !ok {
		t.Fatalf("QueryCommand(%s, %s): 未找到", vid, cmdID)
	}
	return view
}

func mustAck(t *testing.T, g *Gateway, vid, cmdID string, tm int64, success bool) AckResult {
	t.Helper()
	res, err := g.Ack(vid, cmdID, tm, success)
	if err != nil {
		t.Fatalf("Ack(%s, %s, %d, %v): %v", vid, cmdID, tm, success, err)
	}
	return res
}
