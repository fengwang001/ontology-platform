package specimen

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentPatientsAreIsolated(t *testing.T) {
	sys := NewSystem()
	setupProject(t, sys, "x", 100, false, 4)
	results := make([]ApplicationResult, 32)
	var applyWait sync.WaitGroup
	for index := range results {
		applyWait.Add(1)
		go func(index int) {
			defer applyWait.Done()
			result, err := sys.Apply(1, fmt.Sprintf("H%d", index), []string{"x"}, "P")
			if err != nil {
				t.Errorf("apply: %v", err)
				return
			}
			results[index] = result
		}(index)
	}
	applyWait.Wait()

	tubeIDs := make([]string, len(results))
	var collectWait sync.WaitGroup
	for index, result := range results {
		if len(result.ItemIDs) == 0 {
			continue
		}
		collectWait.Add(1)
		go func(index int, result ApplicationResult) {
			defer collectWait.Done()
			tubeID, err := sys.Collect(2, fmt.Sprintf("H%d", index), "tube", result.ItemIDs, 1)
			if err != nil {
				t.Errorf("collect: %v", err)
				return
			}
			tubeIDs[index] = tubeID
		}(index, result)
	}
	collectWait.Wait()

	var dispatchWait sync.WaitGroup
	for index, tubeID := range tubeIDs {
		if tubeID == "" {
			continue
		}
		dispatchWait.Add(1)
		go func(index int, tubeID string) {
			defer dispatchWait.Done()
			if err := sys.Dispatch(3, tubeID, TransportAmbient); err != nil {
				t.Errorf("dispatch: %v", err)
				return
			}
		}(index, tubeID)
	}
	dispatchWait.Wait()

	var signWait sync.WaitGroup
	for index, tubeID := range tubeIDs {
		if tubeID == "" {
			continue
		}
		signWait.Add(1)
		go func(index int, tubeID string) {
			defer signWait.Done()
			decisions, err := sys.Sign(4, tubeID, 0)
			if err != nil || len(decisions) != 1 || !decisions[0].Accepted {
				t.Errorf("sign: %+v %v", decisions, err)
			}
		}(index, tubeID)
	}
	signWait.Wait()
}
