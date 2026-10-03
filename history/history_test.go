package history

import (
	"errors"
	"testing"
)

func TestSeqContiguous(t *testing.T) {
	l := New()
	seq, err := l.AppendUpdate("u1", 6)
	t.Logf("AppendUpdate(u1,+6) -> seq=%d err=%v (expect 1, first event)", seq, err)
	if err != nil || seq != 1 {
		t.Fatalf("got seq=%d err=%v, want 1,nil", seq, err)
	}
	seq, _ = l.AppendApplied(1)
	t.Logf("AppendApplied(1) -> seq=%d (expect 2)", seq)
	if seq != 2 {
		t.Fatalf("got seq=%d, want 2", seq)
	}
	seq, _ = l.AppendUpdate("u2", -4)
	if seq != 3 {
		t.Fatalf("got seq=%d, want 3", seq)
	}
	if l.Len() != 3 {
		t.Fatalf("Len=%d, want 3", l.Len())
	}
}

func TestCloseSealsLog(t *testing.T) {
	l := New()
	if _, err := l.AppendClosed(); err != nil {
		t.Fatalf("AppendClosed: %v", err)
	}
	if !l.IsClosed() {
		t.Fatal("IsClosed=false after C")
	}
	if _, err := l.AppendClosed(); !errors.Is(err, ErrSealed) {
		t.Fatalf("second C: err=%v, want ErrSealed", err)
	}
	if _, err := l.AppendUpdate("u", 1); !errors.Is(err, ErrSealed) {
		t.Fatalf("U after C: err=%v, want ErrSealed", err)
	}
	if _, err := l.AppendApplied(1); !errors.Is(err, ErrSealed) {
		t.Fatalf("A after C: err=%v, want ErrSealed", err)
	}
	t.Log("C 之后所有追加均被 ErrSealed 拒绝，C 至多一条且为末条")
}

func TestEventsPrefixAndCopy(t *testing.T) {
	l := New()
	l.AppendUpdate("u1", 1)
	l.AppendUpdate("u2", 2)
	l.AppendApplied(1)
	ev := l.Events(2)
	if len(ev) != 2 || ev[0].UID != "u1" || ev[1].UID != "u2" {
		t.Fatalf("prefix events wrong: %+v", ev)
	}
	ev[0].UID = "mutated"
	if l.Events(1)[0].UID != "u1" {
		t.Fatal("Events must return a copy")
	}
	t.Logf("Events(2) 返回前缀副本，外部修改不影响日志: %+v", l.Events(3))
}
