package history

import "testing"

func TestAppendAndSequence(t *testing.T) {
	var l Log
	if l.Len() != 0 {
		t.Fatalf("empty len=%d", l.Len())
	}
	u := l.AppendUpdate([]byte("u1"), 5)
	if u.Index != 1 || u.Type != TypeUpdate || u.Delta != 5 || string(u.UID) != "u1" {
		t.Fatalf("bad U: %+v", u)
	}
	a := l.AppendApplied(1)
	if a.Index != 2 || a.Seq != 1 || a.Type != TypeApplied {
		t.Fatalf("bad A: %+v", a)
	}
	first, c := l.AppendClosed()
	if !first || c.Index != 3 || c.Type != TypeClosed {
		t.Fatalf("bad first C: %+v first=%v", c, first)
	}
	again, _ := l.AppendClosed()
	if again || l.Len() != 3 {
		t.Fatalf("C must be idempotent: again=%v len=%d", again, l.Len())
	}
	evs := l.Events()
	if len(evs) != 3 {
		t.Fatalf("events=%d", len(evs))
	}
	// 拷贝隔离：修改外部切片不影响日志。
	evs[0].Delta = 999
	if l.Events()[0].Delta != 5 {
		t.Fatalf("Events() must return a copy")
	}
	t.Logf("IN U/A/C -> OUT 序号 1,2,3 连续；重复 C 幂等，历史不变")
}

func TestStoreIsolation(t *testing.T) {
	s := NewStore()
	if s.Get([]byte("x")) != nil {
		t.Fatalf("missing log should be nil")
	}
	if !s.Create([]byte("x")) {
		t.Fatalf("first create must succeed")
	}
	if s.Create([]byte("x")) {
		t.Fatalf("duplicate create must fail")
	}
	a, b := s.Get([]byte("x")), s.Get([]byte("y"))
	if a == nil || b != nil {
		t.Fatalf("isolation lookup wrong")
	}
	s.Create([]byte("y"))
	s.Get([]byte("y")).AppendUpdate([]byte("yy"), 1)
	if a.Len() != 0 || s.Get([]byte("y")).Len() != 1 {
		t.Fatalf("instance histories must stay isolated")
	}
}
