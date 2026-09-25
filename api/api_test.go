package api_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

// TestSentinelErrors pins the three pairwise-distinct, decidable errors.
func TestSentinelErrors(t *testing.T) {
	if errors.Is(api.ErrInvalidSide, api.ErrInvalidMaxKeyLen) ||
		errors.Is(api.ErrInvalidSide, api.ErrKeyTooLong) ||
		errors.Is(api.ErrInvalidMaxKeyLen, api.ErrKeyTooLong) {
		t.Fatal("the three sentinel errors must be pairwise distinct")
	}
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"invalid side", func() error {
			j, _ := api.New(4)
			return j.Feed(api.Side(7), strptr("x"), 1)
		}, api.ErrInvalidSide},
		{"non-positive maxKeyLen", func() error {
			_, err := api.New(-1)
			return err
		}, api.ErrInvalidMaxKeyLen},
		{"key too long (bytes and runes)", func() error {
			j, _ := api.New(2)
			long := "abc"
			return j.Feed(api.L, &long, 1)
		}, api.ErrKeyTooLong},
		{"multibyte rune budget", func() error {
			j, _ := api.New(2)
			long := "世界語" // 3 runes although 9 bytes
			return j.Feed(api.L, &long, 1)
		}, api.ErrKeyTooLong},
	}
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	j, err := api.New(1)
	if err != nil || j == nil {
		t.Fatal("New(1) must succeed")
	}
	if err := j.Feed(api.L, nil, 1); err != nil {
		t.Fatalf("NULL key is always legal, got %v", err)
	}
}

// TestRejectionLeavesNoTrace pins invariant 4: rejected calls change no
// state (outputs and buffers) and the instance stays usable afterwards.
func TestRejectionLeavesNoTrace(t *testing.T) {
	j, _ := api.New(3)
	k := "key"
	if err := j.Feed(api.R, &k, 10); err != nil {
		t.Fatal(err)
	}
	base := j.Outputs()
	bad := []struct {
		side api.Side
		key  *string
		val  int64
	}{
		{api.Side(5), &k, 1},
		{api.L, strptr("too-long"), 2},
		{api.R, strptr("xxxx"), 3},
		{api.Side(255), nil, 4},
	}
	for i, b := range bad {
		err := j.Feed(b.side, b.key, b.val)
		if err == nil {
			t.Fatalf("bad case %d must be rejected", i)
		}
		if got := j.Outputs(); !reflect.DeepEqual(got, base) {
			t.Fatalf("bad case %d changed outputs: %+v vs %+v", i, got, base)
		}
	}
	// Rejected buffered rows must not match later, and normal use works.
	if err := j.Feed(api.L, &k, 20); err != nil {
		t.Fatalf("instance must stay usable: %v", err)
	}
	o := j.Outputs()
	if len(o) != 1 || o[0].Key != "key" || o[0].LVal != 20 || o[0].RVal != 10 {
		t.Fatalf("rejected rows must not join; got %+v", o)
	}
}

// TestConcurrentReaders pins section 6: many goroutines reading one
// fully-fed instance get field-identical snapshots; race detector clean.
func TestConcurrentReaders(t *testing.T) {
	j, _ := api.New(8)
	for i := 0; i < 50; i++ {
		k := "k"
		if i%3 == 0 {
			k = "other"
		}
		if err := j.Feed(api.Side(i%2), &k, int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	const n = 32
	var wg sync.WaitGroup
	outs := make([][]api.Output, n)
	errs := make([]error, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			outs[g] = j.Outputs()
			errs[g] = j.SelfCheck()
		}(g)
	}
	wg.Wait()
	for g := 0; g < n; g++ {
		if errs[g] != nil {
			t.Fatalf("reader %d: SelfCheck: %v", g, errs[g])
		}
		if !sameFields(outs[0], outs[g]) {
			t.Fatalf("reader %d snapshot differs", g)
		}
	}
}

func sameFields(a, b []api.Output) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].LVal != b[i].LVal || a[i].RVal != b[i].RVal {
			return false
		}
	}
	return true
}

func strptr(s string) *string { return &s }
