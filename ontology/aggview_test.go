package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func ins(id, group string, value int) Change {
	return Change{Row: Row{ID: id, Group: group, Value: value}}
}

func TestGroupLimitRejectedAndAtomic(t *testing.T) {
	mem := &memoryLogger{}
	m, err := NewMaintainer(Config{MinCount: 1, MinSum: 0, MaxGroups: 1}, mem)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Apply([]Change{ins("a", "g1", 1)}); err != nil {
		t.Fatal(err)
	}

	// 一批里同时删 g1、建 g2：提交后仍只有 1 个组，合法（恰好等于上限）。
	if _, _, err := m.Apply([]Change{del("a"), ins("b", "g2", 1)}); err != nil {
		t.Fatalf("delete+create keeps group count at limit, got %v", err)
	}

	// 再新建第二个组：超限，整批拒绝（包括批内的其他插入）。
	_, _, err = m.Apply([]Change{ins("c", "g3", 1), ins("d", "g4", 1)})
	re := rejectErrIs(t, err, ErrTooManyGroups)
	if re.Limit != 1 || re.Current != 3 {
		t.Fatalf("limit details wrong: %+v", re)
	}
	if got := m.Snapshot(); len(got) != 1 || got[0].Group != "g2" {
		t.Fatalf("rejected batch changed state: %+v", got)
	}
	if len(mem.logs) != 2 {
		t.Fatalf("rejected batch must not add log, got %d", len(mem.logs))
	}

	// 超限统计的是全部非空组，即使新组不满足视图阈值也算。
	if _, err := NewMaintainer(Config{MinCount: 1, MaxGroups: -1}, nil); err == nil {
		t.Fatal("negative MaxGroups must be rejected")
	}
	if _, err := NewMaintainer(Config{MinCount: 0}, nil); err == nil {
		t.Fatal("zero MinCount must be rejected")
	}
}

// 下游按日志顺序回放，最终状态必须与 Snapshot 完全一致；
// 同一输入序列重复计算得到完全相同的输出。
func TestDownstreamReplayReconcilesAndDeterministic(t *testing.T) {
	cfg := Config{MinCount: 2, MinSum: 5}
	batches := [][]Change{
		{ins("a", "g1", 3), ins("b", "g1", 3)},
		{ins("c", "g2", 1), ins("d", "g2", 1)},
		{ins("e", "g1", 1)},
		{ins("f", "g2", 4)},
		{del("c"), del("d"), del("f")},
		{ins("x", "g3", 10), ins("y", "g3", -1), del("x"), del("y")},
		{ins("g", "g1", 10)},
	}

	run := func() ([]Entry, []Aggregate) {
		m, _ := NewMaintainer(cfg, nil)
		var all []Entry
		for i, batch := range batches {
			entries, _, err := m.Apply(batch)
			if err != nil {
				t.Fatalf("batch %d: %v", i, err)
			}
			all = append(all, entries...)
		}
		return all, m.Snapshot()
	}

	first, snap := run()

	downstream := map[string]Aggregate{}
	for _, e := range first {
		switch e.Kind {
		case EntryRetract:
			delete(downstream, e.Agg.Group)
		case EntryUpsert:
			downstream[e.Agg.Group] = e.Agg
		}
	}
	want := map[string]Aggregate{}
	for _, agg := range snap {
		want[agg.Group] = agg
	}
	if fmt.Sprint(downstream) != fmt.Sprint(want) {
		t.Fatalf("replay=%v snapshot=%v", downstream, want)
	}

	replay, _ := run()
	if fmt.Sprint(replay) != fmt.Sprint(first) {
		t.Fatalf("non-deterministic output:\nfirst=%v\nreplay=%v", first, replay)
	}
}

