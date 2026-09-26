package pivot

import (
	"math/rand"
	"testing"
)

// 并列最大值时恒取下标最小行，且多次调用结果一致。
func TestPickTieDeterministic(t *testing.T) {
	cases := []struct {
		name string
		a    []float64
		n, k int
		want int
	}{
		{"两行并列", []float64{0, 1, 1, 1, 0, 1, 1, 1, 0}, 3, 0, 1},
		{"三行并列", []float64{2, 0, 2, 0, 2, 0, 0, 0, 1}, 3, 0, 0},
		{"负值并列取绝对值", []float64{0, 0, 0, 0, -5, 0, 0, 5, 1}, 3, 1, 1},
		{"唯一最大", []float64{1, 0, 0, 0, 9, 0, 0, 3, 1}, 3, 1, 1},
		{"k 以下才扫描", []float64{1, 9, 9, 0, 1, 0, 0, 4, 2}, 3, 1, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for i := 0; i < 3; i++ { // 同一输入多次调用结果一致
				got, ok := Pick(c.a, c.n, c.k)
				if !ok || got != c.want {
					t.Fatalf("Pick=%d,%v want %d,true", got, ok, c.want)
				}
			}
		})
	}
}

// 主元行及其以下全为零时 ok=false。
func TestPickSingularColumn(t *testing.T) {
	a := []float64{1, 2, 3, 0, 0, 4, 0, 0, 5} // 第 1 列第 1..2 行全零
	if row, ok := Pick(a, 3, 1); ok || row != -1 {
		t.Fatalf("Pick=%d,%v want -1,false", row, ok)
	}
}

// 整轮消元中「判定奇异的额外扫描」恒为 0，不随 n 增长。
func TestExtraScansAlwaysZero(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		a := genMatrix(n)
		before := extraScans.Load()
		if n <= 1000 { // 小规模跑完整消元
			eliminate(a, n)
		} else { // 大规模驱动逐列主元选择（计数器只在 Pick 内变化）
			for k := 0; k < n; k++ {
				if _, ok := Pick(a, n, k); !ok {
					t.Fatalf("n=%d col %d unexpected singular", n, k)
				}
			}
		}
		if got := extraScans.Load(); got != before || got != 0 {
			t.Fatalf("n=%d extraScans=%d want 0", n, got)
		}
	}
}

// 对角线全非零、其余随机的非奇异矩阵。
func genMatrix(n int) []float64 {
	rng := rand.New(rand.NewSource(int64(n)))
	a := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			a[i*n+j] = rng.Float64()*2 - 1
		}
		a[i*n+i] += 3 // 对角占优，保证非奇异
	}
	return a
}

// 测试内联的标准消元：每列一次 Pick，行交换后消元。
func eliminate(a []float64, n int) {
	for k := 0; k < n; k++ {
		r, ok := Pick(a, n, k)
		if !ok {
			return
		}
		if r != k {
			for j := k; j < n; j++ {
				a[k*n+j], a[r*n+j] = a[r*n+j], a[k*n+j]
			}
		}
		for i := k + 1; i < n; i++ {
			m := a[i*n+k] / a[k*n+k]
			for j := k; j < n; j++ {
				a[i*n+j] -= m * a[k*n+j]
			}
		}
	}
}
