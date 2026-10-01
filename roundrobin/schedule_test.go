package roundrobin

import (
	"reflect"
	"testing"
)

func TestFixedSchedulesCircleRotationAndHomeRules(t *testing.T) {
	testCases := []struct {
		name     string
		n        int
		expected [][]Fixture
	}{
		{
			name: "N4",
			n:    4,
			expected: [][]Fixture{
				{{Round: 1, Index: 0, Home: 1, Away: 4}, {Round: 1, Index: 1, Home: 2, Away: 3}},
				{{Round: 2, Index: 0, Home: 3, Away: 1}, {Round: 2, Index: 1, Home: 2, Away: 4}},
				{{Round: 3, Index: 0, Home: 1, Away: 2}, {Round: 3, Index: 1, Home: 3, Away: 4}},
			},
		},
		{
			name: "N5",
			n:    5,
			expected: [][]Fixture{
				{{Round: 1, Index: 0, Home: 1, Away: 6, Bye: true}, {Round: 1, Index: 1, Home: 2, Away: 5}, {Round: 1, Index: 2, Home: 4, Away: 3}},
				{{Round: 2, Index: 0, Home: 5, Away: 1}, {Round: 2, Index: 1, Home: 4, Away: 6, Bye: true}, {Round: 2, Index: 2, Home: 2, Away: 3}},
				{{Round: 3, Index: 0, Home: 1, Away: 4}, {Round: 3, Index: 1, Home: 5, Away: 3}, {Round: 3, Index: 2, Home: 2, Away: 6, Bye: true}},
				{{Round: 4, Index: 0, Home: 3, Away: 1}, {Round: 4, Index: 1, Home: 2, Away: 4}, {Round: 4, Index: 2, Home: 5, Away: 6, Bye: true}},
				{{Round: 5, Index: 0, Home: 1, Away: 2}, {Round: 5, Index: 1, Home: 3, Away: 6, Bye: true}, {Round: 5, Index: 2, Home: 5, Away: 4}},
			},
		},
		{
			name: "N6",
			n:    6,
			expected: [][]Fixture{
				{{Round: 1, Index: 0, Home: 1, Away: 6}, {Round: 1, Index: 1, Home: 2, Away: 5}, {Round: 1, Index: 2, Home: 4, Away: 3}},
				{{Round: 2, Index: 0, Home: 5, Away: 1}, {Round: 2, Index: 1, Home: 4, Away: 6}, {Round: 2, Index: 2, Home: 2, Away: 3}},
				{{Round: 3, Index: 0, Home: 1, Away: 4}, {Round: 3, Index: 1, Home: 5, Away: 3}, {Round: 3, Index: 2, Home: 2, Away: 6}},
				{{Round: 4, Index: 0, Home: 3, Away: 1}, {Round: 4, Index: 1, Home: 2, Away: 4}, {Round: 4, Index: 2, Home: 5, Away: 6}},
				{{Round: 5, Index: 0, Home: 1, Away: 2}, {Round: 5, Index: 1, Home: 3, Away: 6}, {Round: 5, Index: 2, Home: 5, Away: 4}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			registrar, err := NewRegistrar(tc.n, 1)
			if err != nil {
				t.Fatalf("NewRegistrar(%d, 1): %v", tc.n, err)
			}
			for round, want := range tc.expected {
				got := registrar.Fixtures(round + 1)
				t.Logf("input=%s round=%d output=%#v rule=positions mirror pairs with p0 fixed rotation", tc.name, round+1, got)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("round %d fixtures = %#v, want %#v", round+1, got, want)
				}
			}
		})
	}
}

