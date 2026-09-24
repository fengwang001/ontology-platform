package dedup

import (
	"bytes"
	"errors"
	"math"
	"math/rand"
	"ontology/bf"
	"reflect"
	"sync"
	"testing"
)

// 不变量 1：朴素精确集合判「已见」的键系统必判「重复」；假阳性允许，假阴性禁止。
func TestNoFalseNegatives(t *testing.T) {
	for _, c := range []struct{ m, cap, alpha int }{{8, 2, 12}, {64, 7, 80}, {1024, 50, 900}} {
		d, _ := New(c.m, c.cap)
		exact := map[string]struct{}{}
		r := rand.New(rand.NewSource(42))
		for i := 0; i < 3000; i++ {
			key := string(rune('a'+r.Intn(26))) + string(rune('a'+r.Intn(c.alpha)))
			_, seen := exact[key]
			if res, err := d.Feed([]string{key}); err != nil || seen && res[0] {
				t.Fatalf("m=%d cap=%d key=%q err=%v res=%v", c.m, c.cap, key, err, res)
			}
			exact[key] = struct{}{}
		}
	}
}

// 不变量 2：饱和切换时当前层 OR 进历史层、当前层清空且计数归零。
func TestSaturationSwitch(t *testing.T) {
	d, _ := New(8, 2)
	keys := []string{"a", "b", "c", "d", "i", "f", "a", "b"}
	newK := []bool{true, true, true, true, false, true, false, false}
	curN, histN := []int{1, 2, 1, 2, 0, 1, 1, 1}, []int{0, 0, 2, 2, 4, 4, 4, 4}
	fill := []float64{0, 0, .5, .5, 7. / 8, 7. / 8, 7. / 8, 7. / 8}
	for i, key := range keys {
		if res, err := d.Feed([]string{key}); err != nil || res[0] != newK[i] ||
			d.cur.Count() != curN[i] || d.histN != histN[i] ||
			math.Abs(d.hist.FillRatio()-fill[i]) > 1e-12 {
			t.Fatalf("step %d %s: res=%v cur=%d hist=%d fill=%v",
				i+1, key, res, d.cur.Count(), d.histN, d.hist.FillRatio())
		}
	}
	if d.cur.FillRatio() != .25 || !d.cur.Contains("f") {
		t.Fatal("current layer wrong")
	}
}

// 不变量 3：EstimateFP 精确等于公式代入、单调不减；(丙) 历史 n=4 与错按 n=2。
func TestEstimateFP(t *testing.T) {
	d, _ := New(8, 2)
	prev := -1.0
	for i, key := range []string{"a", "b", "c", "d", "i", "f", "a", "b"} {
		d.feedOne(key)
		want := 1 - (1-layerFP(8, d.cur.Count()))*(1-layerFP(8, d.histN))
		if got := d.EstimateFP(); got != want || got < prev {
			t.Fatalf("step %d fp=%v want=%v from=%v", i+1, got, want, prev)
		}
		prev = d.EstimateFP()
	}
	if fp4, fp2 := layerFP(8, 4), layerFP(8, 2); math.Abs(fp4-.431) > .005 ||
		math.Abs(fp2-.171) > .005 || fp4 <= fp2 {
		t.Fatalf("history FP n=4=%v n=2=%v", fp4, fp2)
	}
}

// 不变量 4 / 第五节：三类哨兵错误互不相同；被拒整批不留痕，之后仍可正常使用。
func TestRejectedOpsNoTrace(t *testing.T) {
	bad := []struct {
		m, cap int
		want   error
	}{
		{0, 2, ErrInvalidM}, {-1, 2, ErrInvalidM}, {8, 0, ErrInvalidCap}, {8, -3, ErrInvalidCap},
	}
	for _, c := range bad {
		if _, err := New(c.m, c.cap); !errors.Is(err, c.want) {
			t.Fatalf("New(%d,%d)=%v want %v", c.m, c.cap, err, c.want)
		}
	}
	if ErrInvalidM == ErrInvalidCap || ErrInvalidM == ErrEmptyKey ||
		ErrInvalidCap == ErrEmptyKey {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	d, _ := New(64, 10)
	d.Feed([]string{"u", "v"})
	for _, batch := range [][]string{{""}, {"u", ""}, {"", "u"}, {"u", "v", ""}} {
		before, cn, hn := d.EstimateFP(), d.cur.Count(), d.histN
		res, err := d.Feed(batch)
		if !errors.Is(err, ErrEmptyKey) || res != nil ||
			d.EstimateFP() != before || d.cur.Count() != cn || d.histN != hn {
			t.Fatalf("batch %v: err=%v res=%v or state changed", batch, err, res)
		}
	}
	if res, err := d.Feed([]string{"w"}); err != nil || !res[0] {
		t.Fatalf("unusable after rejection: %v %v", res, err)
	}
}

// 第四节：探针数不随 N 线性增长，真新键恒 ≤2k=4、重复键短路为 k；非导出字段包内读。
func TestProbeCountConstant(t *testing.T) {
	pc, _ := New(1<<20, 100001)
	kk := func(i int) string { return string(bytes.Repeat([]byte{0xff}, i+1)) }
	seq := 0
	for _, n := range []int{100, 1000, 10000} {
		for ; pc.cur.Count() < n; seq++ {
			pc.feedOne(kk(seq))
		}
		pc.feedOne(kk(seq)) // 真新键：两层都查，探针数恒为 2k
		if pc.lastProbes > 2*bf.K {
			t.Fatalf("N=%d probes=%d > %d", n, pc.lastProbes, 2*bf.K)
		}
		seq++
	}
}

// 第六节：并发只读同一实例，EstimateFP 与 Feed 逐条一致；start channel 同步，无 sleep。
func TestConcurrentReadOnly(t *testing.T) {
	d, _ := New(256, 50)
	d.Feed([]string{"x", "y", "z"})
	keys := []string{"x", "y", "z", "x"}
	const ng = 32
	fps, res := make([]float64, ng), make([][]bool, ng)
	start, wg := make(chan struct{}), sync.WaitGroup{}
	for g := 0; g < ng; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			fps[g] = d.EstimateFP()
			res[g], _ = d.Feed(keys)
		}(g)
	}
	close(start)
	wg.Wait()
	for g := 1; g < ng; g++ {
		if fps[g] != fps[0] || !reflect.DeepEqual(res[g], res[0]) {
			t.Fatalf("g=%d fp=%v/%v res=%v/%v", g, fps[g], fps[0], res[g], res[0])
		}
	}
}

func TestSelfCheck(t *testing.T) {
	d, _ := New(8, 2)
	if err := d.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
