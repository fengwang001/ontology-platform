package debloat

import "testing"

func exampleCfg() Config {
	return Config{
		Bmin: 1024, Bmax: 32768, G: 1024, B0: 4096,
		T: 1000, W: 3, ThU: 25, ThD: 50, Kc: 2,
		C0: 2, Pool: 65536, H: 3,
	}
}

func TestSmokeSpecSequence(t *testing.T) {
	c, err := New(exampleCfg())
	if err != nil {
		t.Fatal(err)
	}
	r1, _ := c.Sample(100000, 1000)
	t.Logf("Sample1 => %+v", r1)
	if r1.Action != ActionPending || r1.Streak != 1 || r1.Cur != 4096 || r1.Cand != 32768 || r1.R != 100000 {
		t.Fatalf("r1 wrong: %+v", r1)
	}
	r2, _ := c.Sample(100000, 1000)
	t.Logf("Sample2 => %+v", r2)
	if r2.Action != ActionApplied || r2.Cur != 32768 {
		t.Fatalf("r2 wrong: %+v", r2)
	}
	r3, _ := c.Sample(1, 1)
	t.Logf("Sample3 => %+v", r3)
	if r3.Action != ActionSkipped {
		t.Fatalf("r3 wrong: %+v", r3)
	}
	r4, _ := c.Sample(20000, 1000)
	t.Logf("Sample4 => %+v", r4)
	if r4.Action != ActionHold || r4.R != 73333 || r4.Cand != 32768 {
		t.Fatalf("r4 wrong: %+v", r4)
	}
	r5, _ := c.Sample(0, 1000)
	t.Logf("Sample5 => %+v", r5)
	if r5.Action != ActionHold || r5.R != 40000 || r5.Cand != 19456 {
		t.Fatalf("r5 wrong: %+v", r5)
	}
	r6, _ := c.Sample(0, 1000)
	t.Logf("Sample6 => %+v", r6)
	if r6.Action != ActionApplied || r6.R != 6666 || r6.Cand != 3072 || r6.Cur != 3072 {
		t.Fatalf("r6 wrong: %+v", r6)
	}

	if err := c.SetChannels(8); err != nil {
		t.Fatal(err)
	}
	if s := c.State(); s.Cur != 3072 {
		t.Fatalf("after C=8 cur=%d", s.Cur)
	}
	if err := c.SetChannels(32); err != nil {
		t.Fatal(err)
	}
	if s := c.State(); s.Cur != 2048 || s.ForcedCount != 1 || !s.Polluted || s.LastDir != DirDown {
		t.Fatalf("after C=32 state=%+v", s)
	}
	if err := c.SetChannels(65); err != ErrInsufficientCapacity {
		t.Fatalf("C=65 err=%v", err)
	}

	// Oscillation suppression continuation.
	if err := c.SetChannels(8); err != nil {
		t.Fatal(err)
	}
	r7, _ := c.Sample(1, 1)
	if r7.Action != ActionSkipped {
		t.Fatalf("r7 wrong: %+v", r7)
	}
	var r Result
	for i := 0; i < 4; i++ {
		r, _ = c.Sample(100000, 1000)
		t.Logf("damp sample %d => %+v state=%+v", i, r, c.State())
	}
	if r.Action != ActionApplied || r.Cur != 8192 {
		t.Fatalf("final grow wrong: %+v", r)
	}
	s := c.State()
	if s.Damp != 0 || s.LastDir != DirUp || s.Gap != 0 {
		t.Fatalf("final state wrong: %+v", s)
	}
}
