package join

// 仅放必须读取非导出字段（lastCandidates / sz）的复杂度测试；其余测试在 join_ext_test.go。

import (
	"testing"

	"ontology/rel"
)

// TestCandidatesNoScan：S.b 域与 T.c 域不相交，插 R 恒 0 候选对，计数不随 m 增长。
func TestCandidatesNoScan(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		j := New(0)
		for i := 0; i < m; i++ {
			_ = j.Insert("S", rel.Tuple{X: i, Y: i})         // b ∈ [0,m)
			_ = j.Insert("T", rel.Tuple{X: m + i, Y: m + i}) // c ∈ [m,2m)
		}
		if err := j.Insert("R", rel.Tuple{X: 7, Y: 0}); err != nil {
			t.Fatal(err)
		}
		if j.lastCandidates != 0 || j.sz != 0 {
			t.Fatalf("m=%d: candidates=%d sz=%d, want 0/0 (index miss, no m×m scan)",
				m, j.lastCandidates, j.sz)
		}
	}
}

// TestCandidatesBounded：有 k 个匹配时，候选对数 ≤ 真正结果多重度 + 常数。
func TestCandidatesBounded(t *testing.T) {
	const slack = 4
	for _, k := range []int{1, 5, 20, 100} {
		j := New(0)
		for i := 0; i < k; i++ {
			_ = j.Insert("S", rel.Tuple{X: 1, Y: i})
			_ = j.Insert("T", rel.Tuple{X: i, Y: 9})
			_ = j.Insert("T", rel.Tuple{X: i, Y: 8}) // 每个 c 两份 T：候选对=结果多重度=2k
		}
		if err := j.Insert("R", rel.Tuple{X: 0, Y: 1}); err != nil {
			t.Fatal(err)
		}
		if j.lastCandidates > j.sz+slack || j.sz != 2*k {
			t.Fatalf("k=%d: candidates=%d sz=%d", k, j.lastCandidates, j.sz)
		}
	}
}
