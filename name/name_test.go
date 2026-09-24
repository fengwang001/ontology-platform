package name

import (
	"errors"
	"testing"
)

func TestNamespace(t *testing.T) {
	cases := []struct {
		name    string
		init    []string
		old     string
		new     string
		wantErr error
		want    []string // 操作后期望集合（排序）；nil 表示不检查
	}{
		{"simple", []string{"a"}, "a", "b", nil, []string{"b"}},
		{"empty name valid", []string{""}, "", "x", nil, []string{"x"}},
		{"slash ordinary", []string{"a/b", "c"}, "a/b", "d/e", nil, []string{"c", "d/e"}},
		{"self no-op", []string{"a", "b"}, "a", "a", nil, []string{"a", "b"}},
		{"target exists", []string{"a", "b"}, "a", "b", ErrExists, []string{"a", "b"}},
		{"source missing", []string{"a"}, "x", "y", ErrMissing, []string{"a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := New(tc.init...)
			err := ns.RenameLocked(tc.old, tc.new)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.want != nil && !EqualSet(ns.Snapshot(), tc.want) {
				t.Fatalf("set = %v, want %v", ns.Snapshot(), tc.want)
			}
		})
	}
}

func TestValidAndSetEquality(t *testing.T) {
	validCases := []string{"", "a", "a/b/c", "带空格 与中文", "\x00", ".."}
	for _, n := range validCases {
		if !Valid(n) {
			t.Fatalf("Valid(%q) = false", n)
		}
	}
	eqCases := []struct {
		a, b []string
		want bool
	}{
		{[]string{"a", "b"}, []string{"b", "a"}, true},
		{nil, []string{}, true},
		{[]string{"a"}, []string{"b"}, false},
		{[]string{"a", "b"}, []string{"a"}, false},
	}
	for _, tc := range eqCases {
		if got := EqualSet(tc.a, tc.b); got != tc.want {
			t.Fatalf("EqualSet(%v,%v)=%v want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestLockBlocksConcurrentRename(t *testing.T) {
	ns := New("a")
	ns.Lock()
	acquired := make(chan struct{})
	go func() {
		ns.RenameLocked("a", "b") // 应阻塞，直到主测试释放
		close(acquired)
	}()
	select {
	case <-acquired:
		t.Fatal("concurrent rename proceeded while lock held")
	default:
	}
	if ns.TryLock() {
		ns.Unlock()
		t.Fatal("TryLock succeeded while lock held")
	}
	ns.Unlock()
	<-acquired
	if !EqualSet(ns.Snapshot(), []string{"b"}) {
		t.Fatalf("set = %v, want [b]", ns.Snapshot())
	}
}
