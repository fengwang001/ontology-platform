// Command demo exercises the version-conditioned upsert/tombstone table.
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var failed atomic.Bool

func check(name string, ok bool) {
	if !ok {
		failed.Store(true)
	}
	fmt.Printf("%s: %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

func snap(t *api.Table, keys []string) (rows, tombs []string) {
	for _, k := range keys {
		if r, ok := t.Get(k); ok {
			rows = append(rows, k+":"+r.Val+"@"+strconv.FormatInt(r.Ver, 10))
		}
		if v, ok := t.Tomb(k); ok {
			tombs = append(tombs, k+"@"+strconv.FormatInt(v, 10))
		}
	}
	slices.Sort(rows)
	slices.Sort(tombs)
	return rows, tombs
}

func main() {
	// Section 3: ten single-event batches. Step5 has Ver==cur (ignored),
	// step3 is blocked by k1's tombstone, step7 purges that tombstone
	// (18-8>=10), so step8's old U k1 v7 applies and k1 ends as f@7.
	evs := []api.Event{
		{Op: 'U', Key: "k1", Val: "a", Ver: 5}, {Op: 'D', Key: "k1", Ver: 8},
		{Op: 'U', Key: "k1", Val: "b", Ver: 6}, {Op: 'U', Key: "k2", Val: "c", Ver: 12},
		{Op: 'U', Key: "k1", Val: "d", Ver: 8}, {Op: 'D', Key: "k3", Ver: 15},
		{Op: 'U', Key: "k2", Val: "e", Ver: 18}, {Op: 'U', Key: "k1", Val: "f", Ver: 7},
		{Op: 'U', Key: "k3", Val: "g", Ver: 14}, {Op: 'U', Key: "k4", Val: "h", Ver: 30},
	}
	wantR := [][]string{{"k1:a@5"}, {}, {}, {"k2:c@12"}, {"k2:c@12"}, {"k2:c@12"},
		{"k2:e@18"}, {"k1:f@7", "k2:e@18"}, {"k1:f@7", "k2:e@18"},
		{"k1:f@7", "k2:e@18", "k4:h@30"}}
	wantT := [][]string{{}, {"k1@8"}, {"k1@8"}, {"k1@8"}, {"k1@8"},
		{"k1@8", "k3@15"}, {"k3@15"}, {"k3@15"}, {"k3@15"}, {}}
	tb, err := api.New(10, 100)
	tenOK := err == nil
	for i, e := range evs {
		tenOK = tenOK && tb.Apply([]api.Event{e}) == nil
		rows, tombs := snap(tb, []string{"k1", "k2", "k3", "k4"})
		tenOK = tenOK && slices.Equal(rows, wantR[i]) && slices.Equal(tombs, wantT[i])
	}
	ign, g := tb.Stats()
	check("ten-steps: step3 blocked, step5 Ver==cur ignored, step7 purge, step8 k1=f@7",
		tenOK && ign == 3 && g == 30)

	// SelfCheck: four invariants, random reordered streams vs the naive
	// reference, three distinguishable errors, no trace after rejection,
	// and purge inspection cost O(1) in the tombstone count.
	st, err := api.New(10, 100)
	check("selfcheck (invariants, naive-match, errors, no-trace, O(1) purge)",
		err == nil && st.SelfCheck() == nil)

	// Errors are distinguishable and a rejected batch leaves no trace.
	small, _ := api.New(10, 1)
	_ = small.Apply([]api.Event{{Op: 'U', Key: "a", Val: "1", Ver: 1}})
	errOK := errors.Is(small.Apply([]api.Event{{Op: '?', Key: "x", Ver: 2}}), api.ErrInvalidEvent)
	errOK = errOK && errors.Is(small.Apply([]api.Event{{Op: 'U', Key: "b", Ver: 2}}), api.ErrTooManyKeys)
	_, err = api.New(0, 1)
	errOK = errOK && errors.Is(err, api.ErrInvalidParam)
	ig2, g2 := small.Stats()
	_, hadB := small.Get("b")
	check("3 sentinel errors + rejected batch leaves no trace",
		errOK && ig2 == 0 && g2 == 1 && !hadB)

	// One writer, N readers: GetMany never shows a half-applied batch.
	ks := []string{"k1", "k2", "k3", "k4", "k5", "k6", "k7", "k8"}
	cs, _ := api.New(1<<60, 10000)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for v := int64(1); v <= 200; v++ {
			b := make([]api.Event, len(ks))
			for i, k := range ks {
				b[i] = api.Event{Op: 'U', Key: k, Val: "v", Ver: v}
			}
			_ = cs.Apply(b)
		}
	}()
	concOK := atomic.Bool{}
	concOK.Store(true)
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 2000; n++ {
				v := int64(-1)
				for _, row := range cs.GetMany(ks) {
					if v < 0 {
						v = row.Ver
					} else if row.Ver != v {
						concOK.Store(false)
					}
				}
			}
		}()
	}
	wg.Wait()
	check("concurrent GetMany never sees a half batch", concOK.Load())

	if failed.Load() {
		os.Exit(1)
	}
}
