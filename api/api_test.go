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

func fiveFields() []wire.Field {
	return []wire.Field{
		{Num: 1, Wire: wire.WireVarint, U: 150},
		{Num: 2, Wire: wire.WireBytes, B: []byte("A")},
		{Num: 3, Wire: wire.WireFixed32, U: 0x01020304},
		{Num: 4, Wire: wire.WireVarint, U: uint64(enc.Zigzag32(-1))},
		{Num: 5, Wire: wire.WireFixed64, U: 0x0807060504030201},
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestAPIRandomRoundTrip: invariant 1 through the public façade across
// generated messages with different sizes and wire types.
func TestAPIRandomRoundTrip(t *testing.T) {
	c := api.New()
	rng := rand.New(rand.NewPCG(7, 8))
	for iter := 0; iter < 40; iter++ {
		n := 1 + rng.IntN(8)
		wires := []int{wire.WireVarint, wire.WireFixed64, wire.WireBytes, wire.WireFixed32}
		fs := make([]wire.Field, n)
		schema := make(map[int]int, n)
		for i := range fs {
			fs[i].Num, fs[i].Wire = i+1, wires[rng.IntN(4)]
			schema[i+1] = fs[i].Wire
			switch fs[i].Wire {
			case wire.WireVarint, wire.WireFixed64:
				fs[i].U = rng.Uint64()
			case wire.WireBytes:
				fs[i].B = make([]byte, rng.IntN(20))
				for k := range fs[i].B {
					fs[i].B[k] = byte(rng.Uint64())
				}
			case wire.WireFixed32:
				fs[i].U = uint64(rng.Uint32())
			}
		}
		raw, err := c.Marshal(fs)
		if err != nil {
			t.Fatal(err)
		}
		m, err := c.Unmarshal(raw, schema)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fs {
			g, ok := c.GetField(m, f.Num)
			if !ok || g.Wire != f.Wire || g.U != f.U || !bytes.Equal(g.B, f.B) {
				t.Fatalf("iter %d field %d mismatch: %+v != %+v", iter, f.Num, g, f)
			}
		}
	}
}

// TestConcurrentMarshalUnmarshal: N readers share one read-only buffer and
// must agree field-by-field; N writers marshal distinct messages and read
// them back. Synchronised by WaitGroup only (run under -race).
func TestConcurrentMarshalUnmarshal(t *testing.T) {
	const n = 64
	c := api.New()
	fs := fiveFields()
	shared, err := c.Marshal(fs)
	if err != nil {
		t.Fatal(err)
	}
	schema := map[int]int{1: 0, 2: 2, 3: 5, 4: 0, 5: 1}
	var wg sync.WaitGroup
	var mu sync.Mutex
	fail := func(format string, a ...any) {
		mu.Lock()
		t.Errorf(format, a...)
		mu.Unlock()
	}
	for g := 0; g < n; g++ {
		wg.Add(3)
		go func() { // concurrent readers of the same read-only bytes
			defer wg.Done()
			m, err := c.Unmarshal(shared, schema)
			if err != nil || len(m) != 5 {
				fail("reader: %v len=%d", err, len(m))
				return
			}
			for _, f := range fs {
				if got := m[f.Num]; got.U != f.U || !bytes.Equal(got.B, f.B) {
					fail("reader field %d mismatch", f.Num)
					return
				}
			}
		}()
		go func(g int) { // concurrent writers of distinct values
			defer wg.Done()
			want := uint64(g*1009 + 1)
			b, err := c.Marshal([]wire.Field{{Num: 1, Wire: 0, U: want}})
			if err != nil {
				fail("writer marshal: %v", err)
				return
			}
			m, err := c.Unmarshal(b, map[int]int{1: 0})
			if err != nil || m[1].U != want {
				fail("writer readback g=%d: %v got=%d", g, err, m[1].U)
			}
		}(g)
		go func() { // concurrent SelfCheck alongside the traffic
			defer wg.Done()
			if err := c.SelfCheck(); err != nil {
				fail("selfcheck: %v", err)
			}
		}()
	}
	wg.Wait()
}
