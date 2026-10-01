package ontology

import (
	"errors"
	"fmt"
	"testing"
)

func mustNew(t *testing.T, L, F, d int, caps []int) *FailoverAllocator {
	t.Helper()
	a, err := NewFailoverAllocator(L, F, d, caps)
	if err != nil {
		t.Fatalf("unexpected config error: %v", err)
	}
	return a
}

func add(t *testing.T, a *FailoverAllocator, level int, id string, healthy bool) {
	t.Helper()
	if err := a.AddHost(level, id, healthy); err != nil {
		t.Fatalf("AddHost(%q): %v", id, err)
	}
}

func loadsOf(t *testing.T, a *FailoverAllocator) []int {
	t.Helper()
	v, err := a.Loads()
	if err != nil {
		t.Fatalf("Loads: %v", err)
	}
	return v
}

func TestInvalidConfig(t *testing.T) {
	bad := []struct {
		L, F, d int
		caps    []int
	}{
		{0, 140, 30, []int{100}},
		{17, 140, 30, make([]int, 17)},
		{2, 99, 30, []int{60, 40}},
		{2, 1001, 30, []int{60, 40}},
		{2, 140, 0, []int{60, 40}},
		{2, 140, 101, []int{60, 40}},
		{2, 140, 30, []int{60}},
		{2, 140, 30, []int{0, 100}},
		{2, 140, 30, []int{101, 100}},
		{3, 140, 30, []int{30, 30, 39}},
	}
	for i, c := range bad {
		if _, err := NewFailoverAllocator(c.L, c.F, c.d, c.caps); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: want ErrInvalidConfig, got %v", i, err)
		}
	}
}

func TestAddHostRejectionOrder(t *testing.T) {
	a := mustNew(t, 2, 140, 30, []int{60, 100})
	add(t, a, 0, "x", true)
	if err := a.AddHost(9, "", false); !errors.Is(err, ErrLevelOutOfRange) {
		t.Fatalf("level first, got %v", err)
	}
	if err := a.AddHost(0, "", false); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("empty id second, got %v", err)
	}
	if err := a.AddHost(0, "x", false); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate third, got %v", err)
	}
	if err := a.RemoveHost("nope"); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("remove missing: %v", err)
	}
	if err := a.SetHealth("nope", true); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("sethealth missing: %v", err)
	}
}

// raw = floor(h*F/t) exactly 100 stays 100; above 100 is capped.
func TestRawCapping(t *testing.T) {
	a := mustNew(t, 2, 200, 100, []int{100, 100})
	add(t, a, 0, "a0", true)
	add(t, a, 0, "a1", true)
	add(t, a, 0, "a2", false)
	add(t, a, 0, "a3", false) // h=2,t=4 => floor(400/4)=100 exactly
	loadsOf(t, a)
	if a.cur[0] != 100 {
		t.Fatalf("raw exactly 100: cur=%d", a.cur[0])
	}
	a2 := mustNew(t, 1, 300, 100, []int{100})
	add(t, a2, 0, "b0", true)
	add(t, a2, 0, "b1", false) // h=1,t=2 => 150 capped to 100
	loadsOf(t, a2)
	if a2.cur[0] != 100 {
		t.Fatalf("raw capped: cur=%d", a2.cur[0])
	}
}

func TestHysteresisEdges(t *testing.T) {
	a := mustNew(t, 1, 100, 30, []int{100})
	for i := 0; i < 100; i++ {
		add(t, a, 0, fmt.Sprintf("h%d", i), i < 40) // raw=40
	}
	loadsOf(t, a)
	if a.cur[0] != 40 {
		t.Fatalf("initial cur=%d", a.cur[0])
	}
	// raw rises to 69 (40 < 69 < 70): inside band => stays 40.
	for i := 40; i < 69; i++ {
		if err := a.SetHealth(fmt.Sprintf("h%d", i), true); err != nil {
			t.Fatal(err)
		}
	}
	loadsOf(t, a)
	if a.cur[0] != 40 {
		t.Fatalf("one below threshold should stay, cur=%d", a.cur[0])
	}
	// raw = 70 = cur + delta exactly => updates.
	if err := a.SetHealth("h69", true); err != nil {
		t.Fatal(err)
	}
	loadsOf(t, a)
	if a.cur[0] != 70 {
		t.Fatalf("at threshold should update, cur=%d", a.cur[0])
	}
	// Drop is immediate even inside the band: raw 69 < cur 70.
	if err := a.SetHealth("h0", false); err != nil {
		t.Fatal(err)
	}
	loadsOf(t, a)
	if a.cur[0] != 69 {
		t.Fatalf("drop must be immediate, cur=%d", a.cur[0])
	}
	// raw equals cur: rule selects the update branch, value unchanged.
	loadsOf(t, a)
	if a.cur[0] != 69 {
		t.Fatalf("raw==cur should stay 69, got %d", a.cur[0])
	}
	// cur becomes 0; recovery from 0 is not hysteretic.
	for i := 1; i < 70; i++ {
		if err := a.SetHealth(fmt.Sprintf("h%d", i), false); err != nil {
			t.Fatal(err)
		}
	}
	loadsOf(t, a)
	if a.cur[0] != 0 {
		t.Fatalf("cur should be 0, got %d", a.cur[0])
	}
	if err := a.SetHealth("h50", true); err != nil {
		t.Fatal(err)
	}
	loadsOf(t, a)
	if a.cur[0] != 1 {
		t.Fatalf("recovery from 0 must not wait for delta, cur=%d", a.cur[0])
	}
}

