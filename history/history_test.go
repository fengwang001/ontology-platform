package history

import (
	"errors"
	"testing"
)

func b(s string) []byte { return []byte(s) }

func steps(n int) []Event {
	evs := make([]Event, n)
	for i := range evs {
		evs[i] = S(b("s"))
	}
	return evs
}

func TestAppendRejectOrder(t *testing.T) {
	s := NewStore()
	wf := b("wf1")
	if err := s.Append(wf, 0, []Event{S(b("a")), M(b("p"))}); err != nil {
		t.Fatalf("seed append: %v", err)
	}
	cases := []struct {
		name   string
		wf     []byte
		expect int
		events []Event
		want   error
	}{
		{"empty wf", nil, 0, nil, ErrInvalid},
		{"empty step name", wf, 2, []Event{S(nil)}, ErrInvalid},
		{"empty pid", wf, 2, []Event{M(b(""))}, ErrInvalid},
		{"conflict", wf, 1, []Event{S(b("x"))}, ErrConflict},
		{"invalid beats conflict", wf, 99, []Event{S(nil)}, ErrInvalid},
		{"conflict beats capacity", wf, 0, steps(MaxEvents), ErrConflict},
		{"capacity", wf, 2, steps(MaxEvents), ErrCapacity},
		{"empty batch ok", wf, 2, nil, nil},
	}
	for _, tc := range cases {
		err := s.Append(tc.wf, tc.expect, tc.events)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v, want errors.Is %v", tc.name, err, tc.want)
		}
		t.Logf("%s: 输入 expect=%d 事件数=%d → 判定 %v（依据：拒绝次序 非法>冲突>容量）",
			tc.name, tc.expect, len(tc.events), err)
	}
	if got := s.Len(wf); got != 2 {
		t.Fatalf("被拒绝的批次写入了历史: len=%d, want 2", got)
	}
	// 容量边界：正好到上限可写，再写一条拒绝。
	s2 := NewStore()
	if err := s2.Append(wf, 0, steps(MaxEvents)); err != nil {
		t.Fatalf("fill to capacity: %v", err)
	}
	if err := s2.Append(wf, MaxEvents, []Event{S(b("x"))}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("overflow: got %v, want ErrCapacity", err)
	}
}

func TestSnapshotIsCopy(t *testing.T) {
	s := NewStore()
	wf := b("wf")
	if err := s.Append(wf, 0, []Event{S(b("a")), M(b("p"))}); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot(wf)
	snap[0].Data[0] = 'X'
	snap[1].Kind = Step
	again := s.Snapshot(wf)
	if !again[0].Equal(S(b("a"))) || !again[1].Equal(M(b("p"))) {
		t.Fatalf("修改拷贝影响了日志: %v", again)
	}
	t.Logf("输入: 修改 Snapshot 返回值 → 日志仍为 %v（依据：深拷贝隔离）", again)
}

func TestAppendStoresCopy(t *testing.T) {
	s := NewStore()
	wf := b("wf")
	name := b("a")
	if err := s.Append(wf, 0, []Event{S(name)}); err != nil {
		t.Fatal(err)
	}
	name[0] = 'Z'
	if got := s.Snapshot(wf); !got[0].Equal(S(b("a"))) {
		t.Fatalf("Append 未拷贝输入: %v", got)
	}
}
