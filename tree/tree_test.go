package tree

import "testing"

// Verify 的 combine 次数必须随 log2(m) 增长而非随 m 线性增长，
// 证明走的是 O(log n) 认证路径而非整树重算。计数器为非导出字段，仅此内部测试可读。
func TestCombineCountLog(t *testing.T) {
	for k := 7; k <= 13; k++ {
		m := 1 << k
		leaves := make([][]byte, m)
		for i := range leaves {
			leaves[i] = []byte{byte(i), byte(i >> 8), byte(i >> 16)}
		}
		tr, err := Build(leaves)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		p, err := tr.Proof(m / 3)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		ok, err := tr.Verify(m/3, leaves[m/3], p)
		if err != nil || !ok {
			t.Fatalf("m=%d: verify failed ok=%v err=%v", m, ok, err)
		}
		if got := tr.last.Load(); got > int64(k)+1 {
			t.Errorf("m=%d: combine count %d exceeds log2(m)+1=%d", m, got, k+1)
		}
	}
}
