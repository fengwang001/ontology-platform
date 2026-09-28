// Command slidingwindow-demo drives the sliding window maximum component
// through tied-max eviction and a decreasing run, logging every decision.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"

	"ontology/slidingwindow"
)

func main() {
	w, err := slidingwindow.New(4, slidingwindow.WithLogger(os.Stdout))
	if err != nil {
		panic(err)
	}

	// Tied maxima, then eviction of the older copy of the tie.
	for _, v := range []float64{7, 7, 3, 2} {
		_, _, err := w.Append(v)
		must(err)
	}
	_, _, err = w.Append(5)
	must(err)        // evicts the older 7; the surviving 7 stays on top
	_, _ = w.Evict() // drops the surviving 7; 5 becomes the maximum

	// Monotonic decreasing run: no element is displaced.
	for _, v := range []float64{9, 8, 6, 4} {
		_, _, err := w.Append(v)
		must(err)
	}

	// Atomic batch: the NaN rejects the entire batch.
	if err := w.BatchAppend([]float64{1, math.NaN(), 2}); err != nil {
		fmt.Fprintf(os.Stdout, "batch rejected as expected: %v\n", err)
	}

	values, max, _ := w.Snapshot()
	fmt.Fprintf(os.Stdout, "final window=%v max=%v moves=%d\n", values, max, w.Moves())

	// Drain to demonstrate the empty-window error.
	for w.Len() > 0 {
		_, _ = w.Evict()
	}
	if _, err := w.Max(); errors.Is(err, slidingwindow.ErrEmptyWindow) {
		fmt.Fprintf(os.Stdout, "empty window rejected as expected: %v\n", err)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
