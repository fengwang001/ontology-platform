package tree

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/fnv"
)

func makeLeaves(n int) [][]byte {
	l := make([][]byte, n)
	for i := range l {
		l[i] = []byte(fmt.Sprintf("leaf-%d", i))
	}
	return l
}

// naiveRoot is the independent reference: leaf hashes combined pairwise,
// level by level, straight to the root.
func naiveRoot(leaves [][]byte) uint32 {
	lvl := make([]uint32, len(leaves))
	for i, d := range leaves {
		lvl[i] = fnv.LeafHash(d)
	}
	for len(lvl) > 1 {
		nx := make([]uint32, len(lvl)/2)
		for i := range nx {
			nx[i] = fnv.Combine(lvl[2*i], lvl[2*i+1])
		}
		lvl = nx
	}
	return lvl[0]
}

func TestRootMatchesNaive(t *testing.T) {
	for _, n := range []int{1, 2, 4, 8, 16, 256, 1024} {
		leaves := makeLeaves(n)
		tr, err := Build(leaves)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if tr.Root() != naiveRoot(leaves) || tr.LeafCount() != n {
			t.Fatalf("n=%d: root 0x%08X != naive 0x%08X or LeafCount=%d", n, tr.Root(), naiveRoot(leaves), tr.LeafCount())
		}
	}
}

func TestProofVerifyRoundTrip(t *testing.T) {
	for _, n := range []int{1, 2, 8, 64} {
		leaves := makeLeaves(n)
		tr, _ := Build(leaves)
		for i := 0; i < n; i++ {
			p, err := tr.Proof(i)
			ok, verr := tr.Verify(i, leaves[i], p)
			if err != nil || verr != nil || !ok {
				t.Fatalf("n=%d i=%d: err=%v verr=%v ok=%v", n, i, err, verr, ok)
			}
		}
	}
}

func TestTamperDetected(t *testing.T) {
	leaves := makeLeaves(8)
	tr, _ := Build(leaves)
	for i := 0; i < 8; i++ {
		bad := []byte("tampered-" + string(leaves[i]))
		p, _ := tr.Proof(i)
		ok, err := tr.Verify(i, bad, p)
		if ok || !errors.Is(err, ErrRootMismatch) {
			t.Fatalf("leaf %d: tamper not detected (ok=%v err=%v)", i, ok, err)
		}
	}
}

func TestVerifyCombineCountLogarithmic(t *testing.T) {
	for k := 7; k <= 13; k++ {
		m := 1 << k
		tr, _ := Build(makeLeaves(m))
		p, _ := tr.Proof(m / 3)
		if _, err := tr.Verify(m/3, []byte(fmt.Sprintf("leaf-%d", m/3)), p); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if got := int(tr.lastCombine.Load()); got != k {
			t.Fatalf("m=%d: combines=%d, want log2(m)=%d (linear growth?)", m, got, k)
		}
	}
}

func TestRejectionKeepsState(t *testing.T) {
	if _, err := Build(nil); !errors.Is(err, ErrEmptyLeaves) {
		t.Fatalf("empty: %v", err)
	}
	for _, n := range []int{3, 5, 6, 7, 100} {
		if _, err := Build(makeLeaves(n)); !errors.Is(err, ErrNotPowerOfTwo) {
			t.Fatalf("n=%d: %v", n, err)
		}
	}
	leaves := makeLeaves(8)
	tr, _ := Build(leaves)
	before := tr.Root()
	for _, idx := range []int{-1, 8, 100} {
		if _, err := tr.Proof(idx); !errors.Is(err, ErrIndexOutOfRange) {
			t.Fatalf("Proof(%d): %v", idx, err)
		}
		if _, err := tr.Verify(idx, leaves[0], nil); !errors.Is(err, ErrIndexOutOfRange) {
			t.Fatalf("Verify(%d): %v", idx, err)
		}
	}
	p, _ := tr.Proof(0)
	if ok, err := tr.Verify(0, []byte("nope"), p); ok || !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("root mismatch: ok=%v err=%v", ok, err)
	}
	if ok, err := tr.Verify(0, leaves[0], p[:len(p)-1]); ok || !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("short path: ok=%v err=%v", ok, err)
	}
	ok, err := tr.Verify(0, leaves[0], p)
	if tr.Root() != before || !ok || err != nil {
		t.Fatal("state changed or unusable after rejections")
	}
}

func TestConcurrentVerify(t *testing.T) {
	leaves := makeLeaves(64)
	tr, _ := Build(leaves)
	var wg sync.WaitGroup
	res := make(chan bool, 256)
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			i := g % 64
			p, _ := tr.Proof(i)
			good, err1 := tr.Verify(i, leaves[i], p)
			tam, err2 := tr.Verify(i, []byte("zz"), p)
			res <- good && err1 == nil && !tam && errors.Is(err2, ErrRootMismatch)
		}(g)
	}
	wg.Wait()
	close(res)
	for r := range res {
		if !r {
			t.Fatal("inconsistent concurrent verify")
		}
	}
}
