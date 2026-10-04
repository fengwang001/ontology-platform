package party

import (
	"errors"
	"testing"
)

func TestForm(t *testing.T) {
	b := NewBook()
	tests := []struct {
		name    string
		pid     string
		members []string
		wantErr error
	}{
		{"empty pid", "", []string{"a"}, ErrInvalidParam},
		{"no members", "p0", nil, ErrInvalidParam},
		{"17 members", "p0", []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q"}, ErrInvalidParam},
		{"empty name", "p0", []string{"a", ""}, ErrInvalidParam},
		{"dup member", "p0", []string{"a", "a"}, ErrInvalidParam},
		{"ok p1", "p1", []string{"a", "b"}, nil},
		{"pid exists", "p1", []string{"c"}, ErrPartyExists},
		{"member busy", "p2", []string{"b"}, ErrMemberBusy},
		{"ok p3", "p3", []string{"c"}, nil},
		{"same member reused by different pid", "p4", []string{"c"}, ErrMemberBusy},
	}
	for _, tc := range tests {
		_, err := b.Form(tc.pid, tc.members)
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("%s: err=%v want %v", tc.name, err, tc.wantErr)
		}
	}
	p, ok := b.Get("p1")
	if !ok || len(p.Members) != 2 || p.Members[0] != "a" || p.Members[1] != "b" {
		t.Fatalf("get p1 = %+v %v", p, ok)
	}
	p.Members[0] = "Z"
	if p2, _ := b.Get("p1"); p2.Members[0] != "a" {
		t.Fatalf("Get must return a copy")
	}
	if _, ok := b.Get("nope"); ok {
		t.Fatalf("missing party should report false")
	}
}
