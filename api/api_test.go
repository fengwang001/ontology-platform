package api_test

import (
	"fmt"
	"maps"
	"math/rand"
	"ontology/api"
	"ontology/pcol"
	"sync"
	"testing"
)

type vm = map[string]pcol.Value

func genBatch(r *rand.Rand, cols, keys []string, ref *api.Table) (batch []pcol.Event) {
	for i := 0; i < 1+r.Intn(8); i++ {
		k, force := keys[r.Intn(len(keys))], cols[r.Intn(len(cols))]
		row, ok := ref.Row(k)
		e := pcol.Event{Kind: pcol.Update, Key: k, Set: vm{}, Before: vm{}}
		for _, c := range cols {
			if !ok || c == force || r.Intn(2) == 1 {
				e.Before[c], e.Set[c] = row[c], []pcol.Value{pcol.Null(), pcol.Str(""), pcol.Str(fmt.Sprint(r.Intn(100))), pcol.Str(fmt.Sprint(r.Intn(100)))}[r.Intn(4)]
			}
		}
		if !ok {
			e.Kind, e.Before = pcol.Insert, nil
		}
		batch = append(batch, e)
		ref.Apply([]pcol.Event{e})
	}
	return batch
}
func TestRandomBatchNaiveConsistency(t *testing.T) {
	cols, keys := []string{"c0", "c1", "c2", "c3"}, []string{"k0", "k1", "k2", "k3", "k4"}
	for seed := int64(0); seed < 10; seed++ {
		r := rand.New(rand.NewSource(seed))
		tb, _ := api.New(cols)  // 被测：整批合并
		tb2, _ := api.New(cols) // 朴素参照：原始事件逐条应用
		tb3, _ := api.New(cols) // 合并输出应用到批前状态
		for b := 0; b < 5; b++ {
			batch := genBatch(r, cols, keys, tb2)
			out, err := tb.Apply(batch)
			if _, err3 := tb3.Apply(out); err != nil || err3 != nil {
				t.Fatalf("seed %d: %v %v", seed, err, err3)
			}
		}
		for _, k := range keys {
			g1, _ := tb.Row(k)
			g2, _ := tb2.Row(k)
			g3, _ := tb3.Row(k)
			if !maps.Equal(g1, g2) || !maps.Equal(g2, g3) {
				t.Fatalf("seed %d key %s: merged/naive/output-applied differ", seed, k)
			}
		}
	}
}
func TestBeforeMirror(t *testing.T) {
	tb, _ := api.New([]string{"a", "b"})
	tb.Apply([]pcol.Event{{Kind: pcol.Insert, Key: "k", Set: vm{"a": pcol.Str("1"), "b": pcol.Str("x")}}})
	out, err := tb.Apply([]pcol.Event{pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"a": pcol.Str("2")}, Before: vm{"a": pcol.Str("1")}}, {Kind: pcol.Update, Key: "k", Set: vm{"b": pcol.Null()}, Before: vm{"b": pcol.Str("x")}}, pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"a": pcol.Str("3")}, Before: vm{"a": pcol.Str("2")}}})
	if err != nil || len(out) != 1 {
		t.Fatalf("apply: %v len %d", err, len(out))
	}
	if want := (vm{"a": pcol.Str("1"), "b": pcol.Str("x")}); !maps.Equal(out[0].Before, want) {
		t.Fatalf("before = %v, want %v", out[0].Before, want)
	}
	for c, v := range out[0].Set {
		if v == out[0].Before[c] {
			t.Fatalf("column %s: Set == Before not eliminated", c)
		}
	}
	if out2, _ := tb.Apply([]pcol.Event{pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"a": pcol.Str("3")}, Before: vm{"a": pcol.Str("3")}}}); len(out2) != 0 {
		t.Fatal("no-change update must not be output")
	}
}
func TestOutputOrderUnique(t *testing.T) {
	seed := []pcol.Event{{Kind: pcol.Insert, Key: "k1", Set: vm{"a": pcol.Str("0")}}, {Kind: pcol.Insert, Key: "k2", Set: vm{"a": pcol.Str("0")}}, {Kind: pcol.Insert, Key: "k3", Set: vm{"a": pcol.Str("0")}}}
	batches := [][]pcol.Event{{pcol.Event{Kind: pcol.Update, Key: "k2", Set: vm{"a": pcol.Str("1")}, Before: vm{"a": pcol.Str("0")}}, pcol.Event{Kind: pcol.Update, Key: "k1", Set: vm{"a": pcol.Str("1")}, Before: vm{"a": pcol.Str("0")}}, pcol.Event{Kind: pcol.Update, Key: "k2", Set: vm{"a": pcol.Str("2")}, Before: vm{"a": pcol.Str("1")}}, pcol.Event{Kind: pcol.Update, Key: "k3", Set: vm{"a": pcol.Str("1")}, Before: vm{"a": pcol.Str("0")}}, pcol.Event{Kind: pcol.Update, Key: "k1", Set: vm{"a": pcol.Str("2")}, Before: vm{"a": pcol.Str("1")}}},
		{pcol.Event{Kind: pcol.Update, Key: "k1", Set: vm{"a": pcol.Str("1")}, Before: vm{"a": pcol.Str("0")}}, pcol.Event{Kind: pcol.Update, Key: "k1", Set: vm{"a": pcol.Str("0")}, Before: vm{"a": pcol.Str("1")}}}} // 回到批前值：剔除后不输出
	wants := [][]string{{"k2", "k1", "k3"}, nil}
	for i, c := range batches {
		tb, _ := api.New([]string{"a"})
		tb.Apply(seed)
		out, err := tb.Apply(c)
		if err != nil || len(out) != len(wants[i]) {
			t.Fatalf("case %d: %v len %d", i, err, len(out))
		}
		for j, want := range wants[i] {
			if out[j].Key != want {
				t.Fatalf("case %d: out[%d] = %s, want %s", i, j, out[j].Key, want)
			}
		}
	}
}
func TestFailureAtomic(t *testing.T) {
	sents := []error{pcol.ErrBadColumn, pcol.ErrNoSuchKey, pcol.ErrKeyExists, pcol.ErrBeforeMismatch}
	if len(map[error]bool{sents[0]: true, sents[1]: true, sents[2]: true, sents[3]: true}) != 4 {
		t.Fatal("sentinels not distinct")
	}
	for _, bad := range [][]string{{"a", "a"}, {""}} {
		if _, err := api.New(bad); err != pcol.ErrBadColumn {
			t.Fatalf("New(%v): want ErrBadColumn", bad)
		}
	}
	tb, _ := api.New([]string{"a"})
	tb.Apply([]pcol.Event{{Kind: pcol.Insert, Key: "k", Set: vm{"a": pcol.Str("1")}}})
	batches := [][]pcol.Event{{{Kind: pcol.Update, Key: "k", Set: vm{"zz": pcol.Str("1")}, Before: vm{"zz": pcol.Str("1")}}}, {pcol.Event{Kind: pcol.Update, Key: "ghost", Set: vm{"a": pcol.Str("1")}, Before: vm{"a": pcol.Str("1")}}}, {{Kind: pcol.Insert, Key: "k", Set: vm{"a": pcol.Str("1")}}}, {pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"a": pcol.Str("2")}, Before: vm{"a": pcol.Str("9")}}}}
	for i, want := range sents {
		batch := append([]pcol.Event{{Kind: pcol.Insert, Key: "n", Set: vm{"a": pcol.Str("2")}}}, batches[i]...)
		if _, err := tb.Apply(batch); err != want {
			t.Fatalf("case %d: got %v, want %v", i, err, want)
		}
	}
	row, _ := tb.Row("k")
	_, leaked := tb.Row("n")
	if !maps.Equal(row, vm{"a": pcol.Str("1")}) || leaked {
		t.Fatal("rejected batch changed state")
	}
	if _, err := tb.Apply([]pcol.Event{pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"a": pcol.Str("2")}, Before: vm{"a": pcol.Str("1")}}}); err != nil {
		t.Fatal("table unusable after rejection")
	}
}
func TestConcurrentDistinctKeys(t *testing.T) {
	tb, _ := api.New([]string{"a", "b"})
	ref, _ := api.New([]string{"a", "b"}) // 共享朴素参照：各 goroutine 逐条应用自己的事件
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, r := fmt.Sprint("k", g), rand.New(rand.NewSource(int64(g)))
			for b := 0; b < 6; b++ {
				if _, err := tb.Apply(genBatch(r, []string{"a", "b"}, []string{key}, ref)); err != nil {
					t.Error(err)
				}
				if err := tb.SelfCheck(); err != nil { // SelfCheck 与 Apply 并发
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	for g := 0; g < 8; g++ {
		g1, _ := tb.Row(fmt.Sprint("k", g))
		g2, _ := ref.Row(fmt.Sprint("k", g))
		if !maps.Equal(g1, g2) {
			t.Errorf("key k%d: row != per-goroutine naive result", g)
		}
	}
}
