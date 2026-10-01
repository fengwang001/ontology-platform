package lsh

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// bruteForce 精确检索：全量余弦降序、并列编号升序，取前 k。
func bruteForce(vectors map[int][]int64, query []int64, k int) []Result {
	ids := make([]int, 0, len(vectors))
	for id := range vectors {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	res := make([]Result, 0, len(ids))
	for _, id := range ids {
		res = append(res, Result{ID: id, Score: cosine(query, vectors[id])})
	}
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].Score != res[j].Score {
			return res[i].Score > res[j].Score
		}
		return res[i].ID < res[j].ID
	})
	if len(res) > k {
		res = res[:k]
	}
	return res
}

func randVector(rng *rand.Rand, dim int) []int64 {
	v := make([]int64, dim)
	for {
		zero := true
		for i := range v {
			v[i] = int64(rng.Intn(21) - 10)
			if v[i] != 0 {
				zero = false
			}
		}
		if !zero {
			return v
		}
	}
}

func buildIndex(t *testing.T, seed int64, n, dim, maxTables, maxBits, nQueries int) (*Index, map[int][]int64, [][]int64) {
	t.Helper()
	idx, err := NewIndex(dim, maxTables, maxBits, seed)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	rng := rand.New(rand.NewSource(seed + 1))
	vectors := make(map[int][]int64)
	for id := 0; id < n; id++ {
		v := randVector(rng, dim)
		if err := idx.Insert(id, v); err != nil {
			t.Fatalf("Insert %d: %v", id, err)
		}
		vectors[id] = v
	}
	queries := make([][]int64, nQueries)
	for i := range queries {
		queries[i] = randVector(rng, dim)
	}
	return idx, vectors, queries
}

func resultIDs(res []Result) []int {
	ids := make([]int, len(res))
	for i, r := range res {
		ids[i] = r.ID
	}
	return ids
}

// 召回率随表数 1、2、4、8 单调不降：候选集合随表数嵌套扩大。
func TestRecallMonotonicInTables(t *testing.T) {
	const (
		dim       = 16
		maxTables = 8
		maxBits   = 8
		k         = 10
	)
	idx, vectors, queries := buildIndex(t, 42, 400, dim, maxTables, maxBits, 20)
	prev := -1.0
	for _, tables := range []int{1, 2, 4, 8} {
		var sum float64
		for qi, q := range queries {
			exact := bruteForce(vectors, q, k)
			got, stats, err := idx.Query(q, tables, maxBits, k)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if stats.Reranked != stats.Candidates {
				t.Fatalf("精排次数 %d != 候选数 %d", stats.Reranked, stats.Candidates)
			}
			top := make(map[int]bool, k)
			for _, r := range exact {
				top[r.ID] = true
			}
			hit := 0
			for _, r := range got {
				if top[r.ID] {
					hit++
				}
			}
			sum += float64(hit) / float64(k)
			if qi == 0 {
				t.Logf("表数=%d 查询0 候选=%d 精排=%d 命中=%d/%d 判定依据=与暴力精确TopK交集",
					tables, stats.Candidates, stats.Reranked, hit, k)
			}
		}
		avg := sum / float64(len(queries))
		t.Logf("表数=%d 平均召回率=%.4f 上一档=%.4f 判定依据=候选集合随表数嵌套扩大故单调不降", tables, avg, prev)
		if avg < prev {
			t.Fatalf("召回率随表数下降: 表数=%d 召回=%.4f < %.4f", tables, avg, prev)
		}
		prev = avg
	}
}

// 候选数随位数增加单调不增：同表内更多位意味着更严格的同桶条件。
func TestCandidatesMonotonicInBits(t *testing.T) {
	const (
		dim       = 16
		maxTables = 4
		maxBits   = 10
		k         = 10
	)
	idx, _, queries := buildIndex(t, 7, 400, dim, maxTables, maxBits, 20)
	prev := -1
	for bits := 1; bits <= maxBits; bits++ {
		total := 0
		for _, q := range queries {
			_, stats, err := idx.Query(q, maxTables, bits, k)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			total += stats.Candidates
		}
		t.Logf("位数=%d 候选总数=%d 上一档=%d 判定依据=位数增加使同桶条件更严格", bits, total, prev)
		if prev >= 0 && total > prev {
			t.Fatalf("候选数随位数增加: 位数=%d 候选=%d > %d", bits, total, prev)
		}
		prev = total
	}
}