func TestSchedulesMatchNaiveImplementationForEveryN(t *testing.T) {
	for n := 2; n <= 64; n++ {
		for _, laps := range []int{1, 2} {
			registrar, err := NewRegistrar(n, laps)
			if err != nil {
				t.Fatalf("N=%d L=%d: %v", n, laps, err)
			}
			want := naiveFixtures(n, laps)
			var got [][]Fixture
			for round := 1; round <= laps*(evenSize(n)-1); round++ {
				got = append(got, registrar.Fixtures(round))
			}
			t.Logf("input=N%d L%d output=%d rounds decision=compare against independent circle regeneration", n, laps, len(got))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("N=%d L=%d schedule differs from naive implementation", n, laps)
			}
		}
	}
}

func TestScheduleInvariantsAndSecondLapReversal(t *testing.T) {
	for n := 2; n <= 64; n++ {
		registrar, err := NewRegistrar(n, 2)
		if err != nil {
			t.Fatalf("N=%d: %v", n, err)
		}
		size := evenSize(n)
		meetings := map[[2]int]int{}
		byeCount := map[int]int{}

		for round := 1; round <= 2*(size-1); round++ {
			seen := map[int]bool{}
			for _, fixture := range registrar.Fixtures(round) {
				if fixture.Bye {
					real := fixture.Home
					if real > n {
						real = fixture.Away
					}
					byeCount[real]++
					continue
				}
				if seen[fixture.Home] || seen[fixture.Away] {
					t.Fatalf("N=%d round=%d has repeated team: %#v", n, round, fixture)
				}
				seen[fixture.Home] = true
				seen[fixture.Away] = true
				meetings[[2]int{fixture.Home, fixture.Away}]++
			}
		}

		for home := 1; home <= n; home++ {
			for away := 1; away <= n; away++ {
				if home != away && meetings[[2]int{home, away}] != 1 {
					t.Fatalf("N=%d missing/duplicate oriented fixture %d vs %d: %d", n, home, away, meetings[[2]int{home, away}])
				}
			}
			if n%2 == 1 && byeCount[home] != 2 {
				t.Fatalf("N=%d odd double round team %d byes = %d, want 2", n, home, byeCount[home])
			}
		}

		first := registrar.Fixtures(1)
		second := registrar.Fixtures(size)
		for i := range first {
			if first[i].Bye {
				continue
			}
			if second[i].Home != first[i].Away || second[i].Away != first[i].Home {
				t.Fatalf("N=%d second lap fixture %d = %#v, want reversal of %#v", n, i, second[i], first[i])
			}
		}
		t.Logf("input=N%d output=complete double round robin decision=each oriented pair once and byes valid", n)
	}
}

func naiveFixtures(n, laps int) [][]Fixture {
	size := evenSize(n)
	positions := make([]int, size)
	for i := range positions {
		positions[i] = i + 1
	}

	firstLap := make([][]Fixture, 0, size-1)
	for round := 1; round <= size-1; round++ {
		fixtures := make([]Fixture, size/2)
		for i := 0; i < size/2; i++ {
			a := positions[i]
			b := positions[size-1-i]
			home, away := a, b
			if (i == 0 && round%2 == 0) || (i > 0 && (round+i)%2 != 0) {
				home, away = away, home
			}
			isBye := n%2 == 1 && (a == size || b == size)
			fixtures[i] = Fixture{Round: round, Index: i, Home: home, Away: away, Bye: isBye}
		}
		firstLap = append(firstLap, fixtures)

		next := make([]int, size)
		next[0] = positions[0]
		next[1] = positions[size-1]
		copy(next[2:], positions[1:size-1])
		positions = next
	}

	all := firstLap
	if laps == 2 {
		for round, fixtures := range firstLap {
			reversed := make([]Fixture, len(fixtures))
			for i, fixture := range fixtures {
				reversed[i] = Fixture{Round: size + round, Index: i, Home: fixture.Away, Away: fixture.Home, Bye: fixture.Bye}
			}
			all = append(all, reversed)
		}
	}
	return all
}

func evenSize(n int) int {
	if n%2 == 0 {
		return n
	}
	return n + 1
}
