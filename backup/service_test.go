package backup

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func reg(t *testing.T, s *Service, now int64, id string, kind Kind, parent string) {
	t.Helper()
	if err := s.RegisterBackup(now, id, kind, parent, 1); err != nil {
		t.Fatalf("RegisterBackup(%d, %q, ...) 失败: %v", now, id, err)
	}
}

func errKind(t *testing.T, err error) ErrorKind {
	t.Helper()
	var be *Error
	if !errors.As(err, &be) {
		t.Fatalf("错误类型不是 *backup.Error: %v", err)
	}
	return be.Kind
}

func expectErr(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %v，实际成功", want)
	}
	if got := errKind(t, err); got != want {
		t.Fatalf("错误类别 = %v, want %v (err=%v)", got, want, err)
	}
}

func retainedMap(p Plan) map[string]Reasons {
	m := make(map[string]Reasons, len(p.Retained))
	for _, e := range p.Retained {
		m[e.ID] = e.Reasons
	}
	return m
}

func deletableSet(p Plan) map[string]bool {
	m := make(map[string]bool, len(p.Deletable))
	for _, id := range p.Deletable {
		m[id] = true
	}
	return m
}

func mustPlan(t *testing.T, s *Service, now int64) Plan {
	t.Helper()
	p, err := s.PlanCleanup(now)
	if err != nil {
		t.Fatalf("PlanCleanup(%d) 失败: %v", now, err)
	}
	return p
}

// 周期边界：恰好落在零点与相邻一秒。
func TestDayBoundaryZeroAndAdjacentSecond(t *testing.T) {
	s := NewService()
	if err := s.SetPolicy(0, Policy{Daily: 1}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, 0, "a", Full, "")
	reg(t, s, secondsPerDay-1, "b", Full, "")
	// 第 0 天最后一秒：代表是当天最晚的 b。
	p := mustPlan(t, s, secondsPerDay-1)
	if got := retainedMap(p); !reflect.DeepEqual(got, map[string]Reasons{"b": ReasonDaily}) {
		t.Fatalf("day0 保留 = %v", got)
	}
	reg(t, s, secondsPerDay, "c", Full, "")
	// 第 1 天零点整：窗口只含第 1 天，代表是 c；a、b 可删。
	p = mustPlan(t, s, secondsPerDay)
	if got := retainedMap(p); !reflect.DeepEqual(got, map[string]Reasons{"c": ReasonDaily}) {
		t.Fatalf("day1 保留 = %v", got)
	}
	if got := deletableSet(p); !got["a"] || !got["b"] || len(got) != 2 {
		t.Fatalf("day1 可删 = %v", got)
	}
}

// 周层：同一周跨月跨年只选一个代表（周一起始）。
func TestWeekLayerAcrossYear(t *testing.T) {
	s := NewService()
	if err := s.SetPolicy(0, Policy{Weekly: 1}); err != nil {
		t.Fatal(err)
	}
	thu := ts(2020, 12, 31, 0, 0, 0)
	fri := ts(2021, 1, 1, 0, 0, 0)
	sun := ts(2021, 1, 3, 23, 59, 59)
	mon := ts(2021, 1, 4, 0, 0, 0)
	reg(t, s, thu, "thu", Full, "")
	reg(t, s, fri, "fri", Full, "")
	// 周四、周五同属一周：代表是最晚的 fri。
	p := mustPlan(t, s, sun)
	if got := retainedMap(p); !reflect.DeepEqual(got, map[string]Reasons{"fri": ReasonWeekly}) {
		t.Fatalf("跨年前保留 = %v", got)
	}
	// 下周一零点：窗口滑动到新年第一周，旧周不再保留。
	p = mustPlan(t, s, mon)
	if len(p.Retained) != 0 || len(p.Deletable) != 2 {
		t.Fatalf("跨年后期望全部可删: %+v", p)
	}
}

// 同一周期内创建时刻并列：标识字典序较大者当选。
func TestSamePeriodTieBreakByID(t *testing.T) {
	s := NewService()
	if err := s.SetPolicy(0, Policy{Daily: 1}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, 100, "alpha", Full, "")
	reg(t, s, 100, "beta", Full, "")
	reg(t, s, 100, "gamma", Full, "")
	p := mustPlan(t, s, 100)
	if got := retainedMap(p); !reflect.DeepEqual(got, map[string]Reasons{"gamma": ReasonDaily}) {
		t.Fatalf("并列时刻保留 = %v, want gamma", got)
	}
}

