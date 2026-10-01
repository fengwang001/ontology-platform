package swiss

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
)

func TestRandomTournamentsMatchNaive(t *testing.T) {
	random := rand.New(rand.NewPCG(1068, 20261001))
	for trial := 0; trial < 2000; trial++ {
		trialNumber := trial + 1
		maxRounds := random.IntN(5) + 1
		actual, actualErr := New(maxRounds)
		reference := newNaive(maxRounds)
		if actualErr != nil {
			t.Fatalf("trial %d: New(%d): %v", trial+1, maxRounds, actualErr)
		}

		playerCount := random.IntN(7) + 2
		names := make([]string, playerCount)
		t.Logf("trial=%d input=New(%d) output=(tournament,nil) basis=R in [1,20]", trialNumber, maxRounds)
		for index := range names {
			names[index] = fmt.Sprintf("trial-%d-player-%d", trialNumber, index+1)
			seed, err := actual.Register(names[index])
			refSeed, refErr := reference.register(names[index])
			t.Logf("trial=%d input=Register(%q) output=(seed=%d,err=%v) reference=(seed=%d,err=%v) basis=unused non-empty unique names",
				trialNumber, names[index], seed, err, refSeed, refErr)
			if err != refErr || seed != refSeed {
				t.Fatalf("trial %d Register mismatch", trialNumber)
			}
		}

		invalidName := ""
		if random.IntN(2) == 0 {
			invalidName = names[0]
		}
		seed, err := actual.Register(invalidName)
		refSeed, refErr := reference.register(invalidName)
		t.Logf("trial=%d input=Register(%q) output=(seed=%d,err=%v) reference=(seed=%d,err=%v) basis=pre-start empty-name or duplicate check",
			trialNumber, invalidName, seed, err, refSeed, refErr)
		if err != refErr || seed != refSeed {
			t.Fatalf("trial %d invalid Register mismatch", trialNumber)
		}

		for round := 1; round <= maxRounds+1; round++ {
			pairs, bye, err := actual.Pair()
			refPairs, refBye, refErr := reference.pair()
			t.Logf("trial=%d input=Pair(round=%d) output=(pairs=%v,bye=%d,err=%v) reference=(pairs=%v,bye=%d,err=%v) basis=bye then score/seed order and first unplayed opponent",
				trialNumber, round, pairs, bye, err, refPairs, refBye, refErr)
			if !sameError(err, refErr) {
				t.Fatalf("trial %d round %d Pair error = %v, want %v", trialNumber, round, err, refErr)
			}
			if err != nil {
				compareStandings(t, trialNumber, actual, reference)
				break
			}
			if !pairsEqual(pairs, refPairs) || bye != refBye {
				t.Fatalf("trial %d round %d Pair = %v,%d; want %v,%d",
					trialNumber, round, pairs, bye, refPairs, refBye)
			}

			duplicatePairs, duplicateBye, duplicateErr := actual.Pair()
			refDuplicatePairs, refDuplicateBye, refDuplicateErr := reference.pair()
			t.Logf("trial=%d input=Pair(while-pending) output=(pairs=%v,bye=%d,err=%v) reference=(pairs=%v,bye=%d,err=%v) basis=pending games reject Pair before round-count check",
				trialNumber, duplicatePairs, duplicateBye, duplicateErr, refDuplicatePairs, refDuplicateBye, refDuplicateErr)
			if !sameError(duplicateErr, refDuplicateErr) || len(duplicatePairs) != len(refDuplicatePairs) {
				t.Fatalf("trial %d pending Pair mismatch", trialNumber)
			}

			if random.IntN(3) == 0 {
				var badA, badB, badResult int
				switch random.IntN(3) {
				case 0:
					badA = random.IntN(playerCount) + 1
					badB = random.IntN(playerCount) + 1
					badResult = 3 + random.IntN(2)
				case 1:
					badA = random.IntN(playerCount) + 1
					badB = badA
					badResult = random.IntN(3)
				default:
					if bye == -1 {
						badA = random.IntN(playerCount) + 1
						badB = badA
					} else {
						badA = bye
						badB = 1
						if badB == badA {
							badB = 2
						}
					}
					badResult = random.IntN(3)
				}
				beforeStandings := actual.Standings()
				err := actual.Report(badA, badB, badResult)
				refErr := reference.report(badA, badB, badResult)
				t.Logf("trial=%d input=Report(%d,%d,%d) output=err=%v reference=err=%v basis=random report probe; rejected probe must not change state",
					trialNumber, badA, badB, badResult, err, refErr)
				if !sameError(err, refErr) {
					t.Fatalf("trial %d rejected Report mismatch", trialNumber)
				}
				if err != nil {
					if afterStandings := actual.Standings(); !reflect.DeepEqual(afterStandings, beforeStandings) {
						t.Fatalf("trial %d rejected Report changed standings", trialNumber)
					}
				}
			}

			orderedPairs := append([]Pairing(nil), pairs...)
			random.Shuffle(len(orderedPairs), func(i, j int) {
				orderedPairs[i], orderedPairs[j] = orderedPairs[j], orderedPairs[i]
			})
			results := make(map[[2]int]int, len(pairs))
			for _, pair := range orderedPairs {
				result := random.IntN(3)
				if random.IntN(2) == 0 {
					err = actual.Report(pair.X, pair.Y, result)
					refErr = reference.report(pair.X, pair.Y, result)
				} else {
					err = actual.Report(pair.Y, pair.X, result)
					refErr = reference.report(pair.Y, pair.X, result)
				}
				results[gameKey(pair.X, pair.Y)] = result
				t.Logf("trial=%d input=Report(%d,%d,%d) output=err=%v reference=err=%v basis=canonical game=%v; orientation reverses score",
					trialNumber, pair.X, pair.Y, result, err, refErr, gameKey(pair.X, pair.Y))
				if !sameError(err, refErr) {
					t.Fatalf("trial %d Report mismatch", trialNumber)
				}

				pairX, pairY := pair.X, pair.Y
				err = actual.Report(pairX, pairY, result)
				refErr = reference.report(pairX, pairY, result)
				t.Logf("trial=%d input=Report(%d,%d,%d) output=err=%v reference=err=%v basis=already-reported check after accepting the game once",
					trialNumber, pairX, pairY, result, err, refErr)
				if !sameError(err, refErr) {
					t.Fatalf("trial %d duplicate Report mismatch", trialNumber)
				}
				compareStandings(t, trialNumber, actual, reference)
			}

			compareStandings(t, trialNumber, actual, reference)
			verifyInvariants(t, trialNumber, round, actual)

			_, postRegisterErr := actual.Register(fmt.Sprintf("trial-%d-late", trialNumber))
			_, postRefRegisterErr := reference.register(fmt.Sprintf("trial-%d-late", trialNumber))
			if !sameError(postRegisterErr, postRefRegisterErr) {
				t.Fatalf("trial %d late Register mismatch", trialNumber)
			}
			t.Logf("trial=%d input=Register(late) output=err=%v reference=err=%v basis=first successful Pair closes registration",
				trialNumber, postRegisterErr, postRefRegisterErr)
		}
	}
}

