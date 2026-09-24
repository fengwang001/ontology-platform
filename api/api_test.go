package api

import "errors"
import "maps"
import "math/rand"
import "reflect"
import "slices"
import "strconv"
import "sync"
import "testing"

func ch(op byte, id, key string, tv int64) Change { return Change{Op: op, ID: id, Key: key, T: tv} }
func o(op byte, key, id string, tv int64) Out     { return Out{Op: op, Key: key, ID: id, T: tv} }
func R(id string, tv int64) Row                   { return Row{ID: id, T: tv} }

// naiveView 是朴素批量重算：存活行按 Key 分组取 (T,ID) 最小。
func naiveView(a map[string]Row, k map[string]string) map[string]Row {
	v := map[string]Row{}
	for id, x := range a {
		if c, ok := v[k[id]]; !ok || x.T < c.T || x.T == c.T && id < c.ID {
			v[k[id]] = R(id, x.T)
		}
	}
	return v
}

func runRandom(t *testing.T, seed int64) (*API, map[string]Row) {
	rng, a := rand.New(rand.NewSource(seed)), New(100000)
	alive, keyOf, down := map[string]Row{}, map[string]string{}, map[string]Row{}
	nid := 0
	for i := 0; i < 300; i++ {
		nid++
		c := ch('+', "id"+strconv.Itoa(nid), "k"+strconv.Itoa(rng.Intn(5)), rng.Int63n(2001)-1000)
		if len(alive) > 0 && rng.Intn(10) >= 7 {
			ids := slices.Collect(maps.Keys(alive))
			id := ids[rng.Intn(len(ids))]
			c = ch('-', id, keyOf[id], 0) // 撤回只按 ID 定位，Key 被忽略
		}
		old, hasOld := naiveView(alive, keyOf)[c.Key]
		got, err := a.Apply([]Change{c})
		if err != nil {
			t.Fatalf("seed=%d i=%d: %v", seed, i, err)
		}
		if c.Op == '+' {
			alive[c.ID], keyOf[c.ID] = R(c.ID, c.T), c.Key
		} else {
			delete(alive, c.ID)
		}
		want := naiveView(alive, keyOf)
		nw, hasNew := want[c.Key]
		if len(got) != 0 == (hasOld && hasNew && old == nw) || len(got) > 2 {
			t.Fatalf("seed=%d i=%d outs=%v", seed, i, got)
		}
		for _, e := range got { // - 必须恰好撤回当前行，+ 时该 Key 必为空
			cur, ok := down[e.Key]
			if e.Op == '-' && (!ok || cur != R(e.ID, e.T)) || e.Op == '+' && ok {
				t.Fatalf("seed=%d bad log entry %v", seed, e)
			}
			if e.Op == '-' {
				delete(down, e.Key)
			} else {
				down[e.Key] = R(e.ID, e.T)
			}
		}
		if !reflect.DeepEqual(down, want) || !reflect.DeepEqual(a.View(), want) {
			t.Fatalf("seed=%d i=%d view diverged", seed, i)
		}
	}
	return a, naiveView(alive, keyOf)
}
func TestViewNaive(t *testing.T)     { runRandom(t, 1); runRandom(t, 2); runRandom(t, 3) }
func TestOutputMinimal(t *testing.T) { runRandom(t, 10); runRandom(t, 11) }
func TestLogPrefixes(t *testing.T)   { runRandom(t, 20); runRandom(t, 21) }

func TestNineSteps(t *testing.T) {
	a := New(100)
	cs := []Change{
		ch('+', "a", "k", 5), ch('+', "b", "k", 8), ch('+', "p", "k", 3),
		ch('+', "m", "k", 3), ch('-', "m", "", 0), ch('+', "e", "k", 1),
		ch('-', "b", "", 0), ch('-', "e", "", 0), ch('-', "p", "", 0),
	}
	leaders := []Row{R("a", 5), R("a", 5), R("p", 3), R("m", 3), R("p", 3), R("e", 1), R("e", 1), R("p", 3), R("a", 5)}
	var prev Row
	for i, c := range cs {
		w := []Out{o('+', "k", leaders[i].ID, leaders[i].T)}
		if i > 0 && prev == leaders[i] {
			w = []Out{}
		} else if i > 0 {
			w = []Out{o('-', "k", prev.ID, prev.T), o('+', "k", leaders[i].ID, leaders[i].T)}
		}
		got, err := a.Apply([]Change{c})
		if err != nil || !reflect.DeepEqual(got, w) || a.View()["k"] != leaders[i] {
			t.Fatalf("step %d got=%v want=%v err=%v", i+1, got, w, err)
		}
		prev = leaders[i]
	}
}

// TestRejectedBatchAtomic 钉住不变量 4：哨兵互不相同；被拒整批不留痕、可继续用。
func TestRejectedBatchAtomic(t *testing.T) {
	a := New(3)
	if _, err := a.Apply([]Change{ch('+', "a", "k", 1), ch('+', "b", "q", 2)}); err != nil {
		t.Fatal(err)
	}
	if err := New(10).SelfCheck(); err != nil {
		t.Fatal(err)
	}
	snap := a.View()
	cases := []struct {
		n string
		c []Change
		e error
	}{
		{"empty id", []Change{ch('+', "", "k", 1)}, ErrInvalidChange}, {"bad op", []Change{ch('?', "x", "k", 1)}, ErrInvalidChange},
		{"empty key", []Change{ch('+', "x", "", 1)}, ErrInvalidChange}, {"dup id", []Change{ch('+', "a", "k", 9)}, ErrIDExists},
		{"missing id", []Change{ch('-', "z", "", 0)}, ErrIDMissing}, {"over limit", []Change{ch('+', "c", "k", 3), ch('+', "d", "k", 4)}, ErrLimit},
		{"late invalid", []Change{ch('+', "c", "k", 3), ch('?', "", "", 0)}, ErrInvalidChange},
	}
	seen := map[error]bool{}
	for _, tc := range cases {
		outs, err := a.Apply(tc.c)
		if !errors.Is(err, tc.e) || outs != nil || !reflect.DeepEqual(a.View(), snap) {
			t.Fatalf("%s: err=%v", tc.n, err)
		}
		seen[err] = true
	}
	if len(seen) != 4 {
		t.Fatalf("want 4 distinct sentinels, got %d", len(seen))
	}
	if got, err := a.Apply([]Change{ch('-', "a", "", 0), ch('+', "a", "k", 7)}); err != nil || len(got) != 2 {
		t.Fatalf("retract+reinsert after reject failed: err=%v", err)
	}
}

func TestConcurrentView(t *testing.T) {
	a, want := runRandom(t, 42)
	var wg sync.WaitGroup
	res := make([]map[string]Row, 16)
	wg.Add(16)
	for i := range res {
		go func(i int) { defer wg.Done(); res[i] = a.View(); _ = a.SelfCheck() }(i)
	}
	wg.Wait()
	for i := 1; i < 16; i++ {
		if !reflect.DeepEqual(res[i], want) {
			t.Fatalf("reader %d diverged", i)
		}
	}
}
