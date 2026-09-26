package blk

import "testing"

// TestPartsCoverage 钉住不变量 2：Parts 精确划分 [0,n)——
// 无重叠、无缝隙、每下标恰属一块，尾块大小恰为 n%b。表驱动多档规模。
func TestPartsCoverage(t *testing.T) {
	cases := []struct{ n, b int }{
		{1, 1}, {5, 2}, {6, 2}, {6, 3}, {7, 3}, {10, 4}, {17, 17}, {100, 17},
	}
	for _, c := range cases {
		ps := Parts(c.n, c.b)
		if len(ps) == 0 {
			t.Fatalf("Parts(%d,%d) empty", c.n, c.b)
		}
		if ps[0].Start != 0 || ps[len(ps)-1].End != c.n {
			t.Fatalf("Parts(%d,%d) 不覆盖 [0,n): %+v", c.n, c.b, ps)
		}
		seen := make([]int, c.n) // 每个下标被多少块覆盖，必须恒为 1
		for i, p := range ps {
			if p.Start >= p.End || p.End-p.Start > c.b {
				t.Fatalf("Parts(%d,%d) 非法块 #%d: %+v", c.n, c.b, i, p)
			}
			if i > 0 && ps[i-1].End != p.Start {
				t.Fatalf("Parts(%d,%d) 块间有缝隙/重叠 @%d", c.n, c.b, i)
			}
			for x := p.Start; x < p.End; x++ {
				seen[x]++
			}
		}
		for x, v := range seen {
			if v != 1 {
				t.Fatalf("Parts(%d,%d) 下标 %d 被覆盖 %d 次", c.n, c.b, x, v)
			}
		}
		// 尾块语义：整除时无尾块；否则仅末块不满且大小为 n%b。
		rem := c.n % c.b
		last := ps[len(ps)-1]
		if rem == 0 {
			if last.End-last.Start != c.b {
				t.Fatalf("Parts(%d,%d) 整除却出现不满块", c.n, c.b)
			}
		} else if last.End-last.Start != rem {
			t.Fatalf("Parts(%d,%d) 尾块大小=%d, 期望 %d", c.n, c.b, last.End-last.Start, rem)
		}
	}
}

// TestTailLookupO1 钉住第四节：定位尾块访问的边界条目数恒为 0，
// 不随 n（100/1000/10000）增长——证明闭式 O(1) 而非线性扫描。
func TestTailLookupO1(t *testing.T) {
	const b = 17
	for _, n := range []int{100, 1000, 10000} {
		wantStart := n - n%b // 尾块起点
		st, sz := TailBlock(n, b, n-1)
		if got := lookupEntriesLoad(); got != 0 {
			t.Fatalf("n=%d TailBlock 访问边界条目 %d 个，必须为 0（非 O(1)）", n, got)
		}
		if st != wantStart || sz != n%b {
			t.Fatalf("n=%d 尾块=(%d,%d) 期望 (%d,%d)", n, st, sz, wantStart, n%b)
		}
		// 普通（非尾）块下标也必须闭式命中，且计数器仍为 0。
		for _, k := range []int{0, b - 1, b, n / 2} {
			ps, _ := TailBlock(n, b, k)
			if got := lookupEntriesLoad(); got != 0 {
				t.Fatalf("n=%d k=%d 访问边界条目 %d 个", n, k, got)
			}
			if ps != (k/b)*b {
				t.Fatalf("n=%d k=%d 块起点=%d 期望 %d", n, k, ps, (k/b)*b)
			}
		}
	}
}

// TestTailBlockMatchesParts 交叉验证：闭式 TailBlock 与 Parts 划分逐块一致。
func TestTailBlockMatchesParts(t *testing.T) {
	for _, c := range []struct{ n, b int }{{5, 2}, {10, 3}, {100, 17}} {
		ps := Parts(c.n, c.b)
		for _, q := range ps {
			for k := q.Start; k < q.End; k++ {
				st, sz := TailBlock(c.n, c.b, k)
				if st != q.Start || st+sz != q.End {
					t.Fatalf("(%d,%d,k=%d) TailBlock=(%d,%d) 与块 %+v 不符", c.n, c.b, k, st, sz, q)
				}
			}
		}
	}
}
