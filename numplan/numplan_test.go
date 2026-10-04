package numplan

import (
	"errors"
	"testing"
)

func TestAssignAndOwner(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		length int
		op     int64
		now    int64
		err    error
	}{
		{"ok", "1380", 11, 1, 0, nil},
		{"nested later", "13805", 11, 2, 10, nil},
		{"dup", "1380", 11, 3, 20, ErrBlockExists},
		{"bad prefix long", "123456789012", 11, 1, 0, ErrInvalidArgument},
		{"bad length", "12", 4, 1, 0, ErrInvalidArgument},
		{"bad op", "139", 11, 0, 0, ErrInvalidArgument},
		{"bad now", "137", 11, 1, -1, ErrInvalidArgument},
	}
	p := New()
	for _, c := range cases {
		err := p.AssignBlock(c.prefix, c.length, c.op, c.now)
		if !errors.Is(err, c.err) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.err)
		}
	}
	const n = "13805001234"
	if op, ok := p.OwnerAt(n, 9); !ok || op != 1 {
		t.Fatalf("t=9 owner = %d,%v want 1,true", op, ok)
	}
	if op, ok := p.OwnerAt(n, 10); !ok || op != 2 {
		t.Fatalf("t=10 owner = %d,%v want 2,true", op, ok)
	}
	if _, ok := p.OwnerAt("13700000000", 100); ok {
		t.Fatal("unrelated prefix must be unallocated")
	}
	if _, ok := p.OwnerAt("1234", 100); ok {
		t.Fatal("4-digit number must be invalid")
	}
	if err := p.AssignBlock("136", 11, 1, 5); !errors.Is(err, ErrClockRewound) {
		t.Fatalf("clock rewind = %v, want ErrClockRewound", err)
	}
}

func TestBlockProbeBound(t *testing.T) {
	p := New()
	// Many blocks, several sharing the queried length.
	prefixes := []string{"1", "13", "138", "1380", "13805", "138050"}
	for i, px := range prefixes {
		if err := p.AssignBlock(px, 11, int64(i+1), int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	p.Probes()
	n := "13805001234"
	if op, ok := p.OwnerAt(n, 100); !ok || op != 6 {
		t.Fatalf("owner = %d,%v", op, ok)
	}
	if got := p.Probes(); got > int64(len(n)) {
		t.Fatalf("block probes = %d, want <= digits %d", got, len(n))
	} else {
		t.Logf("input=%s blocks=%d probes=%d basis=own-prefix-only", n, len(prefixes), got)
	}
}
