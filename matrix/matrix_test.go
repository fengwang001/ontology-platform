package matrix

import (
	"errors"
	"strings"
	"testing"
)

func TestSetAndGet(t *testing.T) {
	m := New()
	cases := []struct {
		a, b string
		min  int
		want int
	}{
		{"A", "B", 20, 20},
		{"B", "A", 15, 15}, // 不对称
		{"A", "C", 0, 0},   // 显式 0 与未设定等价
		{"X", "Y", 100000, 100000},
	}
	for _, c := range cases {
		if err := m.SetChangeover(c.a, c.b, c.min); err != nil {
			t.Fatalf("SetChangeover(%s,%s,%d): %v", c.a, c.b, c.min, err)
		}
		if got := m.Changeover(c.a, c.b); got != c.want {
			t.Errorf("Changeover(%s,%s)=%d want %d", c.a, c.b, got, c.want)
		}
	}
	if got := m.Changeover("B", "C"); got != 0 {
		t.Errorf("unset pair = %d, want 0", got)
	}
	if got := m.Changeover("A", "A"); got != 0 {
		t.Errorf("same family = %d, want 0", got)
	}
}

func TestInvalidArgs(t *testing.T) {
	long := strings.Repeat("z", 33)
	cases := []struct {
		a, b string
		min  int
	}{
		{"", "B", 10},
		{"A", "", 10},
		{long, "B", 10},
		{"A", long, 10},
		{"A", "B", -1},
		{"A", "B", 100001},
		{"A", "A", 1}, // 同族必须 0
	}
	for _, c := range cases {
		err := New().SetChangeover(c.a, c.b, c.min)
		if !errors.Is(err, ErrArgument) {
			t.Errorf("SetChangeover(%q,%q,%d) err=%v, want ErrArgument", c.a, c.b, c.min, err)
		}
	}
}

func TestOverwriteAndZero(t *testing.T) {
	m := New()
	if err := m.SetChangeover("A", "B", 20); err != nil {
		t.Fatal(err)
	}
	if err := m.SetChangeover("A", "B", 5); err != nil {
		t.Fatal(err)
	}
	if got := m.Changeover("A", "B"); got != 5 {
		t.Fatalf("overwrite = %d, want 5", got)
	}
	if err := m.SetChangeover("A", "B", 0); err != nil {
		t.Fatal(err)
	}
	if got := m.Changeover("A", "B"); got != 0 {
		t.Fatalf("reset zero = %d, want 0", got)
	}
}
