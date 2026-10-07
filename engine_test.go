package ontology

import "testing"

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustKind(t *testing.T, err error, k Kind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error kind %v, got nil", k)
	}
	if got := KindOf(err); got != k {
		t.Fatalf("expected error kind %v, got %v (%v)", k, got, err)
	}
}

func mustStatus(t *testing.T, e *Engine, patient string, now int64, want Status, wantRelease int64) StatusResult {
	t.Helper()
	st, err := e.Status(patient, now)
	if err != nil {
		t.Fatalf("Status(%s): %v", patient, err)
	}
	if st.Status != want {
		t.Fatalf("Status(%s@%d) = %v, want %v (sources=%+v)", patient, now, st.Status, want, st.Sources)
	}
	if want != StatusNone && st.ReleaseAt != wantRelease {
		t.Fatalf("Status(%s@%d) release = %d, want %d", patient, now, st.ReleaseAt, wantRelease)
	}
	return st
}

// 累计时长恰为 120 算入密切接触，差一分钟（119）不算。
func TestThresholdExactly120vs119(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.BackfillStay("P", "W1", 1000, 2000, 5000))
	mustOK(t, e.BackfillStay("X1", "W1", 1000, 1120, 5000)) // 与 P 重叠 120 分钟
	mustOK(t, e.BackfillStay("X2", "W1", 1000, 1119, 5000)) // 与 P 重叠 119 分钟
	mustOK(t, e.RegisterCase("C1", "P", 1500, 5000))

	mustStatus(t, e, "X1", 5000, StatusClose, 1120+CloseIsolationMinutes)
	mustStatus(t, e, "X2", 5000, StatusNone, 0)

	ct, err := e.CaseContacts("C1", 5000)
	mustOK(t, err)
	if len(ct.Close) != 1 || ct.Close[0].PatientID != "X1" ||
		ct.Close[0].Minutes != 120 || ct.Close[0].LastContact != 1120 {
		t.Fatalf("close = %+v", ct.Close)
	}
	if len(ct.Secondary) != 0 {
		t.Fatalf("secondary = %+v", ct.Secondary)
	}
}

// 跨病房、跨住宿段的重叠时长累计相加。
func TestCrossWardAccumulation(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.BackfillStay("P", "W1", 1000, 1500, 5000))
	mustOK(t, e.BackfillStay("P", "W2", 2000, 2500, 5000))
	mustOK(t, e.BackfillStay("X", "W1", 1000, 1060, 5000)) // W1 重叠 60
	mustOK(t, e.BackfillStay("X", "W2", 2100, 2160, 5000)) // W2 重叠 60，合计 120
	mustOK(t, e.RegisterCase("C1", "P", 1200, 5000))

	st := mustStatus(t, e, "X", 5000, StatusClose, 2160+CloseIsolationMinutes)
	if len(st.Sources) != 1 || st.Sources[0].LastContact != 2160 {
		t.Fatalf("sources = %+v", st.Sources)
	}
}

// 传染期起点（含）与隔离时刻（不含）取等。
func TestInfectiousWindowBoundaries(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.BackfillStay("P", "W1", 2000, 6000, 10000))
	// 发病 5000 -> 传染期起点 2120（含）
	mustOK(t, e.BackfillStay("Xstart", "W1", 2120, 2240, 10000))  // 自起点起恰 120
	mustOK(t, e.BackfillStay("Xbefore", "W1", 2000, 2120, 10000)) // 止于起点，不计
	// 隔离时刻 4000（不含）
	mustOK(t, e.BackfillStay("Xend", "W1", 3880, 4000, 10000))   // 止于隔离时刻，恰 120
	mustOK(t, e.BackfillStay("Xafter", "W1", 4000, 4120, 10000)) // 起于隔离时刻，不计
	mustOK(t, e.RegisterCase("C1", "P", 5000, 10000))
	mustOK(t, e.RegisterIsolation("C1", 4000, 10000))

	mustStatus(t, e, "Xstart", 10000, StatusClose, 2240+CloseIsolationMinutes)
	mustStatus(t, e, "Xbefore", 10000, StatusNone, 0)
	mustStatus(t, e, "Xend", 10000, StatusClose, 4000+CloseIsolationMinutes)
	mustStatus(t, e, "Xafter", 10000, StatusNone, 0)
}

