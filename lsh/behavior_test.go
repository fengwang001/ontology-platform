package lsh

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

// TestZeroDotSignatureBit 验证点积恰为零时签名位取 1，点积为负时取 0。
// 判定依据：签名规则为点积 >= 0 取 1，否则取 0。
func TestZeroDotSignatureBit(t *testing.T) {
	const (
		dim   = 8
		table = 0
		plane = 0
		seed  = int64(99)
	)
	normal := normalAt(seed, table, plane, dim)
	// 构造与法向量正交的整数向量：取两个非零分量 i、j，
	// v[i] = -normal[j], v[j] = normal[i]，则点积恒为零。
	i, j := -1, -1
	for d := 0; d < dim; d++ {
		if normal[d] == 0 {
			continue
		}
		if i < 0 {
			i = d
		} else {
			j = d
			break
		}
	}
	if i < 0 || j < 0 {
		t.Fatalf("法向量非零分量不足: %v", normal)
	}
	ortho := make([]int64, dim)
	ortho[i] = -normal[j]
	ortho[j] = normal[i]

	normals := [][]int64{normal}
	sigZero := signature(normals, 1, ortho)
	t.Logf("输入: 法向量=%v 正交向量=%v 点积=0; 输出: 签名位=%d; 判定依据: 点积 >= 0 取 1",
		normal, ortho, sigZero&1)
	if sigZero&1 != 1 {
		t.Errorf("点积为零时签名位应为 1，实际为 %d", sigZero&1)
	}

	neg := make([]int64, dim)
	for d := range neg {
		neg[d] = -normal[d]
	}
	sigNeg := signature(normals, 1, neg)
	t.Logf("输入: 法向量=%v 反向向量=%v 点积<0; 输出: 签名位=%d; 判定依据: 点积 < 0 取 0",
		normal, neg, sigNeg&1)
	if sigNeg&1 != 0 {
		t.Errorf("点积为负时签名位应为 0，实际为 %d", sigNeg&1)
	}
}

// TestCosineTieBreakByID 验证余弦并列时按编号升序。
func TestCosineTieBreakByID(t *testing.T) {
	idx := buildIndex(t, 2, 1, 1, 7, map[int][]int64{
		5: {1, 0},
		3: {2, 0},
		9: {0, 1},
	})
	query := []int64{3, 0}
	got, stats, err := idx.Query(query, 3)
	if err != nil {
		t.Fatalf("Query 失败: %v", err)
	}
	t.Logf("输入: 查询=%v K=3; 输出: %v 候选数=%d 精排次数=%d; 判定依据: 余弦降序、并列按编号升序",
		query, got, stats.Candidates, stats.RerankCount)
	// 5 与 3 同向且同桶，余弦均为 1，编号小者在前。
	wantIDs := []int{3, 5}
	if !reflect.DeepEqual(resultIDs(got), wantIDs) {
		t.Errorf("并列排序错误: 得到 %v，期望 %v", resultIDs(got), wantIDs)
	}
	if len(got) == 2 && (got[0].Cosine != 1 || got[1].Cosine != 1) {
		t.Errorf("余弦值不符: %v", got)
	}
}

// TestEmptyCandidates 验证候选为空时返回空结果且统计为零。
func TestEmptyCandidates(t *testing.T) {
	idx := buildIndex(t, 4, 2, 8, 7, map[int][]int64{1: {1, 2, 3, 4}})
	if err := idx.Delete(1); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	got, stats, err := idx.Query([]int64{1, 1, 1, 1}, 5)
	if err != nil {
		t.Fatalf("Query 失败: %v", err)
	}
	t.Logf("输入: 空索引查询 K=5; 输出: 结果=%v 候选数=%d 精排次数=%d; 判定依据: 候选为空返回空结果",
		got, stats.Candidates, stats.RerankCount)
	if len(got) != 0 || stats.Candidates != 0 || stats.RerankCount != 0 {
		t.Errorf("空候选应返回空结果与零统计，得到 %v %+v", got, stats)
	}
}

