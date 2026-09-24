package api_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

// TestSentinelErrors checks the three rejections are distinct and decidable.
func TestSentinelErrors(t *testing.T) {
	distinct := api.ErrEmptyKey != api.ErrNonPositiveSeq &&
		api.ErrNonPositiveSeq != api.ErrDuplicateSeq &&
		api.ErrEmptyKey != api.ErrDuplicateSeq
	if !distinct {
		t.Fatal("the three sentinel errors must be pairwise distinct")
	}
	r := api.New()
	if _, err := r.Feed([]api.Write{{Key: "K", Seq: 1, Val: "x"}}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		w    api.Write
		seed bool
		want error
	}{
		{"empty key", api.Write{Key: "", Seq: 1}, false, api.ErrEmptyKey},
		{"zero seq", api.Write{Key: "K", Seq: 0}, false, api.ErrNonPositiveSeq},
		{"negative seq", api.Write{Key: "K", Seq: -7}, false, api.ErrNonPositiveSeq},
		{"duplicate seq", api.Write{Key: "K", Seq: 1, Val: "y"}, true, api.ErrDuplicateSeq},
	}
	for _, c := range cases {
		x := api.New()
		if c.seed {
			if _, err := x.Feed([]api.Write{{Key: "K", Seq: 1, Val: "x"}}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := x.Feed([]api.Write{c.w}); !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v, want %v", c.name, err, c.want)
		}
	}
}

// TestBatchAtomic checks one bad write rejects the whole batch with no trace.
func TestBatchAtomic(t *testing.T) {
	r := api.New()
	if _, err := r.Feed([]api.Write{{Key: "K", Seq: 3, Val: "a"}}); err != nil {
		t.Fatal(err)
	}
	view, drops := r.View(), r.Dropped()
	cases := [][]api.Write{
		{{Key: "K", Seq: 1, Val: "x"}, {Key: "", Seq: 1}}, // empty key later in batch
		{{Key: "K", Seq: 0, Val: "x"}, {Key: "z", Seq: 1}},
		{{Key: "K", Seq: 3, Val: "dup"}, {Key: "z", Seq: 1}},
	}
	for i, b := range cases {
		if ch, err := r.Feed(b); err == nil || ch != nil {
			t.Fatalf("case %d: expected rejection, got ch=%v err=%v", i, ch, err)
		}
		if !reflect.DeepEqual(r.View(), view) || r.Dropped() != drops {
			t.Fatalf("case %d changed state: %v %d", i, r.View(), r.Dropped())
		}
	}
	if _, err := r.Feed([]api.Write{{Key: "K", Seq: 2, Val: "ok"}}); err != nil {
		t.Fatalf("register unusable after rejection: %v", err)
	}
	if r.View()["K"] != "ok" { // Seq 2 < 3: the earlier write wins under FWW
		t.Fatalf("post-rejection state wrong: %v", r.View())
	}
}

// TestSelfCheck exercises the exported built-in invariant check.
func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestConcurrentReaders checks many goroutines reading a fed instance get
// field-identical snapshots; relies on -race, uses no sleeps.
func TestConcurrentReaders(t *testing.T) {
	r := api.New()
	ws := make([]api.Write, 0, 64)
	for k := 0; k < 32; k++ {
		for s := int64(1); s <= 2; s++ {
			ws = append(ws, api.Write{Key: string(rune('a' + k)), Seq: s * 3, Val: "v"})
		}
	}
	if _, err := r.Feed(ws); err != nil {
		t.Fatal(err)
	}
	const n = 24
	var wg sync.WaitGroup
	views := make([]map[string]string, n)
	dps := make([]int64, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			views[i], dps[i], errs[i] = r.View(), r.Dropped(), r.SelfCheck()
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("reader %d SelfCheck: %v", i, errs[i])
		}
		if !reflect.DeepEqual(views[i], views[0]) || dps[i] != dps[0] {
			t.Fatalf("reader %d disagrees: %v %d vs %v %d", i, views[i], dps[i], views[0], dps[0])
		}
	}
}