// 并发读取期间，Snapshot 返回的每个组都满足过滤条件。
func TestConcurrentReadsAlwaysQualified(t *testing.T) {
	m, _ := NewMaintainer(Config{MinCount: 2, MinSum: 5}, nil)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				for _, agg := range m.Snapshot() {
					if agg.Count < 2 || agg.Sum < 5 {
						t.Errorf("read unqualified aggregate: %+v", agg)
						return
					}
				}
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			id := fmt.Sprintf("r%d", i)
			m.Apply([]Change{ins(id, "g1", 3), ins(id+"-b", "g1", 3)})
			m.Apply([]Change{del(id), del(id + "-b")})
		}
		close(stop)
	}()
	wg.Wait()
}

// TextLogger 必须打印输入、判定依据与输出条目。
func TestTextLoggerContents(t *testing.T) {
	var buf bytes.Buffer
	m, _ := NewMaintainer(Config{MinCount: 1, MinSum: 0}, NewTextLogger(&buf))
	if _, _, err := m.Apply([]Change{ins("a", "g1", 5)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Apply([]Change{del("a")}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"input[0] INSERT",
		"input[0] DELETE",
		"decision group=\"g1\"",
		"-> enter",
		"-> leave",
		"output UPSERT",
		"output RETRACT",
		"=== batch begin ===",
		"=== batch end",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q\n%s", want, out)
		}
	}
}

// 前后都不在视图中的组不产生输出，判定依据为空。
func TestInvisibleGroupProducesNoOutput(t *testing.T) {
	m, err := NewMaintainer(Config{MinCount: 5, MinSum: 100}, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries, log, err := m.Apply([]Change{ins("a", "g1", 1), ins("b", "g1", 1)})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("want no entries, got %+v", entries)
	}
	if len(log.Entries) != 0 || len(log.Groups) != 1 || log.Groups[0].Decision != "" {
		t.Fatalf("decision must be empty, got %+v", log.Groups)
	}

	entries, _, err = m.Apply([]Change{del("a"), del("b")})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("invisible group deletion must emit nothing, got %+v", entries)
	}
}

func TestIllegalInputs(t *testing.T) {
	cases := []struct {
		name    string
		changes []Change
		target  error
	}{
		{"empty group on insert", []Change{ins("a", "", 1)}, ErrEmptyGroup},
		{"empty row id on insert", []Change{ins("", "g1", 1)}, ErrEmptyRowID},
		{"empty row id on delete", []Change{del("")}, ErrEmptyRowID},
		{"delete missing row", []Change{del("ghost")}, ErrDeleteMissing},
		{"duplicate insert", []Change{ins("a", "g2", 2)}, ErrDuplicateInsert},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mem := &memoryLogger{}
			m, err := NewMaintainer(Config{MinCount: 1, MinSum: 0}, mem)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "duplicate insert" {
				if _, _, err := m.Apply([]Change{ins("a", "g1", 1)}); err != nil {
					t.Fatal(err)
				}
			}
			logsBefore := len(mem.logs)

			entries, log, err := m.Apply(tc.changes)
			rejectErrIs(t, err, tc.target)
			if entries != nil || log != nil {
				t.Fatalf("rejected batch must return nil outputs, got %+v %+v", entries, log)
			}
			if len(mem.logs) != logsBefore {
				t.Fatalf("rejected batch must not add log, before=%d after=%d", logsBefore, len(mem.logs))
			}

			// 拒绝后状态仍可用且未被污染。
			ok, log2, err2 := m.Apply([]Change{ins("z", "post-reject", 1)})
			if err2 != nil {
				t.Fatalf("valid insert after rejection failed: %v", err2)
			}
			if len(ok) != 1 || len(log2.Groups) != 1 || log2.Groups[0].Group != "post-reject" {
				t.Fatalf("state corrupted by rejected batch: %+v", ok)
			}
		})
	}
}

// 批内先删后插同一 ID 是合法的更新语义，删除按 ID 定位不要求组名。
func TestDeleteThenInsertSameIDWithinBatch(t *testing.T) {
	m, _ := NewMaintainer(Config{MinCount: 1, MinSum: 0}, nil)
	if _, _, err := m.Apply([]Change{ins("a", "g1", 5)}); err != nil {
		t.Fatal(err)
	}
	entries, _, err := m.Apply([]Change{del("a"), ins("a", "g2", 9)})
	if err != nil {
		t.Fatalf("delete+reinsert same id should be allowed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %+v", entries)
	}
	if entries[0].Kind != EntryRetract || entries[0].Agg.Group != "g1" {
		t.Fatalf("first entry should retract g1, got %+v", entries[0])
	}
	if entries[1].Kind != EntryUpsert || entries[1].Agg != (Aggregate{Group: "g2", Count: 1, Sum: 9}) {
		t.Fatalf("second entry should upsert g2, got %+v", entries[1])
	}
}

