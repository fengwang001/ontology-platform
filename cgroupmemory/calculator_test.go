package cgroupmemory

import (
	"errors"
	"math/big"
	"sync"
	"testing"
)

func TestSpecExampleReclaim(t *testing.T) {
	calc := New()
	mustCreate(t, calc, "/a", 20, 60)
	mustCreate(t, calc, "/b", 0, 0)
	mustSetUsage(t, calc, "/a", 100)
	mustSetUsage(t, calc, "/b", 100)

	result, err := calc.Reclaim(150)
	if err != nil {
		t.Fatalf("Reclaim returned error: %v", err)
	}

	assertEvents(t, result, []ReclaimEvent{
		{Path: "/b", Bytes: 100, Pass: 1},
		{Path: "/a", Bytes: 40, Pass: 1},
		{Path: "/a", Bytes: 10, Pass: 2},
	}, 150, false)
}

func TestSpecExampleInsufficient(t *testing.T) {
	calc := New()
	mustCreate(t, calc, "/a", 20, 60)
	mustCreate(t, calc, "/b", 0, 0)
	mustSetUsage(t, calc, "/a", 100)
	mustSetUsage(t, calc, "/b", 100)

	result, err := calc.Reclaim(250)
	if err != nil {
		t.Fatalf("Reclaim returned error: %v", err)
	}
	assertEvents(t, result, []ReclaimEvent{
		{Path: "/b", Bytes: 100, Pass: 1},
		{Path: "/a", Bytes: 40, Pass: 1},
		{Path: "/a", Bytes: 40, Pass: 2},
	}, 180, true)
}

func TestEffectiveLowUsesUsageWhenLowHigher(t *testing.T) {
	calc := New()
	mustCreate(t, calc, "/a", 10, 100)
	mustSetUsage(t, calc, "/a", 40)

	low, min, err := calc.Effective("/a")
	if err != nil {
		t.Fatalf("Effective returned error: %v", err)
	}
	if low != 40 || min != 10 {
		t.Fatalf("Effective = (%d, %d), want (40, 10)", low, min)
	}
}

func TestRootChildrenAreNotScaled(t *testing.T) {
	calc := New()
	mustCreate(t, calc, "/a", 10, 100)
	mustCreate(t, calc, "/b", 10, 100)
	mustSetUsage(t, calc, "/a", 100)
	mustSetUsage(t, calc, "/b", 100)

	assertEffective(t, calc, "/a", 100, 10)
	assertEffective(t, calc, "/b", 100, 10)
}

func TestChildrenShareByCappedLowAndFloor(t *testing.T) {
	calc := New()
	mustCreate(t, calc, "/p", 0, 10)
	mustCreate(t, calc, "/p/x", 0, 100)
	mustCreate(t, calc, "/p/y", 0, 100)
	mustSetUsage(t, calc, "/p/x", 100)
	mustSetUsage(t, calc, "/p/y", 100)

	assertEffective(t, calc, "/p", 10, 0)
	assertEffective(t, calc, "/p/x", 5, 0)
	assertEffective(t, calc, "/p/y", 5, 0)
}

func TestSharingUsesCappedUsageInsteadOfConfiguredLow(t *testing.T) {
	calc := New()
	mustCreate(t, calc, "/p", 0, 60)
	mustCreate(t, calc, "/p/x", 0, 100)
	mustCreate(t, calc, "/p/y", 0, 100)
	mustSetUsage(t, calc, "/p/x", 30)
	mustSetUsage(t, calc, "/p/y", 100)

	assertEffective(t, calc, "/p/x", 13, 0)
	assertEffective(t, calc, "/p/y", 46, 0)
}

func TestLargeProductsRemainExact(t *testing.T) {
	calc := New()
	mustCreate(t, calc, "/p", 0, 999_999_999_999_999)
	mustCreate(t, calc, "/p/x", 0, 1_000_000_000_000_000)
	mustCreate(t, calc, "/p/y", 0, 1_000_000_000_000_000)
	mustSetUsage(t, calc, "/p/x", 1_000_000_000_000_000)
	mustSetUsage(t, calc, "/p/y", 1_000_000_000_000_000)

	wantProduct := new(big.Int).Mul(
		big.NewInt(1_000_000_000_000_000),
		big.NewInt(999_999_999_999_999),
	)
	want := new(big.Int).Quo(wantProduct, big.NewInt(1_999_999_999_999_999)).Int64()
	assertEffective(t, calc, "/p/x", want, 0)
	assertEffective(t, calc, "/p/y", want, 0)
}

