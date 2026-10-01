package schedule

import (
	"fmt"
	"sync"
	"testing"
)

func currentInstances(m *Manager, id string) []Date {
	m.mu.RLock()
	s := m.series[id]
	m.mu.RUnlock()
	return s.instances()
}

func datesStr(ds []Date) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.String()
	}
	return out
}

func instStr(is []Instance) string {
	out := "["
	for i, x := range is {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("(%s orig=%s actual=%s)", x.SeriesID, x.Original.String(), x.Actual.String())
	}
	return out + "]"
}

// 第 5 个星期不存在的月份被跳过且不占 count。
func TestFifthWeekdaySkipped(t *testing.T) {
	m := NewManager()
	expectCode(t, m.Create(CreateInput{ID: "s", Start: "2024-01-01", Rule: Rule{K: 1, Nth: 5, W: 5}, Count: 3}), "", "create")
	got := datesStr(currentInstances(m, "s"))
	want := []string{"2024-03-29", "2024-05-31", "2024-08-30"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("instances=%v want %v", got, want)
	}
	if m.Snapshot()[0].Count != 3 {
		t.Fatalf("count must stay 3")
	}
	t.Logf("输入 k=1 nth=5 w=5 start=2024-01-01 count=3 => 输出 %v；判定依据：01/02/04 月无第 5 个周五，跳过且不占名额", got)

	nm := newNaive()
	if c := nm.create(CreateInput{ID: "s", Start: "2024-01-01", Rule: Rule{K: 1, Nth: 5, W: 5}, Count: 3}); c != "" {
		t.Fatalf("naive create: %s", c)
	}
	nord := naiveInstances(nm.series["s"])
	wn := []int{mustDate(2024, 3, 29).ord, mustDate(2024, 5, 31).ord, mustDate(2024, 8, 30).ord}
	if fmt.Sprint(nord) != fmt.Sprint(wn) {
		t.Fatalf("naive mismatch: %s", fmtInstances(nord))
	}
}

// 最后一个星期五。
func TestLastFriday(t *testing.T) {
	m := NewManager()
	expectCode(t, m.Create(CreateInput{ID: "s", Start: "2024-01-15", Rule: Rule{K: 1, Nth: -1, W: 5}, Count: 3}), "", "create")
	got := datesStr(currentInstances(m, "s"))
	want := []string{"2024-01-26", "2024-02-23", "2024-03-29"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("instances=%v want %v", got, want)
	}
	t.Logf("输入 nth=-1 w=5 start=2024-01-15 => 输出 %v；判定依据：每月最后一个周五", got)
}

// start 之后同月候选早于 start：早于 start 的候选不算实例，也不顺延。
func TestSameMonthCandidateBeforeStart(t *testing.T) {
	m := NewManager()
	expectCode(t, m.Create(CreateInput{ID: "s", Start: "2024-01-31", Rule: Rule{K: 1, Nth: -1, W: 5}, Count: 2}), "", "create")
	got := datesStr(currentInstances(m, "s"))
	want := []string{"2024-02-23", "2024-03-29"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("instances=%v want %v", got, want)
	}
	t.Logf("输入 start=2024-01-31 nth=-1 w=5 => 输出 %v；判定依据：2024-01-26 早于 start，不算实例且不顺延", got)
}

// 闰年 2 月。
func TestLeapFebruary(t *testing.T) {
	m := NewManager()
	expectCode(t, m.Create(CreateInput{ID: "leap", Start: "2024-02-01", Rule: Rule{K: 1, Nth: 5, W: 4}, Count: 1}), "", "create")
	if got := datesStr(currentInstances(m, "leap")); fmt.Sprint(got) != "[2024-02-29]" {
		t.Fatalf("leap Feb 5th Thursday = %v", got)
	}
	expectCode(t, m.Create(CreateInput{ID: "common", Start: "2023-02-01", Rule: Rule{K: 1, Nth: 5, W: 4}, Count: 1}), "", "create")
	if got := datesStr(currentInstances(m, "common")); fmt.Sprint(got) != "[2023-03-30]" {
		t.Fatalf("common Feb must be skipped, got %v", got)
	}
	t.Logf("判定依据：2024 闰年 2 月有 5 个周四（29 日即周四）；2023 平年 2 月只有 4 个，跳过该月")
}

// 并发调用：串行化保证结果等价于某个串行顺序；展开只看到完整快照。
func TestConcurrentOps(t *testing.T) {
	m := NewManager()
	expectCode(t, m.Create(CreateInput{ID: "c", Start: "2024-01-01", Rule: Rule{K: 1, Nth: 5, W: 5}, Count: 50}), "", "create")
	instances := currentInstances(m, "c")

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for _, d := range instances {
				ds := d.String()
				if (d.ord+g)%3 == 0 {
					_ = m.Cancel("c", ds)
				} else if (d.ord+g)%3 == 1 {
					_ = m.Reschedule(RescheduleInput{ID: "c", Date: ds, NewDate: ds})
				}
				_, _ = m.Expand("2024-01-01", "2030-01-01")
			}
		}(g)
	}
	wg.Wait()

	// 串行不变量：每个实例至多处于一种例外状态；count 仍是 50；展开结果确定。
	snap := m.Snapshot()[0]
	if snap.Count != 50 {
		t.Fatalf("count=%d want 50", snap.Count)
	}
	for k := range snap.Moved {
		if snap.Canceled[k] {
			t.Fatalf("instance %s both canceled and moved", k)
		}
	}
	r1, err := m.Expand("2024-01-01", "2030-01-01")
	expectCode(t, err, "", "expand")
	r2, _ := m.Expand("2024-01-01", "2030-01-01")
	if instStr(r1) != instStr(r2) {
		t.Fatal("concurrent result not stable/deterministic")
	}
	t.Logf("判定依据：8 goroutine 并发取消/改期/展开后无 panic、无状态矛盾，展开可重复")
}
