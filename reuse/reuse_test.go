package reuse

import (
	"reflect"
	"testing"
)

func TestPopDueOrder(t *testing.T) {
	s := NewScheduler()
	s.Upsert(Key{2, 1}, 50)
	s.Upsert(Key{1, 2}, 50)
	s.Upsert(Key{1, 1}, 50)
	s.Upsert(Key{3, 1}, 40)
	s.Upsert(Key{1, 1}, 100) // 更新已有键

	if s.Len() != 4 {
		t.Fatalf("Len=%d, want 4 (upsert 应去重)", s.Len())
	}
	got := s.PopDue(50)
	want := []Item{
		{Key{3, 1}, 40},
		{Key{1, 2}, 50},
		{Key{2, 1}, 50},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PopDue(50)=%v, want %v", got, want)
	}
	// 键 {1,1} 被更新到 100，恰等 now 弹出。
	got = s.PopDue(100)
	want = []Item{{Key{1, 1}, 100}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PopDue(100)=%v, want %v", got, want)
	}
	if s.Len() != 0 {
		t.Fatalf("Len=%d, want 0", s.Len())
	}
	if got := s.PopDue(1000); got != nil {
		t.Fatalf("PopDue on empty=%v, want nil", got)
	}
}

func TestPopDueBoundary(t *testing.T) {
	s := NewScheduler()
	s.Upsert(Key{1, 1}, 10)
	s.Upsert(Key{1, 2}, 11)
	if got := s.PopDue(9); got != nil {
		t.Fatalf("PopDue(9)=%v, want nil", got)
	}
	got := s.PopDue(10) // 恰等即解除
	want := []Item{{Key{1, 1}, 10}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PopDue(10)=%v, want %v", got, want)
	}
	if s.Len() != 1 {
		t.Fatalf("Len=%d, want 1", s.Len())
	}
}
