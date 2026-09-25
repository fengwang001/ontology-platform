package api

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

type op struct {
	del      bool
	key, col string
	ts       int64
	val      string
}

func put(k, c string, ts int64, v string) op { return op{false, k, c, ts, v} }
func del(k, c string, ts int64) op           { return op{true, k, c, ts, ""} }

func run(ops []op) *Store {
	s := New()
	for _, o := range ops {
		if o.del {
			_ = s.Del(o.key, o.col, o.ts)
		} else {
			_ = s.Put(o.key, o.col, o.ts, o.val)
		}
	}
	return s
}

// batchView is the naive per-(Key,Col) max-TS reference (ties: tomb beats
// value, else larger value). Keys ever written appear in the outer map.
func batchView(ops []op) map[string]map[string]string {
	type wn struct {
		ts   int64
		v    string
		tomb bool
	}
	win := map[[2]string]wn{}
	for _, o := range ops {
		k := [2]string{o.key, o.col}
		n, c := wn{o.ts, o.val, o.del}, win[k]
		if _, ok := win[k]; !ok || n.ts > c.ts || n.ts == c.ts &&
			(n.tomb && !c.tomb || n.tomb == c.tomb && n.v > c.v) {
			win[k] = n
		}
	}
	out := map[string]map[string]string{}
	for k, x := range win {
		if out[k[0]] == nil {
			out[k[0]] = map[string]string{}
		}
		if !x.tomb {
			out[k[0]][k[1]] = x.v
		}
	}
	return out
}

func TestBatchEquivalence(t *testing.T) {
	eight := []op{put("R", "c1", 5, "a"), put("R", "c2", 7, "x"), put("R", "c1", 5, "b"), put("R", "c1", 9, "c"),
		del("R", "c2", 8), put("R", "c1", 4, "old"), put("R", "c3", 6, "y"), del("R", "c3", 2)}
	s8 := run(eight)
	if got := s8.View()["R"]; !reflect.DeepEqual(got, map[string]string{"c1": "c", "c3": "y"}) {
		t.Fatalf("eight-step view = %v", got)
	}
	if cf := s8.Conflicted(); !reflect.DeepEqual(cf, map[[2]string]bool{{"R", "c1"}: true}) {
		t.Fatalf("eight-step conflicts = %v", cf)
	}
	for seed := int64(0); seed < 30; seed++ {
		r := rand.New(rand.NewSource(seed))
		var ops []op
		for i := 0; i < 200; i++ {
			o := put(fmt.Sprint("k", r.Intn(3)), fmt.Sprint("c", r.Intn(4)),
				int64(r.Intn(10)), string(rune('a'+r.Intn(4))))
			if r.Intn(4) == 0 {
				o.del = true
			}
			ops = append(ops, o)
		}
		if got, want := run(ops).View(), batchView(ops); !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d: %v want %v", seed, got, want)
		}
	}
}
func TestRejectedOpsNoTrace(t *testing.T) {
	s := run([]op{put("K", "c1", 5, "v"), put("K", "c1", 5, "w")}) // one conflict recorded
	bv, bc := s.View(), s.Conflicted()
	if len(map[error]bool{ErrEmptyKey: true, ErrEmptyCol: true, ErrNegativeTS: true, ErrEmptyVal: true}) != 4 {
		t.Fatal("sentinels not distinct")
	}
	got := []error{s.Put("", "c", 1, "v"), s.Put("k", "", 1, "v"), s.Put("k", "c", -1, "v"),
		s.Put("k", "c", 1, ""), s.Del("", "c", 1), s.Del("k", "", 1), s.Del("k", "c", -1)}
	want := []error{ErrEmptyKey, ErrEmptyCol, ErrNegativeTS, ErrEmptyVal, ErrEmptyKey, ErrEmptyCol, ErrNegativeTS}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bad op %d: got %v want %v", i, got[i], want[i])
		}
	}
	if !reflect.DeepEqual(s.View(), bv) || !reflect.DeepEqual(s.Conflicted(), bc) {
		t.Fatal("rejected op mutated state")
	}
	if err := s.Put("K", "c2", 7, "ok"); err != nil || s.View()["K"]["c2"] != "ok" {
		t.Fatal("store unusable after rejections")
	}
}

func TestConcurrentWrites(t *testing.T) {
	s, start := New(), make(chan struct{})
	const n = 64
	var done atomic.Bool
	var writers, readers sync.WaitGroup
	read := func() {
		defer readers.Done()
		for !done.Load() { // any visible column must always hold its own value
			for col, v := range s.View()["K"] {
				if v != "v"+col[1:] {
					t.Errorf("inconsistent read %s=%s", col, v)
				}
			}
			_ = s.Conflicted()
			_ = s.SelfCheck()
		}
	}
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go read()
	}
	for i := 0; i < n; i++ {
		writers.Add(1)
		go func(i int) {
			defer writers.Done()
			<-start
			_ = s.Put("K", fmt.Sprintf("c%d", i), 1, fmt.Sprintf("v%d", i))
		}(i)
	}
	close(start)
	writers.Wait()
	done.Store(true)
	readers.Wait()
	got := s.View()["K"]
	for i := 0; i < n; i++ {
		if got[fmt.Sprintf("c%d", i)] != fmt.Sprintf("v%d", i) {
			t.Fatalf("column c%d wrong: %v", i, got)
		}
	}
}
