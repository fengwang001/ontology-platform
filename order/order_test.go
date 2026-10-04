package order

import "testing"

func TestNew(t *testing.T) {
	tests := []struct {
		name   string
		n      int
		b1     int
		c      int
		b2     int
		err    error
		kinds  []int
		teams  []int
		pickAt []int
	}{
		{
			name:   "example",
			n:      2,
			b1:     1,
			c:      2,
			b2:     1,
			kinds:  []int{Ban, Ban, Pick, Pick, Ban, Ban, Pick, Pick},
			teams:  []int{1, 2, 1, 2, 2, 1, 2, 1},
			pickAt: []int{-1, -1, 0, 1, -1, -1, 2, 3},
		},
		{
			name:   "no first bans or late picks",
			n:      1,
			b1:     0,
			c:      0,
			b2:     2,
			kinds:  []int{Ban, Ban, Ban, Ban, Pick, Pick},
			teams:  []int{1, 2, 1, 2, 1, 2},
			pickAt: []int{-1, -1, -1, -1, 0, 1},
		},
		{
			name:   "all picks before second ban stage",
			n:      1,
			b1:     1,
			c:      2,
			b2:     0,
			kinds:  []int{Ban, Ban, Pick, Pick},
			teams:  []int{1, 2, 1, 2},
			pickAt: []int{-1, -1, 0, 1},
		},
		{name: "n below range", n: 0, err: ErrInvalidArgument},
		{name: "b1 above range", n: 1, b1: 6, err: ErrInvalidArgument},
		{name: "c above range", n: 1, c: 3, err: ErrInvalidArgument},
		{name: "late ban after all picks", n: 1, c: 2, b2: 1, err: ErrInvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps, err := New(tt.n, tt.b1, tt.c, tt.b2)
			if err != tt.err {
				t.Fatalf("input n=%d b1=%d c=%d b2=%d: error = %v, want %v; reason validation mismatch", tt.n, tt.b1, tt.c, tt.b2, err, tt.err)
			}
			if err != nil {
				return
			}
			if len(steps) != len(tt.kinds) {
				t.Fatalf("input n=%d b1=%d c=%d b2=%d: output %d steps, want %d; reason generated step count", tt.n, tt.b1, tt.c, tt.b2, len(steps), len(tt.kinds))
			}
			for i, step := range steps {
				t.Logf("input n=%d b1=%d c=%d b2=%d step=%d output kind=%d team=%d pick=%d; reason generated order", tt.n, tt.b1, tt.c, tt.b2, i, step.Kind, step.Team, step.PickIndex)
				if step.Kind != tt.kinds[i] || step.Team != tt.teams[i] {
					t.Fatalf("step %d = %+v, want kind %d team %d; reason kind or acting team", i, step, tt.kinds[i], tt.teams[i])
				}
				wantPick := tt.pickAt[i]
				if step.Kind == Pick && step.PickIndex != wantPick {
					t.Fatalf("step %d pick index = %d, want %d; reason continuous pick indices", i, step.PickIndex, wantPick)
				}
			}
		})
	}
}

func TestPickTeams(t *testing.T) {
	tests := []struct {
		index int
		team  int
	}{
		{0, 1}, {1, 2}, {2, 2}, {3, 1}, {4, 1}, {5, 2}, {6, 2}, {7, 1}, {8, 1}, {9, 2},
	}
	steps, err := New(5, 0, 10, 0)
	if err != nil {
		t.Fatalf("input n=5 b1=0 c=10 b2=0: error %v; reason valid input", err)
	}
	for _, tt := range tests {
		got := steps[tt.index].Team
		t.Logf("input n=5 pick=%d output team=%d expected=%d; reason i modulo 4", tt.index, got, tt.team)
		if got != tt.team {
			t.Fatalf("pick %d team = %d, want %d", tt.index, got, tt.team)
		}
	}
}
