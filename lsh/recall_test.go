package lsh

import (
	"math/rand"
	"testing"
)

// TestRecallMonotonicInTables 验证召回率随表数 L=1,2,4,8 单调不降。
// 判定依据：表满足嵌套关系，L 增大时候选集合只会扩大，
// 对候选做精确余弦精排后，recall@K 必然单调不降。
func TestRecallMonotonicInTables(t *testing.T) {
	const (
		dim      = 16
		bits     = 8
		k        = 10
		seed     = int64(20260930)
		numVecs  = 400
		numQuery = 30
	)
	rng := rand.New(rand.NewSource(42))
	vecs := genDataset(rng, numVecs, dim)
	queries := genDataset(rand.New(rand.NewSource(7)), numQuery, dim)

	tableCounts := []int{1, 2, 4, 8}
	recalls := make([]float64, len(tableCounts))
	for i, L := range tableCounts {
		idx := buildIndex(t, dim, L, bits, seed, vecs)
		var sum float64
		for q := 0; q < numQuery; q++ {
			query := queries[q]
			got, stats, err := idx.Query(query, k)
			if err != nil {
				t.Fatalf("L=%d Query 失败: %v", L, err)
			}
			want := bruteForce(vecs, query, k)
			gotSet := make(map[int]struct{}, len(got))
			for _, r := range got {
				gotSet[r.ID] = struct{}{}
			}
			hit := 0
			for _, w := range want {
				if _, ok := gotSet[w.ID]; ok {
					hit++
				}
			}
			recall := float64(hit) / float64(len(want))
			sum += recall
			if q < 3 {
				t.Logf("输入: L=%d b=%d K=%d 查询#%d; 输出: 命中=%d/%d 候选数=%d 精排次数=%d; 判定依据: 与暴力精确 Top-%d 求交集",
					L, bits, k, q, hit, len(want), stats.Candidates, stats.RerankCount, k)
			}
		}
		recalls[i] = sum / float64(numQuery)
	}
	t.Logf("输入: 表数序列 %v; 输出: 平均召回率 %v; 判定依据: 召回率随表数单调不降", tableCounts, recalls)
	for i := 1; len(recalls) > i; i++ {
		if recalls[i] < recalls[i-1] {
			t.Errorf("召回率未单调不降: L=%d 时 %.4f < L=%d 时 %.4f",
				tableCounts[i], recalls[i], tableCounts[i-1], recalls[i-1])
		}
	}
}

// TestCandidatesNonIncreasingInBits 验证候选数随位数 b 增加单调不增。
// 判定依据：位满足嵌套关系，b+1 位的同桶集合是 b 位同桶集合的子集，
// 各表并集后候选集合同样嵌套收缩。
func TestCandidatesNonIncreasingInBits(t *testing.T) {
	const (
		dim      = 16
		tables   = 4
		seed     = int64(20260930)
		numVecs  = 400
		numQuery = 20
	)
	rng := rand.New(rand.NewSource(42))
	vecs := genDataset(rng, numVecs, dim)
	queries := genDataset(rand.New(rand.NewSource(7)), numQuery, dim)

	bitCounts := []int{2, 4, 6, 8, 10, 12}
	indexes := make([]*Index, len(bitCounts))
	for i, b := range bitCounts {
		indexes[i] = buildIndex(t, dim, tables, b, seed, vecs)
	}
	totals := make([]int, len(bitCounts))
	for q := 0; q < numQuery; q++ {
		prev := -1
		perQuery := make([]int, len(bitCounts))
		for i := range bitCounts {
			_, stats, err := indexes[i].Query(queries[q], numVecs)
			if err != nil {
				t.Fatalf("b=%d Query 失败: %v", bitCounts[i], err)
			}
			perQuery[i] = stats.Candidates
			totals[i] += stats.Candidates
			if prev >= 0 && stats.Candidates > prev {
				t.Errorf("查询#%d 候选数未单调不增: b=%d 时 %d > b=%d 时 %d",
					q, bitCounts[i], stats.Candidates, bitCounts[i-1], prev)
			}
			prev = stats.Candidates
		}
		if q < 3 {
			t.Logf("输入: 查询#%d 位数字列 %v; 输出: 候选数序列 %v; 判定依据: 位嵌套导致候选集合嵌套收缩",
				q, bitCounts, perQuery)
		}
	}
	t.Logf("输入: 位数字列 %v; 输出: 候选总数序列 %v; 判定依据: 候选数随位数单调不增", bitCounts, totals)
	for i := 1; i < len(totals); i++ {
		if totals[i] > totals[i-1] {
			t.Errorf("候选总数未单调不增: b=%d 时 %d > b=%d 时 %d",
				bitCounts[i], totals[i], bitCounts[i-1], totals[i-1])
		}
	}
}
