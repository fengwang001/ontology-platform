package schedule

import (
	"bytes"
	"log"
	"reflect"
	"strings"
	"testing"
)

func mustCreate(t *testing.T, m *Manager, in CreateInput) {
	t.Helper()
	if err := m.Create(in); err != nil {
		t.Fatalf("Create(%+v) unexpected error: %v", in, err)
	}
}

func codeOf(err error) ErrCode {
	if err == nil {
		return ""
	}
	e, ok := err.(*Error)
	if !ok {
		return "NON_SCHEDULE_ERROR"
	}
	return e.Code
}

func actuals(ins []Instance) []string {
	out := make([]string, len(ins))
	for i, x := range ins {
		out[i] = x.Actual
	}
	return out
}

// 第 5 个星期不存在的月份被跳过，且不占 count。
func TestFifthWeekdayMissingSkipped(t *testing.T) {
	// k=1, nth=5, w=1(周一)，start=2024-12-30（第 5 个周一）。
	// 2025-01 与 2025-02 均无第 5 个周一，应直接跳到 2025-03-31。
	m := NewManager(nil)
	mustCreate(t, m, CreateInput{
		ID: "s", Start: "2024-12-30",
		Rule: Rule{IntervalMonths: 1, Nth: 5, Weekday: 1},
		Term: Termination{Count: 4},
	})
	ins, err := m.Expand("2024-01-01", "2034-01-08")
	if err != nil {
		t.Fatal(err)
	}
	got := actuals(ins)
	want := []string{"2024-12-30", "2025-03-31", "2025-06-30", "2025-09-29"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// nth=-1：每月最后一个星期五。
func TestLastWeekday(t *testing.T) {
	m := NewManager(nil)
	mustCreate(t, m, CreateInput{
		ID: "s", Start: "2025-01-31",
		Rule: Rule{IntervalMonths: 1, Nth: -1, Weekday: 5},
		Term: Termination{Until: "2025-04-30"},
	})
	ins, err := m.Expand("2024-01-01", "2034-01-08")
	if err != nil {
		t.Fatal(err)
	}
	got := actuals(ins)
	want := []string{"2025-01-31", "2025-02-28", "2025-03-28", "2025-04-25"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// start 之后同月候选早于 start：不算实例，也不顺延。
func TestSameMonthCandidateBeforeStart(t *testing.T) {
	// 第 2 个周一：start=2025-01-13（第 2 个周一当天）。
	m := NewManager(nil)
	mustCreate(t, m, CreateInput{
		ID: "a", Start: "2025-01-13",
		Rule: Rule{IntervalMonths: 1, Nth: 2, Weekday: 1},
		Term: Termination{Count: 2},
	})
	ins, _ := m.Expand("2024-01-01", "2034-01-08")
	if got, want := actuals(ins), []string{"2025-01-13", "2025-02-10"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	// start 晚于该月候选日：2025-01 的第 1 个周一为 01-06，start=01-10，
	// 01-06 早于 start 不算，下一个是 2025-02-03。
	m2 := NewManager(nil)
	mustCreate(t, m2, CreateInput{
		ID: "a", Start: "2025-01-10",
		Rule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1},
		Term: Termination{Count: 2},
	})
	ins2, _ := m2.Expand("2024-01-01", "2034-01-08")
	if got, want := actuals(ins2), []string{"2025-02-03", "2025-03-03"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// 闰年 2 月：候选生成与非法日期拒绝。
func TestLeapFebruary(t *testing.T) {
	// 2024-02 第 4 个周五 = 02-23；2025-02 同样存在。
	m := NewManager(nil)
	mustCreate(t, m, CreateInput{
		ID: "s", Start: "2024-02-23",
		Rule: Rule{IntervalMonths: 12, Nth: 4, Weekday: 5},
		Term: Termination{Count: 2},
	})
	ins, _ := m.Expand("2024-01-01", "2034-01-08")
	if got, want := actuals(ins), []string{"2024-02-23", "2025-02-28"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// 2 月 30 日无论闰平都必须拒绝；2200 非闰年，02-29 拒绝；2024-02-29 合法。
	for _, s := range []string{"2024-02-30", "2023-02-29", "2200-02-29", "1969-12-31", "2201-01-01", "2024-13-01", "2024/02/01", "2024-2-1"} {
		if _, err := parseDate(s); err == nil {
			t.Fatalf("date %q should be rejected", s)
		}
	}
	if _, err := parseDate("2024-02-29"); err != nil {
		t.Fatalf("2024-02-29 should be valid: %v", err)
	}
}

// 取消与改期的实例仍占 count。
func TestCancelledRescheduledStillCount(t *testing.T) {
	m := NewManager(nil)
	mustCreate(t, m, CreateInput{
		ID: "s", Start: "2025-01-06",
		Rule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1},
		Term: Termination{Count: 3}, // 01-06, 02-03, 03-03
	})
	if err := m.Cancel("s", "2025-01-06"); err != nil {
		t.Fatal(err)
	}
	if err := m.Reschedule("s", "2025-02-03", "2025-07-01"); err != nil {
		t.Fatal(err)
	}
	ins, _ := m.Expand("2024-01-01", "2034-01-08")
	if got, want := actuals(ins), []string{"2025-03-03", "2025-07-01"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v（取消/改期不应再补出第 4 个候选）", got, want)
	}
	// 对已改期实例再改期：旧改期日释放、可被他人使用；改期到新日期。
	if err := m.Reschedule("s", "2025-02-03", "2025-08-01"); err != nil {
		t.Fatal(err)
	}
	if err := m.Reschedule("s", "2025-03-03", "2025-07-01"); err != nil {
		t.Fatalf("old rescheduled date should be released: %v", err)
	}
	// 对已取消实例改期 => 与“取消已取消”同因拒绝。
	if err := m.Reschedule("s", "2025-01-06", "2025-09-01"); codeOf(err) != ErrAlreadyCancelled {
		t.Fatalf("got %v want %v", codeOf(err), ErrAlreadyCancelled)
	}
	// 取消已取消实例。
	if err := m.Cancel("s", "2025-01-06"); codeOf(err) != ErrAlreadyCancelled {
		t.Fatalf("got %v want %v", codeOf(err), ErrAlreadyCancelled)
	}
	// 取消已改期实例：释放改期日，冲突检查随之放行。
	if err := m.Cancel("s", "2025-02-03"); err != nil {
		t.Fatal(err)
	}
	if err := m.Reschedule("s", "2025-03-03", "2025-08-01"); err != nil {
		t.Fatalf("cancelling a rescheduled instance must release its date: %v", err)
	}
}

// 改期目标冲突：同系列另一实例原位或改期占用。
func TestRescheduleConflict(t *testing.T) {
	m := NewManager(nil)
	mustCreate(t, m, CreateInput{
		ID: "s", Start: "2025-01-06",
		Rule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1},
		Term: Termination{Count: 3},
	})
	if err := m.Reschedule("s", "2025-01-06", "2025-02-03"); codeOf(err) != ErrTargetOccupied {
		t.Fatalf("in-place occupancy: got %v", codeOf(err))
	}
	if err := m.Reschedule("s", "2025-01-06", "2025-05-01"); err != nil {
		t.Fatal(err)
	}
	if err := m.Reschedule("s", "2025-03-03", "2025-05-01"); codeOf(err) != ErrTargetOccupied {
		t.Fatalf("rescheduled occupancy: got %v", codeOf(err))
	}
	// 改期到自己当前所在日（原位）允许。
	if err := m.Reschedule("s", "2025-02-03", "2025-02-03"); err != nil {
		t.Fatalf("rescheduling to current own date must be allowed: %v", err)
	}
}

// “此次及以后”拆分：基本名额归属与例外丢弃。
func TestSplitBasic(t *testing.T) {
	m := NewManager(nil)
	mustCreate(t, m, CreateInput{
		ID: "s", Start: "2025-01-06",
		Rule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1},
		Term: Termination{Count: 5}, // 01-06 02-03 03-03 04-07 05-05
	})
	if err := m.Cancel("s", "2025-01-06"); err != nil {
		t.Fatal(err)
	}
	if err := m.Reschedule("s", "2025-04-07", "2025-04-08"); err != nil {
		t.Fatal(err)
	}
	// 在 03-03（第 3 个实例）处拆分；旧系列保留 2 个名额，新系列 3 个。
	err := m.Split(SplitInput{ID: "s", Date: "2025-03-03", NewID: "s2",
		NewRule: Rule{IntervalMonths: 2, Nth: 2, Weekday: 3}})
	if err != nil {
		t.Fatal(err)
	}
	ins, _ := m.Expand("2024-01-01", "2034-01-08")
	got := actuals(ins)
	// 旧系列：01-06 已取消；02-03 保留。04-07 的改期属于 date 及之后，丢弃，
	// 且 04-07/05-05 已不在旧系列名额内。
	// 新规则 k=2, 第 2 个周二：2025-03 第 2 个周二 = 03-12（>= 03-03），
	// 之后 2025-05-14、2025-07-09，共 3 个名额。
	want := []string{"2025-02-03", "2025-03-12", "2025-05-14", "2025-07-09"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// 拆分时 date 之前实例数为 0：旧系列保留且 count=0（无实例）。
func TestSplitZeroBefore(t *testing.T) {
	m := NewManager(nil)
	mustCreate(t, m, CreateInput{
		ID: "s", Start: "2025-01-06",
		Rule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1},
		Term: Termination{Count: 3},
	})
	// 即使把首个实例取消，它仍是合法拆分点；之前实例数为 0。
	if err := m.Cancel("s", "2025-01-06"); err != nil {
		t.Fatal(err)
	}
	err := m.Split(SplitInput{ID: "s", Date: "2025-01-06", NewID: "s2",
		NewRule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ins, _ := m.Expand("2024-01-01", "2034-01-08")
	// 新系列从 01-06 起算：01-06 本身（取消例外不跨系列携带）、02-03、03-03。
	if got, want := actuals(ins), []string{"2025-01-06", "2025-02-03", "2025-03-03"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// 旧系列 id 仍存在（无实例），可以继续作为操作目标做存在性校验。
	if err := m.Cancel("s", "2025-02-03"); codeOf(err) != ErrNotInstance {
		t.Fatalf("old series must retain zero-instance identity; got %v", codeOf(err))
	}
}

// until 型拆分：旧 until 变为 date 前一天（可早于 start），新系列沿用原 until。
func TestSplitUntil(t *testing.T) {
	m := NewManager(nil)
	mustCreate(t, m, CreateInput{
		ID: "s", Start: "2025-01-06",
		Rule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1},
		Term: Termination{Until: "2025-03-31"}, // 01-06 02-03 03-03
	})
	err := m.Split(SplitInput{ID: "s", Date: "2025-01-06", NewID: "s2",
		NewRule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ins, _ := m.Expand("2024-01-01", "2034-01-08")
	// 旧 until 变为 2025-01-05（早于 start，无实例）；新系列沿用 until=03-31。
	if got, want := actuals(ins), []string{"2025-01-06", "2025-02-03", "2025-03-03"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// 拆分后新规则首个候选晚于 date（同月候选早于 date 被跳过）。
func TestSplitFirstCandidateAfterDate(t *testing.T) {
	m := NewManager(nil)
	mustCreate(t, m, CreateInput{
		ID: "s", Start: "2025-01-06",
		Rule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1},
		Term: Termination{Count: 4},
	})
	// 在 02-03 拆分；新规则第 1 个周一，start=02-03，2025-02 的第 1 个周一 02-03 即当天。
	// 改用第 2 个周一（02-10）验证“首个候选晚于 date”：02 月候选 02-10 > 02-03。
	err := m.Split(SplitInput{ID: "s", Date: "2025-02-03", NewID: "s2",
		NewRule: Rule{IntervalMonths: 1, Nth: 2, Weekday: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ins, _ := m.Expand("2024-01-01", "2034-01-08")
	// 旧：01-06；新（3 个名额）：02-10、03-10、04-14。
	if got, want := actuals(ins), []string{"2025-01-06", "2025-02-10", "2025-03-10", "2025-04-14"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// 改期跨出展开区间：按实际日期过滤。
func TestRescheduleOutsideWindow(t *testing.T) {
	m := NewManager(nil)
	mustCreate(t, m, CreateInput{
		ID: "s", Start: "2025-01-06",
		Rule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1},
		Term: Termination{Count: 3},
	})
	if err := m.Reschedule("s", "2025-02-03", "2025-06-01"); err != nil {
		t.Fatal(err)
	}
	ins, err := m.Expand("2025-01-01", "2025-04-01")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := actuals(ins), []string{"2025-01-06", "2025-03-03"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// 宽窗口里按实际日期出现在 06-01。
	ins2, _ := m.Expand("2025-01-01", "2026-01-01")
	if got, want := actuals(ins2), []string{"2025-01-06", "2025-03-03", "2025-06-01"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// 全部可区分的拒绝原因。
func TestRejectionReasons(t *testing.T) {
	base := CreateInput{ID: "s", Start: "2025-01-06",
		Rule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1}, Term: Termination{Count: 3}}
	cases := []struct {
		name string
		run  func(m *Manager) error
		want ErrCode
	}{
		{"bad format", func(m *Manager) error {
			c := base
			c.Start = "2025/01/06"
			return m.Create(c)
		}, ErrDateFormat},
		{"impossible date", func(m *Manager) error {
			c := base
			c.Start = "2025-02-29"
			return m.Create(c)
		}, ErrDateRange},
		{"year out of range", func(m *Manager) error {
			c := base
			c.Start = "1899-01-01"
			return m.Create(c)
		}, ErrDateRange},
		{"bad k", func(m *Manager) error {
			c := base
			c.Rule.IntervalMonths = 0
			return m.Create(c)
		}, ErrBadInterval},
		{"nth zero", func(m *Manager) error {
			c := base
			c.Rule.Nth = 0
			return m.Create(c)
		}, ErrBadNth},
		{"nth -2", func(m *Manager) error {
			c := base
			c.Rule.Nth = -2
			return m.Create(c)
		}, ErrBadNth},
		{"nth 6", func(m *Manager) error {
			c := base
			c.Rule.Nth = 6
			return m.Create(c)
		}, ErrBadNth},
		{"bad weekday", func(m *Manager) error {
			c := base
			c.Rule.Weekday = 8
			return m.Create(c)
		}, ErrBadWeekday},
		{"both term", func(m *Manager) error {
			c := base
			c.Term = Termination{Count: 1, Until: "2025-12-31"}
			return m.Create(c)
		}, ErrTerminalCountUntil},
		{"neither term", func(m *Manager) error {
			c := base
			c.Term = Termination{}
			return m.Create(c)
		}, ErrTerminalCountUntil},
		{"count zero", func(m *Manager) error {
			c := base
			c.Term = Termination{Count: -2}
			return m.Create(c)
		}, ErrCountRange},
		{"until before start", func(m *Manager) error {
			c := base
			c.Term = Termination{Until: "2024-12-31"}
			return m.Create(c)
		}, ErrUntilBeforeStart},
		{"duplicate id", func(m *Manager) error {
			mustCreate(t, m, base)
			return m.Create(base)
		}, ErrDuplicateID},
		{"cancel unknown", func(m *Manager) error { return m.Cancel("nope", "2025-01-06") }, ErrUnknownSeries},
		{"reschedule unknown", func(m *Manager) error {
			return m.Reschedule("nope", "2025-01-06", "2025-01-07")
		}, ErrUnknownSeries},
		{"split unknown", func(m *Manager) error {
			return m.Split(SplitInput{ID: "nope", Date: "2025-01-06", NewID: "x",
				NewRule: base.Rule})
		}, ErrUnknownSeries},
		{"cancel non-instance", func(m *Manager) error {
			mustCreate(t, m, base)
			return m.Cancel("s", "2025-01-07")
		}, ErrNotInstance},
		{"split non-instance", func(m *Manager) error {
			mustCreate(t, m, base)
			return m.Split(SplitInput{ID: "s", Date: "2025-01-07", NewID: "s2", NewRule: base.Rule})
		}, ErrNotInstance},
		{"split duplicate new id", func(m *Manager) error {
			mustCreate(t, m, base)
			c2 := base
			c2.ID = "s2"
			c2.Term = Termination{Count: 1}
			mustCreate(t, m, c2)
			return m.Split(SplitInput{ID: "s", Date: "2025-01-06", NewID: "s2", NewRule: base.Rule})
		}, ErrDuplicateID},
		{"expand empty", func(m *Manager) error {
			_, err := m.Expand("2025-02-01", "2025-02-01")
			return err
		}, ErrEmptyRange},
		{"expand inverted", func(m *Manager) error {
			_, err := m.Expand("2025-02-02", "2025-02-01")
			return err
		}, ErrEmptyRange},
		{"expand too wide", func(m *Manager) error {
			_, err := m.Expand("2024-01-01", "2034-01-09") // 3661 天
			return err
		}, ErrRangeTooWide},
		{"expand bad date", func(m *Manager) error {
			_, err := m.Expand("2025-02-30", "2025-03-01")
			return err
		}, ErrDateRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(nil)
			if got := codeOf(tc.run(m)); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// 被拒绝的操作不得改变任何系列：失败后展开结果不变。
func TestRejectionIsAtomic(t *testing.T) {
	m := NewManager(nil)
	mustCreate(t, m, CreateInput{ID: "s", Start: "2025-01-06",
		Rule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1}, Term: Termination{Count: 3}})
	before, _ := m.Expand("2024-01-01", "2034-01-08")
	_ = m.Cancel("s", "2025-01-07")
	_ = m.Reschedule("s", "2025-02-03", "2025-03-03") // 冲突占用
	_ = m.Split(SplitInput{ID: "s", Date: "2025-01-07", NewID: "s2",
		NewRule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1}})
	after, _ := m.Expand("2024-01-01", "2034-01-08")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("state changed by rejected ops:\nbefore=%v\nafter =%v", before, after)
	}
}

// 排序：实际日期、系列 id、原日期升序；改期可使输出顺序与原日期不同。
func TestExpandOrdering(t *testing.T) {
	m := NewManager(nil)
	mustCreate(t, m, CreateInput{ID: "a", Start: "2025-01-06",
		Rule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1}, Term: Termination{Count: 2}})
	mustCreate(t, m, CreateInput{ID: "b", Start: "2025-01-06",
		Rule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1}, Term: Termination{Count: 2}})
	if err := m.Reschedule("a", "2025-01-06", "2025-02-15"); err != nil {
		t.Fatal(err)
	}
	ins, _ := m.Expand("2025-01-01", "2025-03-01")
	got := make([][2]string, len(ins))
	for i, x := range ins {
		got[i] = [2]string{x.Actual, x.SeriesID}
	}
	want := [][2]string{
		{"2025-01-06", "b"},
		{"2025-02-03", "a"}, {"2025-02-03", "b"}, {"2025-02-15", "a"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// 日志打印输入、输出与判定依据。
func TestLogging(t *testing.T) {
	var buf bytes.Buffer
	m := NewManager(log.New(&buf, "", 0))
	mustCreate(t, m, CreateInput{ID: "s", Start: "2025-01-06",
		Rule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1}, Term: Termination{Count: 2}})
	_ = m.Cancel("s", "2025-01-06")
	_, _ = m.Expand("2025-01-01", "2025-04-01")
	logText := buf.String()
	for _, want := range []string{"CREATE OK", "original_dates", "CANCEL OK", "judgement", "EXPAND", "cancelled"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q:\n%s", want, logText)
		}
	}
}