// 解除时刻 = 最后接触 + 7 天（密接）/ + 3 天（次密接），恰到该时刻即已解除。
func TestReleaseExactBoundaries(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.BackfillStay("P", "W1", 1000, 2000, 5000))
	mustOK(t, e.BackfillStay("X1", "W1", 1000, 1120, 5000)) // 密接，最后接触 1120
	mustOK(t, e.BackfillStay("Q", "W1", 1000, 1200, 5000))  // 密接，最后接触 1200
	mustOK(t, e.BackfillStay("Q", "W2", 3000, 3200, 5000))
	mustOK(t, e.BackfillStay("Z", "W2", 3000, 3200, 5000)) // 次密接，最后接触 3200
	mustOK(t, e.RegisterCase("C1", "P", 1500, 5000))

	// 次密接解除时刻 3200 + 4320 = 7520
	mustStatus(t, e, "Z", 7519, StatusSecondary, 7520)
	mustStatus(t, e, "Z", 7520, StatusReleased, 7520)
	// 密接解除时刻 1120 + 10080 = 11200
	mustStatus(t, e, "X1", 11199, StatusClose, 11200)
	mustStatus(t, e, "X1", 11200, StatusReleased, 11200)
}

// 次密接暴露期 = [首个重叠起点, 确诊登记时刻)；与确诊登记时刻取等不计。
func TestSecondaryExposureWindowBoundary(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.BackfillStay("P", "W1", 1000, 2000, 5000))
	mustOK(t, e.BackfillStay("Q", "W1", 1000, 1300, 5000)) // 密接，首个重叠起点 1000
	mustOK(t, e.RegisterCase("C1", "P", 1500, 5000))
	// 病例确诊登记时刻 R = 5000，暴露期 [1000, 5000)；以下均为事后追补
	mustOK(t, e.BackfillStay("Q", "W2", 4000, 5200, 6000))
	mustOK(t, e.BackfillStay("Zin", "W2", 4880, 5100, 6000))    // 暴露期内 [4880,5000) 恰 120
	mustOK(t, e.BackfillStay("Zout", "W2", 5000, 5120, 6000))   // 起于 R，不计
	mustOK(t, e.BackfillStay("Zshort", "W2", 4881, 5000, 6000)) // 暴露期内 119，不足

	mustStatus(t, e, "Zin", 6000, StatusSecondary, 5000+SecondaryObservationMinutes)
	mustStatus(t, e, "Zout", 6000, StatusNone, 0)
	mustStatus(t, e, "Zshort", 6000, StatusNone, 0)

	ct, err := e.CaseContacts("C1", 6000)
	mustOK(t, err)
	if len(ct.Secondary) != 1 || ct.Secondary[0].PatientID != "Zin" ||
		ct.Secondary[0].LastContact != 5000 || ct.Secondary[0].Via != "Q" {
		t.Fatalf("secondary = %+v", ct.Secondary)
	}
}

// 多病例叠加：仍在期内者取最严等级，解除时刻取各来源最晚。
func TestMultiCaseStrictestAndLatest(t *testing.T) {
	e := NewEngine()
	// C1：X 为密接，最后接触 1120，解除 11200
	mustOK(t, e.BackfillStay("P1", "W1", 1000, 2000, 5000))
	mustOK(t, e.BackfillStay("X", "W1", 1000, 1120, 5000))
	mustOK(t, e.RegisterCase("C1", "P1", 1500, 5000))
	// C2：X 为次密接（经 Q2），最后接触 9200，解除 9200+4320 = 13520
	mustOK(t, e.BackfillStay("P2", "W2", 3000, 4000, 5000))
	mustOK(t, e.BackfillStay("Q2", "W2", 3000, 3200, 5000))
	mustOK(t, e.BackfillStay("Q2", "W3", 9000, 9200, 9500))
	mustOK(t, e.BackfillStay("X", "W3", 9000, 9200, 9500))
	mustOK(t, e.RegisterCase("C2", "P2", 3500, 9500))

	// 两来源均在期内：取最严（密接），解除时刻取最晚 13520
	mustStatus(t, e, "X", 10000, StatusClose, 13520)
	// C1 来源已过期、C2 仍在期内：当前状态为次密接观察中
	mustStatus(t, e, "X", 12000, StatusSecondary, 13520)
	// 全部过期：已解除，解除时刻仍为最晚来源 13520
	mustStatus(t, e, "X", 13520, StatusReleased, 13520)
}

