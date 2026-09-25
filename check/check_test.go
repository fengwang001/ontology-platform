package check

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/id"
	"ontology/uf"
)

func TestVsNaive(t *testing.T) { // 语义 1/2/3/5：连通性、计数、传递性、边界
	cases := [][][2]int{ // 每行首元素 {n, 0} 给出规模，其余为 Union 序列
		{{3, 0}, {0, 1}, {1, 2}, {0, 2}},
		{{0, 0}},
		{{1, 0}, {0, 0}},
		{{6, 0}, {0, 5}, {2, 3}, {3, 5}, {1, 2}, {4, 4}},
	}
	for _, c := range cases {
		if !run(uf.New(c[0][0]), c[1:]) {
			t.Fatalf("n=%d ops=%v diverges from naive", c[0][0], c[1:])
		}
	}
}

func TestBadIndex(t *testing.T) { // 语义 4：哨兵错误
	u := uf.New(1)
	_, e1 := u.Find(1)
	_, e2 := u.Union(0, 2)
	_, e3 := u.Connected(-1, 0)
	for _, err := range []error{e1, e2, e3} {
		if !errors.Is(err, id.ErrBadIndex) {
			t.Errorf("got %v, want ErrBadIndex", err)
		}
	}
}

// badUF 内联错误实现：Union 总把 y 的根挂到 x 的根下，不做平衡。
type badUF struct {
	parent []int
	hops   int
}

func (b *badUF) find(x int) int {
	for b.hops = 0; b.parent[x] != x; b.hops++ {
		x = b.parent[x]
	}
	return x
}
func (b *badUF) union(x, y int) { b.parent[b.find(y)] = b.find(x) }
func TestFindHops(t *testing.T) { // 第三、四节：树高与均摊跳数界
	const n = 1 << 16
	u, r := uf.New(n), rand.New(rand.NewSource(1))
	bad := &badUF{parent: make([]int, n)}
	for i := range bad.parent {
		bad.parent[i] = i
	}
	for i := 0; i < n-1; i++ {
		u.Union(i+1, i)
		bad.union(i+1, i)
	}
	if u.Find(0); u.LastFindHops() > 16 {
		t.Errorf("ranked chain hops=%d, want <=16", u.LastFindHops())
	}
	if bad.find(0); bad.hops <= 1000 {
		t.Errorf("unbalanced hops=%d, want >1000", bad.hops)
	}
	rnd := uf.New(n)
	for i := 0; i < 10000; i++ {
		rnd.Union(r.Intn(n), r.Intn(n))
	}
	for x := 0; x < n; x++ {
		if rnd.Find(x); rnd.LastFindHops() > 2*16+2 { // 2·log2(n)+2
			t.Fatalf("Find(%d) hops=%d, want <=%d", x, rnd.LastFindHops(), 2*16+2)
		}
	}
}

func TestConcurrentReads(t *testing.T) { // 第五节：并发只读一致
	u := uf.New(64)
	for i := 0; i < 63; i++ {
		u.Union(i, i+1)
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if ok, _ := u.Connected(0, 63); !ok || u.Count() != 1 {
					t.Error("inconsistent read")
				}
			}
		}()
	}
	wg.Wait()
}
