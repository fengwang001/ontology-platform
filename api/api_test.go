package api_test

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

func naive(p int, mem []int) int { // mem sorted ascending
	best := -1
	for _, m := range mem {
		if m >= p && (best < 0 || m < best) {
			best = m
		}
	}
	if best < 0 {
		best = mem[0]
	}
	return best
}

func enc(g *api.Group) (s string) {
	for _, pt := range g.Snapshot() {
		if pt.Owner < 0 {
			s += "-"
		} else {
			s += string(rune('0' + pt.Owner))
		}
		s += string("UCR"[pt.State])
	}
	return s
}

var eightStep = []struct{ op, want string }{
	{"j5", "5C5C5C5C5C5C5C5C"},
	{"j2", "5R5R5R5C5C5C5R5R"},
	{"a5", "2C2C2C5C5C5C2C2C"},
	{"j4", "2C2C2C5R5R5C2C2C"},
	{"j7", "2C2C2C5R5R5C2R2R"},
	{"a5", "2C2C2C-U-U5C2R2R"},
	{"l4", "2C2C2C-U-U5C2R2R"},
	{"a2", "2C2C2C5C5C5C7C7C"},
}

func TestEightStep(t *testing.T) {
	g, _ := api.New(8)
	ops := map[byte]func(int) error{'j': g.Join, 'l': g.Leave, 'a': g.RevokeAck}
	for i, s := range eightStep {
		if err := ops[s.op[0]](int(s.op[1] - '0')); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		if got := enc(g); got != s.want {
			t.Errorf("step %d: got %s want %s", i+1, got, s.want)
		}
	}
}

func TestFaultInjection(t *testing.T) {
	if _, err := api.New(0); !errors.Is(err, api.ErrBadParam) {
		t.Fatal("New(0) should fail with ErrBadParam")
	}
	g, _ := api.New(8)
	g.Join(3)
	before := g.Snapshot()
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"badParam", func() error { return g.Join(8) }, api.ErrBadParam},
		{"duplicate", func() error { return g.Join(3) }, api.ErrDuplicate},
		{"noMemberLeave", func() error { return g.Leave(4) }, api.ErrNoMember},
		{"noMemberAck", func() error { return g.RevokeAck(4) }, api.ErrNoMember},
		{"noRevoke", func() error { return g.RevokeAck(3) }, api.ErrNoRevoke},
	}
	for _, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
		if !slices.Equal(before, g.Snapshot()) {
			t.Errorf("%s: state changed after rejection", c.name)
		}
	}
	distinct := map[error]bool{api.ErrBadParam: true, api.ErrDuplicate: true,
		api.ErrNoMember: true, api.ErrNoRevoke: true}
	if len(distinct) != 4 {
		t.Fatal("sentinel errors must be distinct")
	}
	if err := g.Join(4); err != nil { // still usable after rejections
		t.Fatalf("group unusable after rejections: %v", err)
	}
}

func TestSelfCheck(t *testing.T) {
	g, _ := api.New(8)
	if err := g.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrent(t *testing.T) {
	const P = 64
	g, _ := api.New(P)
	var bad atomic.Int64
	var done atomic.Bool
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for !done.Load() {
				for _, pt := range g.Snapshot() {
					if (pt.State == api.Unowned) != (pt.Owner == -1) || pt.Owner < -1 || pt.Owner >= P {
						bad.Add(1)
					}
				}
			}
		}()
	}
	var wg sync.WaitGroup
	for _, op := range []func(int) error{g.Join, g.RevokeAck} {
		for i := 0; i < P/2; i++ {
			wg.Add(1)
			go func(x int) {
				defer wg.Done()
				op(x) // ErrNoRevoke is fine
			}(i)
		}
		wg.Wait()
	}
	done.Store(true)
	readers.Wait()
	if bad.Load() != 0 {
		t.Fatalf("%d bad snapshots during concurrency", bad.Load())
	}
	mem := make([]int, P/2)
	for i := range mem {
		mem[i] = i
	}
	for p, pt := range g.Snapshot() {
		if pt.State != api.Consuming || pt.Owner != naive(p, mem) {
			t.Fatalf("p%d final: got %d/%v want %d/Consuming", p, pt.Owner, pt.State, naive(p, mem))
		}
	}
}