// 改正发病时刻使接触者进出：传染期随发病时刻改变。
func TestCorrectOnsetMovesContacts(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.BackfillStay("P", "W1", 2000, 6000, 10000))
	mustOK(t, e.BackfillStay("X", "W1", 2120, 2240, 10000)) // 与 P 重叠 [2120,2240)
	mustOK(t, e.BackfillStay("Z", "W1", 2000, 2120, 10000)) // 与 P 重叠 [2000,2120)
	mustOK(t, e.RegisterCase("C1", "P", 5000, 10000))       // 传染期起点 2120

	// 起点 2120：X 在内（恰 120），Z 止于起点不在内
	mustStatus(t, e, "X", 10000, StatusClose, 2240+CloseIsolationMinutes)
	mustStatus(t, e, "Z", 10000, StatusNone, 0)

	// 改正发病为 4800 -> 起点 1920：Z 进入（200 分钟），X 仍在
	mustOK(t, e.CorrectOnset("C1", 4800, 10000))
	mustStatus(t, e, "Z", 10000, StatusClose, 2120+CloseIsolationMinutes)
	mustStatus(t, e, "X", 10000, StatusClose, 2240+CloseIsolationMinutes)

	// 改正发病为 5200 -> 起点 2320：X、Z 均离开
	mustOK(t, e.CorrectOnset("C1", 5200, 10000))
	mustStatus(t, e, "X", 10000, StatusNone, 0)
	mustStatus(t, e, "Z", 10000, StatusNone, 0)
}

// 追补过去的住宿区间，使已解除者重新进入隔离。
func TestBackfillReIsolatesReleased(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.BackfillStay("P", "W1", 1000, 2000, 5000))
	mustOK(t, e.BackfillStay("X", "W1", 1000, 1120, 5000))
	mustOK(t, e.RegisterCase("C1", "P", 1500, 5000))

	// 解除时刻 1120 + 10080 = 11200，恰到即解除
	mustStatus(t, e, "X", 11200, StatusReleased, 11200)

	// 追补 X 在传染期内更晚的一段住宿：最后接触变为 2000
	mustOK(t, e.BackfillStay("X", "W1", 1900, 2000, 11200))
	mustStatus(t, e, "X", 11200, StatusClose, 2000+CloseIsolationMinutes)
	mustStatus(t, e, "X", 12080, StatusReleased, 12080)
}

// 撤销病例：撤销后不再产生任何接触者。
func TestRevokeCase(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.BackfillStay("P", "W1", 1000, 2000, 5000))
	mustOK(t, e.BackfillStay("X", "W1", 1000, 1120, 5000))
	mustOK(t, e.RegisterCase("C1", "P", 1500, 5000))
	mustStatus(t, e, "X", 5000, StatusClose, 11200)

	mustOK(t, e.RevokeCase("C1", 5000))
	mustStatus(t, e, "X", 5000, StatusNone, 0)

	ct, err := e.CaseContacts("C1", 5000)
	mustOK(t, err)
	if len(ct.Close) != 0 || len(ct.Secondary) != 0 {
		t.Fatalf("revoked case contacts = %+v", ct)
	}

	// 撤销后的病例上的状态型操作均报状态不符
	mustKind(t, e.RevokeCase("C1", 5000), KindStateConflict)
	mustKind(t, e.RegisterIsolation("C1", 2000, 5000), KindStateConflict)
	mustKind(t, e.CorrectOnset("C1", 1000, 5000), KindStateConflict)
}

// 错误优先级：参数非法 > 时钟回退 > 对象不存在 > 状态不符 > 住宿冲突。
func TestErrorPrecedence(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.BackfillStay("A", "W1", 100, 200, 1000)) // 时钟推进到 1000

	// 参数非法 > 时钟回退：空标识 且 now 回退
	mustKind(t, e.Discharge("", 500, 999), KindInvalidParam)
	// 时钟回退 > 状态不符：now 回退 且 无未出住记录
	mustKind(t, e.Discharge("A", 500, 999), KindClockRollback)
	// 时钟回退 > 对象不存在
	mustKind(t, e.RegisterIsolation("NOPE", 100, 999), KindClockRollback)
	// 对象不存在
	mustKind(t, e.RegisterIsolation("NOPE", 100, 1000), KindNotFound)
	_, err := e.CaseContacts("NOPE", 1000)
	mustKind(t, err, KindNotFound)
	// 对象不存在（无任何住宿记录）vs 状态不符（有记录但无未出住）
	mustKind(t, e.Discharge("B", 300, 1000), KindNotFound)
	mustKind(t, e.Discharge("A", 300, 1000), KindStateConflict)

	// 依赖病例状态的参数非法 > 状态不符：隔离时刻早于传染期起点 且 已隔离过
	mustOK(t, e.RegisterCase("C2", "Q", 5000, 10000))
	mustOK(t, e.RegisterIsolation("C2", 3000, 10000))
	mustKind(t, e.RegisterIsolation("C2", 1000, 10000), KindInvalidParam)
	mustKind(t, e.RegisterIsolation("C2", 3000, 10000), KindStateConflict)

	// 状态不符 > 住宿冲突：已有未出住记录时再次入住
	mustOK(t, e.Admit("D", "W1", 100, 10000))
	mustKind(t, e.Admit("D", "W2", 200, 10000), KindStateConflict)
	// 住宿冲突：追补与未出住区间重叠
	mustKind(t, e.BackfillStay("D", "W3", 50, 150, 10000), KindStayConflict)

	// 其余参数非法与状态不符
	mustKind(t, e.RegisterCase("C2", "Z", 100, 10000), KindStateConflict) // 病例 ID 重复
	mustKind(t, e.CorrectOnset("NOPE", 100, 10000), KindNotFound)
	mustKind(t, e.RevokeCase("NOPE", 10000), KindNotFound)
	_, err = e.Status("", 10000)
	mustKind(t, err, KindInvalidParam)
	_, err = e.Status("A", MaxTimeMinutes+1)
	mustKind(t, err, KindInvalidParam)
	mustKind(t, e.RegisterCase("C9", "Z", 10001, 10000), KindInvalidParam)    // 发病晚于 now
	mustKind(t, e.BackfillStay("D", "W4", 200, 100, 10000), KindInvalidParam) // 出住不晚于入住
	mustKind(t, e.Discharge("D", 100, 10000), KindInvalidParam)               // 出住须晚于入住
}

