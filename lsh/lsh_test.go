package lsh

import (
	"math/rand"
	"sort"
	"testing"
)

// bruteForce 为暴力精确检索参照：对全部向量计算余弦，
// 按余弦降序、编号升序取前 k 个。
func bruteForce(vecs map[int][]int64, query []int64, k int) []Result {
	all := make([]Result, 0, len(vecs))
	for id, v := range vecs {
		all = append(all, Result{ID: id, Cosine: cosine(query, v)})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Cosine != all[j].Cosine {
			return all[i].Cosine > all[j].Cosine
		}
		return all[i].ID < all[j].ID
	})
	if len(all) > k {
		all = all[:k]
	}
	return all
}

// genDataset 用固定种子的 PRNG 生成确定性数据集，保证测试可复现。
func genDataset(rng *rand.Rand, n, dim int) map[int][]int64 {
	vecs := make(map[int][]int64, n)
	for id := 0; id < n; id++ {
		v := make([]int64, dim)
		for i := range v {
			v[i] = int64(rng.Intn(21) - 10)
		}
		if v[0] == 0 {
			v[0] = 1
		}
		vecs[id] = v
	}
	return vecs
}

// buildIndex 按编号升序插入数据集，构造索引。
func buildIndex(t *testing.T, dim, tables, bits int, seed int64, vecs map[int][]int64) *Index {
	t.Helper()
	idx, err := New(dim, tables, bits, seed)
	if err != nil {
		t.Fatalf("New(dim=%d, tables=%d, bits=%d) 失败: %v", dim, tables, bits, err)
	}
	ids := make([]int, 0, len(vecs))
	for id := range vecs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		if err := idx.Insert(id, vecs[id]); err != nil {
			t.Fatalf("Insert(%d) 失败: %v", id, err)
		}
	}
	return idx
}

func resultIDs(rs []Result) []int {
	ids := make([]int, len(rs))
	for i, r := range rs {
		ids[i] = r.ID
	}
	return ids
}

// randSource 返回固定种子的 PRNG，保证测试可复现。
func randSource(seed int64) *rand.Rand {
	return rand.New(rand.NewSource(seed))
}
