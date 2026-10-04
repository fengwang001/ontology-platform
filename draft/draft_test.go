package draft

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func newExample(t *testing.T) *Controller {
	t.Helper()
	c, err := New(2, 1, 2, 1, 30, [2]int{20, 20}, 10, 0)
	if err != nil {
		t.Fatalf("input n=2 b1=1 c=2 b2=1 T=30 B=20 H=10 now0=0 output error=%v; reason valid fixture", err)
	}
	return c
}

func TestExample(t *testing.T) {
	c := newExample(t)
	ops := []struct {
		name string
		call func() error
		want error
	}{
		{"Hover(5,0,5)", func() error { return c.Hover(5, 0, 5) }, nil},
		{"Ban(10,0,5)", func() error { return c.Ban(10, 0, 5) }, nil},
		{"Ban(45,2,7)", func() error { return c.Ban(45, 2, 7) }, nil},
		{"Pick(139,3,3)", func() error { return c.Pick(139, 3, 3) }, nil},
		{"Advance(259)", func() error { return c.Advance(259) }, nil},
	}
	for _, op := range ops {
		err := op.call()
		t.Logf("input %s output error=%v; reason fixture action", op.name, err)
		if err != op.want {
			t.Fatalf("%s error=%v want %v", op.name, err, op.want)
		}
	}
	s, err := c.State(259)
	if err != nil {
		t.Fatalf("input State(259) output error=%v", err)
	}
	t.Logf("input State(259) output step=%d team=%d reserve=%v picked=%+v bans=%v; reason one final pick still waiting", s.Step, s.Team, s.Reserve, s.Picked, s.Bans)
	if s.Step != 7 || s.Team != 1 || s.Reserve[0] != 0 || s.Reserve[1] != 0 {
		t.Fatalf("state at 259 = %+v, want step 7, team 1, zero reserves", s)
	}
	if err := c.Advance(260); err != nil {
		t.Fatalf("Advance(260) error=%v", err)
	}
	s, _ = c.State(260)
	wantPicked := []Selection{{0, 1}, {1, 4}, {2, 2}, {3, 3}}
	t.Logf("input Advance(260) output step=%d team=%d picked=%+v bans=%v; reason timeout X=260 completes draft", s.Step, s.Team, s.Picked, s.Bans)
	if s.Step != 8 || s.Team != 0 || len(s.Picked) != 4 {
		t.Fatalf("final state=%+v, want 8 completed steps", s)
	}
	for i, want := range wantPicked {
		if s.Picked[i] != want {
			t.Fatalf("picked %+v, want %+v; reason each player receives one hero", s.Picked, wantPicked)
		}
	}
}

func TestTimeoutEqualityAndClockBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		run       func(*testing.T, *Controller) *Controller
		wantStep  int
		wantTeam  int
		wantHero  int
		wantError error
		wantRes0  int
		wantRes1  int
	}{
		{
			name: "exact T uses no reserve",
			run: func(t *testing.T, c *Controller) *Controller {
				exact, err := New(2, 1, 2, 1, 30, [2]int{20, 20}, 10, 1)
				if err != nil {
					t.Fatalf("input now0=1 output %v", err)
				}
				if err := exact.Ban(31, 0, 9); err != nil {
					t.Fatalf("input Ban(31,0,9) with start=1 output %v", err)
				}
				return exact
			},
			wantStep: 1,
			wantTeam: 2,
			wantRes0: 20,
			wantRes1: 20,
		},
		{
			name: "reserve is consumed exactly to zero",
			run: func(t *testing.T, c *Controller) *Controller {
				c.Advance(50)
				return nil
			},
			wantStep: 1,
			wantTeam: 2,
			wantRes0: 0,
			wantRes1: 20,
		},
		{
			name: "zero reserve timeout is s plus T",
			run: func(t *testing.T, c *Controller) *Controller {
				c.Advance(100)
				c.Advance(129)
				return nil
			},
			wantStep: 2,
			wantTeam: 1,
			wantRes1: 0,
		},
		{
			name: "equality is timeout and wrong kind rejected",
			run: func(t *testing.T, c *Controller) *Controller {
				c.Hover(5, 0, 5)
				c.Ban(10, 0, 5)
				c.Ban(45, 2, 7)
				err := c.Pick(140, 3, 3)
				if !errors.Is(err, ErrWrongStepKind) {
					t.Fatalf("input Pick(140,3,3) output %v; reason equality timeout completed pick and advanced to ban", err)
				}
				s, _ := c.State(140)
				if s.Step != 4 || s.Team != 2 || len(s.Picked) != 2 {
					t.Fatalf("output state=%+v; reason timeout picks player 2 hero 2 then exposes ban", s)
				}
				if s.Picked[0].Hero != 1 || s.Picked[1].Hero != 2 {
					t.Fatalf("output picked=%+v; reason preferred 5 banned and minimum available fallback", s.Picked)
				}
				return nil
			},
			wantStep: 4,
			wantTeam: 2,
			wantRes0: 0,
			wantRes1: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newExample(t)
			if actual := tt.run(t, c); actual != nil {
				c = actual
			}
			s, err := c.State(c.lastNow)
			if err != nil {
				t.Fatalf("State error=%v", err)
			}
			t.Logf("input case=%s output step=%d team=%d reserve=%v picked=%+v bans=%v; reason %s", tt.name, s.Step, s.Team, s.Reserve, s.Picked, s.Bans, tt.name)
			if s.Step != tt.wantStep || s.Team != tt.wantTeam {
				t.Fatalf("state=%+v, want step %d team %d", s, tt.wantStep, tt.wantTeam)
			}
			if s.Reserve[0] != tt.wantRes0 || s.Reserve[1] != tt.wantRes1 {
				t.Fatalf("reserve=%v, want [%d %d]", s.Reserve, tt.wantRes0, tt.wantRes1)
			}
		})
	}
}

func TestRejectionOrderAndNoSideEffects(t *testing.T) {
	tests := []struct {
		name string
		call func(*Controller) error
		want error
	}{
		{"invalid player before rewind", func(c *Controller) error { return c.Pick(-1, -1, 1) }, ErrInvalidArgument},
		{"invalid hero before rewind", func(c *Controller) error { return c.Ban(-1, 0, 0) }, ErrInvalidArgument},
		{"rewind after accepted", func(c *Controller) error { c.Advance(20); return c.Advance(19) }, ErrClockRewound},
		{"finished before kind", func(c *Controller) error { c.Advance(1000); return c.Ban(1000, 0, 1) }, ErrFinished},
		{"wrong kind before team", func(c *Controller) error { return c.Pick(0, 2, 1) }, ErrWrongStepKind},
		{"wrong team before picked", func(c *Controller) error {
			c.Ban(10, 0, 9)
			c.Ban(20, 2, 8)
			return c.Pick(20, 2, 1)
		}, ErrWrongTeam},
		{"picked before hero", func(c *Controller) error {
			c.Ban(10, 0, 9)
			c.Ban(20, 2, 8)
			c.Pick(30, 0, 1)
			c.Pick(40, 2, 2)
			c.Ban(50, 2, 3)
			c.Ban(60, 1, 4)
			c.Pick(70, 3, 5)
			return c.Pick(80, 0, 6)
		}, ErrPlayerPicked},
		{"unavailable hero", func(c *Controller) error {
			c.Ban(20, 0, 6)
			return c.Ban(50, 2, 6)
		}, ErrHeroUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newExample(t)
			err := tt.call(c)
			s, _ := c.State(c.lastNow)
			t.Logf("input case=%s output error=%v state step=%d reserve=%v bans=%v; reason rejection priority and rollback", tt.name, err, s.Step, s.Reserve, s.Bans)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error=%v want %v", err, tt.want)
			}
		})
	}

	c := newExample(t)
	before, _ := c.State(0)
	err := c.Ban(100, 0, 999)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid late Ban returned %v; reason parameters precede clock", err)
	}
	after, _ := c.State(0)
	t.Logf("input invalid Ban(100,0,999) before=%+v after=%+v output; reason rejected operation changes nothing", before, after)
	if after.Step != before.Step || !reflect.DeepEqual(after.Reserve, before.Reserve) {
		t.Fatalf("rejected operation changed state")
	}

	c = newExample(t)
	c.Advance(100)
	timeoutState, _ := c.State(100)
	err = c.Ban(100, 0, 4)
	rolled, _ := c.State(100)
	t.Logf("input late wrong-kind Pick output error=%v timeoutState=%+v rolled=%+v; reason entrance catch-up is rolled back", err, timeoutState, rolled)
	if !errors.Is(err, ErrWrongStepKind) || rolled.Step != timeoutState.Step {
		t.Fatalf("late rejection must not retain entrance processing")
	}
}