// 代表损坏后顺延给次晚可恢复者；整周期无可恢复备份则无代表。
func TestCorruptedFallbackAndEmptyPeriod(t *testing.T) {
	s := NewService()
	if err := s.SetPolicy(0, Policy{Daily: 1}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, 10, "f1", Full, "")
	reg(t, s, 20, "f2", Full, "")
	reg(t, s, 30, "f3", Full, "")
	p := mustPlan(t, s, 30)
	if got := retainedMap(p); !reflect.DeepEqual(got, map[string]Reasons{"f3": ReasonDaily}) {
		t.Fatalf("初始保留 = %v, want f3", got)
	}
	// 最晚者损坏 -> 顺延给 f2。
	if err := s.MarkCorrupted(30, "f3"); err != nil {
		t.Fatal(err)
	}
	p = mustPlan(t, s, 30)
	if got := retainedMap(p); !reflect.DeepEqual(got, map[string]Reasons{"f2": ReasonDaily}) {
		t.Fatalf("f3 损坏后保留 = %v, want f2", got)
	}
	// 重复标记不是错误。
	if err := s.MarkCorrupted(30, "f3"); err != nil {
		t.Fatalf("重复标记损坏应成功: %v", err)
	}
	// f2 也损坏 -> 顺延给 f1。
	if err := s.MarkCorrupted(30, "f2"); err != nil {
		t.Fatal(err)
	}
	p = mustPlan(t, s, 30)
	if got := retainedMap(p); !reflect.DeepEqual(got, map[string]Reasons{"f1": ReasonDaily}) {
		t.Fatalf("f2 损坏后保留 = %v, want f1", got)
	}
	// 全部损坏 -> 整周期无代表，全部可删。
	if err := s.MarkCorrupted(30, "f1"); err != nil {
		t.Fatal(err)
	}
	p = mustPlan(t, s, 30)
	if len(p.Retained) != 0 || len(p.Deletable) != 3 {
		t.Fatalf("整周期无代表时应全部可删: %+v", p)
	}
}

// 祖先损坏使整条后代链不可恢复；其他可恢复备份递补。
func TestCorruptedAncestorBreaksChain(t *testing.T) {
	s := NewService()
	if err := s.SetPolicy(0, Policy{Daily: 1}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, 0, "F", Full, "")
	reg(t, s, 1, "A", Incremental, "F")
	reg(t, s, 1, "H", Full, "")
	reg(t, s, 2, "B", Incremental, "A")
	// 全部可恢复：代表是最晚的 B。
	p := mustPlan(t, s, 2)
	if got := retainedMap(p); !got["B"].Has(ReasonDaily) {
		t.Fatalf("初始代表 = %v, want B(daily)", got)
	}
	// 根损坏 -> A、B 均不可恢复 -> 代表顺延给 H。
	if err := s.MarkCorrupted(2, "F"); err != nil {
		t.Fatal(err)
	}
	p = mustPlan(t, s, 2)
	got := retainedMap(p)
	if !got["H"].Has(ReasonDaily) {
		t.Fatalf("根损坏后代表 = %v, want H(daily)", got)
	}
	if _, ok := got["B"]; ok {
		t.Fatalf("B 不可恢复，不应被保留: %v", got)
	}
	// H 也损坏 -> 整周期无可恢复备份 -> 全部可删。
	if err := s.MarkCorrupted(2, "H"); err != nil {
		t.Fatal(err)
	}
	p = mustPlan(t, s, 2)
	if len(p.Retained) != 0 || len(p.Deletable) != 4 {
		t.Fatalf("无可恢复备份时应全部可删: %+v", p)
	}
}

// 依赖保护：被保留备份的全部祖先也必须保留，原因与直接保留区分。
func TestDependencyProtection(t *testing.T) {
	s := NewService()
	if err := s.SetPolicy(0, Policy{Daily: 1}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, 0, "F", Full, "")
	reg(t, s, 1, "A", Incremental, "F")
	reg(t, s, 2, "B", Incremental, "A")
	p := mustPlan(t, s, 2)
	want := map[string]Reasons{
		"B": ReasonDaily,
		"A": ReasonDependency,
		"F": ReasonDependency,
	}
	if got := retainedMap(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("依赖保护保留 = %v, want %v", got, want)
	}
	// 中间祖先损坏 -> 链不可恢复 -> B 不可恢复；F 自身可恢复，
	// 成为当天最晚可恢复备份而当选代表；A、B 可删。
	if err := s.MarkCorrupted(2, "A"); err != nil {
		t.Fatal(err)
	}
	p = mustPlan(t, s, 2)
	if got := retainedMap(p); !reflect.DeepEqual(got, map[string]Reasons{"F": ReasonDaily}) {
		t.Fatalf("中间祖先损坏后保留 = %v, want F(daily)", got)
	}
}

