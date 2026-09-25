package api

import (
	"bytes"
	"errors"
	"math/rand"
	"sync"
	"testing"
)

func TestSelfCheck(t *testing.T) {
	m, err := New([]byte("abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SelfCheck(); err != nil { // inv.1 cross-checks mtfseq; inv.3 mtf
		t.Fatalf("SelfCheck: %v", err)
	}
	// Random-data round-trip property (cases generated in a loop).
	alpha := []byte("abcdefgh")
	rng := rand.New(rand.NewSource(7))
	for n := 0; n < 20; n++ {
		data := make([]byte, rng.Intn(64))
		for j := range data {
			data[j] = alpha[rng.Intn(len(alpha))]
		}
		g, _ := New(alpha)
		enc, err := g.Encode(data)
		if err != nil {
			t.Fatalf("case %d: %v", n, err)
		}
		if out, e := g.Decode(enc); e != nil || !bytes.Equal(out, data) {
			t.Fatalf("case %d: round trip %q err %v", n, out, e)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	alpha := []byte("abcdef")
	for _, data := range [][]byte{{}, []byte("ababaadd"), []byte("fedcba"), []byte("aaaaaa")} {
		m, _ := New(alpha)
		enc, err := m.Encode(data)
		if err != nil {
			t.Fatal(err)
		}
		out, err := m.Decode(enc) // invariant 2: Decode(Encode(data)) == data
		if err != nil || !bytes.Equal(out, data) {
			t.Fatalf("data %q: round trip %q err %v", data, out, err)
		}
	}
}

func TestDeterministic(t *testing.T) {
	alpha, data := []byte("abcdef"), []byte("ababaadd")
	first, _ := New(alpha)
	ref, err := first.Encode(data)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ { // fresh instances, byte-for-byte equal
		m, _ := New(alpha)
		if got, e := m.Encode(data); e != nil || !bytes.Equal(got, ref) {
			t.Fatalf("run %d: %v err %v", i, got, e)
		}
	}
}

func TestAtomicFailure(t *testing.T) {
	alpha := []byte("abcdef")
	if errors.Is(ErrUnknownSymbol, ErrInvalidIndex) ||
		errors.Is(ErrUnknownSymbol, ErrInvalidAlphabet) ||
		errors.Is(ErrInvalidIndex, ErrInvalidAlphabet) {
		t.Fatal("sentinel errors are not distinct")
	}
	cases := []struct {
		name string
		run  func(*MTF) error
		want error
	}{
		{"unknown symbol", func(m *MTF) error { _, e := m.Encode([]byte("abz")); return e }, ErrUnknownSymbol},
		{"bad index", func(m *MTF) error { _, e := m.Decode([]byte{0, 9}); return e }, ErrInvalidIndex},
		{"empty alphabet", func(*MTF) error { _, e := New(nil); return e }, ErrInvalidAlphabet},
		{"duplicate alphabet", func(*MTF) error { _, e := New([]byte("aba")); return e }, ErrInvalidAlphabet},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := New(alpha)
			if err := tc.run(m); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if m == nil { // constructor rejection: no instance state to probe
				return
			}
			fresh, _ := New(alpha) // rejected call left no trace; still usable
			got, _ := m.Encode([]byte("ab"))
			want, _ := fresh.Encode([]byte("ab"))
			if !bytes.Equal(got, want) {
				t.Fatalf("state changed after rejection: %v vs %v", got, want)
			}
		})
	}
}

// TestConcurrentIndependentInstances: N goroutines each own an instance,
// encode then decode the same input; results equal the serial result. No
// sleeps; a start channel and WaitGroup order the goroutines.
func TestConcurrentIndependentInstances(t *testing.T) {
	const n = 32
	alpha, data := []byte("abcdef"), []byte("ababaaddfedcbafedcba")
	serial, _ := New(alpha)
	serialEnc, err := serial.Encode(data)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start, errs := make(chan struct{}), make(chan error, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			m, err := New(alpha)
			if err != nil {
				errs <- err
				return
			}
			enc, err := m.Encode(data)
			if err != nil || !bytes.Equal(enc, serialEnc) {
				errs <- errors.New("encode differs from serial")
				return
			}
			if out, e := m.Decode(enc); e != nil || !bytes.Equal(out, data) {
				errs <- errors.New("decode differs from input")
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