func pairsEqual(actual []Pairing, reference []naivePairing) bool {
	if len(actual) != len(reference) {
		return false
	}
	for index := range actual {
		if actual[index].X != reference[index].x || actual[index].Y != reference[index].y {
			return false
		}
	}
	return true
}

func verifyInvariants(t *testing.T, trial int, rounds int, tournament *Tournament) {
	t.Helper()
	scoreSum := 0
	gameCount := 0
	byeCount := 0
	for _, score := range tournament.scores {
		scoreSum += score
	}
	for _, bye := range tournament.byes {
		if bye {
			byeCount++
		}
	}
	for range tournament.played {
		gameCount++
	}
	if scoreSum != 2*gameCount+2*byeCount {
		t.Fatalf("trial %d after %d rounds: score sum %d != 2*games(%d)+2*byes(%d)",
			trial, rounds, scoreSum, gameCount, byeCount)
	}
}

func compareStandings(t *testing.T, trial int, actual *Tournament, reference *naiveTournament) {
	t.Helper()
	actualStandings := actual.Standings()
	referenceStandings := reference.standings()
	converted := make([]Standing, len(referenceStandings))
	for index, standing := range referenceStandings {
		converted[index] = Standing{
			Seed:          standing.seed,
			Score:         standing.score,
			OpponentScore: standing.opponentScore,
		}
	}
	if !reflect.DeepEqual(actualStandings, converted) {
		t.Fatalf("trial %d: standings = %#v, want %#v", trial, actualStandings, converted)
	}
}

func sameError(actual error, want error) bool {
	return errors.Is(actual, want)
}
