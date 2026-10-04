package match

import (
	"errors"
	"testing"
)

func mustField(t *testing.T, v, m uint32) Field {
	t.Helper()
	f := Field{Value: v, Mask: m}
	if v&^m != 0 {
		t.Fatalf("invalid field %#x/%#x", v, m)
	}
	return f
}

func TestNewInvalid(t *testing.T) {
	cases := []struct {
		name        string
		f0, f1      Field
		wantInvalid bool
	}{
		{"both wildcard", Field{}, Field{}, false},
		{"exact ok", Field{Value: 0xFFFFFFFF, Mask: 0xFFFFFFFF}, Field{}, false},
		{"bit outside mask", Field{Value: 0x00000001, Mask: 0xFFFFFFFE}, Field{}, true},
		{"high bit outside", Field{}, Field{Value: 0x80000000, Mask: 0x7FFFFFFF}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.f0, c.f1)
			if c.wantInvalid && !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
			if !c.wantInvalid && err != nil {
				t.Fatalf("unexpected err %v", err)
			}
		})
	}
}

func TestHit(t *testing.T) {
	m := Match{F0: Field{Value: 0x0A000000, Mask: 0xFF000000}, F1: Field{}}
	cases := []struct {
		pkt  Pkt
		want bool
	}{
		{Pkt{F0: 0x0A010203}, true},
		{Pkt{F0: 0x0AFFFFFF}, true},
		{Pkt{F0: 0x0B000000}, false},
		{Pkt{F0: 0x0000000A}, false},
	}
	for _, c := range cases {
		if got := m.Hit(c.pkt); got != c.want {
			t.Fatalf("Hit(%#x)=%v want %v", c.pkt.F0, got, c.want)
		}
	}
}

func TestOverlap(t *testing.T) {
	a := Match{F0: Field{Value: 0x0A000000, Mask: 0xFF000000}, F1: Field{}}
	b := Match{F0: Field{Value: 0x0A010000, Mask: 0xFFFF0000}, F1: Field{}}
	c := Match{F0: Field{Value: 0x0B000000, Mask: 0xFF000000}, F1: Field{}}
	exact := Match{F0: Field{Value: 0x0A000001, Mask: 0xFFFFFFFF}, F1: Field{}}
	fullWild := Match{}
	cases := []struct {
		name  string
		x, y  Match
		want  bool
		basis string
	}{
		{"A vs B overlap", a, b, true, "(A^B)&MA&MB=0 on shared byte"},
		{"A vs C disjoint", a, c, false, "top byte differs 0x0A vs 0x0B"},
		{"A vs identical", a, a, true, "same match overlaps itself"},
		{"exact inside A", a, exact, true, "0x0A000001 lies in A"},
		{"exact vs B disjoint", exact, b, false, "0x0A000001 has second byte 0x00"},
		{"wildcard overlaps all", fullWild, exact, true, "mask 0 intersection always 0"},
		{"mask boundary all-ones", Match{F0: mustField(t, 0xFFFFFFFF, 0xFFFFFFFF)}, Match{F0: mustField(t, 0xFFFFFFFE, 0xFFFFFFFE)}, true, "differ only in bit excluded by narrower"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.x.Overlap(c.y); got != c.want {
				t.Fatalf("Overlap=%v want %v; basis: %s", got, c.want, c.basis)
			}
			if got := c.y.Overlap(c.x); got != c.want {
				t.Fatalf("Overlap not symmetric; basis: %s", c.basis)
			}
		})
	}
}

func TestContains(t *testing.T) {
	a := Match{F0: Field{Value: 0x0A000000, Mask: 0xFF000000}, F1: Field{}}
	b := Match{F0: Field{Value: 0x0A010000, Mask: 0xFFFF0000}, F1: Field{}}
	exact := Match{F0: Field{Value: 0x0A010203, Mask: 0xFFFFFFFF}, F1: Field{}}
	cases := []struct {
		name string
		x, y Match
		want bool
	}{
		{"A contains B", a, b, true},
		{"B does not contain A", b, a, false},
		{"B contains exact 0x0A010203", b, exact, true},
		{"A does not contain exact in 0x0B", a, Match{F0: Field{Value: 0x0B010203, Mask: 0xFFFFFFFF}}, false},
		{"wildcard contains all", Match{}, exact, true},
		{"exact contains itself", exact, exact, true},
		{"same mask different value", Match{F0: Field{Value: 1, Mask: 1}}, Match{F0: Field{Value: 0, Mask: 1}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.x.Contains(c.y); got != c.want {
				t.Fatalf("Contains=%v want %v", got, c.want)
			}
		})
	}
}

func TestEqual(t *testing.T) {
	a := Match{F0: Field{Value: 0x0A000000, Mask: 0xFF000000}}
	a2 := Match{F0: Field{Value: 0x0A000000, Mask: 0xFF000000}}
	narrower := Match{F0: Field{Value: 0x0A000000, Mask: 0xFFFF0000}}
	if !a.Equal(a2) {
		t.Fatal("identical matches not equal")
	}
	if a.Equal(narrower) {
		t.Fatal("different masks compared equal")
	}
}
