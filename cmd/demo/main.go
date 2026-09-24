// Command demo verifies the deterministic-replay + LWW compaction engine.
// It reads no arguments and performs no network access; exit code 0 means OK.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"sync"

	"ontology/api"
	"ontology/lww"
)

var failed bool

func check(name string, ok bool, detail ...any) {
	if ok {
		fmt.Printf("OK %s\n", name)
	} else {
		failed = true
		fmt.Printf("FAIL %s %v\n", name, detail)
	}
}

func main() {
	seq := []api.Change{
		{Key: "a", Ver: 10, Val: 100}, {Key: "b", Ver: 5, Val: 50},
		{Key: "a", Ver: 10, Val: 200}, {Key: "c", Ver: 7, Val: 70},
		{Key: "a", Ver: 5, Val: 50}, {Key: "b", Ver: 12, Val: 90},
		{Key: "c", Ver: 7, Val: 77},
	}
	wantSteps := []string{"a100", "a100 b50", "a200 b50", "a200 b50 c70",
		"a200 b50 c70", "a200 b90 c70", "a200 b90 c77"}
	eng := api.New(100)
	stepsOK := true
	for i, c := range seq { // one feed per step; record all three keys' winners
		if err := eng.Feed([]api.Change{c}); err != nil {
			stepsOK = false
		}
		var got string
		for _, r := range eng.Replay() {
			got += fmt.Sprintf("%s%d ", r.Key, r.Val)
		}
		if len(got) > 0 {
			got = got[:len(got)-1]
		}
		stepsOK = stepsOK && got == wantSteps[i]
	}
	check("7-step winners a/b/c", stepsOK, wantSteps)

	rs := eng.Replay()
	check("Replay sorted by Key", reflect.DeepEqual(rs, []api.Record{
		{Key: "a", Val: 200}, {Key: "b", Val: 90}, {Key: "c", Val: 77}}), rs)

	brute := map[string]struct{ v, sn, val int64 }{} // naive reference: max Ver, tie max SN
	for sn, c := range seq {
		w := brute[c.Key]
		if int64(sn) == 0 || c.Ver > w.v || (c.Ver == w.v && int64(sn+1) > w.sn) {
			brute[c.Key] = struct{ v, sn, val int64 }{c.Ver, int64(sn + 1), c.Val}
		}
	}
	keys := make([]string, 0, len(brute))
	for k := range brute {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var ref []api.Record
	for _, k := range keys {
		ref = append(ref, api.Record{Key: k, Val: brute[k].val})
	}
	check("Replay == brute-force reference", reflect.DeepEqual(rs, ref))

	r1 := fmt.Sprint(eng.Replay())
	check("Replay twice byte-identical", r1 == fmt.Sprint(eng.Replay()))

	ha := eng.History("a")
	check("History(a) SN-ascending", reflect.DeepEqual(ha, []api.Change{
		seq[0], seq[2], seq[4]}), ha)

	bad := api.New(1)
	_ = bad.Feed([]api.Change{{Key: "x", Ver: 1, Val: 1}})
	batches := [][]api.Change{
		{{Key: "", Ver: 1, Val: 1}},
		{{Key: "x", Ver: -1, Val: 1}},
		{{Key: "x", Ver: 1, Val: 1}, {Key: "x", Ver: 2, Val: 2}},
	}
	sentinels := []error{api.ErrEmptyKey, api.ErrNegativeVer, api.ErrHistoryOverflow}
	errOK := true
	for i, b := range batches {
		err := bad.Feed(b)
		errOK = errOK && errors.Is(err, sentinels[i])
	}
	errOK = errOK && !errors.Is(sentinels[0], sentinels[1]) && !errors.Is(sentinels[1], sentinels[2])
	check("three distinct sentinel errors", errOK)
	check("rejection leaves no trace", fmt.Sprint(bad.Replay()) == "[{x 1}]")

	check("checked counter bounded in m=100..10000", lww.CounterBounded())

	var wg sync.WaitGroup
	base := eng.Replay()
	same := true
	for g := 0; g < 16; g++ { // concurrent pure readers, no sleeps
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !reflect.DeepEqual(eng.Replay(), base) {
				same = false
			}
		}()
	}
	wg.Wait()
	check("16 concurrent readers identical", same)
	check("SelfCheck", eng.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
