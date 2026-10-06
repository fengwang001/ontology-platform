package dr

import (
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		IntervalTicks:       15,
		TicksPerDay:         1440,
		AdjustmentIntervals: 4,
		QualifyingDays:      3,
		MinQualifyingDays:   2,
		MinCommitment:       10,
		AdjRatioLower:       0.8,
		AdjRatioUpper:       1.2,
		MaxLookbackDays:     40,
	}
}

func mkSystem(t *testing.T) *System {
	t.Helper()
	s, err := New(testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func paramsAt(day, startOff int64, intervals int) EventParams {
	start := day*1440 + startOff
	return EventParams{
		Day:              day,
		WindowStart:      start,
		WindowIntervals:  intervals,
		ResponseDeadline: start - 120,
		ExitDeadline:     start - 60,
		PayUnitPrice:     2,
		PenaltyUnitPrice: 3,
		QualifiedRatio:   0.5,
	}
}

func stdParams(day int64) EventParams { return paramsAt(day, 600, 4) }

func tick(now *int64) int64 { *now++; return *now }

func fwd(now *int64, t int64) int64 {
	if t <= *now {
		t = *now + 1
	}
	*now = t
	return t
}

func mustCreate(t *testing.T, s *System, now *int64, p EventParams) string {
	t.Helper()
	id, err := s.CreateEvent(tick(now), p)
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	return id
}

func mustInvite(t *testing.T, s *System, now *int64, id, p string, requested float64) {
	t.Helper()
	if err := s.Invite(tick(now), id, p, requested); err != nil {
		t.Fatalf("Invite: %v", err)
	}
}

func mustAccept(t *testing.T, s *System, now *int64, id, p string, amount float64) {
	t.Helper()
	if err := s.Respond(tick(now), id, p, true, amount); err != nil {
		t.Fatalf("Respond accept: %v", err)
	}
}

func fillDay(t *testing.T, s *System, now *int64, p string, day int64, value float64) {
	t.Helper()
	for off := int64(0); off < 1440; off += 15 {
		if err := s.RegisterData(tick(now), p, day*1440+off, value); err != nil {
			t.Fatalf("RegisterData day=%d off=%d: %v", day, off, err)
		}
	}
}

func fillWindowAdjust(t *testing.T, s *System, now *int64, p string, params EventParams, value float64) {
	t.Helper()
	start := params.Day*1440 + 540 // 调整期起点 = 窗口起点 600 - 60
	for off := start; off < params.WindowStart+int64(params.WindowIntervals)*15; off += 15 {
		if err := s.RegisterData(tick(now), p, off, value); err != nil {
			t.Fatalf("RegisterData off=%d: %v", off, err)
		}
	}
}

func setupAccepted(t *testing.T, s *System, now *int64, params EventParams, p string, committed float64) string {
	t.Helper()
	id := mustCreate(t, s, now, params)
	mustInvite(t, s, now, id, p, 40)
	mustAccept(t, s, now, id, p, committed)
	return id
}

func errKind(err error) ErrKind {
	if err == nil {
		return -1
	}
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return -1
}

// 应答与退出恰在截止时刻。
func TestRespondExactlyAtDeadline(t *testing.T) {
	s := mkSystem(t)
	var now int64
	params := stdParams(22)
	id := mustCreate(t, s, &now, params)
	mustInvite(t, s, &now, id, "P1", 40)
	if err := s.Respond(params.ResponseDeadline, id, "P1", true, 40); errKind(err) != ErrKindDeadline {
		t.Fatalf("截止时刻应答应报已过截止，得到 %v", err)
	}
	if err := s.Respond(params.ResponseDeadline-1, id, "P1", true, 40); err != nil {
		t.Fatalf("截止前一刻应答应成功：%v", err)
	}
}

func TestWithdrawExactlyAtDeadline(t *testing.T) {
	// 截止前一刻退出：免责，承诺移除。
	s := mkSystem(t)
	var now int64
	params := stdParams(22)
	id := setupAccepted(t, s, &now, params, "P1", 40)
	if err := s.Withdraw(params.ExitDeadline-1, id, "P1"); err != nil {
		t.Fatalf("免责截止前退出应成功：%v", err)
	}
	if _, ok := s.CommitmentOf(id, "P1"); ok {
		t.Fatalf("免责退出后承诺应被移除")
	}

	// 恰在免责退出截止时刻：视为承诺仍在、削减按零考核。
	s2 := mkSystem(t)
	var now2 int64
	id2 := setupAccepted(t, s2, &now2, params, "P1", 40)
	if err := s2.Withdraw(params.ExitDeadline, id2, "P1"); err != nil {
		t.Fatalf("截止时刻退出应走迟到退出路径：%v", err)
	}
	c, ok := s2.CommitmentOf(id2, "P1")
	if !ok || !c.LateWithdrawn {
		t.Fatalf("应为迟到退出承诺：%+v ok=%v", c, ok)
	}

	// 窗口结束后不得退出。
	s3 := mkSystem(t)
	var now3 int64
	id3 := setupAccepted(t, s3, &now3, params, "P1", 40)
	end := params.WindowStart + int64(params.WindowIntervals)*15
	if err := s3.Withdraw(end, id3, "P1"); errKind(err) != ErrKindDeadline {
		t.Fatalf("窗口结束后退出应报已过截止，得到 %v", err)
	}
}

// 承诺量恰等于最小承诺量。
func TestCommitmentExactlyMin(t *testing.T) {
	s := mkSystem(t)
	var now int64
	id := mustCreate(t, s, &now, stdParams(22))
	mustInvite(t, s, &now, id, "P1", 40)
	if err := s.Respond(tick(&now), id, "P1", true, 10); err != nil {
		t.Fatalf("承诺量等于最小承诺量应成功：%v", err)
	}
	mustInvite(t, s, &now, id, "P2", 40)
	if err := s.Respond(tick(&now), id, "P2", true, 9.5); errKind(err) != ErrKindParam {
		t.Fatalf("承诺量小于最小承诺量应报参数非法，得到 %v", err)
	}
	mustInvite(t, s, &now, id, "P3", 40)
	if err := s.Respond(tick(&now), id, "P3", true, 41); errKind(err) != ErrKindParam {
		t.Fatalf("承诺量大于请求量应报参数非法，得到 %v", err)
	}
}

// 资格日恰好够（等于最少数量）与少一天。
func TestQualifyingDaysExactAndShort(t *testing.T) {
	build := func(fillDays []int64) *Assessment {
		s := mkSystem(t)
		var now int64
		params := stdParams(22)
		id := setupAccepted(t, s, &now, params, "P1", 40)
		for _, d := range fillDays {
			fillDay(t, s, &now, "P1", d, 100)
		}
		fillWindowAdjust(t, s, &now, "P1", params, 100)
		res, err := s.Assess(fwd(&now, params.WindowStart+60), id)
		if err != nil {
			t.Fatalf("Assess: %v", err)
		}
		return res
	}
	// 事件日 22 为工作日；候选工作日 21、18 有数据 → 恰好 2 天。
	res := build([]int64{21, 18})
	r := res.Results[0]
	if !r.Assessable || r.QualifyingDays != 2 {
		t.Fatalf("恰好 2 个资格日应可考核：%+v", r)
	}
	if r.Payment != 0 || r.Penalty != 120 {
		t.Fatalf("削减为 0 应只计违约金 120：%+v", r)
	}
	// 少一天 → 不可考核，不付不罚。
	res2 := build([]int64{21})
	r2 := res2.Results[0]
	if r2.Assessable || r2.QualifyingDays != 1 || r2.Payment != 0 || r2.Penalty != 0 {
		t.Fatalf("资格日不足应不可考核且不付不罚：%+v", r2)
	}
}

// 窗口开始前被取消的事件不再占用资格日排除。
func TestCancelledEventNotExcluding(t *testing.T) {
	build := func(cancelA bool) *ParticipantResult {
		s := mkSystem(t)
		var now int64
		paramsA := stdParams(15)
		idA := setupAccepted(t, s, &now, paramsA, "P1", 40)
		if cancelA {
			if err := s.CancelEvent(tick(&now), idA); err != nil {
				t.Fatalf("CancelEvent: %v", err)
			}
		}
		fillDay(t, s, &now, "P1", 21, 100)
		fillDay(t, s, &now, "P1", 18, 100)
		fillDay(t, s, &now, "P1", 15, 200)
		paramsB := stdParams(22)
		idB := setupAccepted(t, s, &now, paramsB, "P1", 40)
		fillWindowAdjust(t, s, &now, "P1", paramsB, 100)
		res, err := s.Assess(fwd(&now, paramsB.WindowStart+60), idB)
		if err != nil {
			t.Fatalf("Assess: %v", err)
		}
		return &res.Results[0]
	}
	r := build(true)
	if r.QualifyingDays != 3 {
		t.Fatalf("被取消事件日 15 不应再排除，资格日应为 3：%+v", r)
	}
	r2 := build(false)
	if r2.QualifyingDays != 2 {
		t.Fatalf("未取消事件日 15 应被排除，资格日应为 2：%+v", r2)
	}
}

// 校正比例恰在上下界（取等不裁剪）及界外裁剪。
func TestAdjustRatioBounds(t *testing.T) {
	build := func(adjustValue float64) *ParticipantResult {
		s := mkSystem(t)
		var now int64
		params := stdParams(22)
		id := setupAccepted(t, s, &now, params, "P1", 40)
		fillDay(t, s, &now, "P1", 21, 100)
		fillDay(t, s, &now, "P1", 18, 100)
		// 事件日：调整期（540..600）用 adjustValue，窗口（600..660）用 100。
		for off := int64(540); off < 600; off += 15 {
			if err := s.RegisterData(tick(&now), "P1", 22*1440+off, adjustValue); err != nil {
				t.Fatalf("RegisterData: %v", err)
			}
		}
		for off := int64(600); off < 660; off += 15 {
			if err := s.RegisterData(tick(&now), "P1", 22*1440+off, 100); err != nil {
				t.Fatalf("RegisterData: %v", err)
			}
		}
		res, err := s.Assess(fwd(&now, params.WindowStart+60), id)
		if err != nil {
			t.Fatalf("Assess: %v", err)
		}
		return &res.Results[0]
	}
	if r := build(80); r.AdjRatio != 0.8 {
		t.Fatalf("比例恰在下界应不裁剪（0.8）：%+v", r)
	}
	if r := build(120); r.AdjRatio != 1.2 {
		t.Fatalf("比例恰在上界应不裁剪（1.2）：%+v", r)
	}
	if r := build(10); r.AdjRatio != 0.8 {
		t.Fatalf("比例低于下界应裁剪到 0.8：%+v", r)
	}
	if r := build(200); r.AdjRatio != 1.2 {
		t.Fatalf("比例高于上界应裁剪到 1.2：%+v", r)
	}
}

// 履约率恰等于合格比例与恰等于一。
func TestRatioExactlyQualifiedAndOne(t *testing.T) {
	build := func(windowValue float64) *ParticipantResult {
		s := mkSystem(t)
		var now int64
		params := stdParams(22)
		id := setupAccepted(t, s, &now, params, "P1", 40)
		fillDay(t, s, &now, "P1", 21, 100)
		fillDay(t, s, &now, "P1", 18, 100)
		for off := int64(540); off < 600; off += 15 {
			if err := s.RegisterData(tick(&now), "P1", 22*1440+off, 100); err != nil {
				t.Fatalf("RegisterData: %v", err)
			}
		}
		for off := int64(600); off < 660; off += 15 {
			if err := s.RegisterData(tick(&now), "P1", 22*1440+off, windowValue); err != nil {
				t.Fatalf("RegisterData: %v", err)
			}
		}
		res, err := s.Assess(fwd(&now, params.WindowStart+60), id)
		if err != nil {
			t.Fatalf("Assess: %v", err)
		}
		return &res.Results[0]
	}
	// 削减 40 = 承诺 40 → 履约率恰为 1，按承诺量全额支付。
	r := build(90)
	if r.Ratio != 1 || r.Payment != 80 || r.Penalty != 0 {
		t.Fatalf("履约率恰为 1 应按承诺量支付 80：%+v", r)
	}
	// 削减 20 → 履约率恰为合格比例 0.5，取等视为达标，按实际削减量支付。
	r2 := build(95)
	if r2.Ratio != 0.5 || r2.Payment != 40 || r2.Penalty != 0 {
		t.Fatalf("履约率恰为合格比例应按实际削减量支付 40：%+v", r2)
	}
	// 削减 16 → 履约率 0.4 低于合格比例，不付并按承诺量计违约金。
	r3 := build(96)
	if r3.Ratio != 0.4 || r3.Payment != 0 || r3.Penalty != 120 {
		t.Fatalf("履约率低于合格比例应只计违约金 120：%+v", r3)
	}
}

// 窗口开始后取消：窗口截断、承诺量折算后再考核。
func TestTruncateCancelScaling(t *testing.T) {
	s := mkSystem(t)
	var now int64
	params := stdParams(22)
	id := setupAccepted(t, s, &now, params, "P1", 40)
	fillDay(t, s, &now, "P1", 21, 100)
	fillDay(t, s, &now, "P1", 18, 100)
	for off := int64(540); off < 600; off += 15 {
		if err := s.RegisterData(tick(&now), "P1", 22*1440+off, 100); err != nil {
			t.Fatalf("RegisterData: %v", err)
		}
	}
	// 只填截断后窗口（前两个间隔）的数据，实际 90 → 每间隔削减 10。
	for off := int64(600); off < 630; off += 15 {
		if err := s.RegisterData(tick(&now), "P1", 22*1440+off, 90); err != nil {
			t.Fatalf("RegisterData: %v", err)
		}
	}
	// 在第三个间隔内取消 → 窗口截断到起点 + 2 个间隔。
	cancelAt := params.WindowStart + 2*15 + 7
	if err := s.CancelEvent(fwd(&now, cancelAt), id); err != nil {
		t.Fatalf("CancelEvent: %v", err)
	}
	if st, _ := s.EventState(id, cancelAt); st != StateCancelled {
		t.Fatalf("取消后状态应为已取消，得到 %s", st)
	}
	res, err := s.Assess(fwd(&now, params.WindowStart+60), id)
	if err != nil {
		t.Fatalf("取消后事件应可考核：%v", err)
	}
	r := res.Results[0]
	if r.Committed != 20 || r.Reduction != 20 || r.Ratio != 1 || r.Payment != 40 || r.Penalty != 0 {
		t.Fatalf("截断折算后承诺 20、削减 20、报酬 40：%+v", r)
	}
	if st, _ := s.EventState(id, fwd(&now, params.WindowStart+61)); st != StateSettled {
		t.Fatalf("考核后状态应为已考核，得到 %s", st)
	}
}

// 窗口开始前取消：释放所有参与者，不考核。
func TestCancelBeforeStartReleases(t *testing.T) {
	s := mkSystem(t)
	var now int64
	params := stdParams(22)
	id := setupAccepted(t, s, &now, params, "P1", 40)
	if err := s.CancelEvent(tick(&now), id); err != nil {
		t.Fatalf("CancelEvent: %v", err)
	}
	if _, ok := s.CommitmentOf(id, "P1"); ok {
		t.Fatalf("窗口前取消应释放承诺")
	}
	// 参与者可接受窗口重叠的新事件。
	params2 := paramsAt(22, 630, 4)
	id2 := mustCreate(t, s, &now, params2)
	mustInvite(t, s, &now, id2, "P1", 40)
	if err := s.Respond(tick(&now), id2, "P1", true, 40); err != nil {
		t.Fatalf("释放后接受重叠事件应成功：%v", err)
	}
	// 已取消事件不得考核。
	if _, err := s.Assess(fwd(&now, params.WindowStart+60), id); errKind(err) != ErrKindEventState {
		t.Fatalf("窗口前取消的事件不得考核，得到 %v", err)
	}
}

// 重叠窗口拒绝。
func TestOverlapReject(t *testing.T) {
	s := mkSystem(t)
	var now int64
	idA := setupAccepted(t, s, &now, stdParams(22), "P1", 40)
	_ = idA
	paramsB := paramsAt(22, 630, 4) // 与 A 的 600..660 重叠
	idB := mustCreate(t, s, &now, paramsB)
	mustInvite(t, s, &now, idB, "P1", 40)
	if err := s.Respond(tick(&now), idB, "P1", true, 40); errKind(err) != ErrKindEventConflict {
		t.Fatalf("重叠窗口应报事件冲突，得到 %v", err)
	}
	// 不重叠的事件可接受。
	paramsC := paramsAt(22, 705, 4)
	idC := mustCreate(t, s, &now, paramsC)
	mustInvite(t, s, &now, idC, "P1", 40)
	if err := s.Respond(tick(&now), idC, "P1", true, 40); err != nil {
		t.Fatalf("不重叠事件应可接受：%v", err)
	}
	// 免责退出 A 后可接受 B。
	if err := s.Withdraw(tick(&now), idA, "P1"); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if err := s.Respond(tick(&now), idB, "P1", true, 40); err != nil {
		t.Fatalf("免责退出后接受重叠事件应成功：%v", err)
	}
}

// 考核被拒绝时报出第一个不满足的参与者与原因，事件状态不变，可补数后重试。
func TestAssessRejectedStateUnchanged(t *testing.T) {
	s := mkSystem(t)
	var now int64
	params := stdParams(22)
	id := mustCreate(t, s, &now, params)
	mustInvite(t, s, &now, id, "P1", 40)
	mustAccept(t, s, &now, id, "P1", 40)
	mustInvite(t, s, &now, id, "P2", 40)
	mustAccept(t, s, &now, id, "P2", 40)
	// P1 数据齐全，P2 缺窗口数据。
	fillDay(t, s, &now, "P1", 21, 100)
	fillDay(t, s, &now, "P1", 18, 100)
	fillWindowAdjust(t, s, &now, "P1", params, 100)
	for off := int64(540); off < 600; off += 15 {
		if err := s.RegisterData(tick(&now), "P2", 22*1440+off, 100); err != nil {
			t.Fatalf("RegisterData: %v", err)
		}
	}
	assessAt := fwd(&now, params.WindowStart+60)
	_, err := s.Assess(assessAt, id)
	e, ok := err.(*Error)
	if !ok || e.Kind != ErrKindIncomplete || e.Participant != "P2" || e.Reason == "" {
		t.Fatalf("应报 P2 数据不齐：%v", err)
	}
	if st, _ := s.EventState(id, assessAt); st != StateEnded {
		t.Fatalf("考核被拒绝后状态应保持已结束，得到 %s", st)
	}
	if _, ok := s.AssessmentOf(id); ok {
		t.Fatalf("考核被拒绝后不应产生结果")
	}
	// 补齐 P2 数据后重试成功。
	fillDay(t, s, &now, "P2", 21, 100)
	fillDay(t, s, &now, "P2", 18, 100)
	fillWindowAdjust(t, s, &now, "P2", params, 100)
	res, err := s.Assess(fwd(&now, params.WindowStart+60), id)
	if err != nil {
		t.Fatalf("补数后考核应成功：%v", err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("应有两名参与者结果：%+v", res)
	}
}

// 考核成功后结果不可变，之后到达的相关数据被拒绝并报已考核。
func TestLateDataRejected(t *testing.T) {
	s := mkSystem(t)
	var now int64
	params := stdParams(22)
	id := setupAccepted(t, s, &now, params, "P1", 40)
	fillDay(t, s, &now, "P1", 21, 100)
	fillDay(t, s, &now, "P1", 18, 100)
	fillWindowAdjust(t, s, &now, "P1", params, 100)
	res, err := s.Assess(fwd(&now, params.WindowStart+60), id)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	again, err := s.Assess(tick(&now), id)
	if errKind(err) != ErrKindEventState || again != nil {
		t.Fatalf("重复考核应被拒绝，得到 %v", err)
	}
	// 已登记间隔的不同值 → 数据冲突（优先于已考核）。
	if err := s.RegisterData(tick(&now), "P1", params.WindowStart, 999); errKind(err) != ErrKindDataConflict {
		t.Fatalf("已登记间隔不同值应报数据冲突，得到 %v", err)
	}
	// 已登记间隔的相同值 → 幂等成功。
	if err := s.RegisterData(tick(&now), "P1", params.WindowStart, 100); err != nil {
		t.Fatalf("相同值重复登记应幂等：%v", err)
	}
	// 窗口前未登记过的间隔（历史日）→ 已考核。
	if err := s.RegisterData(tick(&now), "P1", 20*1440, 5); errKind(err) != ErrKindSettled {
		t.Fatalf("考核后迟到的历史数据应报已考核，得到 %v", err)
	}
	// 窗口结束之后的间隔不受影响。
	if err := s.RegisterData(tick(&now), "P1", 23*1440, 5); err != nil {
		t.Fatalf("窗口之后的数据应可登记：%v", err)
	}
	// 未参与事件的参与者不受水印影响。
	if err := s.RegisterData(tick(&now), "P9", 20*1440, 5); err != nil {
		t.Fatalf("无关参与者应可登记：%v", err)
	}
	_ = res
}

// 数据冲突与幂等。
func TestDataConflictIdempotent(t *testing.T) {
	s := mkSystem(t)
	var now int64
	if err := s.RegisterData(tick(&now), "P1", 100*15, 5); err != nil {
		t.Fatalf("RegisterData: %v", err)
	}
	if err := s.RegisterData(tick(&now), "P1", 100*15, 5); err != nil {
		t.Fatalf("相同值重复登记应幂等：%v", err)
	}
	if err := s.RegisterData(tick(&now), "P1", 100*15, 6); errKind(err) != ErrKindDataConflict {
		t.Fatalf("不同值应报数据冲突，得到 %v", err)
	}
	if v, ok := s.MeterValue("P1", 100*15); !ok || v != 5 {
		t.Fatalf("冲突登记不得改变数据：v=%v ok=%v", v, ok)
	}
	if err := s.RegisterData(tick(&now), "P1", 7, 5); errKind(err) != ErrKindParam {
		t.Fatalf("未对齐间隔应报参数非法，得到 %v", err)
	}
}

// 固定拒绝次序：参数非法 > 时钟回退 > 事件不存在或状态不允许 >
// 参与者未被邀约 > 已过截止 > 事件冲突 > 数据冲突 > 已考核。
func TestRejectOrder(t *testing.T) {
	s := mkSystem(t)
	var now int64
	paramsA := stdParams(22)
	idA := setupAccepted(t, s, &now, paramsA, "P1", 40)
	paramsB := stdParams(23)
	idB := mustCreate(t, s, &now, paramsB)
	mustInvite(t, s, &now, idB, "P2", 40)
	paramsC := paramsAt(22, 630, 4) // 与 A 重叠
	idC := mustCreate(t, s, &now, paramsC)
	mustInvite(t, s, &now, idC, "P1", 40)

	// 参数非法 > 时钟回退：承诺量不足且时刻回退。
	if err := s.Respond(0, idB, "P2", true, 5); errKind(err) != ErrKindParam {
		t.Fatalf("参数非法应优先于时钟回退，得到 %v", err)
	}
	// 时钟回退 > 事件不存在。
	if err := s.Respond(0, "NOPE", "P2", true, 40); errKind(err) != ErrKindClock {
		t.Fatalf("时钟回退应优先于事件不存在，得到 %v", err)
	}
	// 事件不存在 > 参与者未被邀约。
	if err := s.Respond(tick(&now), "NOPE", "P9", true, 40); errKind(err) != ErrKindEventState {
		t.Fatalf("事件不存在应优先于未被邀约，得到 %v", err)
	}
	// 参与者未被邀约 > 已过截止。
	if err := s.Respond(paramsB.ResponseDeadline, idB, "P9", true, 40); errKind(err) != ErrKindNotInvited {
		t.Fatalf("未被邀约应优先于已过截止，得到 %v", err)
	}
	// 已过截止 > 事件冲突：P1 已接受 A，C 与 A 重叠，但在 C 截止后应答。
	if err := s.Respond(paramsC.ResponseDeadline, idC, "P1", true, 40); errKind(err) != ErrKindDeadline {
		t.Fatalf("已过截止应优先于事件冲突，得到 %v", err)
	}
	// 事件冲突：截止前应答 C。
	if err := s.Respond(tick(&now), idC, "P1", true, 40); errKind(err) != ErrKindEventConflict {
		t.Fatalf("应报事件冲突，得到 %v", err)
	}
	// 数据冲突 > 已考核：先完成 A 的考核。
	fillDay(t, s, &now, "P1", 21, 100)
	fillDay(t, s, &now, "P1", 18, 100)
	fillWindowAdjust(t, s, &now, "P1", paramsA, 100)
	if _, err := s.Assess(fwd(&now, paramsA.WindowStart+60), idA); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if err := s.RegisterData(tick(&now), "P1", paramsA.WindowStart, 999); errKind(err) != ErrKindDataConflict {
		t.Fatalf("数据冲突应优先于已考核，得到 %v", err)
	}
	if err := s.RegisterData(tick(&now), "P1", 20*1440, 5); errKind(err) != ErrKindSettled {
		t.Fatalf("应报已考核，得到 %v", err)
	}
}

// 免责退出者不参与考核；迟到退出者削减按零考核。
func TestWithdrawFlows(t *testing.T) {
	s := mkSystem(t)
	var now int64
	params := stdParams(22)
	id := mustCreate(t, s, &now, params)
	mustInvite(t, s, &now, id, "P1", 40)
	mustAccept(t, s, &now, id, "P1", 40)
	mustInvite(t, s, &now, id, "P2", 40)
	mustAccept(t, s, &now, id, "P2", 40)
	// P1 免责退出。
	if err := s.Withdraw(tick(&now), id, "P1"); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	// P2 在免责截止后、窗口结束前退出。
	if err := s.Withdraw(fwd(&now, params.ExitDeadline+1), id, "P2"); err != nil {
		t.Fatalf("迟到退出应成功：%v", err)
	}
	fillDay(t, s, &now, "P2", 21, 100)
	fillDay(t, s, &now, "P2", 18, 100)
	fillWindowAdjust(t, s, &now, "P2", params, 100)
	res, err := s.Assess(fwd(&now, params.WindowStart+60), id)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if len(res.Results) != 1 || res.Results[0].Participant != "P2" {
		t.Fatalf("免责退出者不应出现在结果中：%+v", res.Results)
	}
	r := res.Results[0]
	if !r.LateWithdrawn || r.Reduction != 0 || r.Payment != 0 || r.Penalty != 120 {
		t.Fatalf("迟到退出应削减按零并计违约金 120：%+v", r)
	}
	// P1 无考核水印，考核后仍可补登旧数据。
	if err := s.RegisterData(tick(&now), "P1", 20*1440, 5); err != nil {
		t.Fatalf("免责退出者不受已考核水印限制：%v", err)
	}
}

// 并发调用安全（配合 -race）。
func TestConcurrentSmoke(t *testing.T) {
	s := mkSystem(t)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int64) {
			defer wg.Done()
			p := string(rune('A' + g))
			var now int64
			params := stdParams(22 + g%5)
			id, err := s.CreateEvent(g*1000+1, params)
			if err != nil {
				return
			}
			_ = s.Invite(g*1000+2, id, p, 40)
			_ = s.Respond(g*1000+3, id, p, true, 40)
			for i := int64(0); i < 50; i++ {
				_ = s.RegisterData(g*1000+10+i, p, (22+g%5)*1440+i*15, float64(i))
				now = g*1000 + 10 + i
			}
			_, _ = s.Assess(now+100000, id)
		}(int64(g))
	}
	wg.Wait()
}

// 性能可验证性：对一名参与者完成考核的数据读取次数，
// 不随系统内其他参与者与其他事件数量增长。
func TestAssessCostIndependentOfSystemSize(t *testing.T) {
	build := func(others int) int64 {
		s := mkSystem(t)
		var now int64
		params := stdParams(22)
		id := setupAccepted(t, s, &now, params, "P1", 40)
		fillDay(t, s, &now, "P1", 21, 100)
		fillDay(t, s, &now, "P1", 18, 100)
		fillWindowAdjust(t, s, &now, "P1", params, 100)
		// 其他参与者：各自有事件、承诺与数据。
		for i := 0; i < others; i++ {
			p := string(rune('A'+i%26)) + string(rune('a'+i/26))
			op := stdParams(21)
			oid := setupAccepted(t, s, &now, op, p, 40)
			_ = oid
			fillWindowAdjust(t, s, &now, p, op, 100)
		}
		s.Stats.DataReads = 0
		if _, err := s.Assess(fwd(&now, params.WindowStart+60), id); err != nil {
			t.Fatalf("Assess: %v", err)
		}
		return s.Stats.DataReads
	}
	base := build(0)
	if got := build(200); got != base {
		t.Fatalf("考核开销随其他参与者/事件数量增长：base=%d got=%d", base, got)
	}
}

// 性能可验证性：资格日判定的数据读取次数不随参与者全部历史数据总量增长。
func TestBaselineScanIndependentOfHistorySize(t *testing.T) {
	build := func(historyDays int64) int64 {
		s := mkSystem(t)
		var now int64
		params := stdParams(400)
		id := setupAccepted(t, s, &now, params, "P1", 40)
		for d := int64(0); d < historyDays; d++ {
			fillDay(t, s, &now, "P1", 399-d, 100)
		}
		fillWindowAdjust(t, s, &now, "P1", params, 100)
		s.Stats.DataReads = 0
		if _, err := s.Assess(fwd(&now, params.WindowStart+60), id); err != nil {
			t.Fatalf("Assess: %v", err)
		}
		return s.Stats.DataReads
	}
	small := build(30)
	large := build(300)
	if small != large {
		t.Fatalf("资格日判定开销随历史总量增长：30 天=%d 300 天=%d", small, large)
	}
}
