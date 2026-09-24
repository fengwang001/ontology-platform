package api_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"runtime"
	"sync"
	"testing"

	"ontology/api"
	"ontology/gdelta"
)

// recompute is the naive GROUP BY over an independent reference row table.
func recompute(rows map[int64]gdelta.Row) map[string]gdelta.Agg {
	m := map[string]gdelta.Agg{}
	for _, r := range rows {
		a := m[r.G]
		m[r.G] = gdelta.Agg{Sum: a.Sum + r.V, Count: a.Count + 1}
	}
	return m
}
func mirror(rows map[int64]gdelta.Row, op api.Op, g string, v int64) {
	if op.Kind == gdelta.DeleteOp {
		delete(rows, op.ID)
		return
	}
	rows[op.ID] = gdelta.Row{G: g, V: v}
}

// step returns a random op (Insert upgraded to Update/Delete for known ids).
func step(rng *rand.Rand, ids, groups int64, rows map[int64]gdelta.Row) (api.Op, string, int64) {
	id := int64(rng.Intn(int(ids)))
	g, v := fmt.Sprintf("g%d", rng.Intn(int(groups))), int64(rng.Intn(21)-10)
	op := api.Insert(id, g, v)
	if _, ok := rows[id]; ok {
		if rng.Intn(2) == 0 {
			op = api.Update(id, g, v)
		} else {
			op = api.Delete(id)
		}
	}
	return op, g, v
}

// TestViewMatchesRecompute (I1): View equals naive GROUP BY after random
// legal sequences under several caps.
func TestViewMatchesRecompute(t *testing.T) {
	for seed := int64(0); seed < 8; seed++ {
		capN := 4 + int(seed%6)
		e, rng := api.New(capN), rand.New(rand.NewSource(seed))
		rows := map[int64]gdelta.Row{}
		for i := 0; i < 1500; i++ {
			op, g, v := step(rng, 30, int64(capN+2), rows)
			if _, err := e.Apply([]api.Op{op}); err != nil {
				continue // rejected: reference state untouched
			}
			mirror(rows, op, g, v)
		}
		if !reflect.DeepEqual(recompute(rows), e.View()) {
			t.Fatalf("seed=%d: view %v != recompute %v", seed, e.View(), recompute(rows))
		}
	}
}

// TestChangeLogPrefixes (I2+I3): every '-' retracts exactly the current row, a
// group never holds two rows, each op emits <=4 entries (<=2 per group, '-'
// before '+'), and the full replay equals View.
func TestChangeLogPrefixes(t *testing.T) {
	e, rng := api.New(8), rand.New(rand.NewSource(42))
	rows, down := map[int64]gdelta.Row{}, map[string]gdelta.Agg{}
	for i := 0; i < 2000; i++ {
		op, g, v := step(rng, 25, 9, rows)
		cs, err := e.Apply([]api.Op{op})
		if err != nil {
			continue
		}
		if len(cs) > 4 {
			t.Fatalf("op emitted %d entries (>4)", len(cs))
		}
		seen := map[string]int{}
		for _, c := range cs {
			seen[c.G]++
			cur, have := down[c.G]
			if seen[c.G] > 2 || (c.Add && have) || (!c.Add && (!have || cur != (gdelta.Agg{Sum: c.Sum, Count: c.Count}))) {
				t.Fatalf("bad entry order/retract: %+v have=%v cur=%v", c, have, cur)
			}
			if c.Add {
				down[c.G] = gdelta.Agg{Sum: c.Sum, Count: c.Count}
			} else {
				delete(down, c.G)
			}
		}
		mirror(rows, op, g, v)
	}
	if !reflect.DeepEqual(down, e.View()) {
		t.Fatalf("replay %v != view %v", down, e.View())
	}
}

// TestConcurrent: N workers mutate disjoint ids (shared keys) while a reader
// spins with no sleep; final View equals the known recompute and no view read
// ever exposes a count==0 group.
func TestConcurrent(t *testing.T) {
	const N = 16
	e := api.New(N * 2)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				for _, a := range e.View() {
					if a.Count <= 0 {
						t.Errorf("view exposed count<=0 group %+v", a)
						return
					}
				}
				runtime.Gosched()
			}
		}
	}()
	for w := 0; w < N; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			g, x := fmt.Sprintf("g%d", w%4), fmt.Sprintf("x%d", w)
			for id := 3 * w; id < 3*w+3; id++ {
				e.Apply([]api.Op{api.Insert(int64(id), g, 1)})
			}
			e.Apply([]api.Op{api.Update(int64(3*w), x, 7)})
			e.Apply([]api.Op{api.Delete(int64(3*w + 1))})
			e.Apply([]api.Op{api.Update(int64(3*w+2), g, 4)})
		}(w)
	}
	wg.Wait()
	close(stop)
	want := map[string]gdelta.Agg{}
	for w := 0; w < N; w++ { // each worker leaves g_w(4,1) and x_w(7,1)
		a := want[fmt.Sprintf("g%d", w%4)]
		want[fmt.Sprintf("g%d", w%4)] = gdelta.Agg{Sum: a.Sum + 4, Count: a.Count + 1}
		want[fmt.Sprintf("x%d", w)] = gdelta.Agg{Sum: 7, Count: 1}
	}
	if !reflect.DeepEqual(want, e.View()) {
		t.Fatalf("concurrent view %v != want %v", e.View(), want)
	}
}
