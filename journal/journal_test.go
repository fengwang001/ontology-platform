package journal

import (
	"errors"
	"testing"
)

func TestJournalAppendAndRecords(t *testing.T) {
	j := New(&MemSink{})
	if err := j.Append([]byte("r1")); err != nil {
		t.Fatal(err)
	}
	if err := j.Append([]byte("r2")); err != nil {
		t.Fatal(err)
	}
	recs := j.Records()
	if len(recs) != 2 || string(recs[0]) != "r1" || string(recs[1]) != "r2" {
		t.Fatalf("Records = %q, 期望 [r1 r2]", recs)
	}
}

func TestJournalFailureLeavesNoTrace(t *testing.T) {
	sink := &FlakySink{FailAt: map[int]bool{1: true}}
	j := New(sink)
	if err := j.Append([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	if err := j.Append([]byte("bad")); !errors.Is(err, ErrSink) {
		t.Fatalf("注入失败应报 ErrSink, 得到 %v", err)
	}
	if err := j.Append([]byte("ok2")); err != nil {
		t.Fatal(err)
	}
	recs := j.Records()
	if len(recs) != 2 || string(recs[0]) != "ok" || string(recs[1]) != "ok2" {
		t.Fatalf("失败的追加不应留痕, Records = %q", recs)
	}
	if sink.Calls() != 3 {
		t.Fatalf("Calls = %d, 期望 3", sink.Calls())
	}
}

func TestFlakySinkOnlyFailsConfigured(t *testing.T) {
	sink := &FlakySink{FailAt: map[int]bool{0: true, 2: true}}
	for i := 0; i < 4; i++ {
		err := sink.Append([]byte{byte(i)})
		if (i == 0 || i == 2) && err == nil {
			t.Fatalf("第 %d 次应失败", i)
		}
		if (i == 1 || i == 3) && err != nil {
			t.Fatalf("第 %d 次应成功, 得到 %v", i, err)
		}
	}
}
