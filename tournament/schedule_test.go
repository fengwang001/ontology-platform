package tournament

import (
	"fmt"
	"testing"
)

func naiveFixtures(teams, laps, round int) []Fixture {
	size := teams
	if size%2 == 1 {
		size++
	}
	firstLapRounds := size - 1
	sourceRound := round
	reversed := false
	if laps == 2 && round > firstLapRounds {
		sourceRound = round - firstLapRounds
		reversed = true
	}

	positions := make([]int, size)
	for index := range positions {
		positions[index] = index + 1
	}
	for rotation := 1; rotation < sourceRound; rotation++ {
		next := make([]int, size)
		next[0] = positions[0]
		next[1] = positions[size-1]
		copy(next[2:], positions[1:size-1])
		positions = next
	}

	fixtures := make([]Fixture, 0, size/2)
	for index := 0; index < size/2; index++ {
		a := positions[index]
		b := positions[size-1-index]
		home, away := a, b
		aIsHome := sourceRound%2 == 1
		if index >= 1 {
			aIsHome = (sourceRound+index)%2 == 0
		}
		if !aIsHome {
			home, away = away, home
		}
		if reversed {
			home, away = away, home
		}
		fixtures = append(fixtures, Fixture{
			Round: round,
			Home:  home,
			Away:  away,
			Bye:   teams%2 == 1 && (a == size || b == size),
		})
	}
	return fixtures
}

func TestScheduleMatchesNaiveCircleMethodForAllSizes(t *testing.T) {
	for teams := 2; teams <= 64; teams++ {
		for _, laps := range []int{1, 2} {
			t.Run(fmt.Sprintf("N%dL%d", teams, laps), func(t *testing.T) {
				tournament, err := New(teams, laps)
				if err != nil {
					t.Fatalf("New(%d,%d) error: %v", teams, laps, err)
				}

				size := teams + teams%2
				totalRounds := size - 1
				if laps == 2 {
					totalRounds *= 2
				}

				seen := make(map[string]int)
				byeCount := make(map[int]int)
				pendingMatches := 0
				for round := 1; round <= totalRounds; round++ {
					got := tournament.Fixtures(round)
					want := naiveFixtures(teams, laps, round)
					if !fixturesEqual(got, want) {
						t.Fatalf("round %d = %#v, want %#v", round, got, want)
					}

					appeared := make(map[int]bool)
					for _, fixture := range got {
						if fixture.Bye {
							realTeam := fixture.Home
							if realTeam == size {
								realTeam = fixture.Away
							}
							byeCount[realTeam]++
							seen[fmt.Sprintf("%d-bye-%d", round, realTeam)]++
							continue
						}
						pendingMatches++
						if appeared[fixture.Home] || appeared[fixture.Away] {
							t.Fatalf("team appears twice in round %d: %#v", round, got)
						}
						appeared[fixture.Home] = true
						appeared[fixture.Away] = true
						seen[fmt.Sprintf("%d-v-%d", fixture.Home, fixture.Away)]++
					}
				}

				if got := tournament.Pending(); got != pendingMatches {
					t.Fatalf("Pending = %d, want %d", got, pendingMatches)
				}
				for first := 1; first <= teams; first++ {
					if teams%2 == 1 && byeCount[first] != laps {
						t.Fatalf("team %d byes = %d, want %d", first, byeCount[first], laps)
					}
					if teams%2 == 0 && byeCount[first] != 0 {
						t.Fatalf("team %d has unexpected bye", first)
					}
					for second := first + 1; second <= teams; second++ {
						if laps == 1 {
							count := seen[fmt.Sprintf("%d-v-%d", first, second)] + seen[fmt.Sprintf("%d-v-%d", second, first)]
							if count != 1 {
								t.Fatalf("teams %d,%d meet %d times, want 1", first, second, count)
							}
						} else {
							if seen[fmt.Sprintf("%d-v-%d", first, second)] != 1 || seen[fmt.Sprintf("%d-v-%d", second, first)] != 1 {
								t.Fatalf("teams %d,%d missing home/away meeting: %#v", first, second, seen)
							}
						}
					}
				}
				t.Logf("input New(teams=%d,laps=%d), Fixtures(round=1..%d); output pending=%d byeCount=%d; 判定依据: 所有轮次与朴素圆法逐场相等，真实队每轮至多一场，双循环主客各一次，单循环相遇一次", teams, laps, totalRounds, pendingMatches, len(byeCount))
			})
		}
	}
}

func TestDetailedScheduleRulesN4N5N6(t *testing.T) {
	for _, teams := range []int{4, 5, 6} {
		t.Run(fmt.Sprintf("N%d", teams), func(t *testing.T) {
			tournament, err := New(teams, 1)
			if err != nil {
				t.Fatal(err)
			}

			for round := 1; round <= teams+teams%2-1; round++ {
				got := tournament.Fixtures(round)
				want := naiveFixtures(teams, 1, round)
				t.Logf("input New(teams=%d,laps=1), Fixtures(round=%d); output=%v; 判定依据: p[0]固定、按i取p[i]/p[m-1-i]，i=0看r奇偶，i>=1看r+i奇偶，朴素重生成=%v", teams, round, got, want)
				if !fixturesEqual(got, want) {
					t.Fatalf("round %d mismatch", round)
				}
			}
		})
	}
}

func TestCompleteTournamentTotalsN4N5N6(t *testing.T) {
	for _, teams := range []int{4, 5, 6} {
		for _, laps := range []int{1, 2} {
			t.Run(fmt.Sprintf("N%dL%d", teams, laps), func(t *testing.T) {
				tournament, err := New(teams, laps)
				if err != nil {
					t.Fatal(err)
				}

				size := teams + teams%2
				totalRounds := (size - 1) * laps
				decisive := 0
				draws := 0
				matches := 0
				for round := 1; round <= totalRounds; round++ {
					for index, fixture := range tournament.Fixtures(round) {
						if fixture.Bye {
							continue
						}
						homeGoals := (round + index) % 3
						awayGoals := round % 3
						if err := tournament.Record(round, fixture.Home, fixture.Away, homeGoals, awayGoals); err != nil {
							t.Fatal(err)
						}
						matches++
						if homeGoals == awayGoals {
							draws++
						} else {
							decisive++
						}
					}
				}

				totalPoints := 0
				totalGoalDiff := 0
				expectedPlayed := laps * (teams - 1)
				for team := 1; team <= teams; team++ {
					totalPoints += tournament.Points(team)
					totalGoalDiff += tournament.GoalDiff(team)
					if played := tournament.Played(team); played != expectedPlayed {
						t.Fatalf("team %d played %d, want %d", team, played, expectedPlayed)
					}
				}
				wantPoints := 3*decisive + 2*draws
				if totalPoints != wantPoints || totalGoalDiff != 0 || tournament.Pending() != 0 || matches != decisive+draws {
					t.Fatalf("totals points=%d want=%d, goalDiff=%d, pending=%d, matches=%d", totalPoints, wantPoints, totalGoalDiff, tournament.Pending(), matches)
				}
				t.Logf("input New(teams=%d,laps=%d) and record every non-bye fixture; output matches=%d decisive=%d draws=%d points=%d goalDiffSum=0 pending=0; 判定依据: 3*决胜+2*平局=%d，每队非轮空赛场次=%d", teams, laps, matches, decisive, draws, totalPoints, wantPoints, expectedPlayed)
			})
		}
	}
}

func fixturesEqual(left, right []Fixture) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
