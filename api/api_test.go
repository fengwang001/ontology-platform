package api

import (
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func opsOf(ss []string) []Op {
	r := make([]Op, len(ss))
	for i, s := range ss {
		r[i] = opOf(s)
	}
	return r
}

func nextOp(rng *rand.Rand, live map[string]int64, keys int) (op Op, skip bool) {
	k := "k" + strconv.Itoa(rng.Intn(keys))
	if s, ok := live[k]; ok {
		if rng.Intn(2) == 0 {
			return Op{Key: k, Score: s}, false
		}
		return Op{}, true
	}
	return Op{Key: k, Score: int64(rng.Intn(6)), Add: true}, false
}

func stream(t *testing.T, seed int64, n, cap, keys, steps int) {
	t.Helper()
	mv, _ := New(n, cap)
	rng, live, all := rand.New(rand.NewSource(seed)), map[string]int64{}, []Change{}
	for i := 0; i < steps; i++ {
		op, skip := nextOp(rng, live, keys)
		if skip {
			continue
		}
		var e error
		if all, e = check(mv, live, all, op, nil, n); e != nil {
			t.Fatalf("seed=%d step=%d: %v", seed, i, e)
		}
	}
}

// TestTenStep pins the prescribed ten-step changelog and final Top-N.
func TestTenStep(t *testing.T) {
	mv, _ := New(3, 16)
	seq := strings.Fields("+a50 +b70 +c50 +d60 +e50 -b70 -e50 +f55 -d60 +g45")
	exp := strings.Split("+a50|+b70|+c50|-c50 +d60||-b70 +c50||-c50 +f55|-d60 +c50|", "|")
	live, all := map[string]int64{}, []Change{}
	for i, s := range seq {
		var e error
		if all, e = check(mv, live, all, opOf(s), strings.Fields(exp[i]), 3); e != nil {
			t.Fatalf("step %d: %v", i+1, e)
		}
	}
	want := []Row{{Key: "f", Score: 55}, {Key: "a", Score: 50}, {Key: "c", Score: 50}}
	if got := mv.View(); !reflect.DeepEqual(got, want) {
		t.Fatalf("final view=%v want %v", got, want)
	}
}

// TestBatchRecompute: View() vs full sort+slice on random streams (inv 1,3).
func TestBatchRecompute(t *testing.T) {
	for _, c := range [][4]int{{1, 3, 200, 40}, {2, 5, 200, 40}, {3, 1, 200, 40}, {7, 4, 64, 20}, {11, 8, 500, 60}} {
		stream(t, int64(c[0]), c[1], c[2], c[3], 600)
	}
}

func TestChangelogPrefix(t *testing.T) { stream(t, 99, 3, 64, 20, 400) }

// TestSentinelErrors pins the four distinct, decidable errors.
func TestSentinelErrors(t *testing.T) {
	for _, c := range []struct {
		name      string
		n, cap    int
		setup, op string
		want      error
	}{
		{"N zero", 0, 5, "", "", ErrInvalidN},
		{"cap below N", 5, 3, "", "", ErrInvalidN},
		{"duplicate key", 2, 4, "+a1", "+a1", ErrDuplicateKey},
		{"missing key", 2, 4, "", "-q1", ErrMissingRow},
		{"score mismatch", 2, 4, "+a1", "-a9", ErrMissingRow},
		{"over cap", 2, 2, "+a1 +b1", "+c1", ErrTooManyRows},
	} {
		mv, err := New(c.n, c.cap)
		if c.want == ErrInvalidN {
			if !errors.Is(err, c.want) {
				t.Fatalf("%s: got %v", c.name, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if c.setup != "" {
			if _, err := mv.Apply(opsOf(strings.Fields(c.setup))); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
		}
		if _, err := mv.Apply([]Op{opOf(c.op)}); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.want)
		}
	}
}

// TestAtomicBatch: a rejected later op discards the whole batch (inv 4).
func TestAtomicBatch(t *testing.T) {
	mv, _ := New(2, 8)
	if _, err := mv.Apply(opsOf([]string{"+a1", "+b1", "+c1", "+d1"})); err != nil {
		t.Fatal(err)
	}
	live, view := mv.Live(), mv.View()
	if _, err := mv.Apply(opsOf([]string{"-b1", "+z9", "-q1"})); !errors.Is(err, ErrMissingRow) {
		t.Fatalf("got %v want ErrMissingRow", err)
	}
	if mv.Live() != live || !reflect.DeepEqual(mv.View(), view) {
		t.Fatalf("rejected batch left a trace: %d %v", mv.Live(), mv.View())
	}
}

// TestConcurrentRead: concurrent readers get field-identical views, no sleep.
func TestConcurrentRead(t *testing.T) {
	mv, _ := New(10, 400)
	rng := rand.New(rand.NewSource(5))
	seed := make([]Op, 300)
	for i := range seed {
		seed[i] = Op{Key: "k" + strconv.Itoa(i), Score: int64(rng.Intn(50)), Add: true}
	}
	if _, err := mv.Apply(seed); err != nil {
		t.Fatal(err)
	}
	const g = 32
	var wg sync.WaitGroup
	views := make([][]Row, g)
	wg.Add(g)
	for i := 0; i < g; i++ {
		go func(i int) { defer wg.Done(); views[i] = mv.View() }(i)
	}
	wg.Wait()
	for i := 1; i < g; i++ {
		if !reflect.DeepEqual(views[0], views[i]) {
			t.Fatalf("goroutine %d view differs", i)
		}
	}
}
