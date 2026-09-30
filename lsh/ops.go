package lsh

import (
	"math"
	"sort"
)

// validateVector 校验向量维度与非零，返回对应的可区分错误。
func (idx *Index) validateVector(vec []int64) error {
	if len(vec) != idx.dim {
		return ErrDimMismatch
	}
	zero := true
	for _, v := range vec {
		if v != 0 {
			zero = false
			break
		}
	}
	if zero {
		return ErrZeroVector
	}
	return nil
}

// signatures 计算 vec 在全部表下的签名。
func (idx *Index) signatures(vec []int64) []uint64 {
	sigs := make([]uint64, idx.numTables)
	for t := 0; t < idx.numTables; t++ {
		sigs[t] = signature(idx.normals[t], idx.numBits, vec)
	}
	return sigs
}

// Insert 插入向量。维度不符、零向量或编号重复时整体拒绝，
// 不改变任何桶。先完成全部校验与签名计算，再在写锁内一次性提交。
func (idx *Index) Insert(id int, vec []int64) error {
	if err := idx.validateVector(vec); err != nil {
		return err
	}
	sigs := idx.signatures(vec)
	stored := make([]int64, len(vec))
	copy(stored, vec)

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if _, exists := idx.entries[id]; exists {
		return ErrDuplicateID
	}
	idx.entries[id] = &entry{vec: stored, sigs: sigs}
	for t := 0; t < idx.numTables; t++ {
		bucket := idx.tables[t][sigs[t]]
		if bucket == nil {
			bucket = make(map[int]struct{})
			idx.tables[t][sigs[t]] = bucket
		}
		bucket[id] = struct{}{}
	}
	return nil
}

// Delete 删除编号。编号不存在时整体拒绝，不改变任何桶。
func (idx *Index) Delete(id int) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	e, exists := idx.entries[id]
	if !exists {
		return ErrIDNotFound
	}
	for t := 0; t < idx.numTables; t++ {
		bucket := idx.tables[t][e.sigs[t]]
		delete(bucket, id)
		if len(bucket) == 0 {
			delete(idx.tables[t], e.sigs[t])
		}
	}
	delete(idx.entries, id)
	return nil
}

// cosine 计算两个向量的余弦相似度（调用方保证均非零向量）。
func cosine(a, b []int64) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// Query 返回与 vec 余弦相似度最高的至多 k 个候选。
//
// 候选为各表中与 vec 同桶向量的去重并集；对候选逐一计算精确余弦，
// 按余弦降序排列，余弦并列时按编号升序，取前 k 个。候选为空时返回
// 空结果。维度不符、零向量或 k 非正时整体拒绝。
func (idx *Index) Query(vec []int64, k int) ([]Result, Stats, error) {
	var stats Stats
	if err := idx.validateVector(vec); err != nil {
		return nil, stats, err
	}
	if k <= 0 {
		return nil, stats, ErrNonPositiveK
	}
	sigs := idx.signatures(vec)

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	candidateSet := make(map[int]struct{})
	for t := 0; t < idx.numTables; t++ {
		for id := range idx.tables[t][sigs[t]] {
			candidateSet[id] = struct{}{}
		}
	}
	stats.Candidates = len(candidateSet)

	results := make([]Result, 0, len(candidateSet))
	for id := range candidateSet {
		results = append(results, Result{ID: id, Cosine: cosine(vec, idx.entries[id].vec)})
	}
	stats.RerankCount = len(results)

	sort.Slice(results, func(i, j int) bool {
		if results[i].Cosine != results[j].Cosine {
			return results[i].Cosine > results[j].Cosine
		}
		return results[i].ID < results[j].ID
	})
	if len(results) > k {
		results = results[:k]
	}
	return results, stats, nil
}
