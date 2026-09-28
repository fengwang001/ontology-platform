package ontology

import (
	"reflect"
	"sync"
	"testing"
)

// replay 按日志顺序逐条应用去重计数净变化，模拟下游重放。
func replay(entries []LogEntry) map[string]int {
	view := make(map[string]int)
	for _, e := range entries {
		if !e.Accepted {
			continue
		}
		for _, d := range e.Changes {
			view[d.Group] += d.Delta
			if view[d.Group] == 0 {
				delete(view, d.Group)
			}
		}
	}
	return view
}

func TestRejectedBatchPreservesStateAndPriorLog(t *testing.T) {
	svc := NewDedupService(100)
	ok := []Change{{Group: "g", Value: "a", Kind: KindInsert}}
	res := svc.Apply(ok)
	if !res.Accepted {
		t.Fatalf("setup batch should accept: %+v", res)
	}
	before := svc.Journal().Entries()

	bad := []Change{{Group: "g", Value: "missing", Kind: KindRetract}}
	res = svc.Apply(bad)
	t.Logf("rejected input=%s\n%s", FormatChanges(bad), FormatResult(res))
	if res.Accepted || res.Reason != ReasonRetractZero {
		t.Fatalf("batch should be rejected: %+v", res)
	}

	// 多重性与视图不变。
	if svc.Multiplicity("g", "a") != 1 || svc.View()["g"] != 1 {
		t.Fatalf("state changed after rejection: mult=%d view=%v", svc.Multiplicity("g", "a"), svc.View())
	}
	// 已产生的日志条目逐字段不变。
	after := svc.Journal().Entries()
	if len(after) != 2 {
		t.Fatalf("rejection should append exactly one entry, got %d", len(after))
	}
	if !reflect.DeepEqual(after[0], before[0]) {
		t.Fatalf("prior log entry mutated:\nbefore=%+v\nafter =%+v", before[0], after[0])
	}
	if after[1].Accepted || after[1].Reason != ReasonRetractZero {
		t.Fatalf("rejection entry malformed: %+v", after[1])
	}
}

func TestDeterministicOutputForSameSequence(t *testing.T) {
	seq := [][]Change{
		{{Group: "g", Value: "a", Kind: KindInsert},
			{Group: "g", Value: "b", Kind: KindInsert}},
		{{Group: "g", Value: "a", Kind: KindRetract}},
		{{Group: "h", Value: "x", Kind: KindInsert},
			{Group: "h", Value: "x", Kind: KindRetract}},
		{{Group: "g", Value: "nope", Kind: KindRetract}}, // 拒绝
	}

	run := func() []LogEntry {
		svc := NewDedupService(100)
		var results []ApplyResult
		for _, batch := range seq {
			results = append(results, svc.Apply(batch))
		}
		_ = results
		return svc.Journal().Entries()
	}

	first := run()
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same input sequence produced different outputs:\n%+v\n%+v", first, second)
	}
}

func TestLogReplayYieldsCorrectCounts(t *testing.T) {
	svc := NewDedupService(100)
	batches := [][]Change{
		{{Group: "g", Value: "a", Kind: KindInsert},
			{Group: "g", Value: "a", Kind: KindInsert},
			{Group: "g", Value: "b", Kind: KindInsert}},
		{{Group: "g", Value: "a", Kind: KindRetract}},
		{{Group: "h", Value: "x", Kind: KindInsert},
			{Group: "h", Value: "x", Kind: KindRetract}},
		{{Group: "h", Value: "y", Kind: KindInsert}},
		// 撤回 a（多重性 1 -> 0）与 b（1 -> 0），g 清空。
		{{Group: "g", Value: "a", Kind: KindRetract},
			{Group: "g", Value: "b", Kind: KindRetract}},
		{{Group: "h", Value: "z", Kind: ChangeKind(7)}}, // 拒绝
	}
	for _, b := range batches {
		res := svc.Apply(b)
		t.Logf("input=%s\n%s", FormatChanges(b), FormatResult(res))
	}

	entries := svc.Journal().Entries()
	got := replay(entries)
	want := svc.View()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("replay=%v, live view=%v", got, want)
	}
	if want := map[string]int{"h": 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("replay=%v, want=%v", got, want)
	}
}

func TestConcurrentApplyAndView(t *testing.T) {
	svc := NewDedupService(1000)
	const workers = 16
	const iterations = 200

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				v := "v"
				switch i % 4 {
				case 0:
					svc.Apply([]Change{{Group: "g", Value: v, Kind: KindInsert}})
				case 1:
					svc.View()
				case 2:
					// 仅在存在时撤回：先查多重性再决定，失败也无害（整批拒绝）。
					svc.Apply([]Change{{Group: "g", Value: v, Kind: KindRetract}})
				case 3:
					svc.Multiplicity("g", v)
				}
			}
		}(w)
	}
	wg.Wait()

	// 收敛性校验：最终视图必须与按多重性重算的去重计数一致。
	entries := svc.Journal().Entries()
	if !reflect.DeepEqual(replay(entries), svc.View()) {
		t.Fatalf("journal replay diverges from live view: %v vs %v", replay(entries), svc.View())
	}
}
