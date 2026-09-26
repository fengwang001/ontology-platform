package api_test

import (
	"bytes"
	"math/rand/v2"
	"sync"
	"testing"

	"ontology/api"
	"ontology/enc"
	"ontology/wire"
)

var (
	schema  = map[int]int{1: 0, 2: 2, 3: 5, 4: 0, 5: 1}
	testAPI = api.New()
)

func sampleMsg() []wire.Field {
	return []wire.Field{
		{Num: 1, Wire: 0, U: 150}, {Num: 2, Wire: 2, B: []byte("A")},
		{Num: 3, Wire: 5, U: 0x01020304}, {Num: 4, Wire: 0, U: uint64(enc.Zigzag32(-1))},
		{Num: 5, Wire: 1, U: 0x0807060504030201},
	}
}

func equal(a, b map[int]wire.Field) bool {
	if len(a) != len(b) {
		return false
	}
	for n, f := range a {
		if g, ok := b[n]; !ok || g.U != f.U || g.Wire != f.Wire || !bytes.Equal(g.B, f.B) {
			return false
		}
	}
	return true
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

// TestRoundTrip: random legal field sets survive Marshal->Unmarshal.
func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for trial := 0; trial < 200; trial++ {
		fs := []wire.Field{}
		sc, used := map[int]int{}, map[int]bool{}
		for i, n := 0, 1+rng.IntN(8); i < n; i++ {
			num := 1 + rng.IntN(50)
			for used[num] {
				num = 1 + rng.IntN(50)
			}
			used[num] = true
			wt := []int{0, 1, 2, 5}[rng.IntN(4)]
			f := wire.Field{Num: num, Wire: wt, U: rng.Uint64()}
			if wt == 2 {
				f.B = []byte{byte(num), byte(rng.Uint64()), byte(i)}
			}
			fs, sc[num] = append(fs, f), wt
		}
		dec, err := testAPI.Unmarshal(must(testAPI.Marshal(fs)), sc)
		if err != nil || len(dec) != len(fs) {
			t.Fatalf("trial %d: decode failed", trial)
		}
		for _, f := range fs {
			g, ok := testAPI.GetField(dec, f.Num)
			bad := !ok || g.Wire != f.Wire || !bytes.Equal(g.B, f.B)
			if f.Wire == 5 {
				bad = bad || uint32(g.U) != uint32(f.U)
			} else if f.Wire != 2 {
				bad = bad || g.U != f.U
			}
			if bad {
				t.Fatalf("trial %d: field %d mismatch", trial, f.Num)
			}
		}
	}
}

// TestOrderIndependent: shuffled bytes decode to the same fields.
func TestOrderIndependent(t *testing.T) {
	msg := sampleMsg()
	want, _ := testAPI.Unmarshal(must(testAPI.Marshal(msg)), schema)
	rng := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 50; i++ {
		sh := append([]wire.Field(nil), msg...)
		rng.Shuffle(len(sh), func(x, y int) { sh[x], sh[y] = sh[y], sh[x] })
		if got, err := testAPI.Unmarshal(must(testAPI.Marshal(sh)), schema); err != nil || !equal(want, got) {
			t.Fatal("order-dependent result")
		}
	}
}

// TestNaiveReference: Encode matches a hand-built textbook byte string.
func TestNaiveReference(t *testing.T) {
	got := must(testAPI.Marshal(sampleMsg()))
	want := []byte{0x08, 0x96, 0x01, 0x12, 0x01, 0x41, 0x1D, 0x04, 0x03, 0x02,
		0x01, 0x20, 0x01, 0x29, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %X want %X", got, want)
	}
}

// TestFailureAtomic: rejections return (nil, error) and leave no trace.
func TestFailureAtomic(t *testing.T) {
	bad := [][]byte{{0x12, 0x05, 0x41}, {0x0B, 0x00}, {0x00, 0x00}, {0x08, 0x01, 0x08, 0x02},
		{0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}}
	for _, b := range bad {
		if m, err := testAPI.Unmarshal(b, schema); err == nil || m != nil {
			t.Fatalf("%X: not atomic", b)
		}
	}
	// Usable-after-rejection is pinned by wire.TestFaultInjection.
	if err := testAPI.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrent: parallel Unmarshal of shared bytes + parallel Marshal.
func TestConcurrent(t *testing.T) {
	shared := must(testAPI.Marshal(sampleMsg()))
	const n = 16
	var wg sync.WaitGroup
	errs := make(chan string, 2*n)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if d, err := testAPI.Unmarshal(shared, schema); err != nil || len(d) != 5 || d[4].U != 1 {
				errs <- "unmarshal mismatch"
			}
		}()
		go func(v uint64) {
			defer wg.Done()
			d, err := testAPI.Unmarshal(must(testAPI.Marshal([]wire.Field{{Num: 1, Wire: 0, U: v}})), map[int]int{1: 0})
			if err != nil || d[1].U != v {
				errs <- "own message mismatch"
			}
		}(uint64(i) << 40)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