// TestInvalidInputs 验证各类非法输入被整体拒绝且原因可区分，
// 被拒绝的操作不改变任何桶。
func TestInvalidInputs(t *testing.T) {
	ctorCases := []struct {
		name              string
		dim, tables, bits int
		want              error
	}{
		{"维度非正", 0, 1, 1, ErrInvalidDim},
		{"表数非正", 4, 0, 1, ErrNonPositiveTables},
		{"表数为负", 4, -2, 1, ErrNonPositiveTables},
		{"位数非正", 4, 1, 0, ErrNonPositiveBits},
		{"位数超上限", 4, 1, MaxBits + 1, ErrBitsTooLarge},
	}
	for _, c := range ctorCases {
		_, err := New(c.dim, c.tables, c.bits, 1)
		t.Logf("输入: New(dim=%d, tables=%d, bits=%d); 输出: %v; 判定依据: errors.Is(%v)",
			c.dim, c.tables, c.bits, err, c.want)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: 得到 %v，期望 %v", c.name, err, c.want)
		}
	}

	idx := buildIndex(t, 3, 2, 4, 7, map[int][]int64{1: {1, 0, 0}})
	before := idx.Len()

	opCases := []struct {
		name string
		op   func() error
		want error
	}{
		{"插入维度不符", func() error { return idx.Insert(2, []int64{1, 2}) }, ErrDimMismatch},
		{"插入零向量", func() error { return idx.Insert(2, []int64{0, 0, 0}) }, ErrZeroVector},
		{"插入重复编号", func() error { return idx.Insert(1, []int64{1, 1, 1}) }, ErrDuplicateID},
		{"删除不存在编号", func() error { return idx.Delete(42) }, ErrIDNotFound},
		{"查询维度不符", func() error { _, _, e := idx.Query([]int64{1}, 1); return e }, ErrDimMismatch},
		{"查询零向量", func() error { _, _, e := idx.Query([]int64{0, 0, 0}, 1); return e }, ErrZeroVector},
		{"查询 K 非正", func() error { _, _, e := idx.Query([]int64{1, 1, 1}, 0); return e }, ErrNonPositiveK},
	}
	for _, c := range opCases {
		err := c.op()
		t.Logf("输入: %s; 输出: %v; 判定依据: errors.Is(%v)", c.name, err, c.want)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: 得到 %v，期望 %v", c.name, err, c.want)
		}
	}
	if idx.Len() != before {
		t.Errorf("被拒绝的操作改变了索引: 长度 %d -> %d", before, idx.Len())
	}
	// 各错误原因两两可区分。
	all := []error{ErrInvalidDim, ErrDimMismatch, ErrZeroVector, ErrDuplicateID,
		ErrIDNotFound, ErrNonPositiveTables, ErrNonPositiveBits, ErrBitsTooLarge, ErrNonPositiveK}
	for a := 0; a < len(all); a++ {
		for b := a + 1; b < len(all); b++ {
			if errors.Is(all[a], all[b]) {
				t.Errorf("错误原因不可区分: %v 与 %v", all[a], all[b])
			}
		}
	}
}

// TestDeterminism 验证同一种子与同一操作序列得到逐字节相同的签名与结果。
func TestDeterminism(t *testing.T) {
	const seed = int64(12345)
	vecs := map[int][]int64{
		1: {3, 1, 4, 1}, 2: {5, 9, 2, 6}, 3: {5, 3, 5, 8},
		4: {9, 7, 9, 3}, 5: {2, 3, 8, 4},
	}
	run := func() (*Index, [][]Result) {
		idx := buildIndex(t, 4, 4, 8, seed, vecs)
		if err := idx.Delete(2); err != nil {
			t.Fatalf("Delete 失败: %v", err)
		}
		if err := idx.Insert(6, []int64{6, 2, 6, 4}); err != nil {
			t.Fatalf("Insert 失败: %v", err)
		}
		var out [][]Result
		for q := 0; q < 5; q++ {
			r, _, err := idx.Query([]int64{int64(q + 1), 1, 2, 3}, 3)
			if err != nil {
				t.Fatalf("Query 失败: %v", err)
			}
			out = append(out, r)
		}
		return idx, out
	}
	idx1, out1 := run()
	idx2, out2 := run()

	if !reflect.DeepEqual(idx1.entries, idx2.entries) {
		t.Errorf("相同种子与操作序列下签名不一致:\n%v\n%v", idx1.entries, idx2.entries)
	}
	if !reflect.DeepEqual(idx1.tables, idx2.tables) {
		t.Errorf("相同种子与操作序列下桶不一致")
	}
	if !reflect.DeepEqual(out1, out2) {
		t.Errorf("相同种子与操作序列下查询结果不一致:\n%v\n%v", out1, out2)
	}
	t.Logf("输入: 种子=%d 同一操作序列执行两次; 输出: 结果=%v; 判定依据: 签名、桶与结果逐字节相同",
		seed, out1)
}