// 点积恰为零时签名位取 1（>= 0 规则），负点积取 0。
func TestZeroDotSignatureBit(t *testing.T) {
	idx := &Index{
		dim:       2,
		maxTables: 1,
		maxBits:   3,
		planes: [][][]float64{{
			{1, 0},   // 与 [0,7] 点积恰为 0 -> 位 1
			{-1, -1}, // 与 [0,7] 点积为 -7 -> 位 0
			{1, 1},   // 与 [0,7] 点积为 7  -> 位 1
		}},
		vectors: make(map[int][]int64),
		buckets: []map[uint64]map[int]struct{}{make(map[uint64]map[int]struct{})},
	}
	sig, err := idx.Signature([]int64{0, 7}, 0, 3)
	if err != nil {
		t.Fatalf("Signature: %v", err)
	}
	t.Logf("输入向量=[0 7] 法向量=[[1 0] [-1 -1] [1 1]] 点积=[0 -7 7] 签名=%03b 判定依据=点积>=0取1否则取0", sig)
	if sig != 0b101 {
		t.Fatalf("签名=%03b 期望=101（零点位取1）", sig)
	}
}

// 余弦并列时按编号升序。
func TestCosineTieBreak(t *testing.T) {
	idx, err := NewIndex(2, 1, 4, 1)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	// 三个向量与查询 [1,0] 同向，余弦均为 1；插入顺序故意打乱。
	for _, idv := range []struct {
		id int
		v  []int64
	}{{9, []int64{2, 0}}, {2, []int64{1, 0}}, {5, []int64{3, 0}}} {
		if err := idx.Insert(idv.id, idv.v); err != nil {
			t.Fatalf("Insert %d: %v", idv.id, err)
		}
	}
	res, stats, err := idx.Query([]int64{1, 0}, 1, 4, 3)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	t.Logf("输入查询=[1 0] 候选=%d 输出=%v 分数=[%f %f %f] 判定依据=余弦并列按编号升序",
		stats.Candidates, resultIDs(res), res[0].Score, res[1].Score, res[2].Score)
	want := []int{2, 5, 9}
	got := resultIDs(res)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("并列排序=%v 期望=%v", got, want)
		}
		if res[i].Score != 1.0 {
			t.Fatalf("余弦=%f 期望=1", res[i].Score)
		}
	}
}