func TestSpecialShapes(t *testing.T) {
	tests := []struct {
		name            string
		n, b1, c, b2, h int
		firstTeam       int
	}{
		{"c zero second stage starts team one", 2, 1, 0, 1, 10, 1},
		{"c full has no second bans", 2, 0, 4, 0, 10, 1},
		{"c six starts team two for n five", 5, 0, 6, 1, 20, 2},
		{"no bans", 3, 0, 3, 0, 10, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(tt.n, tt.b1, tt.c, tt.b2, 10, [2]int{0, 0}, tt.h, 0)
			if err != nil {
				t.Fatalf("input %+v output error=%v", tt, err)
			}
			s, _ := c.State(0)
			index := 2*tt.b1 + tt.c
			want := s.Team
			if tt.b2 > 0 {
				want = c.steps[index].Team
			}
			t.Logf("input n=%d b1=%d c=%d b2=%d output first team=%d step count=%d; reason special ordering", tt.n, tt.b1, tt.c, tt.b2, s.Team, len(c.steps))
			if tt.b2 > 0 && want != tt.firstTeam {
				t.Fatalf("second ban team=%d want %d", want, tt.firstTeam)
			}
		})
	}
}

func TestAutoPickTouchedBound(t *testing.T) {
	for _, heroCount := range []int{64, 65536} {
		t.Run(fmt.Sprintf("H=%d", heroCount), func(t *testing.T) {
			c, err := New(5, 5, 0, 5, 10, [2]int{100, 100}, heroCount, 0)
			if err != nil {
				t.Fatalf("input H=%d output error=%v", heroCount, err)
			}
			heroes := c.heroes
			for hero := 1; hero <= 20 && hero <= heroCount; hero++ {
				team := 1
				if hero%2 == 0 {
					team = 2
				}
				if hero%3 == 0 {
					_ = heroes.Ban(hero, team)
				} else {
					_ = heroes.Pick(hero, team, (hero-1)%(2*c.n))
				}
			}
			chosen, touched, ok := heroes.Choose(1)
			t.Logf("input H=%d occupied=20 preferred=1 output hero=%d touched=%d ok=%v; reason auto pick scans records until first gap, not hero IDs", heroCount, chosen, touched, ok)
			if !ok || chosen != 21 || touched > 40 {
				t.Fatalf("H=%d Choose=(%d,%d,%v), want hero 21 touched <= 40", heroCount, chosen, touched, ok)
			}
		})
	}
}

func TestConcurrentOperations(t *testing.T) {
	c := newExample(t)
	if err := c.Hover(5, 0, 5); err != nil {
		t.Fatal(err)
	}
	if err := c.Ban(10, 0, 5); err != nil {
		t.Fatal(err)
	}
	if err := c.Ban(45, 2, 7); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 64; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			switch worker % 4 {
			case 0:
				_, _ = c.State(139)
			case 1:
				_ = c.Advance(139)
			case 2:
				_ = c.Hover(139, worker%4, 1+worker%c.h)
			default:
				_ = c.Pick(139, 3, 3)
			}
		}(worker)
	}
	wg.Wait()
	s, _ := c.State(139)
	t.Logf("input 64 concurrent operations at now=139 output step=%d picked=%+v reserve=%v; reason single mutex gives a serial equivalent", s.Step, s.Picked, s.Reserve)
	if s.Step != 4 || s.Team != 2 || len(s.Picked) != 2 {
		t.Fatalf("concurrent state=%+v, want automatic and accepted picks at ban step 4", s)
	}
}
