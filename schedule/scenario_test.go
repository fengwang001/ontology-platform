package schedule

import (
	"fmt"
	"testing"
)

// 取消与改期的实例仍占 count；取消释放改期日；展开按实际日期；改期跨出展开区间。
func TestCancelAndRescheduleStillCounted(t *testing.T) {
	m := NewManager()
	expectCode(t, m.Create(CreateInput{ID: "s", Start: "2024-01-15", Rule: Rule{K: 1, Nth: -1, W: 5}, Count: 3}), "", "create")
	expectCode(t, m.Cancel("s", "2024-01-26"), "", "cancel #1")
	expectCode(t, m.Reschedule(RescheduleInput{ID: "s", Date: "2024-02-23", NewDate: "2024-07-01"}), "", "move #2")
	if snap := m.Snapshot()[0]; snap.Count != 3 {
		t.Fatalf("count=%d want 3", snap.Count)
	}

	g1, err := m.Expand("2024-01-01", "2024-05-01")
	expectCode(t, err, "", "expand")
	if len(g1) != 1 || !g1[0].Original.Equal(mustDate(2024, 3, 29)) {
		t.Fatalf("moved instance must leave window, got %s", instStr(g1))
	}
	g2, err := m.Expand("2024-06-01", "2024-08-01")
	expectCode(t, err, "", "expand")
	if len(g2) != 1 || !g2[0].Actual.Equal(mustDate(2024, 7, 1)) {
		t.Fatalf("moved instance appears at actual date, got %s", instStr(g2))
	}

	expectCode(t, m.Cancel("s", "2024-01-26"), ErrAlreadyCanceled, "double cancel")
	expectCode(t, m.Reschedule(RescheduleInput{ID: "s", Date: "2024-01-26", NewDate: "2024-09-01"}), ErrAlreadyCanceled, "move canceled")
	expectCode(t, m.Reschedule(RescheduleInput{ID: "s", Date: "2024-02-23", NewDate: "2024-07-15"}), "", "move again")
	expectCode(t, m.Reschedule(RescheduleInput{ID: "s", Date: "2024-03-29", NewDate: "2024-07-01"}), "", "old moved date freed")
	expectCode(t, m.Reschedule(RescheduleInput{ID: "s", Date: "2024-03-29", NewDate: "2024-07-15"}), ErrDateOccupied, "occupied")
	expectCode(t, m.Cancel("s", "2024-03-29"), "", "cancel moved instance")
	expectCode(t, m.Reschedule(RescheduleInput{ID: "s", Date: "2024-02-23", NewDate: "2024-07-01"}), "", "reuse freed date")
	t.Logf("判定依据：count 始终为 3；取消不出现；改期按实际日落窗；再改期释放旧改期日；取消已改期实例释放改期日；冲突按同系列实际日")
}

// 拆分：date 之前 0 实例；新规则首个候选晚于 date。
func TestSplitZeroBefore(t *testing.T) {
	m := NewManager()
	expectCode(t, m.Create(CreateInput{ID: "s", Start: "2024-01-15", Rule: Rule{K: 1, Nth: -1, W: 5}, Count: 4}), "", "create")
	newID, err := m.Split(SplitInput{ID: "s", Date: "2024-01-26", NewID: "s2", NewRule: Rule{K: 1, Nth: 1, W: 1}})
	expectCode(t, err, "", "split")
	if newID != "s2" {
		t.Fatalf("new id=%q", newID)
	}
	snaps := map[string]Series{}
	for _, s := range m.Snapshot() {
		snaps[s.ID] = s
	}
	if snaps["s"].Count != 0 || len(currentInstances(m, "s")) != 0 {
		t.Fatalf("old series must have 0 instances, got count=%d", snaps["s"].Count)
	}
	if snaps["s2"].Count != 4 {
		t.Fatalf("new series count=%d want 4", snaps["s2"].Count)
	}
	got := datesStr(currentInstances(m, "s2"))
	want := []string{"2024-02-05", "2024-03-04", "2024-04-01", "2024-05-06"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("new series instances=%v want %v", got, want)
	}
	t.Logf("输入 split@2024-01-26 新规则 nth=1 w=1 => 旧 count=0（无实例），新 %v；判定依据：1 月首个周一 1 日早于 date 丢弃，首个候选 2024-02-05", got)
}

