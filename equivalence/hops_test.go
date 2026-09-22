package equivalence_test

import (
	"fmt"
	"testing"

	"ontology/equivalence"
)

const chainN = 20001 // v0 .. v20000

func buildChain(t *testing.T, uf *equivalence.UnionFind) {
	t.Helper()
	for i := 0; i < chainN-1; i++ {
		a := fmt.Sprintf("v%d", i)
		b := fmt.Sprintf("v%d", i+1)
		if _, _, err := uf.Union(a, b); err != nil {
			t.Fatal(err)
		}
	}
}

// v0..v20000 链式合并后，对 v20000 连续 Find 一万次：
// 路径压缩生效后平均跳数必须小于 3。
func TestFindHopCountWithPathCompression(t *testing.T) {
	uf := equivalence.New()
	buildChain(t, uf)

	uf.ResetHopCount()
	if rep, err := uf.Find(fmt.Sprintf("v%d", chainN-1)); err != nil || rep != "v0" {
		t.Fatalf("首次 Find 结果 = (%q,%v)，期望 (v0,nil)", rep, err)
	}
	first := uf.HopCount()
	if first < 0 {
		t.Fatalf("首次 Find 跳数异常: %d", first)
	}

	uf.ResetHopCount()
	const repeats = 10000
	for i := 0; i < repeats; i++ {
		if rep, err := uf.Find(fmt.Sprintf("v%d", chainN-1)); err != nil || rep != "v0" {
			t.Fatalf("Find = (%q,%v)", rep, err)
		}
	}
	avg := float64(uf.HopCount()) / repeats
	if avg >= 3 {
		t.Fatalf("压缩后平均跳数 = %.3f，应 < 3", avg)
	}
	t.Logf("首次 Find 跳数=%d，后续 %d 次平均跳数=%.4f", first, repeats, avg)
}

// 平衡式两两合并能在按秩（或按大小）规则下造出深度约 log2(n) 的树。
// 若缺少路径压缩，对最深节点的后续 Find 平均跳数仍是 O(log n) ≫ 3。
func TestDeepTreeRequiresPathCompression(t *testing.T) {
	uf := equivalence.New()

	// 第 0 层：相邻两两合并；第 1 层：跨 2 合并……得到深度逐层增加的秩树。
	for gap := 1; gap < chainN; gap *= 2 {
		for i := 0; i+gap < chainN; i += 2 * gap {
			a := fmt.Sprintf("v%d", i)
			b := fmt.Sprintf("v%d", i+gap)
			if _, _, err := uf.Union(a, b); err != nil {
				t.Fatal(err)
			}
		}
	}

	target := fmt.Sprintf("v%d", chainN-1)
	uf.ResetHopCount()
	if rep, err := uf.Find(target); err != nil || rep != "v0" {
		t.Fatalf("Find = (%q,%v)，期望 v0", rep, err)
	}
	first := uf.HopCount()
	if first < 4 {
		t.Fatalf("深度树首次 Find 跳数 = %d，构造未达预期深度", first)
	}

	uf.ResetHopCount()
	const repeats = 10000
	for i := 0; i < repeats; i++ {
		if rep, err := uf.Find(target); err != nil || rep != "v0" {
			t.Fatalf("Find = (%q,%v)", rep, err)
		}
	}
	avg := float64(uf.HopCount()) / repeats
	if avg >= 3 {
		t.Fatalf("压缩后平均跳数 = %.3f，应 < 3", avg)
	}
	t.Logf("深度树首次 Find 跳数=%d，后续 %d 次平均跳数=%.4f", first, repeats, avg)
}

// 路径压缩后，链路上任意节点再次 Find 都应当接近 O(1)。
func TestPathCompressionFlattensChain(t *testing.T) {
	uf := equivalence.New()
	buildChain(t, uf)

	// 对中间与末尾各 Find 一次以触发整链压缩。
	for _, idx := range []int{chainN - 1, chainN / 2} {
		if _, err := uf.Find(fmt.Sprintf("v%d", idx)); err != nil {
			t.Fatal(err)
		}
	}

	uf.ResetHopCount()
	for i := 0; i < chainN; i++ {
		if _, err := uf.Find(fmt.Sprintf("v%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	avg := float64(uf.HopCount()) / chainN
	if avg >= 1.0 {
		t.Fatalf("压缩后全节点平均跳数 = %.4f，应 <= 1", avg)
	}
}
