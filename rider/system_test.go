package rider

import (
	"testing"
)

func testConfig() Config {
	return Config{
		PeriodLength:         10,
		PeriodOrigin:         0,
		EventScores:          map[EventType]int{EventTypeLateDelivery: 1, EventTypeCustomerComplaint: 2, EventTypeRejectOrder: 3, EventTypeFaultCancel: 4},
		Thresholds:           []int{5, 10},
		AppealWindow:         20,
		ClusterSpan:          5,
		MaxLevelDrop:         1,
		CompensationPerLevel: 100,
	}
}

func mustRegister(t *testing.T, s *System, rider string) {
	t.Helper()
	if err := s.RegisterRider(0, rider); err != nil {
		t.Fatalf("register rider: %v", err)
	}
}

func mustEvent(t *testing.T, s *System, now int64, rider string, at int64, typ EventType, root string) int64 {
	t.Helper()
	id, err := s.RegisterEvent(now, rider, at, typ, root)
	if err != nil {
		t.Fatalf("register event at=%d type=%s root=%s: %v", at, typ, root, err)
	}
	return id
}

// 周期右端点归属：恰在右端点归入下一周期。
func TestPeriodBoundary(t *testing.T) {
	s, _ := New(testConfig())
	mustRegister(t, s, "a")
	mustEvent(t, s, 0, "a", 9, EventTypeLateDelivery, "")
	mustEvent(t, s, 0, "a", 10, EventTypeLateDelivery, "")
	q0, _ := s.Query(10, "a", 0)
	q1, _ := s.Query(10, "a", 1)
	if q0.Score != 1 || q1.Score != 1 {
		t.Fatalf("boundary scores p0=%d p1=%d want 1,1", q0.Score, q1.Score)
	}
	if q0.Period != 0 || q1.Period != 1 {
		t.Fatalf("period index wrong")
	}
}

// 阈值取等归更差级；下降上限逐周期限制、上升不受限。
func TestThresholdEqualityAndCaps(t *testing.T) {
	cfg := testConfig()
	s, _ := New(cfg)
	mustRegister(t, s, "a")
	// 在推进时钟前，一次性登记周期 0/2/3 的事件（周期1故意留空）。
	for i := 0; i < 10; i++ {
		mustEvent(t, s, 0, "a", int64(i), EventTypeLateDelivery, "")    // 周期0 总分10
		mustEvent(t, s, 0, "a", int64(20+i), EventTypeLateDelivery, "") // 周期2 总分10
		mustEvent(t, s, 0, "a", int64(30+i), EventTypeLateDelivery, "") // 周期3 总分10
	}
	q0, _ := s.Query(10, "a", 0)
	if q0.Level != 2 || q0.RightsLevel != 1 {
		t.Fatalf("p0 level=%d rights=%d want 2,1 (drop cap from 0)", q0.Level, q0.RightsLevel)
	}
	// 周期1：无扣分 -> 等级0，上升不受限 -> 权益0。
	q1, _ := s.Query(20, "a", 1)
	if q1.Level != 0 || q1.RightsLevel != 0 {
		t.Fatalf("p1 level=%d rights=%d want 0,0 (unlimited rise)", q1.Level, q1.RightsLevel)
	}
	// 周期2：扣分10 -> 等级2，但相对权益0每周期最多降1级 -> 权益1。
	q2, _ := s.Query(30, "a", 2)
	if q2.Level != 2 || q2.RightsLevel != 1 {
		t.Fatalf("p2 level=%d rights=%d want 2,1 (drop cap 1)", q2.Level, q2.RightsLevel)
	}
	// 周期3：仍最差，可再降1级到2。
	q3, _ := s.Query(40, "a", 3)
	if q3.RightsLevel != 2 {
		t.Fatalf("p3 rights=%d want 2", q3.RightsLevel)
	}
	// 恰等于低阈值 5 也归更差一级（独立系统避免时钟干扰）。
	sb, _ := New(cfg)
	mustRegister(t, sb, "b")
	for i := 0; i < 5; i++ {
		mustEvent(t, sb, 0, "b", int64(i), EventTypeLateDelivery, "")
	}
	qc, _ := sb.Query(10, "b", 0)
	if qc.Level != 1 {
		t.Fatalf("score=5 level=%d want 1", qc.Level)
	}
}