func del(id string) Change {
	return Change{Deleted: true, Row: Row{ID: id}}
}

func rejectErrIs(t *testing.T, err error, target error) *RejectError {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want *RejectError, got %v", err)
	}
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
	return re
}

// 组在恰好等于阈值（行数与求和同时等于）时进入视图。
func TestThresholdBoundaryInclusive(t *testing.T) {
	m, err := NewMaintainer(Config{MinCount: 2, MinSum: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}

	entries, _, err := m.Apply([]Change{ins("a", "g1", 5), ins("b", "g1", 5)})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Kind != EntryUpsert {
		t.Fatalf("want single upsert on enter, got %+v", entries)
	}
	if got := entries[0].Agg; got != (Aggregate{Group: "g1", Count: 2, Sum: 10}) {
		t.Fatalf("unexpected aggregate %+v", got)
	}
	if got := m.Snapshot(); len(got) != 1 || got[0] != entries[0].Agg {
		t.Fatalf("snapshot mismatch: %+v", got)
	}

	// 只满足行数、不满足求和：不进入视图。
	if _, _, err := m.Apply([]Change{ins("c", "g2", 1), ins("d", "g2", 1)}); err != nil {
		t.Fatal(err)
	}
	if got := m.Snapshot(); len(got) != 1 || got[0].Group != "g1" {
		t.Fatalf("g2 must not qualify, snapshot=%+v", got)
	}
}

// 行数降为零时组消失，输出仅撤回旧值。
func TestCountToZeroGroupDisappears(t *testing.T) {
	m, err := NewMaintainer(Config{MinCount: 1, MinSum: 0}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Apply([]Change{ins("a", "g1", 7)}); err != nil {
		t.Fatal(err)
	}

	entries, _, err := m.Apply([]Change{del("a")})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 retract entry, got %+v", entries)
	}
	if entries[0].Kind != EntryRetract || entries[0].Agg != (Aggregate{Group: "g1", Count: 1, Sum: 7}) {
		t.Fatalf("want retract of old value, got %+v", entries[0])
	}
	if got := m.Snapshot(); len(got) != 0 {
		t.Fatalf("group must disappear, got %+v", got)
	}
}

// 求和跌破阈值（行数仍非零）时组同样离开视图。
func TestSumDropLeavesView(t *testing.T) {
	m, err := NewMaintainer(Config{MinCount: 1, MinSum: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Apply([]Change{ins("a", "g1", 20)}); err != nil {
		t.Fatal(err)
	}
	entries, _, err := m.Apply([]Change{del("a"), ins("b", "g1", 1)})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Kind != EntryRetract {
		t.Fatalf("want leave -> single retract, got %+v", entries)
	}
	if got := m.Snapshot(); len(got) != 0 {
		t.Fatalf("snapshot must be empty, got %+v", got)
	}
}

// 组前后都在视图中：先撤回旧值再写入新值。
func TestChangeRetractThenUpsertOrder(t *testing.T) {
	m, err := NewMaintainer(Config{MinCount: 1, MinSum: 0}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Apply([]Change{ins("a", "g1", 1)}); err != nil {
		t.Fatal(err)
	}
	entries, _, err := m.Apply([]Change{ins("b", "g1", 2)})
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Kind: EntryRetract, Agg: Aggregate{Group: "g1", Count: 1, Sum: 1}},
		{Kind: EntryUpsert, Agg: Aggregate{Group: "g1", Count: 2, Sum: 3}},
	}
	if fmt.Sprint(entries) != fmt.Sprint(want) {
		t.Fatalf("entries=%+v, want %+v", entries, want)
	}
}
