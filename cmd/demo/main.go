// Command demo 演示近似最近邻分桶检索器的各项判定，逐行打印 OK/FAIL。
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"

	"ontology/hyper"
	"ontology/persist"
	"ontology/search"
	"ontology/vec"
)

var failed, total int

func check(ok bool, format string, args ...any) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
		failed++
	}
	total++
	fmt.Printf("%s %s\n", tag, fmt.Sprintf(format, args...))
}

// randVec 生成一个各分量独立标准正态的向量。
func randVec(r *rand.Rand, dim int) vec.Vec {
	v := make(vec.Vec, dim)
	for i := range v {
		v[i] = r.NormFloat64()
	}
	return v
}

// clustered 生成 nclu 个簇、共 n 个向量（簇心放大 scale，簇内噪声 noise），
// 以及 nq 个查询（随机簇心加噪声）。固定种子，结果可复现。
func clustered(seed int64, dim, n, nclu, nq int, scale, noise float64) ([]vec.Vec, []vec.Vec) {
	r := rand.New(rand.NewSource(seed))
	centers := make([]vec.Vec, nclu)
	for i := range centers {
		c := randVec(r, dim)
		for j := range c {
			c[j] *= scale
		}
		centers[i] = c
	}
	jitter := func(c vec.Vec) vec.Vec {
		v := make(vec.Vec, dim)
		for j := range v {
			v[j] = c[j] + noise*r.NormFloat64()
		}
		return v
	}
	vs := make([]vec.Vec, n)
	for i := range vs {
		vs[i] = jitter(centers[r.Intn(nclu)])
	}
	qs := make([]vec.Vec, nq)
	for i := range qs {
		qs[i] = jitter(centers[r.Intn(nclu)])
	}
	return vs, qs
}

func main() {
	dot := vec.Dot(vec.Vec{1, 2, 3}, vec.Vec{4, 5, 6})
	dist := vec.Dist(vec.Vec{0, 0, 0}, vec.Vec{1, 2, 2})
	check(dot == 32 && dist == 3 && vec.Check(vec.Vec{math.NaN()}, 1) != nil, "vec: dot/dist/NaN 校验正确")

	const dim, bits, n, nq = 32, 8, 5000, 100
	rng := rand.New(rand.NewSource(7))
	vs := make([]vec.Vec, n)
	for i := range vs {
		vs[i] = randVec(rng, dim)
	}
	fa, fb, fc := hyper.New(dim, bits, 42), hyper.New(dim, bits, 42), hyper.New(dim, bits, 43)
	same, diff := true, false
	for _, v := range vs {
		if fa.Signature(v) != fb.Signature(v) {
			same = false
		}
		if fa.Signature(v) != fc.Signature(v) {
			diff = true
		}
	}
	zero := fa.Signature(make(vec.Vec, dim))
	check(same && diff && zero == 0, "hyper: 同种子签名逐字节相同, 异种子不同, 零向量签名为 0")

	data, qs := clustered(2024, dim, n, 64, nq, 4, 0.6)
	recallAt := func(bits, tables int) (rec, cand float64) {
		ix, err := search.Build(data, dim, bits, tables, 1000)
		if err != nil {
			check(false, "search: Build(bits=%d,L=%d): %v", bits, tables, err)
		}
		for _, q := range qs {
			got, _ := ix.Query(q, 10)
			rec += search.Recall(got, search.BruteForce(data, q, 10))
		}
		return rec / nq, float64(ix.DistCount()) / nq
	}
	r1, _ := recallAt(bits, 1)
	r2, _ := recallAt(bits, 2)
	r4, _ := recallAt(bits, 4)
	r8, c8 := recallAt(bits, 8)
	check(r8 >= 0.6, "search: top10 召回率 %.3f >= 0.6 (b=8,L=8)", r8)
	check(r1 <= r2 && r2 <= r4 && r4 <= r8, "search: 召回随表数单调不降 [%.3f %.3f %.3f %.3f]", r1, r2, r4, r8)
	_, c4 := recallAt(4, 8)
	_, c12 := recallAt(12, 8)
	check(c4 >= c8 && c8 >= c12, "search: 候选数随位数单调下降 [%.0f %.0f %.0f]", c4, c8, c12)
	check(c8 <= 0.1*n, "search: 精排距离计算数 %.0f <= 总量10%% (%d)", c8, n/10)

	fresh, _ := search.Build(data, dim, bits, 8, 1000)
	check(fresh.HashCount() == int64(n*8*bits), "search: 哈希计算次数 %d == N*L*b (%d)", fresh.HashCount(), n*8*bits)

	ident := make([]vec.Vec, 50)
	for i := range ident {
		ident[i] = vec.Vec{1, 2, 3}
	}
	ixIdent, _ := search.Build(ident, 3, bits, 4, 5)
	gotIdent, _ := ixIdent.Query(vec.Vec{1, 2, 3}, 10)
	check(search.Recall(gotIdent, search.BruteForce(ident, vec.Vec{1, 2, 3}, 10)) == 1, "search: 全同向量召回为 1 (候选=%d)", ixIdent.DistCount())

	dir, _ := os.MkdirTemp("", "ontology-demo")
	defer os.RemoveAll(dir)
	small, _ := search.Build(data[:200], dim, bits, 2, 77)
	full := filepath.Join(dir, "idx.bin")
	fams, set := small.Snapshot()
	pidx := &persist.Index{Dim: dim, Bits: bits, NVecs: small.NumVecs(), Seed: 77}
	for _, f := range fams {
		pidx.Planes = append(pidx.Planes, f.Planes())
	}
	for _, tb := range set.Tables {
		pidx.Tables = append(pidx.Tables, tb.Buckets())
	}
	check(persist.Save(full, pidx) == nil, "persist: 索引落盘成功")
	raw, _ := os.ReadFile(full)
	planesEnd := persist.HeaderSize + 2*bits*dim*8
	cuts := []struct {
		at   int
		want error
	}{
		{persist.HeaderSize - 1, persist.ErrTruncHeader},
		{planesEnd - 1, persist.ErrTruncPlanes},
		{len(raw) - 5, persist.ErrTruncBuckets},
		{len(raw) - 1, persist.ErrCRC},
	}
	classOK := true
	for i, c := range cuts {
		p := filepath.Join(dir, fmt.Sprint("cut", i))
		_ = os.WriteFile(p, raw[:c.at], 0o600)
		_, err := persist.Load(p)
		classOK = classOK && errors.Is(err, c.want)
	}
	check(classOK, "persist: 四类截断分类各一例均正确")

	bad := append([]byte(nil), raw...)
	binary.LittleEndian.PutUint32(bad[planesEnd+16:], math.MaxUint32) // 桶表区首个 ID 改悬挂
	pBad := filepath.Join(dir, "bad")
	_ = os.WriteFile(pBad, bad, 0o600)
	rec, st, err := persist.Recover(pBad)
	noDangle := err == nil && st.DanglingIDs >= 1
	for _, tab := range rec.Tables {
		for _, ids := range tab {
			for _, id := range ids {
				noDangle = noDangle && id < rec.NVecs
			}
		}
	}
	check(noDangle, "persist: 恢复剔除悬挂 ID %d 个且无残留", st.DanglingIDs)

	fmt.Printf("SUMMARY %d checks, %d failed\n", total, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