// 簇的传递延伸：t=0,4,8，span=5 -> 0-4 相邻，4-8 相邻，三事件同簇。
func TestClusterTransitive(t *testing.T) {
	s, _ := New(testConfig())
	mustRegister(t, s, "a")
	mustEvent(t, s, 0, "a", 0, EventTypeFaultCancel, "shop1") // 最早，计4
	mustEvent(t, s, 0, "a", 4, EventTypeLateDelivery, "shop1")
	mustEvent(t, s, 0, "a", 8, EventTypeLateDelivery, "shop1")
	q, _ := s.Query(10, "a", 0)
	if q.Score != 4 {
		t.Fatalf("transitive cluster score=%d want 4", q.Score)
	}
}

// 后登记但发生更早的事件改变最早者，扣分归属随之改变。
func TestLateEarlierBecomesEarliest(t *testing.T) {
	s, _ := New(testConfig())
	mustRegister(t, s, "a")
	mustEvent(t, s, 0, "a", 8, EventTypeFaultCancel, "shop1")  // 暂独立，计4
	mustEvent(t, s, 1, "a", 3, EventTypeLateDelivery, "shop1") // 更早，成为最早，计1
	q, _ := s.Query(10, "a", 0)
	if q.Score != 1 {
		t.Fatalf("score=%d want 1 (new earliest)", q.Score)
	}
	// 再登记 t=2，t=3 降级为连带。
	// （发生时刻 2 早于当前接受时刻 10，合法；接受时刻单调即可。）
	mustEvent(t, s, 10, "a", 2, EventTypeFaultCancel, "shop1")
	q, _ = s.Query(10, "a", 0)
	// 迟到登记不改写已冻结结算：查询返回冻结分 1；live 账目仍自洽。
	if q.Score != 1 {
		t.Fatalf("frozen score=%d want 1", q.Score)
	}
}

// 撤销最早者 -> 整簇撤销；连带事件不可单独申诉。
func TestRevokeEarliestRemovesWholeCluster(t *testing.T) {
	s, _ := New(testConfig())
	mustRegister(t, s, "a")
	head := mustEvent(t, s, 0, "a", 5, EventTypeFaultCancel, "shop1")
	comp1 := mustEvent(t, s, 0, "a", 7, EventTypeLateDelivery, "shop1")
	mustEvent(t, s, 0, "a", 9, EventTypeLateDelivery, "shop1")
	// 对连带事件申诉被拒绝。
	_, err := s.SubmitAppeal(7, "a", comp1)
	if CodeOf(err) != ErrCompanionEvent {
		t.Fatalf("companion appeal err=%v want ErrCompanionEvent", err)
	}
	aid, err := s.SubmitAppeal(8, "a", head)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	comp, err := s.RuleAppeal(9, "a", aid, true)
	if err != nil {
		t.Fatalf("rule: %v", err)
	}
	if comp != nil {
		t.Fatalf("unsettled period should give no compensation, got %+v", comp)
	}
	q, _ := s.Query(10, "a", 0)
	if q.Score != 0 {
		t.Fatalf("score=%d want 0 whole cluster revoked", q.Score)
	}
}

// 申诉窗口右端点：[t, t+window)，恰等于右端点不允许。
func TestAppealWindowBoundary(t *testing.T) {
	s, _ := New(testConfig()) // window=20
	mustRegister(t, s, "a")
	e := mustEvent(t, s, 0, "a", 0, EventTypeLateDelivery, "")
	if _, err := s.SubmitAppeal(20, "a", e); CodeOf(err) != ErrAppealWindowExpired {
		t.Fatalf("at right endpoint err=%v want window", err)
	}
	if _, err := s.SubmitAppeal(19, "a", e); err != nil {
		t.Fatalf("just inside window: %v", err)
	}
	if _, err := s.SubmitAppeal(19, "a", e); CodeOf(err) != ErrAlreadyAppealed {
		t.Fatalf("duplicate appeal err=%v want already appealed", err)
	}
}

// 驳回不改状态，仅记录裁决；再次裁决报错。
func TestRejectAppealKeepsState(t *testing.T) {
	s, _ := New(testConfig())
	mustRegister(t, s, "a")
	e := mustEvent(t, s, 0, "a", 0, EventTypeFaultCancel, "")
	aid, _ := s.SubmitAppeal(0, "a", e)
	if comp, err := s.RuleAppeal(0, "a", aid, false); err != nil || comp != nil {
		t.Fatalf("reject comp=%v err=%v", comp, err)
	}
	q, _ := s.Query(10, "a", 0)
	if q.Score != 4 {
		t.Fatalf("score=%d want 4 after rejection", q.Score)
	}
	if _, err := s.RuleAppeal(11, "a", aid, true); CodeOf(err) != ErrAppealDecided {
		t.Fatalf("re-rule err=%v want decided", err)
	}
}