// 依赖保护与法律保留叠加；法律保留不受可恢复性限制，可随时解除。
func TestLegalHoldStacking(t *testing.T) {
	s := NewService()
	if err := s.SetPolicy(0, Policy{Daily: 1}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, 0, "F", Full, "")
	reg(t, s, 1, "A", Incremental, "F")
	reg(t, s, 2, "B", Incremental, "A")
	// 对根设置法律保留：根获得 依赖保护+法律保留。
	if err := s.SetLegalHold(2, "F"); err != nil {
		t.Fatal(err)
	}
	p := mustPlan(t, s, 2)
	want := map[string]Reasons{
		"B": ReasonDaily,
		"A": ReasonDependency,
		"F": ReasonDependency | ReasonLegalHold,
	}
	if got := retainedMap(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("法律保留叠加 = %v, want %v", got, want)
	}
	// 解除后重新按策略判定：F 只剩依赖保护。
	if err := s.ReleaseLegalHold(2, "F"); err != nil {
		t.Fatal(err)
	}
	p = mustPlan(t, s, 2)
	if got := retainedMap(p); got["F"] != ReasonDependency {
		t.Fatalf("解除法律保留后 F 原因 = %v, want dependency", got["F"])
	}
	// 对损坏备份也可设置法律保留。
	if err := s.MarkCorrupted(2, "A"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLegalHold(2, "A"); err != nil {
		t.Fatal(err)
	}
	p = mustPlan(t, s, 2)
	got := retainedMap(p)
	// A 损坏 -> B 不可恢复 -> 无直接代表；A 被法律保留，其祖先 F 同样不得删除。
	if !got["A"].Has(ReasonLegalHold) || !got["F"].Has(ReasonLegalHold) {
		t.Fatalf("损坏备份法律保留 = %v", got)
	}
	if _, ok := got["B"]; ok {
		t.Fatalf("B 不可恢复且无保留原因，应可删: %v", got)
	}
	if !deletableSet(p)["B"] {
		t.Fatalf("B 应在可删集合: %+v", p)
	}
}

// 保留数量为零与上限。
func TestRetentionCountZeroAndMax(t *testing.T) {
	s := NewService()
	reg(t, s, 0, "d0", Full, "")
	reg(t, s, 5*secondsPerDay, "d5", Full, "")
	reg(t, s, 10*secondsPerDay, "d10", Full, "")
	// 三层均为零：不保留任何备份。
	p := mustPlan(t, s, 10*secondsPerDay)
	if len(p.Retained) != 0 || len(p.Deletable) != 3 {
		t.Fatalf("零保留时应全部可删: %+v", p)
	}
	// 上限 1000：窗口覆盖全部历史，每个周期各选一个代表 -> 全部保留。
	if err := s.SetPolicy(10*secondsPerDay, Policy{Daily: MaxRetentionCount, Weekly: MaxRetentionCount, Monthly: MaxRetentionCount}); err != nil {
		t.Fatal(err)
	}
	p = mustPlan(t, s, 10*secondsPerDay)
	if len(p.Retained) != 3 || len(p.Deletable) != 0 {
		t.Fatalf("上限保留时应全部保留: %+v", p)
	}
	// d0 在 1970-01-01（周四，第 0 周），d5/d10 同在第 1 周且三者同在 1970 年 1 月：
	// d0 = 日|周，d5 = 日，d10 = 日|周|月。
	want := map[string]Reasons{
		"d0":  ReasonDaily | ReasonWeekly,
		"d5":  ReasonDaily,
		"d10": ReasonDaily | ReasonWeekly | ReasonMonthly,
	}
	if got := retainedMap(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("上限保留原因 = %v, want %v", got, want)
	}
}

// 策略放宽与收紧：只影响之后的计划，本身不删除任何备份。
func TestPolicyRelaxAndTighten(t *testing.T) {
	s := NewService()
	reg(t, s, 0, "a", Full, "")
	reg(t, s, secondsPerDay, "b", Full, "")
	now := secondsPerDay
	// 收紧到 1：只保留当天代表 b。
	if err := s.SetPolicy(now, Policy{Daily: 1}); err != nil {
		t.Fatal(err)
	}
	p := mustPlan(t, s, now)
	if got := retainedMap(p); !reflect.DeepEqual(got, map[string]Reasons{"b": ReasonDaily}) {
		t.Fatalf("收紧后保留 = %v", got)
	}
	// 放宽到 2：a 重新被保留。
	if err := s.SetPolicy(now, Policy{Daily: 2}); err != nil {
		t.Fatal(err)
	}
	p = mustPlan(t, s, now)
	if got := retainedMap(p); len(got) != 2 {
		t.Fatalf("放宽后保留 = %v", got)
	}
	// 再次收紧：a 又可删，但策略变更本身不删除任何备份。
	if err := s.SetPolicy(now, Policy{Daily: 1}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.GetBackup("a"); !ok {
		t.Fatal("策略变更不应删除任何备份")
	}
	p = mustPlan(t, s, now)
	if got := deletableSet(p); !got["a"] {
		t.Fatalf("收紧后 a 应可删: %+v", p)
	}
}

// 清理幂等：连续执行两次，第二次不删除任何备份；
// 且删除后不会留下父备份已不存在的备份。
func TestCleanupIdempotent(t *testing.T) {
	s := NewService()
	if err := s.SetPolicy(0, Policy{Daily: 1}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, 0, "F", Full, "")
	reg(t, s, 1, "A", Incremental, "F")
	reg(t, s, 2, "B", Incremental, "A")
	reg(t, s, 3, "junk", Full, "")
	if err := s.MarkCorrupted(3, "junk"); err != nil {
		t.Fatal(err)
	}
	p1, err := s.ExecuteCleanup(3)
	if err != nil {
		t.Fatal(err)
	}
	// B 是当天代表；F、A 依赖保护；junk 损坏不可恢复被删。
	if got := deletableSet(p1); !got["junk"] || len(got) != 1 {
		t.Fatalf("第一次清理可删 = %v", got)
	}
	if _, ok := s.GetBackup("junk"); ok {
		t.Fatal("junk 应已被删除")
	}
	// 依赖链完整：B 的祖先 F、A 都还在。
	for _, id := range []string{"F", "A", "B"} {
		if _, ok := s.GetBackup(id); !ok {
			t.Fatalf("%s 应被保留", id)
		}
	}
	// 第二次执行：不删除任何备份。
	p2, err := s.ExecuteCleanup(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Deletable) != 0 {
		t.Fatalf("第二次清理应不删除任何备份: %v", p2.Deletable)
	}
	if !reflect.DeepEqual(p1.Retained, p2.Retained) {
		t.Fatalf("两次清理保留集合应一致: %v vs %v", p1.Retained, p2.Retained)
	}
}

// 同一标识删除后可重新登记为新备份。
func TestReRegisterAfterDelete(t *testing.T) {
	s := NewService()
	reg(t, s, 0, "x", Full, "")
	// 默认策略不保留 -> x 被清理。
	if _, err := s.ExecuteCleanup(0); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.GetBackup("x"); ok {
		t.Fatal("x 应已被删除")
	}
	// 删除后其标识不再被当作已登记：对其标记损坏报“备份不存在”。
	expectErr(t, s.MarkCorrupted(0, "x"), ErrBackupNotFound)
	// 允许以同一标识重新登记。
	reg(t, s, 1, "x", Full, "")
	info, ok := s.GetBackup("x")
	if !ok || info.CreatedAt != 1 {
		t.Fatalf("重新登记后 x = %+v, ok=%v", info, ok)
	}
	// 未删除时重复登记报错。
	expectErr(t, s.RegisterBackup(1, "x", Full, "", 1), ErrDuplicateID)
}

// 时钟回退与各类拒绝：错误类别可区分、只报最靠前的一类，且不留痕。
func TestRejectionsLeaveNoTrace(t *testing.T) {
	s := NewService()
	reg(t, s, 10, "a", Full, "")
	reg(t, s, 20, "b", Incremental, "a")
	baseline := mustPlan(t, s, 20)

	type op func() error
	cases := []struct {
		name string
		op   op
		want ErrorKind
	}{
		// 参数非法优先于时钟回退。
		{"空标识+回退", func() error { return s.RegisterBackup(0, "", Full, "", 1) }, ErrInvalidArgument},
		{"时刻为负", func() error { return s.RegisterBackup(-1, "z", Full, "", 1) }, ErrInvalidArgument},
		{"时刻超上限", func() error { return s.MarkCorrupted(MaxTime+1, "a") }, ErrInvalidArgument},
		{"大小为负", func() error { return s.RegisterBackup(20, "z", Full, "", -1) }, ErrInvalidArgument},
		{"大小超上限", func() error { return s.RegisterBackup(20, "z", Full, "", MaxSize+1) }, ErrInvalidArgument},
		{"全量带父", func() error { return s.RegisterBackup(20, "z", Full, "a", 1) }, ErrInvalidArgument},
		{"增量无父", func() error { return s.RegisterBackup(20, "z", Incremental, "", 1) }, ErrInvalidArgument},
		{"类型未知", func() error { return s.RegisterBackup(20, "z", Kind(9), "", 1) }, ErrInvalidArgument},
		// 时钟回退优先于标识重复、父备份不存在、备份不存在、层数量越界。
		{"回退+重复", func() error { return s.RegisterBackup(19, "a", Full, "", 1) }, ErrClockRollback},
		{"回退+父缺失", func() error { return s.RegisterBackup(19, "z", Incremental, "ghost", 1) }, ErrClockRollback},
		{"回退+备份缺失", func() error { return s.MarkCorrupted(19, "ghost") }, ErrClockRollback},
		{"回退+层越界", func() error { return s.SetPolicy(19, Policy{Daily: 1001}) }, ErrClockRollback},
		{"计划回退", func() error { _, err := s.PlanCleanup(19); return err }, ErrClockRollback},
		{"清理回退", func() error { _, err := s.ExecuteCleanup(19); return err }, ErrClockRollback},
		// 标识重复。
		{"重复登记", func() error { return s.RegisterBackup(20, "a", Full, "", 1) }, ErrDuplicateID},
		{"重复优先于父缺失", func() error { return s.RegisterBackup(20, "b", Incremental, "ghost", 1) }, ErrDuplicateID},
		// 父备份不存在。
		{"父缺失", func() error { return s.RegisterBackup(20, "z", Incremental, "ghost", 1) }, ErrParentNotFound},
		// 备份不存在。
		{"损坏标记缺失", func() error { return s.MarkCorrupted(20, "ghost") }, ErrBackupNotFound},
		{"法律保留缺失", func() error { return s.SetLegalHold(20, "ghost") }, ErrBackupNotFound},
		{"解除保留缺失", func() error { return s.ReleaseLegalHold(20, "ghost") }, ErrBackupNotFound},
		// 层数量越界。
		{"层数量为负", func() error { return s.SetPolicy(20, Policy{Daily: -1}) }, ErrRetentionLimit},
		{"层数量超上限", func() error { return s.SetPolicy(20, Policy{Monthly: MaxRetentionCount + 1}) }, ErrRetentionLimit},
	}
	for _, c := range cases {
		expectErr(t, c.op(), c.want)
	}
	// 被拒绝的操作不改变任何状态与时钟：
	// 计划与基线一致，且等于基线时刻的时刻仍被接受（时钟未推进）。
	if got := mustPlan(t, s, 20); !reflect.DeepEqual(got, baseline) {
		t.Fatalf("拒绝后状态被污染:\n基线 %+v\n现在 %+v", baseline, got)
	}
	if _, ok := s.GetBackup("z"); ok {
		t.Fatal("被拒绝的登记不应留痕")
	}
	if info, _ := s.GetBackup("a"); info.Corrupted || info.LegalHold {
		t.Fatal("被拒绝的标记不应留痕")
	}
}

// 月层：月长不等，窗口按公历月计。
func TestMonthLayerUnequalLengths(t *testing.T) {
	s := NewService()
	jan := ts(2021, 1, 15, 0, 0, 0)
	feb := ts(2021, 2, 15, 0, 0, 0)
	mar := ts(2021, 3, 15, 0, 0, 0)
	if err := s.SetPolicy(jan, Policy{Monthly: 2}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, jan, "jan", Full, "")
	reg(t, s, feb, "feb", Full, "")
	reg(t, s, mar, "mar", Full, "")
	// 窗口 = 最近 2 个月（2 月、3 月）：jan 可删。
	p := mustPlan(t, s, mar)
	want := map[string]Reasons{"feb": ReasonMonthly, "mar": ReasonMonthly}
	if got := retainedMap(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("月层保留 = %v, want %v", got, want)
	}
	// 放宽到 3 个月：jan 重新保留。
	if err := s.SetPolicy(mar, Policy{Monthly: 3}); err != nil {
		t.Fatal(err)
	}
	p = mustPlan(t, s, mar)
	if got := retainedMap(p); len(got) != 3 {
		t.Fatalf("放宽后月层保留 = %v", got)
	}
}

// 结果顺序按（创建时刻，标识）唯一确定，等时刻段内按标识排序。
func TestPlanOrdering(t *testing.T) {
	s := NewService()
	if err := s.SetPolicy(0, Policy{Daily: 1}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, 5, "y", Full, "")
	reg(t, s, 5, "a", Full, "")
	reg(t, s, 5, "k", Full, "")
	p := mustPlan(t, s, 5)
	// 等时刻：字典序最大的 y 当选；可删集合按标识排序。
	if !reflect.DeepEqual(p.Deletable, []string{"a", "k"}) {
		t.Fatalf("可删顺序 = %v, want [a k]", p.Deletable)
	}
	if !reflect.DeepEqual(p.Retained, []RetainedEntry{{ID: "y", Reasons: ReasonDaily}}) {
		t.Fatalf("保留顺序 = %+v", p.Retained)
	}
}

// 清理计划只读：连续两次计划结果一致，且不改变后续执行结果。
func TestPlanIsReadOnly(t *testing.T) {
	s := NewService()
	if err := s.SetPolicy(0, Policy{Daily: 1}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, 0, "F", Full, "")
	reg(t, s, 1, "A", Incremental, "F")
	reg(t, s, 2, "junk", Full, "")
	if err := s.MarkCorrupted(2, "junk"); err != nil {
		t.Fatal(err)
	}
	p1 := mustPlan(t, s, 2)
	p2 := mustPlan(t, s, 2)
	if !reflect.DeepEqual(p1, p2) {
		t.Fatalf("两次计划不一致: %v vs %v", p1, p2)
	}
	exec, err := s.ExecuteCleanup(2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p1, exec) {
		t.Fatalf("执行的计划应与只读计划一致: %v vs %v", p1, exec)
	}
}

// 相同操作序列重放得到完全相同的计划与保留原因。
func TestReplayDeterminism(t *testing.T) {
	run := func() []Plan {
		s := NewService()
		var plans []Plan
		step := func(err error) {
			if err != nil {
				t.Fatal(err)
			}
		}
		step(s.SetPolicy(0, Policy{Daily: 2, Weekly: 1, Monthly: 1}))
		step(s.RegisterBackup(0, "F", Full, "", 7))
		step(s.RegisterBackup(3600, "A", Incremental, "F", 3))
		step(s.RegisterBackup(7200, "B", Incremental, "A", 4))
		step(s.MarkCorrupted(7200, "A"))
		step(s.SetLegalHold(7200, "B"))
		p, err := s.PlanCleanup(7200)
		step(err)
		plans = append(plans, p)
		step(s.ReleaseLegalHold(8000, "B"))
		step(s.RegisterBackup(9000, "G", Full, "", 1))
		p, err = s.PlanCleanup(9000)
		step(err)
		plans = append(plans, p)
		return plans
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重放结果不一致:\n%v\n%v", first, second)
	}
}

// 并发调用：结果等价于某个串行顺序（配合 -race 运行）。
func TestConcurrentSmoke(t *testing.T) {
	s := NewService()
	if err := s.SetPolicy(0, Policy{Daily: 3}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var clock atomic.Int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				now := clock.Add(1)
				id := "g" + string(rune('a'+g)) + "-" + string(rune('0'+i%10)) + string(rune('0'+i/10))
				_ = s.RegisterBackup(now, id, Full, "", 1)
				_, _ = s.PlanCleanup(now)
				_ = s.MarkCorrupted(now, id)
			}
		}(g)
	}
	wg.Wait()
	// 串行收尾：执行清理后再次计划，可删集合必须为空（幂等性在并发后仍成立）。
	if _, err := s.ExecuteCleanup(clock.Add(1)); err != nil {
		t.Fatal(err)
	}
	p, err := s.PlanCleanup(clock.Load())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Deletable) != 0 {
		t.Fatalf("清理后仍有可删备份: %v", p.Deletable)
	}
}