func TestNonLeafUsageIsSumOfLeaves(t *testing.T) {
	calc := New()
	mustCreate(t, calc, "/p", 0, 1_000_000_000_000_000)
	mustCreate(t, calc, "/p/x", 0, 1_000_000_000_000_000)
	mustCreate(t, calc, "/p/y", 0, 1_000_000_000_000_000)
	mustSetUsage(t, calc, "/p/x", 40)
	mustSetUsage(t, calc, "/p/y", 60)

	assertEffective(t, calc, "/p", 100, 0)
	if err := calc.SetUsage("/p", 1); !errors.Is(err, ErrNotLeaf) {
		t.Fatalf("SetUsage parent error = %v, want ErrNotLeaf", err)
	}
}

func TestEffectiveMinIsClampedByEffectiveLow(t *testing.T) {
	calc := New()
	mustCreate(t, calc, "/p", 100, 100)
	mustCreate(t, calc, "/p/x", 100, 100)
	mustCreate(t, calc, "/p/y", 0, 100)
	mustSetUsage(t, calc, "/p/x", 100)
	mustSetUsage(t, calc, "/p/y", 100)

	assertEffective(t, calc, "/p/x", 50, 50)
	assertEffective(t, calc, "/p/y", 50, 0)
}

func TestTieBreaksByPathByteOrder(t *testing.T) {
	calc := New()
	for _, path := range []string{"/b", "/a", "/aa"} {
		mustCreate(t, calc, path, 0, 0)
		mustSetUsage(t, calc, path, 10)
	}

	result, err := calc.Reclaim(25)
	if err != nil {
		t.Fatalf("Reclaim returned error: %v", err)
	}
	assertEvents(t, result, []ReclaimEvent{
		{Path: "/a", Bytes: 10, Pass: 1},
		{Path: "/aa", Bytes: 10, Pass: 1},
		{Path: "/b", Bytes: 5, Pass: 1},
	}, 25, false)
}

func TestFirstPassSatisfiesNeedSkipsSecond(t *testing.T) {
	calc := New()
	mustCreate(t, calc, "/a", 40, 60)
	mustSetUsage(t, calc, "/a", 100)

	result, err := calc.Reclaim(10)
	if err != nil {
		t.Fatalf("Reclaim returned error: %v", err)
	}
	assertEvents(t, result, []ReclaimEvent{{Path: "/a", Bytes: 10, Pass: 1}}, 10, false)
}

func TestSecondPassUsesPostFirstUsageButFrozenProtection(t *testing.T) {
	calc := New()
	mustCreate(t, calc, "/p", 40, 60)
	mustCreate(t, calc, "/p/leaf1", 30, 40)
	mustCreate(t, calc, "/p/leaf2", 0, 40)
	mustSetUsage(t, calc, "/p/leaf1", 40)
	mustSetUsage(t, calc, "/p/leaf2", 150)

	result, err := calc.Reclaim(150)
	if err != nil {
		t.Fatalf("Reclaim returned error: %v", err)
	}
	assertEvents(t, result, []ReclaimEvent{
		{Path: "/p/leaf2", Bytes: 120, Pass: 1},
		{Path: "/p/leaf1", Bytes: 10, Pass: 1},
		{Path: "/p/leaf2", Bytes: 20, Pass: 2},
	}, 150, false)
}

func TestConcurrentMixedOperations(t *testing.T) {
	calc := New()
	for _, path := range []string{"/a", "/b", "/p", "/p/x", "/p/y"} {
		mustCreate(t, calc, path, 10, 50)
	}

	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for index := 0; index < 200; index++ {
				path := []string{"/a", "/b", "/p/x", "/p/y"}[(worker+index)%4]
				if err := calc.SetProtect(path, int64(worker%50), 50+int64(index%50)); err != nil {
					t.Errorf("SetProtect: %v", err)
					return
				}
				if err := calc.SetUsage(path, int64((worker+index)%150)); err != nil {
					t.Errorf("SetUsage: %v", err)
					return
				}
				if _, _, err := calc.Effective(path); err != nil {
					t.Errorf("Effective: %v", err)
					return
				}
				if _, err := calc.Reclaim(int64(1 + (worker+index)%120)); err != nil {
					t.Errorf("Reclaim: %v", err)
					return
				}
			}
		}(worker)
	}
	wait.Wait()
}