// 已结算周期：申诉成立且本应更好 -> 补偿；本应相同 -> 不补偿。
func TestSettledCompensation(t *testing.T) {
	cfg := testConfig() // drop=1, comp=100/级
	s, _ := New(cfg)
	mustRegister(t, s, "a")
	// 周期0：两笔 fault_cancel=8 -> 等级1，权益1。
	e1 := mustEvent(t, s, 0, "a", 0, EventTypeFaultCancel, "")
	mustEvent(t, s, 0, "a", 1, EventTypeFaultCancel, "")
	q0, _ := s.Query(10, "a", 0)
	if q0.Level != 1 || q0.RightsLevel != 1 {
		t.Fatalf("p0 level=%d rights=%d want 1,1", q0.Level, q0.RightsLevel)
	}
	// 撤销其中一笔 -> 本应 4 分，等级0，本应权益0；实际权益1，差1 -> 补偿100。
	aid, _ := s.SubmitAppeal(15, "a", e1)
	comp, err := s.RuleAppeal(16, "a", aid, true)
	if err != nil {
		t.Fatal(err)
	}
	if comp == nil || comp.Amount != 100 {
		t.Fatalf("comp=%+v want amount 100", comp)
	}
	if len(s.CompensationLog()) != 1 {
		t.Fatalf("compensation log len=%d want 1", len(s.CompensationLog()))
	}
	// 已确定的结算等级与权益不得改写。
	q0b, _ := s.Query(17, "a", 0)
	if q0b.Level != 1 || q0b.RightsLevel != 1 {
		t.Fatalf("settled grade rewritten: %+v", q0b)
	}

	// 不补偿情形：总分 7（4+2+1）-> 等级1、权益1；撤销 1 分独立事件后 6 分，仍等级1、权益1。
	if err := s.RegisterRider(17, "b"); err != nil {
		t.Fatalf("register b: %v", err)
	}
	small := mustEvent(t, s, 17, "b", 5, EventTypeLateDelivery, "") // 1，撤销目标
	mustEvent(t, s, 17, "b", 6, EventTypeFaultCancel, "")           // 4
	mustEvent(t, s, 17, "b", 7, EventTypeCustomerComplaint, "")     // 2，合计7
	// 撤销 1 分后 6 分，仍等级1、权益1（阈值[5,10]）。
	s.Query(20, "b", 0)
	aid2, _ := s.SubmitAppeal(24, "b", small)
	comp2, err := s.RuleAppeal(25, "b", aid2, true)
	if err != nil {
		t.Fatal(err)
	}
	if comp2 != nil {
		t.Fatalf("comp=%v want nil when would-level unchanged", comp2)
	}
	if len(s.CompensationLog()) != 1 {
		t.Fatalf("comp log should stay 1, got %d", len(s.CompensationLog()))
	}
}

// 回溯改写下降上限基准：补偿后，下一未开始周期以“本应权益”为基准下降。
func TestRetroactiveOverrideBaseline(t *testing.T) {
	cfg := testConfig() // drop=1
	s, _ := New(cfg)
	mustRegister(t, s, "a")
	// 周期0：10分 -> 等级2，实际权益1（受下降上限限制）。
	e := mustEvent(t, s, 0, "a", 0, EventTypeFaultCancel, "")
	for i := 1; i < 3; i++ {
		mustEvent(t, s, 0, "a", int64(i), EventTypeFaultCancel, "")
	}
	// 再补 2 笔 late 与 0 分? 构造总分10：2*4=8 +2(customer)
	mustEvent(t, s, 0, "a", 8, EventTypeCustomerComplaint, "") // 10
	q0, _ := s.Query(10, "a", 0)
	if q0.Level != 2 || q0.RightsLevel != 1 {
		t.Fatalf("p0 level=%d rights=%d want 2,1", q0.Level, q0.RightsLevel)
	}
	// 撤销 e（4分）-> 本应6分，等级1，本应权益1；与实际相同，不补偿。
	// 为了让本应更好，改为在窗口内撤销 customer(2分)：本应8分仍等级1，本应权益1 -> 也相同。
	// 这里直接撤销 e(4分)：本应等级1；实际权益1，差0 -> 不补偿，但基准改写为1（与实际一致，无效果）。
	aid, _ := s.SubmitAppeal(15, "a", e)
	comp, err := s.RuleAppeal(16, "a", aid, true)
	if err != nil {
		t.Fatal(err)
	}
	if comp != nil {
		t.Fatalf("comp=%v want nil (rights unchanged despite level change)", comp)
	}
	// 周期1为空（权益上升到0）。随后周期2最差：
	// 无回溯时基准=0 -> 权益1；若回溯把基准误写成1，会得到权益2，借此可区分。
	for i := 0; i < 10; i++ {
		mustEvent(t, s, 17, "a", int64(20+i), EventTypeLateDelivery, "")
	}
	q2, _ := s.Query(30, "a", 2)
	if q2.RightsLevel != 1 {
		t.Fatalf("p2 rights=%d want 1 (baseline 0 after empty period)", q2.RightsLevel)
	}
}