// 各类非法输入必须整体拒绝且不得改变任何桶。
func TestInvalidInputs(t *testing.T) {
	idx, err := NewIndex(3, 2, 4, 1)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	if err := idx.Insert(1, []int64{1, 2, 3}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	before, _, err := idx.Query([]int64{1, 1, 1}, 2, 4, 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	check := func(name string, got, want error) {
		t.Helper()
		t.Logf("用例=%s 错误=%v 判定依据=errors.Is(%v)", name, got, want)
		if !errors.Is(got, want) {
			t.Fatalf("%s: 错误=%v 期望=%v", name, got, want)
		}
	}
	check("插入维度不符", idx.Insert(2, []int64{1, 2}), ErrDimensionMismatch)
	check("插入零向量", idx.Insert(3, []int64{0, 0, 0}), ErrZeroVector)
	check("插入编号重复", idx.Insert(1, []int64{4, 5, 6}), ErrDuplicateID)
	check("删除不存在编号", idx.Delete(99), ErrNotFound)

	_, _, qerr := idx.Query([]int64{1, 2}, 2, 4, 5)
	check("查询维度不符", qerr, ErrDimensionMismatch)
	_, _, qerr = idx.Query([]int64{0, 0, 0}, 2, 4, 5)
	check("查询零向量", qerr, ErrZeroVector)
	_, _, qerr = idx.Query([]int64{1, 1, 1}, 0, 4, 5)
	check("表数非正", qerr, ErrInvalidTables)
	_, _, qerr = idx.Query([]int64{1, 1, 1}, 3, 4, 5)
	check("表数超上限", qerr, ErrInvalidTables)
	_, _, qerr = idx.Query([]int64{1, 1, 1}, 2, 0, 5)
	check("位数非正", qerr, ErrInvalidBits)
	_, _, qerr = idx.Query([]int64{1, 1, 1}, 2, 5, 5)
	check("位数超上限", qerr, ErrInvalidBits)
	_, _, qerr = idx.Query([]int64{1, 1, 1}, 2, 4, 0)
	check("K 非正", qerr, ErrInvalidK)

	_, cerr := NewIndex(0, 2, 4, 1)
	check("索引维度非正", cerr, ErrInvalidDim)
	_, cerr = NewIndex(3, 0, 4, 1)
	check("索引表数非正", cerr, ErrInvalidTables)
	_, cerr = NewIndex(3, 2, 0, 1)
	check("索引位数非正", cerr, ErrInvalidBits)
	_, cerr = NewIndex(3, 2, MaxBits+1, 1)
	check("索引位数超上限", cerr, ErrInvalidBits)

	// 被拒绝的操作不得改变任何桶：向量数与查询结果均不变。
	if n := idx.Len(); n != 1 {
		t.Fatalf("拒绝操作后向量数=%d 期望=1", n)
	}
	after, _, err := idx.Query([]int64{1, 1, 1}, 2, 4, 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("拒绝操作改变了桶: 前=%v 后=%v", before, after)
	}
	t.Logf("输入=连续非法操作 输出向量数=%d 判定依据=拒绝前后查询结果逐字节一致", idx.Len())
}

// 同一种子与同一操作序列得到逐字节相同的签名与结果。
func TestDeterministicReplay(t *testing.T) {
	run := func() string {
		idx, err := NewIndex(8, 4, 8, 99)
		if err != nil {
			t.Fatalf("NewIndex: %v", err)
		}
		rng := rand.New(rand.NewSource(7))
		var buf bytes.Buffer
		for id := 0; id < 100; id++ {
			v := randVector(rng, 8)
			if err := idx.Insert(id, v); err != nil {
				t.Fatalf("Insert: %v", err)
			}
			sig, err := idx.Signature(v, 2, 8)
			if err != nil {
				t.Fatalf("Signature: %v", err)
			}
			fmt.Fprintf(&buf, "%d:%x;", id, sig)
		}
		for i := 0; i < 20; i++ {
			q := randVector(rng, 8)
			res, stats, err := idx.Query(q, 4, 8, 5)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			fmt.Fprintf(&buf, "%v|%d/%d;", resultIDs(res), stats.Candidates, stats.Reranked)
		}
		return buf.String()
	}
	first, second := run(), run()
	t.Logf("输入=种子99+固定操作序列 输出摘要长度=%d 判定依据=两次重放逐字节相同", len(first))
	if first != second {
		t.Fatal("同一种子同一操作序列的签名与结果不一致")
	}
}

// 候选为空时返回空结果。
func TestEmptyCandidates(t *testing.T) {
	idx, err := NewIndex(4, 2, 4, 1)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	res, stats, err := idx.Query([]int64{1, 2, 3, 4}, 2, 4, 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	t.Logf("输入=空索引查询 输出结果数=%d 候选=%d 精排=%d 判定依据=候选为空返回空结果",
		len(res), stats.Candidates, stats.Reranked)
	if len(res) != 0 || stats.Candidates != 0 || stats.Reranked != 0 {
		t.Fatalf("空候选应返回空结果: res=%v stats=%+v", res, stats)
	}
}

// 表与位的嵌套：b 位签名是更多位签名的低位前缀；更大表数索引的前 L 张表一致。
func TestNesting(t *testing.T) {
	idx, err := NewIndex(6, 4, 8, 5)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	v := []int64{3, -2, 5, 1, 0, -7}
	full, err := idx.Signature(v, 2, 8)
	if err != nil {
		t.Fatalf("Signature: %v", err)
	}
	for b := 1; b <= 8; b++ {
		part, err := idx.Signature(v, 2, b)
		if err != nil {
			t.Fatalf("Signature: %v", err)
		}
		want := full & (uint64(1)<<uint(b) - 1)
		t.Logf("位数=%d 签名=%08b 完整签名低%d位=%08b 判定依据=位嵌套", b, part, b, want)
		if part != want {
			t.Fatalf("位数=%d 签名=%08b 期望=%08b", b, part, want)
		}
	}
	bigger, err := NewIndex(6, 8, 8, 5)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	for tb := 0; tb < 4; tb++ {
		s1, _ := idx.Signature(v, tb, 8)
		s2, _ := bigger.Signature(v, tb, 8)
		t.Logf("表=%d 4表索引签名=%08b 8表索引签名=%08b 判定依据=表嵌套", tb, s1, s2)
		if s1 != s2 {
			t.Fatalf("表=%d 签名不一致: %08b != %08b", tb, s1, s2)
		}
	}
}

// 返回的每一项都属于候选集合且排序与精确余弦一致。
func TestResultMembershipAndOrder(t *testing.T) {
	const (
		dim    = 12
		tables = 3
		bits   = 6
		k      = 7
	)
	idx, vectors, queries := buildIndex(t, 11, 300, dim, 4, 8, 10)
	for qi, q := range queries {
		res, stats, err := idx.Query(q, tables, bits, k)
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		// 独立重算候选集合：任一前 tables 张表中签名相等即入候选。
		cand := make(map[int]bool)
		for id, v := range vectors {
			for tb := 0; tb < tables; tb++ {
				sv, _ := idx.Signature(v, tb, bits)
				sq, _ := idx.Signature(q, tb, bits)
				if sv == sq {
					cand[id] = true
					break
				}
			}
		}
		if len(cand) != stats.Candidates {
			t.Fatalf("查询%d 候选数=%d 独立重算=%d", qi, stats.Candidates, len(cand))
		}
		for i, r := range res {
			if !cand[r.ID] {
				t.Fatalf("查询%d 结果 %d 不在候选集合中", qi, r.ID)
			}
			if want := cosine(q, vectors[r.ID]); r.Score != want {
				t.Fatalf("查询%d 结果 %d 分数=%v 精确余弦=%v", qi, r.ID, r.Score, want)
			}
			if i > 0 {
				p := res[i-1]
				if p.Score < r.Score || (p.Score == r.Score && p.ID > r.ID) {
					t.Fatalf("查询%d 排序违反余弦降序/编号升序: %+v 后接 %+v", qi, p, r)
				}
			}
		}
		if qi == 0 {
			t.Logf("查询0 候选=%d 精排=%d 输出=%v 判定依据=逐项属于候选且分数等于精确余弦",
				stats.Candidates, stats.Reranked, resultIDs(res))
		}
	}
}

// 插入、删除与查询并发调用（配合 -race 验证）。
func TestConcurrentAccess(t *testing.T) {
	idx, err := NewIndex(8, 4, 8, 3)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < 50; i++ {
				id := w*1000 + i
				v := randVector(rng, 8)
				if err := idx.Insert(id, v); err != nil {
					t.Errorf("Insert: %v", err)
					return
				}
				if _, _, err := idx.Query(v, 2, 6, 5); err != nil {
					t.Errorf("Query: %v", err)
					return
				}
				if i%3 == 0 {
					if err := idx.Delete(id); err != nil {
						t.Errorf("Delete: %v", err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	t.Logf("输入=4协程并发插入/删除/查询 输出向量数=%d 判定依据=-race 无数据竞争", idx.Len())
}
