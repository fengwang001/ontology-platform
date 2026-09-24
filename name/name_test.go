package name

import (
	"errors"
	"testing"
	"time"
)

func TestValidAndEqual(t *testing.T) {
	validCases := []struct {
		name string
		in   string
	}{
		{"empty", ""}, {"slash", "a/b"}, {"backslash", `a\b`},
		{"nul", "a\x00b"}, {"unicode", "名字"},
	}
	for _, tc := range validCases {
		t.Run(tc.name, func(t *testing.T) {
			if !Valid(tc.in) {
				t.Fatalf("Valid(%q) = false", tc.in)
			}
		})
	}
	equalCases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"both empty", nil, nil, true},
		{"same order", []string{"a", "b"}, []string{"a", "b"}, true},
		{"diff order", []string{"b", "a", ""}, []string{"", "a", "b"}, true},
		{"len differ", []string{"a"}, []string{"a", "b"}, false},
		{"elem differ", []string{"a", "c"}, []string{"a", "b"}, false},
	}
	for _, tc := range equalCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Equal(tc.a, tc.b); got != tc.want {
				t.Fatalf("Equal=%v want %v", got, tc.want)
			}
		})
	}
}

func TestRenameLocked(t *testing.T) {
	cases := []struct {
		name        string
		init        []string
		from, to    string
		force       bool
		wantErr     error
		wantMembers  []string
	}{
		{"ok", []string{"a"}, "a", "b", false, nil, []string{"b"}},
		{"missing", []string{"a"}, "x", "b", false, ErrMissing, []string{"a"}},
		{"exists", []string{"a", "b"}, "a", "b", false, ErrExists, []string{"a", "b"}},
		{"force overwrite", []string{"a", "b"}, "a", "b", true, nil, []string{"b"}},
		{"empty names", []string{""}, "", "x", false, nil, []string{"x"}},
		{"slash target", []string{"a"}, "a", "d/e", false, nil, []string{"d/e"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := New(tc.init)
			n.Lock()
			err := n.RenameLocked(tc.from, tc.to, tc.force)
			got := n.SnapshotLocked()
			n.Unlock()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if !Equal(got, tc.wantMembers) {
				t.Fatalf("members=%v want %v", got, tc.wantMembers)
			}
		})
	}
}

func TestLockBlocksConcurrentWrite(t *testing.T) {
	n := New([]string{"a"})
	n.Lock()
	done := make(chan struct{})
	go func() {
		n.Lock()
		n.RenameLocked("a", "b", false)
		n.Unlock()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("concurrent write proceeded while write lock held")
	case <-time.After(20 * time.Millisecond):
	}
	n.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent write still blocked after unlock")
	}
}
