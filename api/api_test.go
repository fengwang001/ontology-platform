package api_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/fnv"
)

func makeLeaves(n int) [][]byte { // 确定性伪随机叶子，长 1~16 字节
	lv := make([][]byte, n)
	for i := range lv {
		b := make([]byte, 1+i%16)
		for j := range b {
			b[j] = byte(i*31 + j*17)
		}
		lv[i] = b
	}
	return lv
}

func naiveRoot(leaves [][]byte) [4]byte { // 朴素参照：从叶子哈希逐层 combine 到根
	lvl := make([]uint32, len(leaves))
	for i, d := range leaves {
		lvl[i] = fnv.Leaf(d)
	}
	for len(lvl) > 1 {
		next := make([]uint32, len(lvl)/2)
		for i := range next {
			next[i] = fnv.Combine(lvl[2*i], lvl[2*i+1])
		}
		lvl = next
	}
	return [4]byte{byte(lvl[0] >> 24), byte(lvl[0] >> 16), byte(lvl[0] >> 8), byte(lvl[0])}
}

// eachTree 对每档规模（1..1024 个叶子）建树后交给 f。
func eachTree(t *testing.T, f func(tr *api.Tree, lv [][]byte)) {
	for _, n := range []int{1, 2, 4, 8, 16, 64, 256, 1024} {
		lv := makeLeaves(n)
		tr, err := api.Build(lv)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		f(tr, lv)
	}
}

// 不变量 1：任意叶子的认证路径都能通过 Verify。
func TestProofVerify(t *testing.T) {
	eachTree(t, func(tr *api.Tree, lv [][]byte) {
		for i, d := range lv {
			p, _ := tr.Proof(i)
			if ok, err := tr.Verify(i, d, p); err != nil || !ok {
				t.Fatalf("n=%d i=%d: ok=%v err=%v", len(lv), i, ok, err)
			}
		}
	})
}

// 不变量 2：Root 与朴素参照逐字节相同。
func TestRootMatchesNaive(t *testing.T) {
	eachTree(t, func(tr *api.Tree, lv [][]byte) {
		if tr.Root() != naiveRoot(lv) {
			t.Errorf("n=%d: root differs from naive reference", len(lv))
		}
	})
}

// 不变量 3：改动任意叶子的任意一个字节，Verify 必为 false。
func TestTamperDetected(t *testing.T) {
	eachTree(t, func(tr *api.Tree, lv [][]byte) {
		for i, d := range lv {
			p, _ := tr.Proof(i)
			bad := append([]byte(nil), d...)
			bad[0] ^= 0xFF
			if ok, err := tr.Verify(i, bad, p); ok || !errors.Is(err, api.ErrRootMismatch) {
				t.Errorf("n=%d i=%d: tampered leaf accepted ok=%v err=%v", len(lv), i, ok, err)
			}
		}
	})
}

// 不变量 4 + 故障注入：四类拒绝产生四个互不相同的哨兵错误，且不改变树状态、树仍可用。
func TestRejectionKeepsState(t *testing.T) {
	lv := makeLeaves(16)
	tr, err := api.Build(lv)
	if err != nil {
		t.Fatal(err)
	}
	root, n := tr.Root(), tr.LeafCount()
	p0, _ := tr.Proof(0)
	_, e1 := api.Build(nil)
	_, e2 := api.Build(lv[:3])
	_, e3 := tr.Proof(-1)
	_, e4 := tr.Verify(0, []byte("tampered"), p0)
	got := []error{e1, e2, e3, e4}
	want := []error{api.ErrEmptyLeaves, api.ErrNotPowerOfTwo, api.ErrIndexOutOfRange, api.ErrRootMismatch}
	seen := map[error]bool{}
	for i, w := range want {
		if seen[w] || got[i] != w {
			t.Errorf("case %d: dup=%v got=%v want=%v", i, seen[w], got[i], w)
		}
		seen[w] = true
	}
	if tr.Root() != root || tr.LeafCount() != n {
		t.Fatal("state changed after rejections")
	}
	p, _ := tr.Proof(5)
	if ok, err := tr.Verify(5, lv[5], p); !ok || err != nil {
		t.Fatal("tree unusable after rejections")
	}
}

// 并发：64 个 goroutine 同时验证正确路径与篡改路径，结果必须一致。
func TestConcurrentVerify(t *testing.T) {
	lv := makeLeaves(128)
	tr, _ := api.Build(lv)
	paths := make([][][4]byte, len(lv))
	for i := range lv {
		paths[i], _ = tr.Proof(i)
	}
	var wg sync.WaitGroup
	var bad atomic.Int64
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range lv {
				b := append([]byte(nil), lv[i]...)
				b[0]++
				ok1, e1 := tr.Verify(i, lv[i], paths[i])
				ok2, e2 := tr.Verify(i, b, paths[i])
				if !ok1 || e1 != nil || ok2 || !errors.Is(e2, api.ErrRootMismatch) {
					bad.Add(1)
				}
			}
			if tr.SelfCheck() != nil || tr.LeafCount() != 128 {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Errorf("%d inconsistent results", bad.Load())
	}
}
