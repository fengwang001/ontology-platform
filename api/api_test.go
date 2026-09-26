package api_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/gh"
)

func P(x ...int) []gh.Point {
	o := make([]gh.Point, 0, len(x)/2)
	for i := 0; i+1 < len(x); i += 2 {
		o = append(o, gh.Point{X: x[i], Y: x[i+1]})
	}
	return o
}

// TestSimilarityInvariant 钉住不变量2：对 T、S 同施保向相似，映射逐字段不变。
func TestSimilarityInvariant(t *testing.T) {
	sq := P(0, 0, 2, 0, 2, 2, 0, 2)
	e, err := api.NewTemplate(sq)
	if err != nil {
		t.Fatal(err)
	}
	for i, cs := range [][4]int{{1, 0, 5, 5}, {0, 1, 100, 0}, {2, 0, -30, -40}, {2, 1, 1000, 1000}, {-1, 2, 3000, 3000}} {
		m, found, err := e.Match(gh.Xform(sq, cs[0], cs[1], cs[2], cs[3]))
		if err != nil || !found || !reflect.DeepEqual(m, []int{0, 1, 2, 3}) { // 映射逐字段不变
			t.Fatalf("case %d found=%v m=%v err=%v", i, found, m, err)
		}
	}
}

// TestMirrorRejected 钉住不变量3：仅含镜像副本 found=false（用手性三角形）。
func TestMirrorRejected(t *testing.T) {
	tr := P(0, 0, 4, 0, 1, 3)
	e, err := api.NewTemplate(tr)
	if err != nil {
		t.Fatal(err)
	}
	refRot := gh.Xform(P(0, 0, 4, 0, 1, -3), 0, 1, 50, 50)
	withNoise := append(P(9, 9, 13, 9, 10, 6), gh.Point{X: 0, Y: 0}, gh.Point{X: 7, Y: 1})
	for i, mir := range [][]gh.Point{P(9, 9, 13, 9, 10, 6), refRot, withNoise} {
		_, found, err := e.Match(mir)
		if err != nil || found || gh.BruteForceContains(tr, mir) {
			t.Fatalf("mirror case %d: found=%v err=%v", i, found, err)
		}
	}
}

// TestRejectionErrors 钉住故障注入：五类错误各自命中、互不相同。
func TestRejectionErrors(t *testing.T) {
	e, _ := api.NewTemplate(P(0, 0, 2, 0, 2, 2, 0, 2))
	cases := []struct {
		call func() error
		want error
	}{
		{func() error { _, e := api.NewTemplate(P(0, 0, 1, 0)); return e }, api.ErrTooFewTemplatePoints},
		{func() error { _, e := api.NewTemplate(P(0, 0, 1, 0, 2, 0)); return e }, api.ErrCollinearTemplate},
		{func() error { _, _, e := e.Match(P(0, 0, 1, 0)); return e }, api.ErrSceneTooSmall},
		{func() error { _, _, e := e.Match(P(0, 0, 1, 0, 0, 1, 0, 0)); return e }, api.ErrDuplicatePoint},
		{func() error { _, _, e := e.Match(P(0, 0, 1, 0, 0, 1, 10001, 0)); return e }, api.ErrCoordinateOutOfRange},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Fatalf("got %v want %v", err, c.want)
		}
		if seen[c.want] {
			t.Fatalf("sentinel %v not distinct", c.want)
		}
		seen[c.want] = true
	}
	if len(seen) != 5 {
		t.Fatalf("want 5 distinct errors, got %d", len(seen))
	}
}

// TestNoPartialResult 钉住不变量4：拒绝无部分结果，引擎之后仍正常可用。
func TestNoPartialResult(t *testing.T) {
	e, _ := api.NewTemplate(P(0, 0, 2, 0, 2, 2, 0, 2))
	for i, b := range [][]gh.Point{
		P(0, 0, 1, 0), P(0, 0, 1, 0, 0, 1, 0, 0), P(0, 0, 1, 0, 0, 1, 0, 10001),
	} {
		if m, found, err := e.Match(b); err == nil || m != nil || found {
			t.Fatalf("reject %d partial: m=%v f=%v err=%v", i, m, found, err)
		}
	}
	m, found, err := e.Match(P(5, 5, 5, 7, 3, 7, 3, 5))
	if err != nil || !found || !reflect.DeepEqual(m, []int{0, 1, 2, 3}) {
		t.Fatalf("engine unusable after rejection: m=%v f=%v err=%v", m, found, err)
	}
	if e2, err := api.NewTemplate(P(0, 0, 1, 0)); err == nil || e2 != nil {
		t.Fatal("rejected NewTemplate produced partial engine")
	}
}

// TestConcurrentMatch 钉住并发：WaitGroup 同步（不用 sleep），多 goroutine
// 只读同一模板/场景，found 与映射逐字段相同，race 干净。
func TestConcurrentMatch(t *testing.T) {
	tr := P(0, 0, 4, 0, 1, 3)
	e, _ := api.NewTemplate(tr)
	sce := append(gh.Xform(tr, 2, 1, 5000, 5000), gh.Point{X: 0, Y: 0}, gh.Point{X: 1, Y: 9}, gh.Point{X: 9, Y: 1})
	const N = 32
	var wg sync.WaitGroup
	found := make([]bool, N)
	maps := make([][]int, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); maps[g], found[g], _ = e.Match(sce) }(g)
	}
	wg.Wait()
	for g := 1; g < N; g++ {
		if found[g] != found[0] || !reflect.DeepEqual(maps[g], maps[0]) {
			t.Fatalf("goroutine %d differs", g)
		}
	}
	if !found[0] {
		t.Fatal("expected match")
	}
}

// TestSelfCheck 钉住内置自检通过且可并发调用。
func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- api.SelfCheck() }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent SelfCheck: %v", err)
		}
	}
}
