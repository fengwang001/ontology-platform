package meas

import (
	"math/rand"
	"testing"

	"ontology/poly"
)

// genChain 生成 m 个点的严格凸链（抛物线上的点），用于规模测试。
func genChain(m int) []poly.Point {
	v := make([]poly.Point, m)
	for i := range v {
		v[i] = poly.Point{X: int64(i), Y: int64(i * i)}
	}
	return v
}

// TestLinearReads 白盒断言：一次 Area/Centroid 扫描实际读取计数恰好等于顶点数，
// 每条边读一次、每顶点 O(1)，证明 O(n) 线性、无重复遍历。
func TestLinearReads(t *testing.T) {
	sizes := []int{100, 500, 1000, 5000, 10000}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 5; i++ { // 随机规模补档
		sizes = append(sizes, 100+rng.Intn(9901))
	}
	for _, m := range sizes {
		if _, _, _, reads := measure(genChain(m)); reads != m {
			t.Errorf("m=%d: reads=%d, want exactly %d", m, reads, m)
		}
	}
}

// TestVerifyLinearReads 自检谓词在正确规模上应返回 true。
func TestVerifyLinearReads(t *testing.T) {
	if !VerifyLinearReads(3, 100, 10000) {
		t.Error("VerifyLinearReads returned false")
	}
}
