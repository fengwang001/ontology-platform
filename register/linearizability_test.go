package register

import (
	"math/rand"
	"testing"
)

func TestRandomDifferentialAgainstExhaustive(t *testing.T) {
	const casesPerSize = 80
	rng := rand.New(rand.NewSource(983))

	for n := 1; n <= 8; n++ {
		for caseIndex := 0; caseIndex < casesPerSize; caseIndex++ {
			ops := randomHistory(rng, n)
			got, witness, reason := findWitness(ops)
			want := exhaustiveLinearizable(ops)

			t.Logf("n=%d case=%d\ninput=%v\noutput={linearizable:%t witness:%v} reason=%q reference=%t",
				n, caseIndex, ops, got, witness, reason, want)

			if got != want {
				t.Fatalf("n=%d case=%d: checker = %t, exhaustive reference = %t", n, caseIndex, got, want)
			}
			if got && !validWitness(ops, witness) {
				t.Fatalf("n=%d case=%d: returned witness %v is not a valid proof", n, caseIndex, witness)
			}
		}
	}
}

func TestDeterministicWitnessForSameSnapshot(t *testing.T) {
	rng := rand.New(rand.NewSource(20260930))
	ops := randomHistory(rng, 8)
	first, firstWitness, _ := findWitness(ops)

	for i := 0; i < 20; i++ {
		got, witness, _ := findWitness(ops)
		if got != first || !equalIntSlices(witness, firstWitness) {
			t.Fatalf("run %d = (%t, %v), want (%t, %v)", i, got, witness, first, firstWitness)
		}
	}
}

func TestConcurrentBeginEndAndCheck(t *testing.T) {
	c := NewChecker()
	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			result := c.Check()
			if result.Linearizable && !validWitness(c.Snapshot(), result.Witness) {
				t.Errorf("concurrent check returned an invalid witness: %v", result.Witness)
				return
			}
		}
	}()

	for clientIndex := 0; clientIndex < 4; clientIndex++ {
		client := string(rune('A' + clientIndex))
		for i := 0; i < 5; i++ {
			invokeTime := clientIndex*20 + i*2
			op := Op{Kind: KindWrite, Value: invokeTime}
			var result *EndResult

			id, err := c.Begin(client, op, invokeTime)
			if err != nil {
				t.Errorf("Begin: %v", err)
				<-done
				return
			}
			if err := c.End(id, invokeTime+1, result); err != nil {
				t.Errorf("End: %v", err)
				<-done
				return
			}
		}
	}

	<-done
	final := logCheck(t, c, "concurrent access final snapshot")
	t.Logf("concurrent final input=%v", c.Snapshot())
	if !final.Linearizable || !validWitness(c.Snapshot(), final.Witness) {
		t.Fatalf("final concurrent history result = %+v", final)
	}
}

func randomHistory(rng *rand.Rand, n int) []Operation {
	ops := make([]Operation, 0, n)
	for i := 0; i < n; i++ {
		invokeTime := rng.Intn(6)
		completed := rng.Intn(5) != 0

		op := Operation{
			ID:         i + 1,
			Kind:       OpKind(1 + rng.Intn(3)),
			InvokeTime: invokeTime,
			Completed:  completed,
		}

		if completed && rng.Intn(3) == 0 {
			op.ReturnTime = invokeTime
		} else if completed {
			op.ReturnTime = invokeTime + rng.Intn(4)
		}

		switch op.Kind {
		case KindWrite:
			op.Value = rngValue(rng, 4)
		case KindRead:
			op.ReadValue = rngValue(rng, 4)
		case KindCAS:
			op.Expected = rngValue(rng, 4)
			op.New = rngValue(rng, 4)
			if completed {
				op.CASSucceeded = rng.Intn(2) == 0
			}
		}
		ops = append(ops, op)
	}
	return ops
}

func rngValue(rng *rand.Rand, maxExclusive int) int {
	return int('a') + rng.Intn(maxExclusive)
}

func validWitness(ops []Operation, witness []int) bool {
	byID := make(map[int]Operation, len(ops))
	placed := make(map[int]bool, len(witness))
	completedCount := 0

	for _, op := range ops {
		byID[op.ID] = op
		if op.Completed {
			completedCount++
		}
	}

	completedPlaced := 0
	currentValue := 0
	for position, id := range witness {
		op, ok := byID[id]
		if !ok || placed[id] {
			return false
		}
		if !op.Completed && op.Kind == KindRead {
			return false
		}

		for _, blocker := range ops {
			if blocker.ID == id || !blocker.Completed || placed[blocker.ID] {
				continue
			}
			if blocker.ReturnTime < op.InvokeTime {
				return false
			}
		}

		nextValue, ok := applyOperation(op, currentValue)
		if !ok {
			return false
		}

		currentValue = nextValue
		placed[id] = true
		if op.Completed {
			completedPlaced++
		}

		if position+1 == len(witness) && completedPlaced != completedCount {
			return false
		}
	}

	return completedPlaced == completedCount
}

func equalIntSlices(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
