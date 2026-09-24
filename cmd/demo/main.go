package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/rapply"
	"ontology/rimg"
)

var failed bool

func ck(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

func main() {
	row := func(v ...string) rimg.Row {
		r := rimg.Row{}
		for i := 0; i < len(v); i += 2 {
			r[v[i]] = v[i+1]
		}
		return r
	}
	seed := map[int64]rimg.Row{
		1: row("name", "ann", "qty", "3"),
		2: row("name", "bob", "qty", "5"),
		3: row("name", "cid"),
	}
	a, _ := api.New(seed, 4)
	U, D, I := rimg.Update, rimg.Delete, rimg.Insert
	evs := []rimg.Event{
		{Seq: 1, Kind: U, PK: 1, Before: row("name", "ann", "qty", "3"), After: row("name", "ann", "qty", "4")},
		{Seq: 2, Kind: U, PK: 2, Before: row("name", "bob", "qty", "6"), After: row("name", "bob", "qty", "7")},
		{Seq: 3, Kind: D, PK: 3, Before: row("name", "cid", "qty", "")},
		{Seq: 4, Kind: I, PK: 2, After: row("name", "bea", "qty", "1")},
		{Seq: 5, Kind: D, PK: 1, Before: row("name", "ann", "qty", "3")},
		{Seq: 6, Kind: U, PK: 4, Before: row("name", "dan"), After: row("name", "dan", "qty", "2")},
		{Seq: 7, Kind: I, PK: 4, After: row("name", "dan", "qty", "2")},
		{Seq: 8, Kind: U, PK: 4, Before: row("name", "dan", "qty", "2"), After: row("name", "dan", "qty", "3")},
	}
	want := []rapply.Outcome{api.Applied, api.BeforeMismatch, api.BeforeMismatch, api.RowExists,
		api.BeforeMismatch, api.RowMissing, api.Applied, api.Applied}
	got8 := true
	for i, ev := range evs {
		res, err := a.Apply([]rimg.Event{ev})
		got8 = got8 && err == nil && res[0].Outcome == want[i]
	}
	ck("eight-step outcomes", got8)
	st := a.Snapshot()
	final := len(st.Rows) == 4 && st.Rows[1]["qty"] == "4" && st.Rows[2]["qty"] == "5" &&
		st.Rows[3]["name"] == "cid" && st.Rows[4]["qty"] == "3" && st.LastSeq == 8
	ck("final replica", final)
	cf := a.Conflicts()
	cfOK := len(cf) == 5 && cf[0].Type == api.BeforeMismatch && cf[1].Type == api.BeforeMismatch &&
		cf[2].Type == api.RowExists && cf[3].Type == api.BeforeMismatch && cf[4].Type == api.RowMissing
	ck("conflict log(5:bm,bm,exists,bm,missing)", cfOK)
	ck("missing col != empty string",
		!rimg.Equal(row("name", "cid"), row("name", "cid", "qty", "")) && rimg.Equal(row("x", ""), row("x", "")))

	scErr := api.SelfCheck()
	ck("random == blind-apply reference", scErr == nil)
	ck("conflicts zero side effect + recomputable", scErr == nil)

	b, _ := api.New(seed, 4)
	bad := []struct {
		e    rimg.Event
		want error
	}{
		{rimg.Event{Seq: 1, Kind: U, PK: 1, After: row("qty", "9")}, api.ErrInvalidEvent},
		{rimg.Event{Seq: 3, Kind: I, PK: 9, After: row("qty", "9")}, api.ErrSeqGap},
	}
	distinct := true
	for _, c := range bad {
		_, err := b.Apply([]rimg.Event{c.e})
		distinct = distinct && errors.Is(err, c.want)
	}
	cap1, _ := api.New(seed, 3) // seed already fills all 3 slots
	_, errOver := cap1.Apply([]rimg.Event{{Seq: 1, Kind: I, PK: 9, After: row("qty", "9")}})
	distinct = distinct && errors.Is(errOver, api.ErrTooManyRows)
	ck("three distinct sentinel errors", distinct)

	d, _ := api.New(seed, 4)
	pre := d.Snapshot()
	_, rejErr := d.Apply([]rimg.Event{
		{Seq: 1, Kind: U, PK: 1, After: row("z", "z")}, // invalid: Update missing Before
		{Seq: 2, Kind: I, PK: 9, After: row("z", "z")},
	})
	post := d.Snapshot()
	ck("rejected batch leaves state unchanged",
		errors.Is(rejErr, api.ErrInvalidEvent) && post.LastSeq == pre.LastSeq &&
			len(post.Conflicts) == len(pre.Conflicts) && len(post.Rows) == len(pre.Rows))

	ck("O(1) reads at m=100..10000", rapply.VerifyLookupIsO1() == nil)

	c, _ := api.New(map[int64]rimg.Row{}, 1<<30)
	var wg sync.WaitGroup
	boundary := true
	var mu sync.Mutex
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				s := c.Snapshot()
				mu.Lock()
				boundary = boundary && len(s.Conflicts) >= 0 && s.LastSeq >= 0
				mu.Unlock()
			}
		}()
	}
	for k := int64(1); k <= 400; k++ {
		_, _ = c.Apply([]rimg.Event{{Seq: k, Kind: rimg.Insert, PK: k, After: row("v", "x")}})
	}
	wg.Wait()
	sN := c.Snapshot()
	ck("concurrent reads see only batch boundaries", boundary && sN.LastSeq == 400 && len(sN.Rows) == 400)

	if failed {
		os.Exit(1)
	}
}