// S == 100 exactly uses greedy; S == 99 scales and the remainder goes
// to the smallest level whose cur > 0.
func TestSum100And99(t *testing.T) {
	a := mustNew(t, 3, 100, 100, []int{100, 100, 100})
	for i := 0; i < 100; i++ {
		add(t, a, 0, fmt.Sprintf("a%d", i), i < 50)
		add(t, a, 1, fmt.Sprintf("b%d", i), i < 30)
		add(t, a, 2, fmt.Sprintf("c%d", i), i < 20)
	}
	got := loadsOf(t, a) // cur = [50,30,20], S=100
	if fmt.Sprint(got) != "[50 30 20]" {
		t.Fatalf("S=100 greedy: %v", got)
	}
	// cur [49,30,20], S=99 => floors [49,30,20]=99, remainder 1 to level 0.
	if err := a.SetHealth("a49", false); err != nil {
		t.Fatal(err)
	}
	got = loadsOf(t, a)
	if fmt.Sprint(got) != "[50 30 20]" {
		t.Fatalf("S=99 scaling + remainder: %v", got)
	}
}

// Remainder skips the smallest level when its cur is 0.
func TestRemainderSkipsZeroCur(t *testing.T) {
	a := mustNew(t, 3, 100, 100, []int{100, 100, 100})
	for i := 0; i < 100; i++ {
		add(t, a, 0, fmt.Sprintf("a%d", i), false) // level 0 exists, cur 0
		add(t, a, 1, fmt.Sprintf("b%d", i), i < 60)
		add(t, a, 2, fmt.Sprintf("c%d", i), i < 30)
	}
	got := loadsOf(t, a) // cur [0,60,30], S=90 => [0,66,33]=99, +1 to level 1
	if fmt.Sprint(got) != "[0 67 33]" {
		t.Fatalf("remainder must skip level 0: %v", got)
	}
}

// Greedy when S>=100: later levels get nothing once 100 is consumed.
func TestGreedyStarvesLaterLevels(t *testing.T) {
	a := mustNew(t, 2, 1000, 100, []int{100, 100})
	add(t, a, 0, "a", true)
	add(t, a, 1, "b", true)
	got := loadsOf(t, a) // cur [100,100], greedy => [100,0]
	if fmt.Sprint(got) != "[100 0]" {
		t.Fatalf("greedy starvation: %v", got)
	}
}

// Panic: eligibility in redistribution follows "has any host".
func TestPanicFallback(t *testing.T) {
	a := mustNew(t, 2, 140, 30, []int{60, 100})
	add(t, a, 0, "a", false)
	add(t, a, 1, "b", false)
	got := loadsOf(t, a) // panic, level 0 capped 60, 40 spills to level 1
	if fmt.Sprint(got) != "[60 40]" {
		t.Fatalf("panic redistribute by hosts: %v", got)
	}
	a2 := mustNew(t, 2, 140, 30, []int{60, 100})
	add(t, a2, 1, "b", false) // level 0 has no host at all
	got = loadsOf(t, a2)
	if fmt.Sprint(got) != "[0 100]" {
		t.Fatalf("panic skips empty level 0: %v", got)
	}
}

