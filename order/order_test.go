package order

import "testing"

func TestPickTeam(t *testing.T) {
	cases := []struct {
		i    int
		want int
	}{
		{0, 1}, {1, 2}, {2, 2}, {3, 1},
		{4, 1}, {5, 2}, {6, 2}, {7, 1},
		{8, 1}, {9, 2},
	}
	for _, tc := range cases {
		if got := PickTeam(tc.i); got != tc.want {
			t.Fatalf("PickTeam(%d)=%d want %d", tc.i, got, tc.want)
		}
	}
}

func teams(steps []Step) []int {
	out := make([]int, len(steps))
	for i, s := range steps {
		out[i] = s.Team
	}
	return out
}

func kinds(steps []Step) []Kind {
	out := make([]Kind, len(steps))
	for i, s := range steps {
		out[i] = s.Kind
	}
	return out
}

func eqInt(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func eqKind(a, b []Kind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBuild(t *testing.T) {
	cases := []struct {
		name      string
		cfg       Config
		wantTeams []int
		wantKinds []Kind
	}{
		{
			name:      "example_n2_b1_1_c_2_b2_1",
			cfg:       Config{N: 2, B1: 1, C: 2, B2: 1},
			wantTeams: []int{1, 2, 1, 2, 2, 1, 2, 1},
			wantKinds: []Kind{KindBan, KindBan, KindPick, KindPick, KindBan, KindBan, KindPick, KindPick},
		},
		{
			name:      "n5_c6_b2_first_team2",
			cfg:       Config{N: 5, B1: 0, C: 6, B2: 2},
			wantTeams: []int{1, 2, 2, 1, 1, 2, 2, 1, 2, 1, 2, 1, 1, 2},
			wantKinds: []Kind{KindPick, KindPick, KindPick, KindPick, KindPick, KindPick,
				KindBan, KindBan, KindBan, KindBan, KindPick, KindPick, KindPick, KindPick},
		},
		{
			name:      "c0_bans_first_then_picks",
			cfg:       Config{N: 1, B1: 1, C: 0, B2: 1},
			wantTeams: []int{1, 2, 1, 2, 1, 2},
			wantKinds: []Kind{KindBan, KindBan, KindBan, KindBan, KindPick, KindPick},
		},
		{
			name:      "c_full_b2_zero",
			cfg:       Config{N: 2, B1: 2, C: 4, B2: 0},
			wantTeams: []int{1, 2, 1, 2, 1, 2, 2, 1},
			wantKinds: []Kind{KindBan, KindBan, KindBan, KindBan, KindPick, KindPick, KindPick, KindPick},
		},
		{
			name:      "no_bans",
			cfg:       Config{N: 3, B1: 0, C: 3, B2: 0},
			wantTeams: []int{1, 2, 2, 1, 1, 2},
			wantKinds: []Kind{KindPick, KindPick, KindPick, KindPick, KindPick, KindPick},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Build(tc.cfg)
			if !eqInt(teams(got), tc.wantTeams) {
				t.Fatalf("teams=%v want %v", teams(got), tc.wantTeams)
			}
			if !eqKind(kinds(got), tc.wantKinds) {
				t.Fatalf("kinds=%v want %v", kinds(got), tc.wantKinds)
			}
		})
	}
}
