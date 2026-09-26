package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
)

func leaves8() [][]byte {
	l := make([][]byte, 8)
	for i := range l {
		l[i] = []byte(fmt.Sprint(i + 1))
	}
	return l
}

func TestSelfCheck(t *testing.T) {
	tr, err := api.Build(leaves8())
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestKnownVector(t *testing.T) {
	tr, _ := api.Build(leaves8())
	if got, want := tr.Root(), [4]byte{0x08, 0x87, 0xB3, 0xC1}; got != want {
		t.Fatalf("root %X != %X", got, want)
	}
	p, err := tr.Proof(2)
	if err != nil {
		t.Fatal(err)
	}
	want := [][4]byte{{0x1D, 0x76, 0xAA, 0xB1}, {0xA6, 0xAB, 0xC1, 0xF2}, {0x09, 0x7F, 0xB8, 0x4D}}
	if len(p) != len(want) {
		t.Fatalf("path len %d != %d", len(p), len(want))
	}
	for k := range want {
		if p[k] != want[k] {
			t.Fatalf("path[%d] %X != %X", k, p[k], want[k])
		}
	}
	ok, err := tr.Verify(2, []byte("3"), p)
	if !ok || err != nil {
		t.Fatalf("verify: ok=%v err=%v", ok, err)
	}
}

func TestErrorsDistinct(t *testing.T) {
	errs := []error{api.ErrEmptyLeaves, api.ErrNotPowerOfTwo, api.ErrIndexOutOfRange, api.ErrRootMismatch}
	for i := range errs {
		for j := range errs {
			if i != j && errors.Is(errs[i], errs[j]) {
				t.Fatalf("errors %d and %d not distinct: %v / %v", i, j, errs[i], errs[j])
			}
		}
	}
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"empty", func() error { _, e := api.Build(nil); return e }, api.ErrEmptyLeaves},
		{"not-pow2", func() error { _, e := api.Build(leaves8()[:5]); return e }, api.ErrNotPowerOfTwo},
	}
	for _, c := range cases {
		if err := c.run(); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	tr, _ := api.Build(leaves8())
	if _, err := tr.Proof(-1); !errors.Is(err, api.ErrIndexOutOfRange) {
		t.Fatalf("Proof(-1): %v", err)
	}
	p, _ := tr.Proof(0)
	if ok, err := tr.Verify(0, []byte("x"), p); ok || !errors.Is(err, api.ErrRootMismatch) {
		t.Fatalf("mismatch: ok=%v err=%v", ok, err)
	}
}

func TestConcurrentAPI(t *testing.T) {
	tr, _ := api.Build(leaves8())
	root := tr.Root()
	var wg sync.WaitGroup
	res := make(chan bool, 128)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			i := g % 8
			p, _ := tr.Proof(i)
			good, _ := tr.Verify(i, []byte(fmt.Sprint(i+1)), p)
			tam, err := tr.Verify(i, []byte("zz"), p)
			ok := good && !tam && errors.Is(err, api.ErrRootMismatch) &&
				tr.Root() == root && tr.LeafCount() == 8 && tr.SelfCheck() == nil
			res <- ok
		}(g)
	}
	wg.Wait()
	close(res)
	for r := range res {
		if !r {
			t.Fatal("inconsistent concurrent api use")
		}
	}
}
