package system

import (
	"fmt"
	"testing"

	"ontology/calendar"
	"ontology/domain"
)

func d(y, m, day int) int { return calendar.FromCivil(y, m, day) }

func ymd(z int) string {
	y, m, dd := calendar.ToCivil(z)
	return fmt.Sprintf("%04d-%02d-%02d", y, m, dd)
}

func testConfigs() map[domain.Category]domain.Config {
	return map[domain.Category]domain.Config{
		domain.CatBoiler:         {PeriodMonths: 12, EarlyWindowDays: 30, MinUnsealDays: 10, WarnAheadDays: 60},
		domain.CatPressureVessel: {PeriodMonths: 24, EarlyWindowDays: 30, MinUnsealDays: 10, WarnAheadDays: 60},
		domain.CatSafetyValve:    {PeriodMonths: 6, EarlyWindowDays: 15, MinUnsealDays: 5, WarnAheadDays: 30},
		domain.CatPressureGauge:  {PeriodMonths: 6, EarlyWindowDays: 15, MinUnsealDays: 5, WarnAheadDays: 30},
	}
}

func newSys(t *testing.T) *System {
	t.Helper()
	s, err := New(testConfigs())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustKind(t *testing.T, err error, kind domain.ErrKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error kind %s, got nil", kind)
	}
	de, ok := err.(*domain.Error)
	if !ok {
		t.Fatalf("want *domain.Error, got %T: %v", err, err)
	}
	if de.Kind != kind {
		t.Fatalf("want kind %s, got %s (%s)", kind, de.Kind, de.Msg)
	}
}

func expiryOf(t *testing.T, s *System, id string) int {
	t.Helper()
	obj, ok := s.objects[id]
	if !ok {
		t.Fatalf("object %s not found", id)
	}
	return obj.Expiry
}

// 月末日期推算：到期日为首次检验合格日期加一个检验周期。
func TestRegisterMonthEnd(t *testing.T) {
	s := newSys(t)
	must(t, s.Register("B1", domain.CatBoiler, d(2024, 1, 31)))
	if got, want := expiryOf(t, s, "B1"), d(2025, 1, 31); got != want {
		t.Fatalf("expiry=%v want %v", ymd(got), ymd(want))
	}
	must(t, s.Register("V1", domain.CatSafetyValve, d(2024, 8, 31)))
	if got, want := expiryOf(t, s, "V1"), d(2025, 2, 28); got != want {
		t.Fatalf("expiry=%v want %v", ymd(got), ymd(want))
	}
}

// 提前检验窗口恰等边界：恰在窗口起点按原到期日为基准，早一天则以检验日为基准。
func TestEarlyWindowBoundary(t *testing.T) {
	s := newSys(t)
	for _, id := range []string{"B1", "B2", "B3", "B4"} {
		must(t, s.Register(id, domain.CatBoiler, d(2024, 1, 1))) // 到期 2025-01-01
	}
	exp := d(2025, 1, 1)
	winStart := exp - 30 // 窗口起点（含）

	must(t, s.Inspect("B2", winStart-1, domain.ResultPass, 0))
	if got, want := expiryOf(t, s, "B2"), calendar.AddMonths(winStart-1, 12); got != want {
		t.Fatalf("B2(早一天) expiry=%v want %v", ymd(got), ymd(want))
	}
	must(t, s.Inspect("B1", winStart, domain.ResultPass, 0))
	if got, want := expiryOf(t, s, "B1"), calendar.AddMonths(exp, 12); got != want {
		t.Fatalf("B1(恰在窗口起点) expiry=%v want %v", ymd(got), ymd(want))
	}
	must(t, s.Inspect("B3", exp, domain.ResultPass, 0))
	if got, want := expiryOf(t, s, "B3"), calendar.AddMonths(exp, 12); got != want {
		t.Fatalf("B3(到期日当天) expiry=%v want %v", ymd(got), ymd(want))
	}
	must(t, s.Inspect("B4", exp+1, domain.ResultPass, 0))
	if got, want := expiryOf(t, s, "B4"), calendar.AddMonths(exp+1, 12); got != want {
		t.Fatalf("B4(到期次日) expiry=%v want %v", ymd(got), ymd(want))
	}
}

