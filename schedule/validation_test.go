package schedule

import "testing"

func TestRejectionsAreAtomicAndDistinct(t *testing.T) {
	m := NewManager()
	check := func(name string, want ErrorCode, fn func() error) {
		t.Helper()
		before := len(m.Snapshot())
		err := fn()
		expectCode(t, err, want, name)
		if len(m.Snapshot()) != before {
			t.Fatalf("%s: rejected op changed state", name)
		}
		t.Logf("输入 %-28s => 拒绝原因 %-24q；状态未改变（系列数=%d）", name, want, before)
	}

	mustCreate := func(id string, in CreateInput) {
		t.Helper()
		if err := m.Create(in); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	check("bad date", ErrInvalidDate, func() error {
		_, e := m.Expand("2024-02-30", "2024-03-01")
		return e
	})
	check("k<1", ErrInvalidInterval, func() error {
		return m.Create(CreateInput{ID: "x", Start: "2024-01-01", Rule: Rule{K: 0, Nth: 1, W: 1}, Count: 1})
	})
	check("nth=0", ErrInvalidNth, func() error {
		return m.Create(CreateInput{ID: "x", Start: "2024-01-01", Rule: Rule{K: 1, Nth: 0, W: 1}, Count: 1})
	})
	check("nth=-2", ErrInvalidNth, func() error {
		return m.Create(CreateInput{ID: "x", Start: "2024-01-01", Rule: Rule{K: 1, Nth: -2, W: 1}, Count: 1})
	})
	check("nth=6", ErrInvalidNth, func() error {
		return m.Create(CreateInput{ID: "x", Start: "2024-01-01", Rule: Rule{K: 1, Nth: 6, W: 1}, Count: 1})
	})
	check("weekday=0", ErrInvalidWeekday, func() error {
		return m.Create(CreateInput{ID: "x", Start: "2024-01-01", Rule: Rule{K: 1, Nth: 1, W: 0}, Count: 1})
	})
	check("weekday=8", ErrInvalidWeekday, func() error {
		return m.Create(CreateInput{ID: "x", Start: "2024-01-01", Rule: Rule{K: 1, Nth: 1, W: 8}, Count: 1})
	})
	check("count+until", ErrInvalidEnd, func() error {
		return m.Create(CreateInput{ID: "x", Start: "2024-01-01", Rule: Rule{K: 1, Nth: 1, W: 1}, Count: 3, Until: "2024-12-31"})
	})
	check("neither", ErrInvalidEnd, func() error {
		return m.Create(CreateInput{ID: "x", Start: "2024-01-01", Rule: Rule{K: 1, Nth: 1, W: 1}})
	})
	check("until<start", ErrUntilBeforeStart, func() error {
		return m.Create(CreateInput{ID: "x", Start: "2024-03-01", Rule: Rule{K: 1, Nth: 1, W: 1}, Until: "2024-02-01"})
	})

	mustCreate("s", CreateInput{ID: "s", Start: "2024-01-15", Rule: Rule{K: 1, Nth: -1, W: 5}, Count: 3})

	check("dup id", ErrDuplicateSeries, func() error {
		return m.Create(CreateInput{ID: "s", Start: "2024-01-15", Rule: Rule{K: 1, Nth: -1, W: 5}, Count: 3})
	})
	check("unknown series", ErrUnknownSeries, func() error {
		return m.Cancel("ghost", "2024-01-26")
	})
	check("not instance", ErrNotInstance, func() error {
		return m.Cancel("s", "2024-01-27")
	})
	check("empty range", ErrInvalidRange, func() error {
		_, e := m.Expand("2024-05-01", "2024-05-01")
		return e
	})
	check("reversed range", ErrInvalidRange, func() error {
		_, e := m.Expand("2024-05-02", "2024-05-01")
		return e
	})
	check("span>3660", ErrInvalidRange, func() error {
		_, e := m.Expand("2000-01-01", "2010-01-10")
		return e
	})

	// 3660 天整（差 3660）是允许的
	if _, err := m.Expand("2000-01-01", "2010-01-01"); err != nil {
		t.Fatalf("exactly 3660-day span must be allowed: %v", err)
	}
	t.Logf("判定依据：[from,to) 半开区间，from<to 且 to-from<=3660；天数差用序日直接相减")
}

// Example 文档化典型输入输出。
func TestExampleWorkflow(t *testing.T) {
	m := NewManager()
	_ = m.Create(CreateInput{ID: "team-sync", Start: "2024-01-01", Rule: Rule{K: 1, Nth: -1, W: 5}, Count: 6})
	rows, _ := m.Expand("2024-01-01", "2024-04-01")
	for _, r := range rows {
		t.Logf("展开输出：series=%s original=%s actual=%s", r.SeriesID, r.Original.String(), r.Actual.String())
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows (01-26,02-23,03-29), got %d", len(rows))
	}
}
