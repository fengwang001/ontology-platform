package ontology

import (
	"encoding/json"
	"sync"
	"testing"
)

// buildDeterminismPair 构造一对包含多种差异的快照。
func buildDeterminismPair() (*Snapshot, *Snapshot) {
	b0 := personBuilder()
	for i := int64(1); i <= 20; i++ {
		b0.PutObject("person", IntValue(i), map[string]Value{
			"p-name": StringValue("n" + string(rune('a'+i%26))),
			"p-age":  IntValue(20 + i),
		})
	}
	s0 := b0.Build()

	b1 := FromSnapshot(s0)
	b1.Advance(2)
	b1.RenameProperty("person", "p-name", "fullName")
	b1.PutObject("person", IntValue(1), map[string]Value{
		"p-name": StringValue("changed"), "p-age": IntValue(21),
	})
	b1.PutObject("person", IntValue(100), map[string]Value{
		"p-name": StringValue("new"), "p-age": IntValue(1),
	})
	b1.DeleteObject("person", IntValue(2))
	s1 := b1.Build()
	return s0, s1
}

func canonicalJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

// 反复比对同一对快照，结果不随执行次数漂移。
func TestRepeatedCompareIsStable(t *testing.T) {
	s0, s1 := buildDeterminismPair()
	first := canonicalJSON(t, mustCompare(t, s0, s1))
	for i := 0; i < 50; i++ {
		got := canonicalJSON(t, mustCompare(t, s0, s1))
		if got != first {
			t.Fatalf("iteration %d: result drifted\nfirst: %s\ngot:   %s", i, first, got)
		}
	}
}

// 多个调用方并发比对同一对快照，所有结果必须完全一致。
func TestConcurrentCompareIdentical(t *testing.T) {
	s0, s1 := buildDeterminismPair()
	want := canonicalJSON(t, mustCompare(t, s0, s1))

	const workers = 32
	results := make([]string, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := Compare(s0, s1)
			if err != nil {
				t.Errorf("Compare failed: %v", err)
				return
			}
			data, err := json.Marshal(res)
			if err != nil {
				t.Errorf("marshal: %v", err)
				return
			}
			results[idx] = string(data)
		}(w)
	}
	wg.Wait()
	for i, got := range results {
		if got != want {
			t.Fatalf("worker %d got different result", i)
		}
	}
}

// 比对过程不得修改输入快照。
func TestCompareDoesNotMutateInputs(t *testing.T) {
	s0, s1 := buildDeterminismPair()
	fp0, fp1 := Fingerprint(s0), Fingerprint(s1)
	for i := 0; i < 10; i++ {
		mustCompare(t, s0, s1)
	}
	if Fingerprint(s0) != fp0 || Fingerprint(s1) != fp1 {
		t.Fatalf("compare mutated input snapshots")
	}
}