// 次密接不再向外传递。
func TestSecondaryDoesNotPropagate(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.BackfillStay("P", "W1", 1000, 2000, 10000))
	mustOK(t, e.BackfillStay("Q", "W1", 1000, 1300, 10000)) // 密接
	mustOK(t, e.BackfillStay("Q", "W2", 3000, 3400, 10000))
	mustOK(t, e.BackfillStay("Z", "W2", 3000, 3400, 10000)) // 次密接
	mustOK(t, e.BackfillStay("Z", "W3", 5000, 5600, 10000))
	mustOK(t, e.BackfillStay("W", "W3", 5000, 5600, 10000)) // 与次密接长时间同室，不应被认定
	mustOK(t, e.RegisterCase("C1", "P", 1500, 10000))

	mustStatus(t, e, "Q", 10000, StatusClose, 1300+CloseIsolationMinutes)
	// Z 的解除时刻为 3400+4320=7720，now=10000 时已解除
	mustStatus(t, e, "Z", 10000, StatusReleased, 3400+SecondaryObservationMinutes)
	mustStatus(t, e, "W", 10000, StatusNone, 0)
}

// 被拒绝的操作不改变任何状态与时钟。
func TestRejectedOpKeepsClockAndState(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.BackfillStay("A", "W1", 100, 200, 1000))
	if got := e.Clock(); got != 1000 {
		t.Fatalf("clock = %d", got)
	}
	// 住宿冲突被拒绝：时钟不变、状态不变
	mustKind(t, e.BackfillStay("A", "W1", 150, 250, 5000), KindStayConflict)
	if got := e.Clock(); got != 1000 {
		t.Fatalf("clock changed after rejection: %d", got)
	}
	// 时钟回退被拒绝：时钟不变
	mustKind(t, e.Discharge("A", 150, 500), KindClockRollback)
	if got := e.Clock(); got != 1000 {
		t.Fatalf("clock changed after rejection: %d", got)
	}
	// 以原时钟继续操作仍被接受
	mustOK(t, e.BackfillStay("A2", "W1", 300, 400, 1000))
	// 状态不符被拒绝：A 的住宿记录不受影响（仍无未出住记录）
	mustKind(t, e.Discharge("A", 150, 1000), KindStateConflict)
	mustOK(t, e.Admit("A", "W1", 500, 1000))
	mustOK(t, e.Discharge("A", 600, 1000))
}

// 病例本人不是自己的接触者；入住-出住流程与开区间语义。
func TestCasePatientAndOpenStay(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.BackfillStay("P", "W1", 500, 2000, 5000))
	mustOK(t, e.Admit("X", "W1", 1000, 5000)) // 未出住，视为持续到 now
	mustOK(t, e.RegisterCase("C1", "P", 1500, 5000))

	// 病例本人状态为无关
	mustStatus(t, e, "P", 5000, StatusNone, 0)
	// X 的开区间与 P 重叠 [1000, 2000)，最后接触 2000
	mustStatus(t, e, "X", 5000, StatusClose, 2000+CloseIsolationMinutes)

	// 登记出住后，重叠收缩为 [1000, 1500)
	mustOK(t, e.Discharge("X", 1500, 6000))
	mustStatus(t, e, "X", 6000, StatusClose, 1500+CloseIsolationMinutes)
}