// 拆分保留 date 之前的例外、丢弃 date 及之后的例外；until 型沿用 until 并把旧 until 改为前一天。
func TestSplitKeepsEarlierExceptions(t *testing.T) {
	m := NewManager()
	expectCode(t, m.Create(CreateInput{ID: "s", Start: "2024-01-15", Rule: Rule{K: 1, Nth: -1, W: 5}, Until: "2024-06-30"}), "", "create")
	expectCode(t, m.Cancel("s", "2024-01-26"), "", "cancel before")
	expectCode(t, m.Reschedule(RescheduleInput{ID: "s", Date: "2024-02-23", NewDate: "2024-02-25"}), "", "move before")
	expectCode(t, m.Cancel("s", "2024-03-29"), "", "cancel at")
	expectCode(t, m.Reschedule(RescheduleInput{ID: "s", Date: "2024-04-26", NewDate: "2024-04-28"}), "", "move at")
	_, err := m.Split(SplitInput{ID: "s", Date: "2024-03-29", NewID: "s2", NewRule: Rule{K: 1, Nth: -1, W: 5}})
	expectCode(t, err, "", "split")

	snaps := map[string]Series{}
	for _, s := range m.Snapshot() {
		snaps[s.ID] = s
	}
	if !snaps["s"].HasUntil || snaps["s"].Until.String() != "2024-03-28" {
		t.Fatalf("old until=%+v want 2024-03-28", snaps["s"].Until)
	}
	if !snaps["s2"].HasUntil || snaps["s2"].Until.String() != "2024-06-30" {
		t.Fatalf("new until=%+v want 2024-06-30", snaps["s2"].Until)
	}
	if !snaps["s"].Canceled["2024-01-26"] {
		t.Fatal("old must keep earlier cancellation")
	}
	if !snaps["s"].Moved["2024-02-23"].Equal(mustDate(2024, 2, 25)) {
		t.Fatal("old must keep earlier reschedule")
	}
	if len(snaps["s2"].Canceled) != 0 || len(snaps["s2"].Moved) != 0 {
		t.Fatalf("new series must start with no exceptions, got %+v %+v", snaps["s2"].Canceled, snaps["s2"].Moved)
	}
	// 拆分点本身必须是新系列的第一个实例（含被取消者）
	if got := datesStr(currentInstances(m, "s2")); got[0] != "2024-03-29" {
		t.Fatalf("new series first instance=%v want 2024-03-29", got)
	}
	t.Logf("判定依据：date 之前例外随旧系列（until=2024-03-28）；date 及之后的取消/改期全部丢弃；新系列沿用 until=2024-06-30 且无例外")
}

// 拆分点必须是原日期（含已取消/改期者），非实例日拒绝；新 id 重复拒绝。
func TestSplitValidation(t *testing.T) {
	m := NewManager()
	expectCode(t, m.Create(CreateInput{ID: "s", Start: "2024-01-15", Rule: Rule{K: 1, Nth: -1, W: 5}, Count: 3}), "", "create")
	expectCode(t, m.Cancel("s", "2024-01-26"), "", "cancel")
	expectCode(t, m.Reschedule(RescheduleInput{ID: "s", Date: "2024-02-23", NewDate: "2024-05-01"}), "", "move")
	var err error
	_, err = m.Split(SplitInput{ID: "s", Date: "2024-01-27", NewID: "x", NewRule: Rule{K: 1, Nth: -1, W: 5}})
	expectCode(t, err, ErrNotInstance, "non-instance")
	_, err = m.Split(SplitInput{ID: "nope", Date: "2024-01-26", NewID: "x", NewRule: Rule{K: 1, Nth: -1, W: 5}})
	expectCode(t, err, ErrUnknownSeries, "unknown")
	_, err = m.Split(SplitInput{ID: "s", Date: "2024-01-26", NewID: "s2", NewRule: Rule{K: 1, Nth: -1, W: 5}})
	expectCode(t, err, "", "split on canceled date")
	_, err = m.Split(SplitInput{ID: "s", Date: "2024-02-23", NewID: "s2", NewRule: Rule{K: 1, Nth: -1, W: 5}})
	expectCode(t, err, ErrDuplicateSeries, "dup new id")
	// 旧系列被改期实例的实际日 5-1 不算拆分点，必须用原日期
	_, err = m.Split(SplitInput{ID: "s", Date: "2024-05-01", NewID: "s3", NewRule: Rule{K: 1, Nth: -1, W: 5}})
	expectCode(t, err, ErrNotInstance, "actual date is not split point")
	t.Logf("判定依据：拆分只认实例原日期（含取消/改期者），改期后的实际日不是拆分点")
}
