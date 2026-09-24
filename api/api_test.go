package api_test

import "errors"
import "maps"
import "math/rand"
import "reflect"
import "strconv"
import "strings"
import "sync"
import "testing"
import "ontology/api"

func mp(s string) map[string]api.Value {
	m := map[string]api.Value{}
	for _, p := range strings.Split(s, ",") {
		k, v, _ := strings.Cut(p, ":")
		m[k] = map[bool]api.Value{true: {Null: true}, false: {S: v}}[v == "NULL"]
	}
	return m
}
func up(k, s, b string) api.Event {
	return api.Event{Kind: api.KindUpdate, Key: k, Set: mp(s), Before: mp(b)}
}
func in(k, s string) api.Event { return api.Event{Kind: api.KindInsert, Key: k, Set: mp(s)} }
func genBatch(cols, keys []string, sh map[string]map[string]api.Value, r *rand.Rand) []api.Event {
	b := []api.Event{}
	rnd := func() api.Value { return []api.Value{{Null: true}, {}, {S: strconv.Itoa(r.Intn(4))}}[r.Intn(3)] }
	for n := r.Intn(6) + 1; n > 0; n-- {
		k := keys[r.Intn(len(keys))]
		cur, old := sh[k]
		if !old {
			s := map[string]api.Value{}
			for _, c := range cols {
				s[c] = rnd()
			}
			b = append(b, api.Event{Kind: api.KindInsert, Key: k, Set: s})
			sh[k] = maps.Clone(s)
			continue
		}
		s, bf := map[string]api.Value{}, map[string]api.Value{}
		for i, c := range cols {
			if i == 0 || r.Intn(2) == 0 {
				x := rnd()
				s[c], bf[c], cur[c] = x, cur[c], x
			}
		}
		b = append(b, api.Event{Kind: api.KindUpdate, Key: k, Set: s, Before: bf})
	}
	return b
}
func TestRandomBatchesMatchNaive(t *testing.T) {
	for ci, nc := range []int{1, 3, 8} {
		cols := []string{"c0", "c1", "c2", "c3", "c4", "c5", "c6", "c7"}[:nc]
		e, _ := api.New(cols)
		r := rand.New(rand.NewSource(int64(ci + 1)))
		sh, keys := map[string]map[string]api.Value{}, []string{"k0", "k1", "k2", "k3"}
		for range 200 {
			e.Apply(genBatch(cols, keys, sh, r))
		}
		for _, k := range keys {
			g, _ := e.Row(k)
			if !reflect.DeepEqual(g, sh[k]) {
				t.Fatalf("nc=%d k=%s got %v want %v", nc, k, g, sh[k])
			}
		}
	}
	e2, _ := api.New([]string{"a", "b", "c"})
	if err := e2.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
func TestBeforeMirror(t *testing.T) {
	e, _ := api.New([]string{"a", "b", "c"})
	e.Apply([]api.Event{in("k", "a:1,b:x,c:NULL")})
	steps := []api.Event{up("k", "a:2", "a:1"), up("k", "b:NULL", "b:x"), up("k", "a:3,c:z", "a:2,c:NULL"), up("k", "b:x", "b:NULL"), up("k", "c:NULL", "c:z"), up("k", "a:NULL", "a:3")}
	out, _ := e.Apply(steps)
	if len(out) != 1 || !reflect.DeepEqual(out[0].Set, mp("a:NULL")) || !reflect.DeepEqual(out[0].Before, mp("a:1")) {
		t.Fatalf("set=%v before=%v", out, out)
	}
}
func TestOutputOrder(t *testing.T) {
	e, _ := api.New([]string{"a"})
	e.Apply([]api.Event{in("k0", "a:0"), in("k1", "a:0"), in("k2", "a:0")})
	out, _ := e.Apply([]api.Event{up("k2", "a:1", "a:0"), up("k1", "a:1", "a:0"), up("k0", "a:1", "a:0"), up("k1", "a:0", "a:1"), up("k2", "a:2", "a:1")})
	if len(out) != 2 || out[0].Key != "k2" || out[1].Key != "k0" {
		t.Fatalf("order=%v (dup or wrong order)", out)
	}
}
func TestRejectedBatchNoTrace(t *testing.T) {
	sn := []error{api.ErrBadColumn, api.ErrKeyNotFound, api.ErrKeyExists, api.ErrBeforeMismatch}
	for i := range sn {
		for _, x := range sn[i+1:] {
			if sn[i] == x {
				t.Fatal("sentinel errors not distinct")
			}
		}
	}
	for _, cols := range [][]string{{"a", ""}, {"a", "a"}} {
		if _, err := api.New(cols); !errors.Is(err, api.ErrBadColumn) {
			t.Fatalf("cols=%v: %v", cols, err)
		}
	}
	e, _ := api.New([]string{"a", "b"})
	e.Apply([]api.Event{in("k", "a:1,b:2")})
	snap, _ := e.Row("k")
	bad := [][]api.Event{
		{in("k", "a:9,b:9")}, {up("zz", "a:1", "a:1")}, {up("k", "a:2", "a:9")},
		{up("k", "z:2", "z:1")}, {in("z", "a:1")}, {in("n", "a:1,b:2"), up("k", "a:2", "a:9")},
	}
	wants := []error{api.ErrKeyExists, api.ErrKeyNotFound, api.ErrBeforeMismatch, api.ErrBadColumn, api.ErrBadColumn, api.ErrBeforeMismatch}
	for i := range bad {
		if _, err := e.Apply(bad[i]); !errors.Is(err, wants[i]) {
			t.Fatalf("case %d: err=%v want %v", i, err, wants[i])
		}
	}
	r, _ := e.Row("k")
	if _, ok := e.Row("n"); ok || !reflect.DeepEqual(r, snap) {
		t.Fatal("rejected batch left trace")
	}
	e.Apply([]api.Event{up("k", "a:7", "a:1")})
}
func TestConcurrentDistinctKeys(t *testing.T) {
	e, _ := api.New([]string{"a", "b"})
	const N = 8
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := "k" + strconv.Itoa(g)
			row := mp("a:0,b:NULL")
			e.Apply([]api.Event{{Kind: api.KindInsert, Key: key, Set: row}})
			for i := 1; i <= 20; i++ {
				c := []string{"a", "b"}[i%2]
				x := api.Value{S: strconv.Itoa(g*100 + i)}
				e.Apply([]api.Event{{Kind: api.KindUpdate, Key: key, Set: map[string]api.Value{c: x}, Before: map[string]api.Value{c: row[c]}}})
				row[c] = x
			}
		}(g)
	}
	wg.Wait()
	for g := 0; g < N; g++ {
		want := map[string]api.Value{"a": {S: strconv.Itoa(g*100 + 20)}, "b": {S: strconv.Itoa(g*100 + 19)}}
		r, _ := e.Row("k" + strconv.Itoa(g))
		if !reflect.DeepEqual(r, want) {
			t.Fatalf("g=%d row=%v want %v", g, r, want)
		}
	}
}