func TestRejectionsDoNotChangeState(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Calculator) error
		want error
	}{
		{name: "invalid path", run: func(c *Calculator) error { return c.Create("//a", 0, 0) }, want: ErrInvalidArgument},
		{name: "trailing slash", run: func(c *Calculator) error { return c.Create("/a/", 0, 0) }, want: ErrInvalidArgument},
		{name: "negative min", run: func(c *Calculator) error { return c.Create("/a", -1, 0) }, want: ErrInvalidArgument},
		{name: "min above low", run: func(c *Calculator) error { return c.Create("/a", 2, 1) }, want: ErrInvalidArgument},
		{name: "low above max", run: func(c *Calculator) error { return c.Create("/a", 0, 1_000_000_000_000_001) }, want: ErrInvalidArgument},
		{name: "invalid usage", run: func(c *Calculator) error { return c.SetUsage("/a", -1) }, want: ErrInvalidArgument},
		{name: "invalid need", run: func(c *Calculator) error { _, err := c.Reclaim(0); return err }, want: ErrInvalidArgument},
		{name: "root create", run: func(c *Calculator) error { return c.Create("/", 0, 0) }, want: ErrRootNotOperable},
		{name: "root set protect", run: func(c *Calculator) error { return c.SetProtect("/", 0, 0) }, want: ErrRootNotOperable},
		{name: "root set usage", run: func(c *Calculator) error { return c.SetUsage("/", 0) }, want: ErrRootNotOperable},
		{name: "root remove", run: func(c *Calculator) error { return c.Remove("/") }, want: ErrRootNotOperable},
		{name: "root effective", run: func(c *Calculator) error { _, _, err := c.Effective("/"); return err }, want: ErrRootNotOperable},
		{name: "missing protect", run: func(c *Calculator) error { return c.SetProtect("/missing", 0, 0) }, want: ErrNotFound},
		{name: "missing usage", run: func(c *Calculator) error { return c.SetUsage("/missing", 0) }, want: ErrNotFound},
		{name: "missing remove", run: func(c *Calculator) error { return c.Remove("/missing") }, want: ErrNotFound},
		{name: "missing effective", run: func(c *Calculator) error { _, _, err := c.Effective("/missing"); return err }, want: ErrNotFound},
		{name: "duplicate create", run: func(c *Calculator) error { return c.Create("/a", 0, 0) }, want: ErrAlreadyExists},
		{name: "missing parent", run: func(c *Calculator) error { return c.Create("/missing/a", 0, 0) }, want: ErrParentNotFound},
		{name: "used leaf parent", run: func(c *Calculator) error { return c.Create("/a/b", 0, 0) }, want: ErrParentHasUsage},
		{name: "nonleaf usage", run: func(c *Calculator) error { return c.SetUsage("/p", 0) }, want: ErrNotLeaf},
		{name: "remove parent", run: func(c *Calculator) error { return c.Remove("/p") }, want: ErrHasChildren},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calc := New()
			mustCreate(t, calc, "/a", 10, 20)
			mustSetUsage(t, calc, "/a", 30)
			mustCreate(t, calc, "/p", 0, 0)
			mustCreate(t, calc, "/p/x", 0, 0)

			if err := tt.run(calc); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}

			assertEffective(t, calc, "/a", 20, 10)
			low, _, err := calc.Effective("/p/x")
			if err != nil || low != 0 {
				t.Fatalf("after rejection Effective(/p/x) low = %d, err = %v, want 0", low, err)
			}
		})
	}
}

func mustCreate(t *testing.T, calc *Calculator, path string, min, low int64) {
	t.Helper()
	if err := calc.Create(path, min, low); err != nil {
		t.Fatalf("Create(%q) = %v", path, err)
	}
}

func mustSetUsage(t *testing.T, calc *Calculator, path string, bytes int64) {
	t.Helper()
	if err := calc.SetUsage(path, bytes); err != nil {
		t.Fatalf("SetUsage(%q) = %v", path, err)
	}
}

func assertEffective(t *testing.T, calc *Calculator, path string, wantLow, wantMin int64) {
	t.Helper()
	gotLow, gotMin, err := calc.Effective(path)
	if err != nil {
		t.Fatalf("Effective(%q) returned error: %v", path, err)
	}
	if gotLow != wantLow || gotMin != wantMin {
		t.Fatalf("Effective(%q) = (%d, %d), want (%d, %d)", path, gotLow, gotMin, wantLow, wantMin)
	}
}

func assertEvents(t *testing.T, got ReclaimResult, want []ReclaimEvent, wantReclaimed int64, wantInsufficient bool) {
	t.Helper()
	if got.Reclaimed != wantReclaimed || got.Insufficient != wantInsufficient {
		t.Fatalf("result = reclaimed %d insufficient %v, want %d %v", got.Reclaimed, got.Insufficient, wantReclaimed, wantInsufficient)
	}
	if len(got.Events) != len(want) {
		t.Fatalf("events = %#v, want %#v", got.Events, want)
	}
	for index := range want {
		if got.Events[index] != want[index] {
			t.Fatalf("events[%d] = %#v, want %#v; all got: %#v", index, got.Events[index], want[index], got.Events)
		}
	}
}
