// Command demo 是稀疏向量点积的离线自检演示：不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"ontology/api"
	"ontology/spv"
	"os"
	"slices"
	"sync"
)

var failed bool

func report(name string, ok bool) {
	s := "OK"
	if !ok {
		s, failed = "FAIL", true
	}
	fmt.Printf("%s: %s\n", name, s)
}
func main() {
	a, _ := api.Build(10, []int{1, 3, 5}, []float64{2, 3, 5})
	b, _ := api.Build(10, []int{0, 1, 5}, []float64{4, 7, 6})
	tr, sum := trace(a, b) // 第三节五行归并：指针、比较、匹配、贡献、累计
	fmt.Printf("merge: %s\n", tr)
	report("merge-result=44", sum == 44)
	x, y, z := int(merge(a, b, 'A')), int(merge(a, b, 'B')), int(merge(a, b, 'C'))
	report(fmt.Sprintf("bugs A(甲)=%d B(乙)=%d C(丙)=%d", x, y, z), x == 14 && y == 59 && z == 30)
	report("dense-reference", api.Dot(a, b) == dense(a, b))
	report("canonical", canonical())
	report("sentinel-errors", fourErrors())
	report("no-trace", noTrace(a, b))
	report("access-count-over-m", api.SelfCheck() == nil)
	report("concurrent", concurrent(a, b))
	if failed {
		os.Exit(1)
	}
}

// trace 回放两指针归并，输出五行，第 5 行为双端耗尽。
func trace(a, b *spv.Vec) (string, float64) {
	ia, va := a.Snapshot()
	ib, vb := b.Snapshot()
	out, sum, i, j, n := "", 0.0, 0, 0, 0
	for i < len(ia) && j < len(ib) {
		n++
		x, y := ia[i], ib[j]
		switch {
		case x == y:
			sum += va[i] * vb[j]
			out += fmt.Sprintf("%d(i%d,j%d)A%d=B%d hit+%g→%g|", n, i, j, x, y, va[i]*vb[j], sum)
			i, j = i+1, j+1
		case x < y:
			out += fmt.Sprintf("%d(i%d,j%d)A%d<B%d miss→%g|", n, i, j, x, y, sum)
			i++
		default:
			out += fmt.Sprintf("%d(i%d,j%d)A%d>B%d miss→%g|", n, i, j, x, y, sum)
			j++
		}
	}
	return out + fmt.Sprintf("%d(i%d,j%d) end→%g", n+1, i, j, sum), sum
}

// merge 可注入三种错误实现：'A'=(甲)上界少一；'B'=(乙)无视下标按位置对齐；'C'=(丙)不等时同进。
func merge(a, b *spv.Vec, mode rune) float64 {
	ia, va := a.Snapshot()
	ib, vb := b.Snapshot()
	sum, i, j, lim := 0.0, 0, 0, len(ia)
	if mode == 'A' {
		lim--
	}
	for i < lim && j < len(ib) {
		switch {
		case mode == 'B' || ia[i] == ib[j]:
			sum, i, j = sum+va[i]*vb[j], i+1, j+1
		case mode == 'C':
			i, j = i+1, j+1
		case ia[i] < ib[j]:
			i++
		default:
			j++
		}
	}
	return sum
}

// dense 是朴素稠密参照：按 m 个稠密位置逐位取值乘加（缺失位 Get 返回 0）。
func dense(a, b *spv.Vec) float64 {
	sum := 0.0
	for k := 0; k < 10; k++ {
		sum += a.Get(k) * b.Get(k)
	}
	return sum
}

// canonical 以随机顺序 Set（含覆盖与零值删除），再核验规范形。
func canonical() bool {
	v, r := spv.New(50), rand.New(rand.NewSource(1))
	for n := 0; n < 200; n++ {
		v.Set(r.Intn(49), float64(r.Intn(5)-2))
	}
	idx, val := v.Snapshot()
	if len(idx) != len(val) {
		return false
	}
	for k, x := range idx {
		if x < 0 || x >= 50 || val[k] == 0 || (k > 0 && idx[k-1] >= x) {
			return false
		}
	}
	return true
}
func fourErrors() bool {
	idxS := [][]int{{10}, {1, 2}, {1}, {2, 1}}
	valS := [][]float64{{1}, {1, 0}, {1, 2}, {1, 2}}
	errs := []error{api.ErrIndexOutOfRange, api.ErrZeroValue, api.ErrLenMismatch, api.ErrNotSorted}
	seen := map[error]bool{}
	for k, want := range errs {
		_, err := api.Build(10, idxS[k], valS[k])
		if !errors.Is(err, want) || seen[err] {
			return false
		}
		seen[err] = true
	}
	return len(seen) == 4
}
func noTrace(a, b *spv.Vec) bool {
	d := api.Dot(a, b)
	ok := errors.Is(a.Set(-1, 1), api.ErrIndexOutOfRange) && api.Dot(a, b) == d
	ok = ok && a.Set(1, 9) == nil && a.Get(1) == 9 // 拒绝后仍可正常写
	return ok && a.Set(1, 2) == nil && api.Dot(a, b) == d
}
func concurrent(a, b *spv.Vec) bool {
	var wg sync.WaitGroup
	res := make([]uint64, 64)
	wg.Add(len(res))
	for g := range res {
		go func(g int) { defer wg.Done(); res[g] = math.Float64bits(api.Dot(a, b)) }(g)
	}
	wg.Wait()
	return slices.Equal(res[1:], res[:len(res)-1])
}
