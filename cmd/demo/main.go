package main

import "errors"
import "fmt"
import "os"
import "reflect"
import "strings"
import "sync"
import "ontology/api"
import "ontology/pcol"

func main() {
	for _, f := range []func() error{six, misc, fails, concurrent} {
		if err := f(); err != nil {
			fmt.Println("FAIL", err)
			os.Exit(1)
		}
	}
}
func mm(s string) map[string]pcol.Value {
	m := map[string]pcol.Value{}
	for _, p := range strings.Split(s, ",") {
		x := pcol.Value{S: p[2:]}
		if x.S == "null" {
			x = pcol.Value{Null: true}
		}
		m[p[:1]] = x
	}
	return m
}
func fm(m map[string]pcol.Value) string {
	p := []string{}
	for _, c := range []string{"a", "b", "c"} {
		if x, ok := m[c]; ok {
			s := x.S
			if x.Null {
				s = "null"
			}
			p = append(p, c+":"+s)
		}
	}
	return strings.Join(p, ",")
}

const sixData = `a:2|a:1|a:2|a:1|
b:null|b:x|a:2,b:null|a:1,b:x|
a:3,c:z|a:2,c:null|a:3,b:null,c:z|a:1,b:x,c:null|
b:x|b:null|a:3,c:z|a:1,c:null|
c:null|c:z|a:3|a:1|
a:null|a:3|a:null|a:1| (final output)`

func six() error {
	var mg *pcol.Merger
	for i, d := range strings.Split(sixData, "\n") {
		p := strings.SplitN(d, "|", 5)
		ev := pcol.Event{Kind: pcol.KindUpdate, Key: "k", Set: mm(p[0]), Before: mm(p[1])}
		if i == 0 {
			mg, _ = pcol.NewMerger(ev, map[string]struct{}{"a": {}, "b": {}, "c": {}})
		} else if err := mg.Merge(ev); err != nil {
			return err
		}
		r, _ := mg.Result("k")
		if g := fm(r.Set) + "|" + fm(r.Before); g != p[2]+"|"+p[3] {
			return fmt.Errorf("step %d got %s", i+1, g)
		}
		fmt.Printf("OK step %d: Set={%s} Before={%s}%s\n", i+1, fm(r.Set), fm(r.Before), p[4])
	}
	return nil
}
func misc() error {
	z, q := pcol.Value{Null: true}, pcol.Value{}
	if !z.Equal(z) || z.Equal(q) || !q.Equal(q) {
		return fmt.Errorf("absent/null/\"\" distinction wrong")
	}
	mg, _ := pcol.NewMerger(pcol.Event{Kind: pcol.KindInsert, Set: mm("a:1,b:null")}, map[string]struct{}{"a": {}, "b": {}})
	_ = mg.Merge(pcol.Event{Kind: pcol.KindUpdate, Key: "k", Set: mm("a:2"), Before: mm("a:1")})
	r, _ := mg.Result("k")
	if r.Kind != pcol.KindInsert || fm(r.Set) != "a:2,b:null" {
		return fmt.Errorf("insert+update compaction wrong")
	}
	e, _ := api.New([]string{"a", "b", "c"})
	if err := e.SelfCheck(); err != nil {
		return err
	}
	fmt.Println("OK absent/null/\"\" distinct; Insert+Update=>Insert; 300 random batches naive-equal, 4 invariants")
	return nil
}
func fails() error {
	if _, err := api.New([]string{"a", ""}); !errors.Is(err, api.ErrBadColumn) {
		return err
	}
	e, _ := api.New([]string{"a", "b"})
	ins := pcol.Event{Kind: api.KindInsert, Key: "k", Set: mm("a:1,b:null")}
	_, _ = e.Apply([]pcol.Event{ins})
	upd := func(k, s, b string) pcol.Event {
		return pcol.Event{Kind: api.KindUpdate, Key: k, Set: mm(s), Before: mm(b)}
	}
	evs := []pcol.Event{ins, upd("zz", "a:1", "a:1"), upd("k", "a:2", "a:9"), upd("k", "x:2", "x:1"), {Kind: api.KindInsert, Key: "z", Set: mm("a:1")}, {Kind: api.KindUpdate, Key: "k", Set: map[string]pcol.Value{}, Before: map[string]pcol.Value{}}}
	wants := []error{api.ErrKeyExists, api.ErrKeyNotFound, api.ErrBeforeMismatch, api.ErrBadColumn, api.ErrBadColumn, api.ErrBadColumn}
	for i := range evs {
		if _, err := e.Apply([]pcol.Event{evs[i]}); !errors.Is(err, wants[i]) {
			return fmt.Errorf("case %d: %w", i, err)
		}
	}
	if r, _ := e.Row("k"); !reflect.DeepEqual(r, ins.Set) {
		return fmt.Errorf("state trace left after rejection")
	}
	if err := pcol.ComplexitySelfCheck(); err != nil {
		return err
	}
	fmt.Println("OK 4 distinct sentinel errors, no trace on reject; large-m checks O(event cols)")
	return nil
}
func concurrent() error {
	e, _ := api.New([]string{"a", "b"})
	var wg sync.WaitGroup
	errs := [4]error{}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := "k" + string(rune('0'+g))
			row := mm("a:0,b:null")
			apply := func(ev pcol.Event) bool { _, err := e.Apply([]pcol.Event{ev}); errs[g] = err; return err == nil }
			if !apply(pcol.Event{Kind: api.KindInsert, Key: key, Set: row}) {
				return
			}
			for t := 1; t <= 3; t++ {
				c := string("ab"[t%2])
				x := pcol.Value{S: fmt.Sprint(g, t)}
				ev := pcol.Event{Kind: api.KindUpdate, Key: key, Set: map[string]pcol.Value{c: x}, Before: map[string]pcol.Value{c: row[c]}}
				if !apply(ev) {
					return
				}
				row[c] = x
			}
		}(g)
	}
	wg.Wait()
	for g := 0; g < 4; g++ {
		want := map[string]pcol.Value{"a": {S: fmt.Sprint(g, 2)}, "b": {S: fmt.Sprint(g, 3)}}
		r, ok := e.Row("k" + string(rune('0'+g)))
		if errs[g] != nil || !ok || !reflect.DeepEqual(r, want) {
			return fmt.Errorf("concurrent key %d: %v", g, errs[g])
		}
	}
	fmt.Println("OK 4 goroutines on distinct keys: every row equals its own naive result")
	return nil
}
