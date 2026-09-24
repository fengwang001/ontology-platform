// Command demo runs in-process self-checks for the upsert-to-retract normalizer.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
)

var failed bool

func ck(name string, ok bool) {
	prefix := "OK "
	if !ok {
		prefix, failed = "FAIL ", true
	}
	fmt.Println(prefix + name)
}
func up(k string, v int64) api.Op      { return api.Op{Kind: api.OpUpsert, Key: k, Val: v} }
func dl(k string) api.Op               { return api.Op{Kind: api.OpDelete, Key: k} }
func ins(k string, v int64) api.Change { return api.Change{Kind: api.ChgInsert, Key: k, Val: v} }
func ret(k string, v int64) api.Change { return api.Change{Kind: api.ChgRetract, Key: k, Val: v} }
func tok(c api.Change) string          { return fmt.Sprintf("%d:%s:%d|", c.Kind, c.Key, c.Val) }
func eq(a, b map[string]int64) bool    { return fmt.Sprint(a) == fmt.Sprint(b) }

// replay applies the log from the empty table, validating every prefix:
// each - must hit a key holding exactly that value, each + must hit a free key.
func replay(lg []api.Change) (map[string]int64, bool) {
	t := map[string]int64{}
	for _, c := range lg {
		v, exists := t[c.Key]
		if c.Kind == api.ChgRetract && (!exists || v != c.Val) {
			return t, false
		}
		if c.Kind == api.ChgInsert && exists {
			return t, false
		}
		delete(t, c.Key) // no-op when absent; insert re-adds right after
		if c.Kind == api.ChgInsert {
			t[c.Key] = c.Val
		}
	}
	return t, true
}

// naive replays ops one by one: the reference semantics for I1.
func naive(t map[string]int64, b []api.Op) {
	for _, o := range b {
		delete(t, o.Key)
		if o.Kind == api.OpUpsert {
			t[o.Key] = o.Val
		}
	}
}

func main() {
	batches := [][]api.Op{
		{up("a", 1), up("b", 1), up("a", 2)}, {dl("a"), up("a", 2)},
		{dl("c"), up("c", 5), dl("c")}, {up("b", 1)},
		{up("b", 3), dl("a")}, {dl("a"), up("a", 7)},
		{up("d", 1), dl("d"), dl("b")},
	}
	want := [][]api.Change{ // hand-derived minimal folds (NOTES.md table)
		{ins("a", 2), ins("b", 1)}, nil, nil, nil,
		{ret("b", 1), ins("b", 3), ret("a", 2)},
		{ins("a", 7)}, {ret("b", 3)},
	}
	n := api.New(1 << 20)
	ref, foldOK, minOK := map[string]int64{}, true, true
	for i, b := range batches {
		got, err := n.Apply(b)
		foldOK = foldOK && err == nil && fmt.Sprint(got) == fmt.Sprint(want[i])
		minOK = minOK && len(got) == len(want[i])
		naive(ref, b)
	}
	tbl, pfx := replay(n.Log())
	ck("seven batches exact (B2/B3 empty, B6 +(a,7))", foldOK)
	ck("log replay == naive reference == snapshot", pfx && eq(tbl, ref) && eq(tbl, n.Snapshot()))
	ck("every log prefix self-consistent", pfx)
	ck("per-batch output minimal", minOK)

	z := api.New(1)
	_, _ = z.Apply([]api.Op{up("x", 1)})
	errOK, noTrace := true, true
	cases := []struct {
		b    []api.Op
		want error
	}{
		{[]api.Op{up("", 1)}, api.ErrEmptyKey},
		{[]api.Op{{Kind: api.OpKind(7), Key: "y"}}, api.ErrInvalidOp},
		{[]api.Op{up("y", 2)}, api.ErrTooManyKeys},
	}
	for _, tc := range cases {
		snap, n0 := z.Snapshot(), len(z.Log())
		_, err := z.Apply(tc.b)
		errOK = errOK && errors.Is(err, tc.want)
		noTrace = noTrace && eq(z.Snapshot(), snap) && len(z.Log()) == n0
	}
	_, stillUsable := z.Apply([]api.Op{up("x", 9)})
	distinct := api.ErrEmptyKey != api.ErrInvalidOp && api.ErrInvalidOp != api.ErrTooManyKeys
	ck("three distinct decidable errors", errOK && distinct && stillUsable == nil)
	ck("rejected batch leaves no trace", noTrace)
	ck("checked keys do not grow with m", api.SelfCheck() == nil)

	c := api.New(1 << 20)
	var wg sync.WaitGroup
	const G, R = 8, 50
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			k := fmt.Sprintf("g%d", g)
			for j := 0; j < R; j++ {
				_, _ = c.Apply([]api.Op{up(k, int64(2*j)), up(k, int64(2*j+1))})
			}
		}(g)
	}
	wg.Wait()
	lg := c.Log()
	var sb strings.Builder
	for _, x := range lg {
		sb.WriteString(tok(x))
	}
	joined, contig := sb.String(), true
	for g := 0; g < G; g++ { // every batch j>=1 must show adjacent -(2j-1) +(2j+1)
		k := fmt.Sprintf("g%d", g)
		for j := 1; j < R; j++ {
			pat := tok(ret(k, int64(2*j-1))) + tok(ins(k, int64(2*j+1)))
			contig = contig && strings.Contains(joined, pat)
		}
	}
	ct, cvalid := replay(lg)
	ck("concurrent apply contiguous; replay==snapshot", contig && cvalid && eq(ct, c.Snapshot()))

	if failed {
		os.Exit(1)
	}
}