// TestConcurrent 验证插入、删除与查询可被并发调用（配合 -race）。
func TestConcurrent(t *testing.T) {
	const (
		dim   = 8
		seed  = int64(77)
		round = 200
	)
	idx, err := New(dim, 4, 8, seed)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	vec := func(id int) []int64 {
		v := make([]int64, dim)
		for i := range v {
			v[i] = int64((id*31 + i*17) % 13)
		}
		v[0]++
		return v
	}
	var wg sync.WaitGroup
	// 并发插入互不重叠的编号段。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for n := 0; n < round; n++ {
				if err := idx.Insert(base+n, vec(base+n)); err != nil {
					t.Errorf("Insert(%d) 失败: %v", base+n, err)
				}
			}
		}(w * 1000)
	}
	// 并发查询。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(q int) {
			defer wg.Done()
			for n := 0; n < round; n++ {
				if _, _, err := idx.Query(vec(q+n), 5); err != nil {
					t.Errorf("Query 失败: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
	// 并发删除与查询。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for n := 0; n < round/2; n++ {
				if err := idx.Delete(base + n); err != nil {
					t.Errorf("Delete(%d) 失败: %v", base+n, err)
				}
			}
		}(w * 1000)
	}
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(q int) {
			defer wg.Done()
			for n := 0; n < round; n++ {
				_, _, _ = idx.Query(vec(q+n), 5)
			}
		}(w)
	}
	wg.Wait()
	wantLen := 4*round - 4*(round/2)
	t.Logf("输入: 4 协程插入+4 协程查询+4 协程删除并发; 输出: 最终长度=%d; 判定依据: 无竞态且长度=%d",
		idx.Len(), wantLen)
	if idx.Len() != wantLen {
		t.Errorf("并发后长度错误: 得到 %d，期望 %d", idx.Len(), wantLen)
	}
}

// TestResultsConsistentWithExact 验证返回的每一项都属于候选集合，
// 且排序与对候选集合做精确余弦排序一致。
func TestResultsConsistentWithExact(t *testing.T) {
	const (
		dim    = 12
		tables = 4
		bits   = 6
		seed   = int64(555)
		k      = 7
	)
	rng := randSource(314)
	vecs := genDataset(rng, 300, dim)
	idx := buildIndex(t, dim, tables, bits, seed, vecs)
	queries := genDataset(randSource(271), 10, dim)

	for q := 0; q < 10; q++ {
		query := queries[q]
		got, stats, err := idx.Query(query, k)
		if err != nil {
			t.Fatalf("Query 失败: %v", err)
		}
		// 重建候选集合。
		sigs := idx.signatures(query)
		idx.mu.RLock()
		cand := make(map[int][]int64)
		for tb := 0; tb < tables; tb++ {
			for id := range idx.tables[tb][sigs[tb]] {
				cand[id] = vecs[id]
			}
		}
		idx.mu.RUnlock()
		if stats.Candidates != len(cand) || stats.RerankCount != len(cand) {
			t.Errorf("查询#%d 统计不符: 候选数=%d 精排次数=%d 实际候选=%d",
				q, stats.Candidates, stats.RerankCount, len(cand))
		}
		want := bruteForce(cand, query, k)
		t.Logf("输入: 查询#%d K=%d; 输出: %v; 判定依据: 与候选集合精确余排序列 %v 完全一致",
			q, k, resultIDs(got), resultIDs(want))
		if !reflect.DeepEqual(got, want) {
			t.Errorf("查询#%d 结果与候选精确排序不一致: 得到 %v，期望 %v", q, got, want)
		}
		for _, r := range got {
			if _, ok := cand[r.ID]; !ok {
				t.Errorf("查询#%d 结果 %d 不属于候选集合", q, r.ID)
			}
		}
	}
}
