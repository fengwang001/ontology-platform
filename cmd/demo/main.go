// Command demo 演示分层布隆过滤器去重，不读参数、不联网，退出码恒为 0。
package main

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"ontology/api"
	"ontology/bf"
	"ontology/dedup"
)

func ok(label string, cond bool) {
	if cond {
		fmt.Println("OK: " + label)
	} else {
		fmt.Println("FAIL: " + label)
	}
}

// fp 按题给公式复算单层假阳性率。
func fp(m, n int) float64 {
	e := math.Pow(1-1/float64(m), float64(2*n))
	return (1 - e) * (1 - e)
}

func main() {
	// 1) bf 包：Add/Contains/Merge/Reset/Count/FillRatio 与非法 m。
	b, _ := bf.New(8)
	b.Add("a")
	b2, _ := bf.New(8)
	b2.Add("b")
	b.Merge(b2)
	ratio := b.FillRatio()
	b.Reset()
	_, errBadM := bf.New(0)
	ok("bf Add/Contains/Merge/Reset/Count/FillRatio",
		b2.Count() == 1 && ratio == 0.5 && b.Count() == 0 &&
			!b.Contains("a") && errors.Is(errBadM, bf.ErrInvalidM))

	// 2) 第三节 m=8 cap=2 八步判定（饱和切换由切换后判定结果体现）。
	d, _ := api.New(8, 2)
	keys := []string{"a", "b", "c", "d", "i", "f", "a", "b"}
	want := []bool{true, true, true, true, false, true, false, false}
	dec := make([]bool, 8)
	for i, key := range keys {
		dec[i] = d.Feed([]string{key})[0]
	}
	ok("八步判定与饱和切换序列 a b c d i f a b", func() bool {
		for i := range want {
			if dec[i] != want[i] {
				return false
			}
		}
		return true
	}())

	// 3) 第 5 步 i 假阳性；第 7/8 步 a、b 为真重复。
	ok("第5步i假阳性, 第7/8步a/b真重复",
		!dec[4] && !dec[6] && !dec[7] && dec[5])

	// 4) EstimateFP 与公式代入（当前层 1 键、历史层累计 4 键）完全一致。
	ok("EstimateFP 与公式一致(当前1/历史4)",
		d.EstimateFP() == 1-(1-fp(8, 1))*(1-fp(8, 4)))

	// 5) 长序列与朴素精确集合对照：只许假阳性，绝不许假阴性。
	d2, _ := api.New(128, 37)
	exact := map[string]struct{}{}
	noFN := true
	for i := 0; i < 2000; i++ {
		key := fmt.Sprintf("k%d", (i*37)%211)
		_, known := exact[key]
		if d2.Feed([]string{key})[0] && known {
			noFN = false
		}
		exact[key] = struct{}{}
	}
	ok("与朴素精确集合包含一致(无假阴性)", noFN)

	// 6) 三类可判定哨兵错误且互不相同（空串经 dedup.Feed 的 error 判定）。
	_, eM := api.New(0, 2)
	_, eCap := api.New(8, 0)
	dd, _ := dedup.New(64, 10)
	_, eKey := dd.Feed([]string{"ok", ""})
	ok("三类错误可判定且互不相同",
		errors.Is(eM, dedup.ErrInvalidM) && errors.Is(eCap, dedup.ErrInvalidCap) &&
			errors.Is(eKey, dedup.ErrEmptyKey) &&
			eM != eCap && eKey != eM && eKey != eCap && d2.Feed([]string{"x", ""}) == nil)

	// 7) 被拒整批不留痕：位集/键数（体现为 EstimateFP）不变，之后可继续使用。
	before := dd.EstimateFP()
	_, errRej := dd.Feed([]string{"fresh1", "", "fresh2"})
	after := dd.EstimateFP()
	r, _ := dd.Feed([]string{"fresh1"})
	ok("整批被拒后状态不变且仍可用",
		errors.Is(errRej, dedup.ErrEmptyKey) && before == after && r[0])

	// 8) 大 N（100/1000/10000）下检查个数恒 ≤2k=4：自检内建，不暴露数值。
	ok("大N下检查个数不随N增长(SelfCheck)", d.SelfCheck() == nil)

	// 9) N 个 goroutine 并发只读同一已喂满实例：EstimateFP 与 Feed 逐条相同。
	full, _ := api.New(64, 8)
	full.Feed([]string{"x", "y", "z"})
	const ng = 16
	var wg sync.WaitGroup
	res := make([][]bool, ng)
	fps := make([]float64, ng)
	for g := 0; g < ng; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			fps[g] = full.EstimateFP()
			res[g] = full.Feed([]string{"x", "y", "z"})
		}(g)
	}
	wg.Wait()
	same := true
	for g := 1; g < ng; g++ {
		if fps[g] != fps[0] {
			same = false
		}
		for i := range res[0] {
			if res[g][i] != res[0][i] {
				same = false
			}
		}
	}
	ok("并发只读 EstimateFP/Feed 逐条一致", same)
}