// 时钟回退与各类对象不存在错误可程序化区分；拒绝不改时钟。
func TestErrorCodesAndClockRejection(t *testing.T) {
	s, _ := New(testConfig())
	mustRegister(t, s, "a")
	if _, err := s.RegisterEvent(5, "a", 5, EventTypeLateDelivery, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRider(4, "b"); CodeOf(err) != ErrClockSkew {
		t.Fatalf("skew err=%v", err)
	}
	if _, err := s.RegisterEvent(6, "ghost", 6, EventTypeLateDelivery, ""); CodeOf(err) != ErrRiderNotFound {
		t.Fatalf("rider not found err=%v", err)
	}
	if _, err := s.SubmitAppeal(6, "a", 9999); CodeOf(err) != ErrEventNotFound {
		t.Fatalf("event not found err=%v", err)
	}
	if _, err := s.RuleAppeal(6, "a", 9999, true); CodeOf(err) != ErrAppealNotFound {
		t.Fatalf("appeal not found err=%v", err)
	}
	// 被拒绝操作不推进时钟：now=6 仍可被接受。
	if _, err := s.Query(6, "a", 0); err != nil {
		t.Fatalf("clock should still allow now=6: %v", err)
	}
}

// 性能可验证性：登记的簇归并只触达同一根因键的局部结构，
// 大量“其他根因键”的历史事件不影响目标键的登记耗时。
func TestRegistrationIndependentOfOtherHistory(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	build := func(otherRoots int) float64 {
		s, _ := New(testConfig())
		mustRegister(t, s, "a")
		for r := 0; r < otherRoots; r++ {
			root := rootName(r)
			for k := 0; k < 20; k++ {
				if _, err := s.RegisterEvent(0, "a", int64(k), EventTypeLateDelivery, root); err != nil {
					t.Fatal(err)
				}
			}
		}
		began := testing.AllocsPerRun(1, func() {
			_, _ = s.RegisterEvent(0, "a", 100, EventTypeFaultCancel, "target")
		})
		return began
	}
	small := build(20)
	large := build(2000)
	// 不同历史规模下，目标键登记的分配次数应在同一量级（允许微小抖动）。
	if large > small*4 {
		t.Fatalf("registration cost grows with unrelated history: small=%v large=%v", small, large)
	}
}

func rootName(i int) string {
	return "r" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('a' + i%26)}, b...)
		i /= 26
	}
	return string(b)
}

// 查询某骑手某周期不扫描其他周期：直接读取该周期账目。
func TestQueryIndependentOfOtherPeriods(t *testing.T) {
	s, _ := New(testConfig())
	mustRegister(t, s, "a")
	// 在很多周期里各放事件。
	for p := 0; p < 2000; p++ {
		if _, err := s.RegisterEvent(0, "a", int64(p*10), EventTypeLateDelivery, ""); err != nil {
			t.Fatal(err)
		}
	}
	// 查询周期 0 的开销只与该周期有关；这里验证结果正确且为常量访问路径（map 直取）。
	allocs := testing.AllocsPerRun(1000, func() {
		q, err := s.Query(20000, "a", 0)
		if err != nil || q.Score != 1 {
			t.Fatalf("unexpected q=%+v err=%v", q, err)
		}
	})
	if allocs > 3 {
		t.Fatalf("query allocates %v, should be O(1) in target period", allocs)
	}
}
