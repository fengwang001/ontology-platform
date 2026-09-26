package api

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

var pentagon = []Point{{X: 0, Y: 0}, {X: 5, Y: 1}, {X: 6, Y: 4}, {X: 3, Y: 6}, {X: 1, Y: 5}}

func randConvex(rng *rand.Rand, m int) []Point {
	k := m/2 + 1
	xs := rng.Perm(61)[:k]
	sort.Ints(xs)
	poly := make([]Point, 0, 2*k-2)
	for _, v := range xs {
		x := int64(v - 30)
		poly = append(poly, Point{X: x, Y: x * x})
	}
	for i := k - 2; i >= 1; i-- {
		x := int64(xs[i] - 30)
		poly = append(poly, Point{X: x, Y: 2701 - x*x})
	}
	return poly
}
func rc0(poly []Point) (int64, [][2]int) {
	if err := New(poly); err != nil {
		return -1, nil
	}
	d2, pairs, _ := Diameter()
	return d2, pairs
}

func TestFaultInjectionDistinctErrors(t *testing.T) {
	cases := []struct {
		name string
		poly []Point
		want error
	}{
		{"顶点不足", []Point{{X: 0, Y: 0}, {X: 1, Y: 1}}, ErrTooFewVertices},
		{"坐标越界-负Y", []Point{{X: 0, Y: 0}, {X: 0, Y: -10001}, {X: 5, Y: 0}}, ErrOutOfRange},
		{"非凸-凹顶点", []Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 4}, {X: 2, Y: 2}}, ErrNotConvex},
		{"自交", []Point{{X: 0, Y: 0}, {X: 4, Y: 4}, {X: 0, Y: 4}, {X: 4, Y: 0}}, ErrNotConvex},
		{"顶点重复", []Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 4}, {X: 0, Y: 0}}, ErrNotConvex},
		{"顺时针", []Point{{X: 0, Y: 0}, {X: 0, Y: 4}, {X: 4, Y: 4}, {X: 4, Y: 0}}, ErrNotConvex},
	}
	for _, c := range cases {
		if err := New(c.poly); !errors.Is(err, c.want) {
			t.Errorf("%s: 错误 %v，应为 %v", c.name, err, c.want)
		}
	}
}

func TestBruteForceConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	polys := [][]Point{pentagon, {{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 6, Y: 4}, {X: 0, Y: 4}}}
	for _, m := range []int{4, 6, 10, 30, 60} {
		for r := 0; r < 3; r++ {
			polys = append(polys, randConvex(rng, m))
		}
	}
	for i, poly := range polys {
		if err := New(poly); err != nil {
			t.Fatalf("多边形 %d 被误拒: %v", i, err)
		}
		if d2, _, err := Diameter(); err != nil || d2 != bruteMaxD2(poly) {
			t.Errorf("多边形 %d: d² 与暴力不一致", i)
		}
	}
}

func TestReturnedPairsAreAntipodal(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for r := 0; r < 10; r++ {
		poly := randConvex(rng, 4+2*r)
		d2, pairs := rc0(poly)
		for _, pr := range pairs {
			if d, ok := allAntipodalPairs(poly)[pr]; !ok || d != d2 {
				t.Errorf("多边形 %d: 点对 %v 非对跖点对或 d²≠最大", r, pr)
			}
		}
	}
}

func TestTiesComplete(t *testing.T) {
	tied := [][]Point{{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 6, Y: 4}, {X: 0, Y: 4}}, {{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 4}, {X: 0, Y: 4}}}
	for _, poly := range tied {
		if _, pairs := rc0(poly); len(pairs) != 2 || pairs[0] != [2]int{0, 2} || pairs[1] != [2]int{1, 3} {
			t.Errorf("并列两对 (0,2),(1,3) 未全部列出: %v", pairs)
		}
	}
	rng := rand.New(rand.NewSource(3))
	for r := 0; r < 10; r++ {
		poly := randConvex(rng, 4+2*r)
		d2, pairs := rc0(poly)
		want := 0
		for _, d := range allAntipodalPairs(poly) {
			if d == d2 {
				want++
			}
		}
		if want != len(pairs) {
			t.Errorf("多边形 %d: 并列对跖点对 %d 个，返回 %d 个", r, want, len(pairs))
		}
	}
}

func TestRejectionLeavesNoPartialResult(t *testing.T) {
	if err := New(pentagon); err != nil {
		t.Fatal(err)
	}
	d0, p0, _ := Diameter()
	bads := [][]Point{{{X: 0, Y: 0}, {X: 1, Y: 1}}, {{X: 0, Y: 0}, {X: 20000, Y: 0}, {X: 0, Y: 5}}, {{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 4}, {X: 2, Y: 2}}}
	for i, b := range bads {
		if New(b) == nil {
			t.Fatalf("非法输入 %d 未被拒绝", i)
		}
		if d1, p1, err := Diameter(); err != nil || d1 != d0 || !reflect.DeepEqual(p1, p0) {
			t.Errorf("非法输入 %d 拒绝后状态被污染", i)
		}
	}
}

func TestConcurrentDiameterConsistent(t *testing.T) {
	if err := New(pentagon); err != nil {
		t.Fatal(err)
	}
	d0, p0, _ := Diameter()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 50; i++ {
				d, p, err := Diameter()
				if err != nil || d != d0 || !reflect.DeepEqual(p, p0) || SelfCheck() != nil {
					t.Error("并发读取结果不一致")
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
}
