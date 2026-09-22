package hll

import "testing"

func TestNewRejectsInvalidPrecision(t *testing.T) {
	for _, p := range []int{-1, 0, 3, 17, 64} {
		if _, err := New(p); err == nil {
			t.Fatalf("New(%d) = nil error, want error", p)
		}
	}
	for _, p := range []int{4, 10, 16} {
		if _, err := New(p); err != nil {
			t.Fatalf("New(%d) unexpected error: %v", p, err)
		}
	}
}

func TestRegisterSemanticsHandcrafted(t *testing.T) {
	const p = 4
	e, err := New(p)
	if err != nil {
		t.Fatal(err)
	}

	// Hash layouts, low 4 bits are the register index:
	// 0x0000_0000_0000_0001 idx=1, all high bits zero      -> rho max (64)
	// 0x8000_0000_0000_0002 idx=2, high starts with 1      -> rho 1
	// 0x4000_0000_0000_0002 idx=2, high 010...             -> rho 2 (replaces)
	// 0x2000_0000_0000_0002 idx=2, high 0010...            -> rho 3 (replaces)
	// 0x1000_0000_0000_0002 idx=2, high 0001...            -> rho 4 (replaces)
	// 0x4000_0000_0000_0009 idx=9, rho 2                   -> smaller later:
	// 0x1000_0000_0000_0009 idx=9, rho 4                   -> then larger:
	hashes := []uint64{
		0x0000000000000001,
		0x8000000000000002,
		0x4000000000000002,
		0x2000000000000002,
		0x1000000000000002,
		0x4000000000000009,
		0x1000000000000009,
	}
	checks := []struct {
		step int
		idx  int
		val  uint8
	}{
		{0, 1, 64}, // all 60 high bits zero: rho capped at 64
		{1, 2, 1},
		{2, 2, 2}, // larger rho replaces
		{3, 2, 3},
		{4, 2, 4},
		{5, 9, 2},
		{6, 9, 4}, // larger rho replaces the smaller one
	}
	for i, h := range hashes {
		e.Add(h)
		c := checks[i]
		if got := e.registers[c.idx]; got != c.val {
			t.Fatalf("after Add[%d]=%#016x register %d = %d, want %d",
				i, h, c.idx, got, c.val)
		}
	}

	// A smaller rho on an already populated register must not change it.
	e.Add(0x8000000000000002) // idx=2, rho 1 < 4
	if got := e.registers[2]; got != 4 {
		t.Fatalf("register 2 = %d after smaller rho, want unchanged 4", got)
	}

	wantFinal := make([]uint8, 16)
	wantFinal[1] = 64
	wantFinal[2] = 4
	wantFinal[9] = 4
	gotFinal := e.InspectRegisters()
	for i := range wantFinal {
		if gotFinal[i] != wantFinal[i] {
			t.Fatalf("final register %d = %d, want %d (snapshot=%v)",
				i, gotFinal[i], wantFinal[i], gotFinal)
		}
	}
}

func TestInspectRegistersIsCopy(t *testing.T) {
	e, _ := New(4)
	e.Add(0x1000000000000002)
	snap := e.InspectRegisters()
	snap[2] = 99
	snap[0] = 99
	again := e.InspectRegisters()
	if again[2] != 4 || again[0] != 0 {
		t.Fatalf("mutating snapshot leaked into estimator: %v", again)
	}
}

func TestRhoBoundaries(t *testing.T) {
	cases := []struct {
		w    uint64
		want uint8
	}{
		{0, 64},          // no set bit: capped maximum
		{1 << 63, 1},     // top bit set
		{1 << 62, 2},     // one leading zero
		{1, 64},          // only lowest bit set
		{1 << 31, 33},
	}
	for _, c := range cases {
		if got := rho(c.w); got != c.want {
			t.Fatalf("rho(%#016x) = %d, want %d", c.w, got, c.want)
		}
	}
}
