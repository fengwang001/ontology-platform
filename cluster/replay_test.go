package cluster

import (
	"reflect"
	"testing"
)

func TestReplayProducesSameResult(t *testing.T) {
	messages := []string{
		"open file a.txt ok",
		"open file b.txt ok",
		"open dir c.txt fail",
		"open 7 files ok",
		"close file a.txt ok",
		"close another file now",
	}
	run := func() ([]Result, []TemplateInfo, int64) {
		m, _ := New(50, 1, 1)
		results := make([]Result, 0, len(messages))
		for _, msg := range messages {
			result, _ := m.Ingest("tenant", msg)
			results = append(results, result)
		}
		infos, overflow, _ := m.Templates("tenant")
		return results, infos, overflow
	}
	firstResults, firstInfos, firstOverflow := run()
	secondResults, secondInfos, secondOverflow := run()
	t.Logf("input=%v output=%v templates=%+v overflow=%d decision=replay same sequence", messages, secondResults, secondInfos, secondOverflow)
	if !reflect.DeepEqual(firstResults, secondResults) || !reflect.DeepEqual(firstInfos, secondInfos) || firstOverflow != secondOverflow {
		t.Fatalf("replay differs: %+v/%+v/%d vs %+v/%+v/%d", firstResults, firstInfos, firstOverflow, secondResults, secondInfos, secondOverflow)
	}
	if firstOverflow != 2 {
		t.Fatalf("overflow=%d, want 2", firstOverflow)
	}
}
