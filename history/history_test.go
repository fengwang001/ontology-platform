package history

import (
	"errors"
	"testing"
)

func TestAppendAndSnapshot(t *testing.T) {
	s := &Store{}
	wf := []byte("wf-1")

	if err := s.Append(wf, 0, []Event{Step([]byte("a")), Marker([]byte("p"))}); err != nil {
		t.Fatalf("append batch1: %v", err)
	}
	if err := s.Append(wf, 2, []Event{Step([]byte("b"))}); err != nil {
		t.Fatalf("append batch2: %v", err)
	}

	got := s.Snapshot(wf)
	want := []Event{Step([]byte("a")), Marker([]byte("p")), Step([]byte("b"))}
	if len(got) != len(want) {
		t.Fatalf("snapshot len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !eventsEqual(got[i], want[i]) {
			t.Fatalf("snapshot[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// 修改拷贝不得影响日志。
	got[0].Name[0] = 'Z'
	again := s.Snapshot(wf)
	if string(again[0].Name) != "a" {
		t.Fatalf("snapshot must be a defensive copy, got %q", again[0].Name)
	}

	// 不同工作流隔离。
	if len(s.Snapshot([]byte("other"))) != 0 {
		t.Fatalf("unknown workflow snapshot must be empty")
	}
}

func TestAppendRejectionOrder(t *testing.T) {
	s := &Store{}
	wf := []byte("wf")

	// 1. 参数非法优先于 expect 冲突。
	err := s.Append(nil, 99, []Event{Step([]byte("a"))})
	if !errors.Is(err, ErrArgument) {
		t.Fatalf("empty wf: want ErrArgument, got %v", err)
	}
	err = s.Append(wf, 99, []Event{Step(nil)})
	if !errors.Is(err, ErrArgument) {
		t.Fatalf("empty name: want ErrArgument, got %v", err)
	}
	err = s.Append(wf, 99, []Event{Marker(nil)})
	if !errors.Is(err, ErrArgument) {
		t.Fatalf("empty pid: want ErrArgument, got %v", err)
	}
	err = s.Append(wf, 99, []Event{{Kind: 9}})
	if !errors.Is(err, ErrArgument) {
		t.Fatalf("bad kind: want ErrArgument, got %v", err)
	}

	// 空批次是成功的无操作：wf 为空也不报错，且不创建日志。
	if err := s.Append(nil, 0, nil); err != nil {
		t.Fatalf("empty batch must be no-op success, got %v", err)
	}

	// 2. expect 冲突优先于容量。
	err = s.Append(wf, 1, []Event{Step([]byte("a"))})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expect mismatch: want ErrConflict, got %v", err)
	}

	// 3. 容量：在 expect 正确时才检查。
	big := make([]Event, MaxEventsPerWorkflow+1)
	for i := range big {
		big[i] = Step([]byte("x"))
	}
	err = s.Append(wf, 0, big)
	if !errors.Is(err, ErrCapacity) {
		t.Fatalf("over capacity: want ErrCapacity, got %v", err)
	}

	// 任一拒绝都整批不写。
	if len(s.Snapshot(wf)) != 0 {
		t.Fatalf("rejected appends must not write anything")
	}

	// 恰好等于上限成功；再追加一条失败。
	full := make([]Event, MaxEventsPerWorkflow)
	for i := range full {
		full[i] = Step([]byte("x"))
	}
	if err := s.Append(wf, 0, full); err != nil {
		t.Fatalf("fill to capacity: %v", err)
	}
	if err := s.Append(wf, MaxEventsPerWorkflow, []Event{Step([]byte("y"))}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("one beyond capacity: want ErrCapacity, got %v", err)
	}
}

func eventsEqual(a, b Event) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.Kind == KindStep {
		return string(a.Name) == string(b.Name)
	}
	return string(a.Pid) == string(b.Pid)
}