// 到期日当天仍可使用，次日起超期不可使用；被拒绝的登记不推进时钟。
func TestExpiryDayAndNext(t *testing.T) {
	s := newSys(t)
	must(t, s.Register("D1", domain.CatBoiler, d(2024, 1, 1)))
	must(t, s.Register("V1", domain.CatSafetyValve, d(2024, 7, 1))) // 到期 2025-01-01
	must(t, s.Attach("D1", "V1", d(2024, 7, 2)))

	exp := d(2025, 1, 1) // 锅炉到期日
	ok, denial, err := s.RegisterUse("D1", exp)
	if err != nil || !ok {
		t.Fatalf("到期日当天应可使用: ok=%v denial=%+v err=%v", ok, denial, err)
	}
	ok, denial, err = s.RegisterUse("D1", exp+1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok || denial == nil || denial.Reason != "overdue" {
		t.Fatalf("到期次日应因超期拒绝: ok=%v denial=%+v", ok, denial)
	}
	// 被拒绝的登记不推进时钟：时钟仍停留在 exp，故以 exp 为日期的操作仍被接受。
	must(t, s.Seal("D1", exp))
}

// 有条件合格：新到期日取合格规则日期与 检验日+整改限期 中较早者；
// 整改完成后再次检验合格，按合格规则重算。
func TestConditionalPass(t *testing.T) {
	s := newSys(t)
	must(t, s.Register("B1", domain.CatBoiler, d(2024, 1, 1))) // 到期 2025-01-01
	must(t, s.Register("B2", domain.CatBoiler, d(2024, 1, 1)))

	// B1：检验日+整改限期更早 → 取整改限期
	insp1 := d(2024, 12, 15) // 窗口内
	must(t, s.Inspect("B1", insp1, domain.ResultConditional, 10))
	if got, want := expiryOf(t, s, "B1"), insp1+10; got != want {
		t.Fatalf("B1 有条件合格应取整改限期: expiry=%v want %v", ymd(got), ymd(want))
	}
	// B2：合格规则日期更早 → 取合格规则日期
	must(t, s.Inspect("B2", insp1, domain.ResultConditional, 1000))
	if got, want := expiryOf(t, s, "B2"), calendar.AddMonths(d(2025, 1, 1), 12); got != want {
		t.Fatalf("B2 有条件合格应取合格规则日期: expiry=%v want %v", ymd(got), ymd(want))
	}
	// B1 整改完成后再次检验合格：以当前到期日 2024-12-25 为原到期日按合格规则重算
	insp2 := d(2024, 12, 20) // 落在 [2024-12-25-30, 2024-12-25] 窗口内
	must(t, s.Inspect("B1", insp2, domain.ResultPass, 0))
	if got, want := expiryOf(t, s, "B1"), calendar.AddMonths(insp1+10, 12); got != want {
		t.Fatalf("B1 复检合格重算: expiry=%v want %v", ymd(got), ymd(want))
	}
}

// 不合格立即停用，到期日不变；复检合格恢复使用。
func TestFailAndReinspect(t *testing.T) {
	s := newSys(t)
	must(t, s.Register("B1", domain.CatBoiler, d(2024, 1, 1)))
	must(t, s.Register("V1", domain.CatSafetyValve, d(2024, 1, 1)))
	must(t, s.Attach("B1", "V1", d(2024, 1, 2)))
	exp := expiryOf(t, s, "B1")

	must(t, s.Inspect("B1", d(2024, 6, 1), domain.ResultFail, 0))
	if got := expiryOf(t, s, "B1"); got != exp {
		t.Fatalf("不合格后到期日不变: got %v want %v", ymd(got), ymd(exp))
	}
	ok, denial, err := s.RegisterUse("B1", d(2024, 6, 2))
	if err != nil || ok || denial.Reason != "suspended" {
		t.Fatalf("停用后应拒绝使用: ok=%v denial=%+v err=%v", ok, denial, err)
	}
	// 复检合格恢复使用；此时已过早（窗口未开），以检验日为基准
	must(t, s.Inspect("B1", d(2024, 6, 3), domain.ResultPass, 0))
	if got, want := expiryOf(t, s, "B1"), calendar.AddMonths(d(2024, 6, 3), 12); got != want {
		t.Fatalf("复检合格重算: got %v want %v", ymd(got), ymd(want))
	}
	ok, _, err = s.RegisterUse("B1", d(2024, 6, 4))
	if err != nil || !ok {
		t.Fatalf("复检合格后应可使用: ok=%v err=%v", ok, err)
	}
}

// 封存顺延：启封时到期日顺延封存天数；保障天数不足拒绝启封。
func TestSealUnsealShift(t *testing.T) {
	s := newSys(t)
	must(t, s.Register("V1", domain.CatSafetyValve, d(2024, 1, 1))) // 到期 2024-07-01
	must(t, s.Register("V2", domain.CatSafetyValve, d(2024, 1, 1)))

	// V1：封存 29 天，到期日顺延 29 天
	must(t, s.Seal("V1", d(2024, 2, 1)))
	must(t, s.Unseal("V1", d(2024, 3, 1))) // 2 月 1 日 -> 3 月 1 日 = 29 天（闰年）
	if got, want := expiryOf(t, s, "V1"), d(2024, 7, 1)+29; got != want {
		t.Fatalf("封存顺延: got %v want %v", ymd(got), ymd(want))
	}

	// V2：封存时距到期仅 4 天，启封保障天数（5）不足 → 拒绝，状态保持封存
	must(t, s.Seal("V2", d(2024, 6, 28)))
	err := s.Unseal("V2", d(2024, 6, 29))
	mustKind(t, err, domain.ErrConditionUnmet)
	if s.objects["V2"].Status != domain.StatusSealed {
		t.Fatalf("拒绝启封后应保持封存, got %s", s.objects["V2"].Status)
	}
}

// 封存期检验后的启封：只顺延检验之后经过的封存天数。
func TestSealInspectUnseal(t *testing.T) {
	s := newSys(t)
	must(t, s.Register("V1", domain.CatSafetyValve, d(2024, 1, 1))) // 到期 2024-07-01
	must(t, s.Seal("V1", d(2024, 2, 1)))

	// 封存期内检验（过早，窗口未开）：以检验日为基准重算，锚点移至检验日
	must(t, s.Inspect("V1", d(2024, 3, 1), domain.ResultPass, 0))
	if got, want := expiryOf(t, s, "V1"), d(2024, 9, 1); got != want {
		t.Fatalf("封存期检验重算: got %v want %v", ymd(got), ymd(want))
	}
	if s.objects["V1"].Status != domain.StatusSealed {
		t.Fatalf("封存期检验后应保持封存")
	}
	// 启封：只顺延 检验日(3-1) -> 启封日(4-1) 的 31 天，而非封存日起的 60 天
	must(t, s.Unseal("V1", d(2024, 4, 1)))
	if got, want := expiryOf(t, s, "V1"), d(2024, 9, 1)+31; got != want {
		t.Fatalf("启封只顺延检验后的封存天数: got %v want %v", ymd(got), ymd(want))
	}
}

// 封存前提：超期或停用对象不得封存；封存对象不得被挂接。
func TestSealPreconditions(t *testing.T) {
	s := newSys(t)
	must(t, s.Register("B1", domain.CatBoiler, d(2024, 1, 1)))
	must(t, s.Register("V1", domain.CatSafetyValve, d(2024, 1, 1)))
	must(t, s.Register("V2", domain.CatSafetyValve, d(2024, 1, 1)))

	// 停用不得封存
	must(t, s.Inspect("V1", d(2024, 2, 1), domain.ResultFail, 0))
	mustKind(t, s.Seal("V1", d(2024, 2, 2)), domain.ErrStateNotAllowed)
	// 超期不得封存（V2 到期 2024-07-01）
	mustKind(t, s.Seal("V2", d(2024, 7, 2)), domain.ErrConditionUnmet)
	// 封存中的附件不得被挂接
	must(t, s.Seal("B1", d(2024, 7, 3)))
	must(t, s.Register("V3", domain.CatSafetyValve, d(2024, 7, 4)))
	must(t, s.Seal("V3", d(2024, 7, 5)))
	mustKind(t, s.Attach("B1", "V3", d(2024, 7, 6)), domain.ErrConditionUnmet)
}

// 附件转移后设备可使用性变化。
func TestAccessoryTransfer(t *testing.T) {
	s := newSys(t)
	must(t, s.Register("D1", domain.CatBoiler, d(2024, 1, 1)))
	must(t, s.Register("D2", domain.CatBoiler, d(2024, 1, 1)))
	must(t, s.Register("V1", domain.CatSafetyValve, d(2024, 1, 1)))
	must(t, s.Attach("D1", "V1", d(2024, 1, 2)))

	ok, _, err := s.Usable("D1", d(2024, 1, 3))
	if err != nil || !ok {
		t.Fatalf("D1 应可使用")
	}
	// 转移到 D2：D1 失去唯一安全阀 → 不可用；D2 可用
	must(t, s.Attach("D2", "V1", d(2024, 1, 4)))
	ok, denial, _ := s.Usable("D1", d(2024, 1, 5))
	if ok || denial.Reason != "no_safety_valve" {
		t.Fatalf("D1 转移后应缺少安全阀: ok=%v denial=%+v", ok, denial)
	}
	ok, _, _ = s.Usable("D2", d(2024, 1, 5))
	if !ok {
		t.Fatalf("D2 应可使用")
	}
	// 重复挂接同一设备 → 状态不允许
	mustKind(t, s.Attach("D2", "V1", d(2024, 1, 6)), domain.ErrStateNotAllowed)
}

// 附件超期/停用使设备不可用，无需修改设备状态。
func TestAccessoryOverdueBlocksDevice(t *testing.T) {
	s := newSys(t)
	must(t, s.Register("D1", domain.CatBoiler, d(2024, 1, 1)))      // 到期 2025-01-01
	must(t, s.Register("V1", domain.CatSafetyValve, d(2024, 1, 1))) // 到期 2024-07-01
	must(t, s.Attach("D1", "V1", d(2024, 1, 2)))

	ok, denial, _ := s.Usable("D1", d(2024, 7, 2))
	if ok || denial.Reason != "accessory" || denial.AccID != "V1" {
		t.Fatalf("附件超期应使设备不可用: ok=%v denial=%+v", ok, denial)
	}
	if s.objects["D1"].Status != domain.StatusInService {
		t.Fatalf("设备状态不应被修改")
	}
	// 附件检验合格后设备恢复可用
	must(t, s.Inspect("V1", d(2024, 7, 3), domain.ResultPass, 0))
	ok, _, _ = s.Usable("D1", d(2024, 7, 4))
	if !ok {
		t.Fatalf("附件复检合格后设备应可用")
	}
}

// 使用登记拒绝原因次序：封存 > 停用 > 超期 > 缺少安全阀 > 附件不满足（编号最小）。
func TestUseDenialOrder(t *testing.T) {
	s := newSys(t)
	must(t, s.Register("D1", domain.CatBoiler, d(2024, 1, 1)))
	must(t, s.Register("G1", domain.CatPressureGauge, d(2024, 1, 1)))
	must(t, s.Register("V1", domain.CatSafetyValve, d(2024, 1, 1)))
	must(t, s.Attach("D1", "G1", d(2024, 1, 2)))
	must(t, s.Attach("D1", "V1", d(2024, 1, 3)))

	// 封存优先于一切
	must(t, s.Seal("D1", d(2024, 1, 4)))
	_, denial, _ := s.RegisterUse("D1", d(2024, 1, 5))
	if denial.Reason != "sealed" {
		t.Fatalf("want sealed, got %+v", denial)
	}
	must(t, s.Unseal("D1", d(2024, 1, 6)))
	// 附件不满足时取编号最小者：G1 < V1，令两者都超期
	_, denial, _ = s.RegisterUse("D1", d(2024, 7, 2))
	if denial.Reason != "accessory" || denial.AccID != "G1" {
		t.Fatalf("附件不满足应给编号最小者: %+v", denial)
	}
	// 缺少安全阀：摘除 V1，G1 复检合格
	must(t, s.Detach("V1", d(2024, 7, 3)))
	must(t, s.Inspect("G1", d(2024, 7, 4), domain.ResultPass, 0))
	_, denial, _ = s.RegisterUse("D1", d(2024, 7, 5))
	if denial.Reason != "no_safety_valve" {
		t.Fatalf("want no_safety_valve, got %+v", denial)
	}
}

// 报废自动脱离：设备报废附件脱离；附件报废从设备摘除；报废后拒绝一切操作。
func TestScrapDetaches(t *testing.T) {
	s := newSys(t)
	must(t, s.Register("D1", domain.CatBoiler, d(2024, 1, 1)))
	must(t, s.Register("D2", domain.CatBoiler, d(2024, 1, 1)))
	must(t, s.Register("V1", domain.CatSafetyValve, d(2024, 1, 1)))
	must(t, s.Attach("D1", "V1", d(2024, 1, 2)))

	// 设备报废 → 附件自动脱离，可挂到别的设备
	must(t, s.Scrap("D1", d(2024, 2, 1)))
	if s.objects["V1"].HostID != "" {
		t.Fatalf("设备报废后附件应脱离")
	}
	must(t, s.Attach("D2", "V1", d(2024, 2, 2)))
	// 附件报废 → 自动从设备摘除
	must(t, s.Scrap("V1", d(2024, 2, 3)))
	if len(s.objects["D2"].Attach) != 0 {
		t.Fatalf("附件报废后应从设备摘除")
	}
	// 报废后不再接受任何操作
	mustKind(t, s.Inspect("V1", d(2024, 2, 4), domain.ResultPass, 0), domain.ErrScrapped)
	mustKind(t, s.Seal("D1", d(2024, 2, 4)), domain.ErrScrapped)
	mustKind(t, s.Scrap("D1", d(2024, 2, 4)), domain.ErrScrapped)
}

// 错误优先级：参数非法 > 日期回退 > 对象不存在 > 已报废 > 状态不允许 > 条件不满足。
func TestErrorPrecedence(t *testing.T) {
	s := newSys(t)
	must(t, s.Register("B1", domain.CatBoiler, d(2024, 1, 1)))
	must(t, s.Register("B2", domain.CatBoiler, d(2024, 1, 1)))
	must(t, s.Scrap("B2", d(2024, 2, 1))) // 时钟推进到 2024-02-01

	// 参数非法优先于日期回退
	mustKind(t, s.Inspect("", 0, domain.ResultPass, 0), domain.ErrInvalidParam)
	// 日期回退优先于对象不存在
	mustKind(t, s.Inspect("NOPE", d(2024, 1, 15), domain.ResultPass, 0), domain.ErrDateRegression)
	// 对象不存在优先于已报废（不同对象无法同现，分别验证）
	mustKind(t, s.Inspect("NOPE", d(2024, 2, 2), domain.ResultPass, 0), domain.ErrNotFound)
	mustKind(t, s.Inspect("B2", d(2024, 2, 2), domain.ResultPass, 0), domain.ErrScrapped)
	// 状态不允许：对未封存对象启封
	mustKind(t, s.Unseal("B1", d(2024, 2, 2)), domain.ErrStateNotAllowed)
	// 条件不满足：超期封存
	mustKind(t, s.Seal("B1", d(2025, 1, 2)), domain.ErrConditionUnmet)
	// 被拒绝的操作不推进时钟：2024-02-01 仍可用
	must(t, s.Inspect("B1", d(2024, 2, 1), domain.ResultPass, 0))
}

// 预警：窗口内含边界，排除封存/停用，按到期日升序、并列按编号升序；
// 设备因附件临近到期被触发时列出附件。
func TestWarn(t *testing.T) {
	s := newSys(t)
	must(t, s.Register("D1", domain.CatBoiler, d(2024, 1, 1)))      // 到期 2025-01-01
	must(t, s.Register("V1", domain.CatSafetyValve, d(2024, 1, 1))) // 到期 2024-07-01
	must(t, s.Register("V2", domain.CatSafetyValve, d(2024, 1, 1))) // 到期 2024-07-01
	must(t, s.Attach("D1", "V1", d(2024, 1, 2)))
	must(t, s.Register("D2", domain.CatBoiler, d(2024, 6, 1))) // 到期 2025-06-01
	must(t, s.Attach("D2", "V2", d(2024, 6, 1)))

	// 2024-06-01 起：V1/V2 在 30 天窗口内（含边界 2024-07-01 = 06-01+30）
	entries, err := s.Warn(d(2024, 6, 1))
	must(t, err)
	var ids []string
	byID := map[string]WarnEntry{}
	for _, e := range entries {
		ids = append(ids, e.ID)
		byID[e.ID] = e
	}
	// 四者排序键同为 2024-07-01（D1/D2 由附件触发，取附件到期日），并列按编号升序
	want := []string{"D1", "D2", "V1", "V2"}
	if len(ids) != len(want) {
		t.Fatalf("warn ids=%v want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("warn ids=%v want %v", ids, want)
		}
	}
	if len(byID["D1"].Via) != 1 || byID["D1"].Via[0] != "V1" {
		t.Fatalf("D1 应由 V1 触发: %+v", byID["D1"])
	}
	// 封存 V1 后：V1 不再出现，D1 也不再被触发
	must(t, s.Seal("V1", d(2024, 6, 2)))
	entries, _ = s.Warn(d(2024, 6, 2))
	for _, e := range entries {
		if e.ID == "V1" || e.ID == "D1" {
			t.Fatalf("封存对象不应出现在预警: %+v", e)
		}
	}
}