// Worked example from the specification.
func TestSpecExample(t *testing.T) {
	a := mustNew(t, 2, 120, 30, []int{60, 100})
	for i := 0; i < 5; i++ {
		add(t, a, 0, fmt.Sprintf("a%d", i), i < 2)
	}
	for i := 0; i < 2; i++ {
		add(t, a, 1, fmt.Sprintf("b%d", i), i < 1)
	}
	if got := loadsOf(t, a); fmt.Sprint(got) != "[48 52]" {
		t.Fatalf("step1: %v", got)
	}
	if err := a.SetHealth("a2", true); err != nil {
		t.Fatal(err)
	}
	if got := loadsOf(t, a); fmt.Sprint(got) != "[48 52]" {
		t.Fatalf("step2 hysteresis: %v", got)
	}
	if err := a.SetHealth("a3", true); err != nil {
		t.Fatal(err)
	}
	if got := loadsOf(t, a); fmt.Sprint(got) != "[60 40]" {
		t.Fatalf("step3 cap + redistribute: %v", got)
	}
	// Level 1 fully unhealthy: cur=0 immediately, S=96, scaling gives
	// [100,0], cap removes 40 and nobody is eligible to receive it.
	if err := a.SetHealth("b0", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Loads(); !errors.Is(err, ErrOutOfCapacity) {
		t.Fatalf("want ErrOutOfCapacity, got %v", err)
	}
	if a.cur[0] != 96 || a.cur[1] != 60 {
		t.Fatalf("rejected Loads must keep prior cur [96 60], got %v", a.cur)
	}
	if err := a.SetHealth("b0", true); err != nil {
		t.Fatal(err)
	}
	if got := loadsOf(t, a); fmt.Sprint(got) != "[60 40]" {
		t.Fatalf("restore: %v", got)
	}
}

func TestErrorOrderingAndStateRejection(t *testing.T) {
	a := mustNew(t, 2, 140, 30, []int{60, 100})
	if _, err := a.Loads(); !errors.Is(err, ErrNoHosts) {
		t.Fatalf("Loads empty: %v", err)
	}
	// PickLevel: r range first, then no-host, then capacity.
	if _, err := a.PickLevel(100); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("point checked first: %v", err)
	}
	if _, err := a.PickLevel(-1); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("negative point: %v", err)
	}
	if _, err := a.PickLevel(0); !errors.Is(err, ErrNoHosts) {
		t.Fatalf("no host second: %v", err)
	}
	// Hosts present but caps cannot hold 100 => capacity error.
	c := mustNew(t, 2, 140, 30, []int{60, 40})
	add(t, c, 0, "a", true)
	if _, err := c.Loads(); !errors.Is(err, ErrOutOfCapacity) {
		t.Fatalf("want capacity, got %v", err)
	}
	if _, err := c.PickLevel(0); !errors.Is(err, ErrOutOfCapacity) {
		t.Fatalf("pick capacity: %v", err)
	}
	if c.cur[0] != 0 {
		t.Fatalf("rejected allocation must not touch cur: %v", c.cur)
	}
	// Rejected mutations change nothing.
	before := len(c.hosts)
	if err := c.AddHost(9, "z", true); !errors.Is(err, ErrLevelOutOfRange) {
		t.Fatalf("add reject: %v", err)
	}
	if err := c.RemoveHost("ghost"); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("remove reject: %v", err)
	}
	if err := c.SetHealth("ghost", false); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("sethealth reject: %v", err)
	}
	if len(c.hosts) != before {
		t.Fatalf("rejected ops changed host set")
	}
}

// PickLevel uses half-open intervals [prefix, prefix+load_p).
func TestPickLevelBoundaries(t *testing.T) {
	a := mustNew(t, 3, 100, 100, []int{100, 100, 100})
	for i := 0; i < 100; i++ {
		add(t, a, 0, fmt.Sprintf("a%d", i), i < 20)
		add(t, a, 1, fmt.Sprintf("b%d", i), i < 30)
		add(t, a, 2, fmt.Sprintf("c%d", i), i < 50)
	}
	// Loads once to set cur [20,30,50]; every later PickLevel sees the
	// same shares (raw == cur).
	if got := loadsOf(t, a); fmt.Sprint(got) != "[20 30 50]" {
		t.Fatalf("setup: %v", got)
	}
	cases := map[int]int{
		0: 0, 19: 0,
		20: 1, 49: 1,
		50: 2, 99: 2,
	}
	for r, want := range cases {
		got, err := a.PickLevel(r)
		if err != nil {
			t.Fatalf("PickLevel(%d): %v", r, err)
		}
		if got != want {
			t.Fatalf("PickLevel(%d)=%d want %d", r, got, want)
		}
	}
	// A zero-share level is never returned.
	z := mustNew(t, 2, 1000, 100, []int{100, 100})
	add(t, z, 0, "a", true)
	add(t, z, 1, "b", true)
	for r := 0; r < 100; r++ {
		got, err := z.PickLevel(r)
		if err != nil || got != 0 {
			t.Fatalf("r=%d got=%d err=%v, want level 0", r, got, err)
		}
	}
}
